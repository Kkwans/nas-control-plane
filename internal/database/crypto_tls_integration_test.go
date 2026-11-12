package database

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 仅使用内存凭据、合成证书和错误；不拨号、不握手、不监听。
func TestTLSIntegrationPublicCodecVersions(t *testing.T) {
	t.Parallel()
	ca := newDatabaseTLSCA(t, nil)
	for _, tt := range []struct {
		name    string
		policy  *TLSPolicy
		version int
	}{
		{name: "legacy", version: 1},
		{name: "system roots", policy: &TLSPolicy{Mode: TLSVerifyFull}, version: 2},
		{name: "explicit policy", policy: &TLSPolicy{Mode: TLSVerifyFull, CAPEM: ca.pem, ServerName: "DB.Example."}, version: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			keys, material := versionedFixtureKeys(t)
			input := versionedFixtureCredentials()
			input.TLS = tt.policy
			want := CloneCredentials(input)
			encoded, keyVersion, err := EncryptCredentials(context.Background(), keys, versionedFixtureAAD, input)
			if err != nil || keyVersion != material.Version || versionedEnvelope(t, encoded).Version != tt.version {
				t.Fatalf("public encryption chose the wrong envelope: keyVersion=%d err=%v", keyVersion, err)
			}
			if bytes.Contains(encoded, []byte(input.Password)) || bytes.Contains(encoded, []byte(input.Token)) {
				t.Fatal("public envelope contains a plaintext credential")
			}
			got, err := DecryptCredentials(context.Background(), keys, versionedFixtureAAD, encoded)
			if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(input, want) {
				t.Fatal("public codec did not preserve all credential and policy fields")
			}
			again, err := DecryptCredentials(context.Background(), keys, versionedFixtureAAD, encoded)
			if err != nil || !reflect.DeepEqual(again, want) {
				t.Fatal("second public decrypt changed the stored value")
			}
			if tt.policy == nil {
				if got.TLS != nil || again.TLS != nil {
					t.Fatal("v1 unexpectedly gained a TLS policy")
				}
				return
			}
			if got.TLS == input.TLS || got.TLS == again.TLS || again.TLS == input.TLS {
				t.Fatal("public codec retained an aliased TLS policy")
			}
			got.TLS.Mode, got.TLS.CAPEM, got.TLS.ServerName = "prefer", "changed", "changed.invalid"
			if !reflect.DeepEqual(input, want) || !reflect.DeepEqual(again, want) {
				t.Fatal("mutating a decrypted policy changed another credential value")
			}
		})
	}
}

type tlsIntegrationMutationKeys struct {
	KeyProvider
	beforeCurrent func()
}

func (keys tlsIntegrationMutationKeys) Current(ctx context.Context) (KeyMaterial, error) {
	keys.beforeCurrent()
	return keys.KeyProvider.Current(ctx)
}

func TestTLSIntegrationPublicEncryptFreezesBeforeKeyLookup(t *testing.T) {
	t.Parallel()
	keys, _ := versionedFixtureKeys(t)
	input := versionedFixtureCredentials()
	input.TLS = &TLSPolicy{Mode: TLSVerifyFull, CAPEM: newDatabaseTLSCA(t, nil).pem, ServerName: "Frozen.Example."}
	want := CloneCredentials(input)
	called := 0
	provider := tlsIntegrationMutationKeys{KeyProvider: keys, beforeCurrent: func() {
		called++
		input.TLS.Mode, input.TLS.CAPEM, input.TLS.ServerName = "prefer", "changed-secret", "*.invalid"
		input.TLS = nil
	}}
	encoded, _, err := EncryptCredentials(context.Background(), provider, versionedFixtureAAD, input)
	if err != nil || called != 1 || versionedEnvelope(t, encoded).Version != 2 {
		t.Fatalf("encryption did not freeze TLS before Current: calls=%d err=%v", called, err)
	}
	got, err := DecryptCredentials(context.Background(), keys, versionedFixtureAAD, encoded)
	if err != nil || !reflect.DeepEqual(got, want) || got.TLS == want.TLS {
		t.Fatal("key-provider callback changed the frozen public policy")
	}
}

func TestTLSIntegrationPrivateCarrierHasOnePolicy(t *testing.T) {
	t.Parallel()
	_, material := versionedFixtureKeys(t)
	input := versionedFixtureCredentials()
	input.TLS = &TLSPolicy{Mode: TLSVerifyFull, ServerName: "db.example"}
	want := CloneCredentials(input)
	value := credentialValueFromCredentials(input)
	if value.legacyCredentials.TLS != nil || value.tls == nil || value.tls == input.TLS || !reflect.DeepEqual(value.credentials(), want) {
		t.Fatal("public-to-private conversion did not canonicalize and copy the policy")
	}
	input.TLS.ServerName = "mutated.invalid"
	if *value.tls != *want.TLS {
		t.Fatal("private carrier shares the caller's policy")
	}
	public := value.credentials()
	if public.TLS == value.tls {
		t.Fatal("private-to-public conversion returned an aliased policy")
	}
	public.TLS.ServerName = "another.invalid"
	if *value.tls != *want.TLS {
		t.Fatal("public conversion can mutate the private policy")
	}
	for _, tt := range []struct {
		name   string
		policy *TLSPolicy
	}{
		{name: "public field only"},
		{name: "conflicting fields", policy: &TLSPolicy{Mode: TLSVerifyFull, ServerName: "different.invalid"}},
		{name: "equal fields", policy: want.TLS},
	} {
		t.Run(tt.name, func(t *testing.T) {
			malformed := credentialValue{legacyCredentials: want, tls: tt.policy}
			encoded, version, err := encryptCredentialValueWithKey(context.Background(), material, versionedFixtureAAD, malformed)
			assertTLSIntegrationEncryptFailure(t, encoded, version, err)
		})
	}
}

func TestTLSIntegrationInvalidPublicPoliciesFailSafely(t *testing.T) {
	t.Parallel()
	keys, material := versionedFixtureKeys(t)
	for _, tt := range []struct {
		name   string
		policy TLSPolicy
	}{
		{name: "empty"},
		{name: "prefer", policy: TLSPolicy{Mode: "prefer"}},
		{name: "require", policy: TLSPolicy{Mode: "require"}},
		{name: "wrong case", policy: TLSPolicy{Mode: "VERIFY-FULL"}},
		{name: "bad CA", policy: TLSPolicy{Mode: TLSVerifyFull, CAPEM: "synthetic-CA-secret"}},
		{name: "bad name", policy: TLSPolicy{Mode: TLSVerifyFull, ServerName: "*.synthetic-secret.invalid"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := versionedFixtureCredentials()
			input.TLS = &tt.policy
			encoded, version, err := EncryptCredentials(context.Background(), keys, versionedFixtureAAD, input)
			assertTLSIntegrationEncryptFailure(t, encoded, version, err)
			encoded, version, err = encryptWithKey(context.Background(), material, versionedFixtureAAD, input)
			assertTLSIntegrationEncryptFailure(t, encoded, version, err)
			assertTLSIntegrationSafeError(t, ValidateRequiredTLSPolicy(input.TLS), CodeTLSConfigurationInvalid)
		})
	}
	assertTLSIntegrationSafeError(t, ValidateRequiredTLSPolicy(nil), CodeTLSConfigurationInvalid)
}

func TestTLSIntegrationRotationUsesVersionedPolicyBuilder(t *testing.T) {
	t.Parallel()
	keys, oldMaterial := versionedFixtureKeys(t)
	input := versionedFixtureCredentials()
	input.TLS = &TLSPolicy{Mode: TLSVerifyFull, CAPEM: newDatabaseTLSCA(t, nil).pem, ServerName: "Rotate.Example."}
	before := CloneCredentials(input)
	original, _, err := EncryptCredentials(context.Background(), keys, versionedFixtureAAD, input)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecryptCredentials(context.Background(), keys, versionedFixtureAAD, original)
	if err != nil {
		t.Fatal(err)
	}
	newMaterial, err := keys.Rotate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rotated, keyVersion, err := encryptWithKey(context.Background(), newMaterial, versionedFixtureAAD, decoded)
	if err != nil || keyVersion != newMaterial.Version || versionedEnvelope(t, rotated).Version != 2 {
		t.Fatal("rotation lost the v2 envelope or new key version")
	}
	// 独立拼接已冻结的 v2 AAD，比较普通保存和轮换的完整明文合同。
	aad := append([]byte("ncp/database-credentials/envelope\x00"), 0, 0, 0, 2)
	aad = binary.BigEndian.AppendUint64(aad, uint64(len(versionedFixtureAAD)))
	aad = append(aad, versionedFixtureAAD...)
	oldPayload := versionedOpenFixture(t, oldMaterial, aad, original)
	newPayload := versionedOpenFixture(t, newMaterial, aad, rotated)
	if !bytes.Equal(oldPayload, newPayload) {
		t.Fatal("rotation changed the shared v2 payload contract")
	}
	got, err := DecryptCredentials(context.Background(), keys, versionedFixtureAAD, rotated)
	if err != nil || !reflect.DeepEqual(got, before) || got.TLS == decoded.TLS || got.TLS == input.TLS || !reflect.DeepEqual(input, before) {
		t.Fatal("rotation did not preserve all credential and independent policy fields")
	}
}

func assertTLSIntegrationEncryptFailure(t *testing.T, encoded []byte, keyVersion int, err error) {
	t.Helper()
	if encoded != nil || keyVersion != 0 {
		t.Fatal("invalid policy produced ciphertext or a key version")
	}
	assertTLSIntegrationSafeError(t, err, CodeCredentialCorrupt)
}

func assertTLSIntegrationSafeError(t *testing.T, err error, code ErrorCode) *DatabaseError {
	t.Helper()
	var safe *DatabaseError
	if err == nil || !errors.As(err, &safe) || safe == nil || safe.Code != code || err.Error() != string(code) {
		t.Fatalf("expected fixed %s error, got %v", code, err)
	}
	if safe.Cause != nil || errors.Unwrap(err) != nil {
		t.Fatal("safe TLS boundary retained an underlying error chain")
	}
	return safe
}

func TestTLSIntegrationTypedRuntimeErrorMapping(t *testing.T) {
	t.Parallel()
	const secret = "synthetic-certificate-secret"
	cert := &x509.Certificate{Subject: pkix.Name{CommonName: secret}, DNSNames: []string{secret + ".invalid"}, Raw: []byte(secret)}
	unknown := x509.UnknownAuthorityError{Cert: cert}
	invalid := x509.CertificateInvalidError{Cert: cert, Reason: x509.Expired, Detail: secret}
	hostname := x509.HostnameError{Certificate: cert, Host: secret + ".invalid"}
	roots := x509.SystemRootsError{Err: errors.New(secret)}
	algorithm := x509.InsecureAlgorithmError(x509.MD5WithRSA)
	constraint := x509.ConstraintViolationError{}
	extension := x509.UnhandledCriticalExtension{}
	header := tls.RecordHeaderError{Msg: secret, RecordHeader: [5]byte{1, 2, 3, 4, 5}}
	alert := tls.AlertError(42)
	source := Source{ID: "synthetic-source", Driver: DriverPostgreSQL, Host: "db.invalid", Port: 5432}
	for _, tt := range []struct {
		name string
		err  error
	}{
		{"unknown authority value", unknown}, {"unknown authority pointer", &unknown},
		{"invalid certificate value", invalid}, {"invalid certificate pointer", &invalid},
		{"hostname value", hostname}, {"hostname pointer", &hostname},
		{"system roots value", roots}, {"system roots pointer", &roots},
		{"insecure algorithm value", algorithm}, {"insecure algorithm pointer", &algorithm},
		{"constraint value", constraint}, {"constraint pointer", &constraint},
		{"critical extension value", extension}, {"critical extension pointer", &extension},
		{"record header value", header}, {"record header pointer", &header},
		{"alert value", alert}, {"alert pointer", &alert},
		{"certificate verification", &tls.CertificateVerificationError{UnverifiedCertificates: []*x509.Certificate{cert}, Err: errors.New(secret)}},
		{"ECH rejection", &tls.ECHRejectionError{RetryConfigList: []byte(secret)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, wrapped := range []bool{false, true} {
				t.Run(fmt.Sprint(wrapped), func(t *testing.T) {
					incoming := tt.err
					if wrapped {
						incoming = fmt.Errorf("password authentication failed %s: %w", secret, incoming)
					}
					if code := ClassifyError(context.Background(), "connect", incoming); code != CodeTLSVerificationFailed {
						t.Errorf("typed TLS error classified as %s", code)
					}
					mapped := MapError(context.Background(), source, "connect", incoming)
					safe := assertTLSIntegrationSafeError(t, mapped, CodeTLSVerificationFailed)
					if safe.Driver != source.Driver || safe.SourceID != source.ID || safe.Endpoint != sourceEndpoint(source, "") || safe.Operation != "connect" {
						t.Fatal("TLS mapping lost safe operation context")
					}
					if strings.Contains(fmt.Sprint(mapped), secret) || errors.Is(mapped, incoming) {
						t.Fatal("TLS mapping exposed raw certificate or driver data")
					}
				})
			}
		})
	}
	// 文本外观不能伪造 typed TLS 分类。
	plain := errors.New("x509: certificate signed by unknown authority; tls: handshake failure")
	if code := ClassifyError(context.Background(), "connect", plain); code == CodeTLSVerificationFailed {
		t.Fatal("TLS classification relied on untrusted error text")
	}
}

func TestTLSIntegrationConfigurationAndStableCodes(t *testing.T) {
	t.Parallel()
	for _, incoming := range []error{
		errDatabaseTLSConfigurationInvalid,
		fmt.Errorf("synthetic-configuration-secret: %w", errDatabaseTLSConfigurationInvalid),
	} {
		assertTLSIntegrationSafeError(t, MapError(context.Background(), Source{}, "connect", incoming), CodeTLSConfigurationInvalid)
	}
	for _, code := range []ErrorCode{CodeTLSConfigurationInvalid, CodeTLSPolicyUnsupported, CodeTLSVerificationFailed} {
		if got := CodeFromString(string(code)); got != code {
			t.Fatalf("wire codec rejected stable TLS code %s", code)
		}
		if got := CodeFromString(" \n" + string(code) + "\t"); got != code {
			t.Fatalf("wire codec lost existing trim behavior for %s", code)
		}
		for _, invalid := range []string{strings.ToUpper(string(code)), string(code) + ": synthetic-secret", "prefix_" + string(code)} {
			if CodeFromString(invalid) != "" {
				t.Fatal("wire codec accepted a non-contract TLS error code")
			}
		}
		original := &DatabaseError{Code: code, Operation: "connect", Cause: errors.New("synthetic-driver-secret")}
		mapped := assertTLSIntegrationSafeError(t, MapError(context.Background(), Source{}, "query", fmt.Errorf("wrapped: %w", original)), code)
		if mapped == original || original.Cause == nil || mapped.Operation != original.Operation {
			t.Fatal("safe remapping mutated the original error or lost its operation")
		}
	}
}

func TestTLSIntegrationContextPrecedesTLSErrors(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		cause error
		ctx   func() (context.Context, context.CancelFunc)
	}{
		{name: "canceled", cause: context.Canceled, ctx: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}},
		{name: "deadline", cause: context.DeadlineExceeded, ctx: func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Unix(1, 0))
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.ctx()
			defer cancel()
			raw := &tls.CertificateVerificationError{Err: errors.New("synthetic-raw-TLS-secret")}
			cases := []struct {
				name string
				err  error
			}{
				{name: "typed runtime", err: fmt.Errorf("synthetic-wrapper-secret: %w", raw)},
				{name: "configuration sentinel", err: fmt.Errorf("synthetic-wrapper-secret: %w", errDatabaseTLSConfigurationInvalid)},
			}
			for _, code := range []ErrorCode{CodeTLSConfigurationInvalid, CodeTLSPolicyUnsupported, CodeTLSVerificationFailed} {
				cases = append(cases, struct {
					name string
					err  error
				}{name: string(code), err: &DatabaseError{Code: code, Operation: "connect", Cause: raw}})
			}
			for _, item := range cases {
				t.Run(item.name, func(t *testing.T) {
					for _, fromChain := range []bool{false, true} {
						activeContext, incoming := ctx, item.err
						if fromChain {
							activeContext = context.Background()
							incoming = errors.Join(item.err, tt.cause)
						}
						if code := ClassifyError(activeContext, "connect", incoming); code != CodeTimeout {
							t.Fatalf("context lost classification priority: %s", code)
						}
						mapped := MapError(activeContext, Source{}, "connect", incoming)
						var safe *DatabaseError
						if !errors.As(mapped, &safe) || safe.Code != CodeTimeout || mapped.Error() != string(CodeTimeout) || safe.Cause != tt.cause || errors.Unwrap(mapped) != tt.cause {
							t.Fatal("context mapping must retain only the exact context sentinel")
						}
						if !errors.Is(mapped, tt.cause) || errors.Is(mapped, raw) || errors.Unwrap(safe.Cause) != nil {
							t.Fatal("context mapping retained the TLS error chain")
						}
						if original, ok := item.err.(*DatabaseError); ok && (original.Cause != raw || original.Code == CodeTimeout) {
							t.Fatal("context mapping mutated the original TLS DatabaseError")
						}
					}
				})
			}
		})
	}
}

func TestTLSIntegrationNetworkTimeoutDoesNotInventContextDeadline(t *testing.T) {
	incoming := errors.Join(
		&net.DNSError{Err: "synthetic-network-secret", Name: "synthetic.invalid", IsTimeout: true},
		&tls.CertificateVerificationError{Err: errors.New("synthetic-certificate-secret")},
	)
	mapped := MapError(context.Background(), Source{}, "connect", incoming)
	assertTLSIntegrationSafeError(t, mapped, CodeTimeout)
	if errors.Is(mapped, context.DeadlineExceeded) || errors.Is(mapped, context.Canceled) {
		t.Fatal("network timeout invented a context cancellation")
	}
}
