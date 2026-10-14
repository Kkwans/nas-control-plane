package database

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// 夹具只有固定合成 key/nonce/凭据，不访问 key 文件、网络或真实 vault。
const versionedFixtureAAD = "ncp/database-credentials/v1/fixture/mysql/db.invalid:3306/db"
const versionedFixtureV1 = `{"version":1,"algorithm":"AES-256-GCM","keyVersion":7,"nonce":"AAECAwQFBgcICQoL","ciphertext":"fvzkrtRenGrwY2Yp87syQ0bovA+NPJGMYbsMBep1zowj1PvGUwoAipMbcntq9bwPRJ51ecm+Wz0P5pfj5DbC5btJnCEt4zkhMkbVvvqb5fzzkEmOaiyURDWBIV/4gxRXXbZH1jlSNdL5EJQJsjIdHUjXF7yFtNzyHoU"}`
const versionedFixturePayload = `{"username":"fixture-user","password":"synthetic-password","token":"synthetic-token","database":"fixture"}`

func versionedFixtureKeys(t *testing.T) (*MemoryKeyProvider, KeyMaterial) {
	t.Helper()
	material := KeyMaterial{Version: 7, Key: bytes.Repeat([]byte{0x42}, 32)}
	keys, err := NewMemoryKeyProvider(material.Version, material.Key)
	if err != nil {
		t.Fatal(err)
	}
	return keys, material
}

func versionedFixtureCredentials() Credentials {
	return Credentials{Username: "fixture-user", Password: "synthetic-password", Token: "synthetic-token", Database: "fixture"}
}

// 独立构造测试密文，不复用被测 builder、AAD 或 payload codec。
func versionedSealFixture(t *testing.T, material KeyMaterial, version int, aad []byte, payload string) []byte {
	t.Helper()
	block, err := aes.NewCipher(material.Key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	return versionedEncodeEnvelope(t, encryptedCredentialEnvelope{
		Version: version, Algorithm: "AES-256-GCM", KeyVersion: material.Version,
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(gcm.Seal(nil, nonce, []byte(payload), aad)),
	})
}

func versionedEncodeEnvelope(t *testing.T, envelope encryptedCredentialEnvelope) []byte {
	t.Helper()
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func versionedEnvelope(t *testing.T, encoded []byte) encryptedCredentialEnvelope {
	t.Helper()
	var envelope encryptedCredentialEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}

func versionedOpenFixture(t *testing.T, material KeyMaterial, aad, encoded []byte) []byte {
	t.Helper()
	envelope := versionedEnvelope(t, encoded)
	block, err := aes.NewCipher(material.Key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce, nonceErr := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	ciphertext, ciphertextErr := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if nonceErr != nil || ciphertextErr != nil {
		t.Fatal("fixture has invalid base64")
	}
	payload, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		t.Fatal("fixture authentication failed")
	}
	t.Cleanup(func() { clearBytes(payload) })
	return payload
}

// 模拟旧 reader 的明确版本 gate，不能用新 helper 假装旧 reader。
func versionedLegacyDecode(t *testing.T, material KeyMaterial, aad string, encoded []byte) (Credentials, error) {
	t.Helper()
	envelope := versionedEnvelope(t, encoded)
	if envelope.Version != 1 || envelope.Algorithm != "AES-256-GCM" || envelope.KeyVersion <= 0 {
		return Credentials{}, errors.New("unsupported fixture envelope")
	}
	var decoded struct {
		Username string `json:"username,omitempty"`
		Password string `json:"password,omitempty"`
		Token    string `json:"token,omitempty"`
		Database string `json:"database,omitempty"`
	}
	if err := json.Unmarshal(versionedOpenFixture(t, material, []byte(aad), encoded), &decoded); err != nil {
		return Credentials{}, errors.New("invalid fixture payload")
	}
	return Credentials{Username: decoded.Username, Password: decoded.Password, Token: decoded.Token, Database: decoded.Database}, nil
}

func assertVersionedFailure(t *testing.T, value credentialValue, err error, code ErrorCode) {
	t.Helper()
	if value != (credentialValue{}) || ErrorCodeOf(err) != code || err.Error() != string(code) {
		t.Fatalf("expected zero value and fixed %s error", code)
	}
	var safe *DatabaseError
	if !errors.As(err, &safe) || safe.Cause != nil || safe.SourceID != "" || safe.Endpoint != "" || safe.Database != "" {
		t.Fatal("codec error retained context or a raw cause")
	}
}

func TestCredentialVersionedFixedV1Compatibility(t *testing.T) {
	t.Parallel()
	keys, material := versionedFixtureKeys(t)
	if aad := credentialAAD("fixture", DriverMySQL, "db.invalid:3306/db"); aad != versionedFixtureAAD {
		t.Fatal("legacy source binding changed")
	}
	if fixture := versionedSealFixture(t, material, 1, []byte(versionedFixtureAAD), versionedFixturePayload); string(fixture) != versionedFixtureV1 {
		t.Fatal("independent fixed v1 fixture changed")
	}
	encoded := []byte(versionedFixtureV1)
	before := append([]byte(nil), encoded...)
	want := versionedFixtureCredentials()
	got, err := DecryptCredentials(context.Background(), keys, versionedFixtureAAD, encoded)
	if err != nil || got != want {
		t.Fatal("public reader cannot read fixed old v1 ciphertext")
	}
	value, err := decryptCredentialValue(context.Background(), keys, versionedFixtureAAD, encoded)
	if err != nil || value.legacyCredentials != want || value.tls != nil || !bytes.Equal(before, encoded) {
		t.Fatal("private reader changed fixed v1 data")
	}
	for name, encrypt := range map[string]func(Credentials) ([]byte, int, error){
		"public": func(c Credentials) ([]byte, int, error) {
			return EncryptCredentials(context.Background(), keys, versionedFixtureAAD, c)
		},
		"rotation": func(c Credentials) ([]byte, int, error) {
			return encryptWithKey(context.Background(), material, versionedFixtureAAD, c)
		},
		"private": func(c Credentials) ([]byte, int, error) {
			return encryptCredentialValueWithKey(context.Background(), material, versionedFixtureAAD, credentialValue{legacyCredentials: c})
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, c := range []Credentials{want, {}, {Password: "<synthetic>&\u2028中文", Database: "db"}} {
				encoded, version, err := encrypt(c)
				if err != nil || version != material.Version || versionedEnvelope(t, encoded).Version != 1 {
					t.Fatal("nil policy no longer writes v1")
				}
				got, err := versionedLegacyDecode(t, material, versionedFixtureAAD, encoded)
				if err != nil || got != c {
					t.Fatal("new v1 ciphertext is not readable by old codec")
				}
				// 比较完整明文字节，包含字段顺序、omitempty 和 JSON 转义。
				wantPayload, err := json.Marshal(struct {
					Username string `json:"username,omitempty"`
					Password string `json:"password,omitempty"`
					Token    string `json:"token,omitempty"`
					Database string `json:"database,omitempty"`
				}{c.Username, c.Password, c.Token, c.Database})
				if err != nil || !bytes.Equal(wantPayload, versionedOpenFixture(t, material, []byte(versionedFixtureAAD), encoded)) {
					t.Fatal("v1 payload byte contract changed")
				}
				clearBytes(wantPayload)
			}
		})
	}
}

func TestCredentialVersionedV1UnknownFieldsRemainCompatible(t *testing.T) {
	t.Parallel()
	keys, material := versionedFixtureKeys(t)
	for _, payload := range []string{
		`{"username":"fixture-user","unknown":{"future":true},"tls":{"mode":"prefer"}}`,
		`{"Username":"fixture-user","password":null}`,
		`null`,
	} {
		encoded := versionedSealFixture(t, material, 1, []byte(versionedFixtureAAD), payload)
		want, err := versionedLegacyDecode(t, material, versionedFixtureAAD, encoded)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decryptCredentialValue(context.Background(), keys, versionedFixtureAAD, encoded)
		if err != nil || got.legacyCredentials != want || got.tls != nil {
			t.Fatal("v1 plain Unmarshal compatibility changed")
		}
	}
}

func TestCredentialVersionedAADExactBytes(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"", "v1", "opaque\x00/中文/v1", strings.Repeat("a", 257), strings.Repeat("b", 65537)} {
		got, err := credentialEnvelopeAAD(base, 1)
		if err != nil || string(got) != base {
			t.Fatal("v1 AAD changed")
		}
		got, err = credentialEnvelopeAAD(base, 2)
		want := append([]byte("ncp/database-credentials/envelope\x00"), 0, 0, 0, 2)
		for shift := 56; shift >= 0; shift -= 8 {
			want = append(want, byte(uint64(len(base))>>shift))
		}
		want = append(want, []byte(base)...)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("v2 domain/version/byte-length contract changed")
		}
	}
	for _, version := range []int{-1, 0, 3, 256} {
		aad, err := credentialEnvelopeAAD("secret-sentinel", version)
		if aad != nil || ErrorCodeOf(err) != CodeCredentialCorrupt || errors.Unwrap(err) != nil {
			t.Fatal("unsupported AAD version must fail closed")
		}
	}
}

func TestCredentialVersionedV2RoundTripAndCopies(t *testing.T) {
	t.Parallel()
	keys, material := versionedFixtureKeys(t)
	ca := newDatabaseTLSCA(t, nil)
	for _, policy := range []databaseTLSPolicy{
		{Mode: databaseTLSVerifyFull},
		{Mode: databaseTLSVerifyFull, ServerName: "DB.Example."},
		{Mode: databaseTLSVerifyFull, CAPEM: ca.pem},
		{Mode: databaseTLSVerifyFull, CAPEM: ca.pem, ServerName: "2001:db8::1"},
	} {
		value := credentialValue{legacyCredentials: versionedFixtureCredentials(), tls: &policy}
		before := cloneCredentialValue(value)
		if before.tls == value.tls || *before.tls != *value.tls {
			t.Fatal("policy clone aliases the original")
		}
		// 不含任何合法 source/host 的 opaque AAD 仍可使用 intrinsic codec。
		aad := "opaque\x00/not-a-host/sqlite/中文"
		encoded, version, err := encryptCredentialValueWithKey(context.Background(), material, aad, value)
		if err != nil || version != material.Version || versionedEnvelope(t, encoded).Version != 2 || *value.tls != *before.tls {
			t.Fatal("valid private policy must write v2 without changing inputs")
		}
		for _, secret := range []string{value.legacyCredentials.Password, value.legacyCredentials.Token, "verify-full", "CERTIFICATE"} {
			if bytes.Contains(encoded, []byte(secret)) {
				t.Fatal("envelope contains plaintext credential or policy")
			}
		}
		got, err := decryptCredentialValue(context.Background(), keys, aad, encoded)
		if err != nil || !reflect.DeepEqual(got, value) || got.tls == value.tls {
			t.Fatal("private v2 round trip lost data or shared policy")
		}
		boundAAD, err := credentialEnvelopeAAD(aad, 2)
		if err != nil {
			t.Fatal(err)
		}
		payload := versionedOpenFixture(t, material, boundAAD, encoded)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(payload, &fields); err != nil || fields["tls"] == nil || fields["TLS"] != nil {
			t.Fatal("v2 policy field is not canonical")
		}
		var tlsFields map[string]json.RawMessage
		if err := json.Unmarshal(fields["tls"], &tlsFields); err != nil || string(tlsFields["mode"]) != `"verify-full"` {
			t.Fatal("v2 mode field is not canonical")
		}
		if policy.CAPEM != "" && tlsFields["caPEM"] == nil || policy.ServerName != "" && tlsFields["serverName"] == nil {
			t.Fatal("v2 optional policy fields are not canonical")
		}
		got.tls.Mode, got.tls.CAPEM, got.tls.ServerName = "prefer", "changed", "changed"
		if *value.tls != *before.tls {
			t.Fatal("decoded policy aliases input")
		}
		again, err := decryptCredentialValue(context.Background(), keys, aad, encoded)
		if err != nil || !reflect.DeepEqual(again, before) || again.tls == got.tls {
			t.Fatal("independent decrypts share mutable policy")
		}
		public, err := DecryptCredentials(context.Background(), keys, aad, encoded)
		if err != nil || !reflect.DeepEqual(public, before.credentials()) || public.TLS == before.tls {
			t.Fatal("public reader lost or aliased v2 policy")
		}
		old, err := versionedLegacyDecode(t, material, aad, encoded)
		if old != (Credentials{}) || err == nil {
			t.Fatal("old v1 decoder accepted v2")
		}
	}
}

func TestCredentialVersionedInvalidPolicies(t *testing.T) {
	t.Parallel()
	keys, material := versionedFixtureKeys(t)
	nonCA := newDatabaseTLSCA(t, func(c *x509.Certificate) { c.IsCA = false })
	policies := []*databaseTLSPolicy{nil, {},
		{Mode: "require"}, {Mode: "prefer"}, {Mode: "verify-ca"}, {Mode: "VERIFY-FULL"},
		{Mode: databaseTLSVerifyFull, CAPEM: "private-CA-secret-sentinel"},
		{Mode: databaseTLSVerifyFull, CAPEM: "   "},
		{Mode: databaseTLSVerifyFull, CAPEM: nonCA.pem},
		{Mode: databaseTLSVerifyFull, CAPEM: strings.Repeat("x", databaseTLSMaxCABytes+1)},
		{Mode: databaseTLSVerifyFull, ServerName: "*.example"},
		{Mode: databaseTLSVerifyFull, ServerName: "db.example:5432"},
		{Mode: databaseTLSVerifyFull, ServerName: "fe80::1%eth0"},
	}
	aad, err := credentialEnvelopeAAD(versionedFixtureAAD, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i, policy := range policies {
		t.Run(fmt.Sprintf("policy_%d", i), func(t *testing.T) {
			if err := validateRequiredDatabaseTLSPolicy(policy); err != errDatabaseTLSConfigurationInvalid || errors.Unwrap(err) != nil {
				t.Fatal("intrinsic validator accepted invalid or absent policy")
			}
			if policy != nil {
				encoded, version, err := encryptCredentialValueWithKey(context.Background(), material, versionedFixtureAAD,
					credentialValue{legacyCredentials: versionedFixtureCredentials(), tls: policy})
				if encoded != nil || version != 0 {
					t.Fatal("invalid policy produced ciphertext")
				}
				assertVersionedFailure(t, credentialValue{}, err, CodeCredentialCorrupt)
			}
			payload, err := json.Marshal(credentialPayloadV2{TLS: policy})
			if err != nil {
				t.Fatal(err)
			}
			encoded := versionedSealFixture(t, material, 2, aad, string(payload))
			clearBytes(payload)
			got, err := decryptCredentialValue(context.Background(), keys, versionedFixtureAAD, encoded)
			assertVersionedFailure(t, got, err, CodeCredentialCorrupt)
		})
	}
}

func TestCredentialVersionedV2StrictPayload(t *testing.T) {
	t.Parallel()
	keys, material := versionedFixtureKeys(t)
	aad, err := credentialEnvelopeAAD(versionedFixtureAAD, 2)
	if err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string]string{
		"missing":             `{"username":"secret-sentinel"}`,
		"null":                `{"username":"secret-sentinel","tls":null}`,
		"empty":               `{"tls":{}}`,
		"null mode":           `{"tls":{"mode":null}}`,
		"unknown root":        `{"tls":{"mode":"verify-full"},"future":"secret-sentinel"}`,
		"unknown nested":      `{"tls":{"mode":"verify-full","insecureSkipVerify":true}}`,
		"unknown before mode": `{"tls":{"secret-sentinel":true,"mode":"verify-full"}}`,
		"wrong type":          `{"tls":{"mode":"verify-full","caPEM":42}}`,
		"null root":           `null`,
		"array root":          `[]`,
		"malformed":           `{"password":"secret-sentinel","tls":`,
		"second object":       `{"tls":{"mode":"verify-full"}} {}`,
		"second null":         `{"tls":{"mode":"verify-full"}} null`,
		"trailing garbage":    `{"tls":{"mode":"verify-full"}} secret-sentinel`,
	} {
		t.Run(name, func(t *testing.T) {
			encoded := versionedSealFixture(t, material, 2, aad, payload)
			got, err := decryptCredentialValue(context.Background(), keys, versionedFixtureAAD, encoded)
			assertVersionedFailure(t, got, err, CodeCredentialCorrupt)
		})
	}
	encoded := versionedSealFixture(t, material, 2, aad, "{\"tls\":{\"mode\":\"verify-full\"}} \n\t\r")
	got, err := decryptCredentialValue(context.Background(), keys, versionedFixtureAAD, encoded)
	if err != nil || got.tls == nil || got.tls.Mode != databaseTLSVerifyFull {
		t.Fatal("valid trailing JSON whitespace must be accepted")
	}
}

func TestCredentialVersionedTamperingAndNoAADRetry(t *testing.T) {
	t.Parallel()
	keys, material := versionedFixtureKeys(t)
	for _, policy := range []*databaseTLSPolicy{nil, {Mode: databaseTLSVerifyFull}} {
		encoded, _, err := encryptCredentialValueWithKey(context.Background(), material, versionedFixtureAAD,
			credentialValue{legacyCredentials: versionedFixtureCredentials(), tls: policy})
		if err != nil {
			t.Fatal(err)
		}
		for _, aad := range []string{"", versionedFixtureAAD + "x",
			credentialAAD("other-source", DriverMySQL, "db.invalid:3306/db"),
			credentialAAD("fixture", DriverPostgreSQL, "db.invalid:3306/db"),
			credentialAAD("fixture", DriverMySQL, "other.invalid:3306/db"),
			credentialAAD("fixture", DriverMySQL, "db.invalid:3306/other-db"),
		} {
			got, err := decryptCredentialValue(context.Background(), keys, aad, encoded)
			assertVersionedFailure(t, got, err, CodeCredentialCorrupt)
		}
		original := versionedEnvelope(t, encoded)
		for name, change := range map[string]func(*encryptedCredentialEnvelope){
			"version flip":        func(e *encryptedCredentialEnvelope) { e.Version = 3 - e.Version },
			"version missing":     func(e *encryptedCredentialEnvelope) { e.Version = 0 },
			"version unsupported": func(e *encryptedCredentialEnvelope) { e.Version = 3 },
			"algorithm":           func(e *encryptedCredentialEnvelope) { e.Algorithm = "secret-sentinel" },
			"key version":         func(e *encryptedCredentialEnvelope) { e.KeyVersion = -1 },
			"nonce base64":        func(e *encryptedCredentialEnvelope) { e.Nonce = "%secret-sentinel" },
			"nonce size":          func(e *encryptedCredentialEnvelope) { e.Nonce = "AA" },
			"ciphertext base64":   func(e *encryptedCredentialEnvelope) { e.Ciphertext = "%secret-sentinel" },
			"ciphertext short":    func(e *encryptedCredentialEnvelope) { e.Ciphertext = "AA" },
			"ciphertext auth": func(e *encryptedCredentialEnvelope) {
				ct, _ := base64.RawStdEncoding.DecodeString(e.Ciphertext)
				ct[0] ^= 1
				e.Ciphertext = base64.RawStdEncoding.EncodeToString(ct)
			},
			"nonce auth": func(e *encryptedCredentialEnvelope) {
				nonce, _ := base64.RawStdEncoding.DecodeString(e.Nonce)
				nonce[0] ^= 1
				e.Nonce = base64.RawStdEncoding.EncodeToString(nonce)
			},
		} {
			t.Run(fmt.Sprintf("v%d/%s", original.Version, name), func(t *testing.T) {
				envelope := original
				change(&envelope)
				got, err := decryptCredentialValue(context.Background(), keys, versionedFixtureAAD, versionedEncodeEnvelope(t, envelope))
				assertVersionedFailure(t, got, err, CodeCredentialCorrupt)
			})
		}
	}
	payload := `{"password":"secret-sentinel","tls":{"mode":"verify-full"}}`
	// 合法 v2 payload 却用 v1 AAD 封装；任何 fallback 都会错误接受。
	encoded := versionedSealFixture(t, material, 2, []byte(versionedFixtureAAD), payload)
	got, err := decryptCredentialValue(context.Background(), keys, versionedFixtureAAD, encoded)
	assertVersionedFailure(t, got, err, CodeCredentialCorrupt)
	v2AAD, err := credentialEnvelopeAAD(versionedFixtureAAD, 2)
	if err != nil {
		t.Fatal(err)
	}
	encoded = versionedSealFixture(t, material, 1, v2AAD, versionedFixturePayload)
	got, err = decryptCredentialValue(context.Background(), keys, versionedFixtureAAD, encoded)
	assertVersionedFailure(t, got, err, CodeCredentialCorrupt)
}

type versionedFakeKeys struct {
	material KeyMaterial
	err      error
	calls    int
}

func (p *versionedFakeKeys) Current(context.Context) (KeyMaterial, error) {
	p.calls++
	return p.material, p.err
}

func (p *versionedFakeKeys) ForVersion(context.Context, int) (KeyMaterial, error) {
	p.calls++
	return p.material, p.err
}

func TestCredentialVersionedKeyFailuresAndPublicV2(t *testing.T) {
	t.Parallel()
	_, material := versionedFixtureKeys(t)
	for _, provider := range []KeyProvider{nil,
		&versionedFakeKeys{err: errors.New("password-token-CA-secret-sentinel")},
		&versionedFakeKeys{material: KeyMaterial{Version: 7, Key: []byte("secret-sentinel")}},
		&versionedFakeKeys{material: KeyMaterial{Key: material.Key}},
	} {
		encoded, version, err := EncryptCredentials(context.Background(), provider, versionedFixtureAAD, versionedFixtureCredentials())
		if encoded != nil || version != 0 {
			t.Fatal("key failure produced ciphertext")
		}
		assertVersionedFailure(t, credentialValue{}, err, CodeKeyUnavailable)
		got, err := decryptCredentialValue(context.Background(), provider, versionedFixtureAAD, []byte(versionedFixtureV1))
		assertVersionedFailure(t, got, err, CodeKeyUnavailable)
	}
	provider := &versionedFakeKeys{material: KeyMaterial{Version: 8, Key: material.Key}}
	got, err := decryptCredentialValue(context.Background(), provider, versionedFixtureAAD, []byte(versionedFixtureV1))
	assertVersionedFailure(t, got, err, CodeKeyUnavailable)
	provider = &versionedFakeKeys{material: material}
	encoded, _, err := encryptCredentialValueWithKey(context.Background(), material, versionedFixtureAAD,
		credentialValue{legacyCredentials: versionedFixtureCredentials(), tls: &databaseTLSPolicy{Mode: databaseTLSVerifyFull}})
	if err != nil {
		t.Fatal(err)
	}
	public, err := DecryptCredentials(context.Background(), provider, versionedFixtureAAD, encoded)
	if err != nil || public.TLS == nil || public.TLS.Mode != TLSVerifyFull || provider.calls != 1 {
		t.Fatal("public reader must authenticate and preserve v2")
	}
	provider.calls = 0
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err = decryptCredentialValue(ctx, provider, versionedFixtureAAD, []byte(versionedFixtureV1))
	if got != (credentialValue{}) || ErrorCodeOf(err) != CodeTimeout || provider.calls != 0 {
		t.Fatal("cancelled decrypt performed key lookup or returned a value")
	}
	encoded, version, err := encryptCredentialValueWithKey(ctx, material, versionedFixtureAAD, credentialValue{})
	if encoded != nil || version != 0 || ErrorCodeOf(err) != CodeTimeout {
		t.Fatal("cancelled encrypt returned a value")
	}
}

func TestCredentialVersionedRotationCannotDowngradeV2(t *testing.T) {
	store := revisionTestStore(t)
	keys, material := versionedFixtureKeys(t)
	vault := activationVault(t, store, keys)
	ctx := context.Background()
	// 临时 SQLite 注入合成 v2，验证轮换继续写 v2。
	record := revisionFixture("private-v2")
	var err error
	record.Ciphertext, record.KeyVersion, err = encryptCredentialValueWithKey(ctx, material,
		credentialAAD(record.SourceID, record.Driver, record.Endpoint),
		credentialValue{legacyCredentials: versionedFixtureCredentials(), tls: &databaseTLSPolicy{Mode: databaseTLSVerifyFull}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwapCredential(ctx, revisionSnapshot(t, store, record.SourceID).Baseline, record); err != nil {
		t.Fatal(err)
	}
	before := revisionSnapshot(t, store, record.SourceID).Record
	// 已知合成新 key 直接传给 re-encryption，不调用任何 provider Rotate。
	newMaterial := KeyMaterial{Version: 8, Key: bytes.Repeat([]byte{0x24}, 32)}
	err = vault.reencryptWithKey(ctx, []StoredCredential{before}, newMaterial)
	if err != nil {
		t.Fatal(err)
	}
	after := revisionSnapshot(t, store, record.SourceID).Record
	if bytes.Equal(after.Ciphertext, before.Ciphertext) || after.KeyVersion != newMaterial.Version ||
		after.AutomaticEnabled != before.AutomaticEnabled || after.LastErrorCode != before.LastErrorCode ||
		versionedEnvelope(t, after.Ciphertext).Version != 2 {
		t.Fatal("rotation lost v2 or changed saved state")
	}
	got, err := DecryptCredentials(ctx, &versionedFakeKeys{material: newMaterial},
		credentialAAD(after.SourceID, after.Driver, after.Endpoint), after.Ciphertext)
	if err != nil || got.TLS == nil || got.TLS.Mode != TLSVerifyFull {
		t.Fatal("rotation damaged v2 payload")
	}
}

func TestCredentialVersionedProviderCancellationStaysSafe(t *testing.T) {
	t.Parallel()
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		provider := &versionedFakeKeys{err: fmt.Errorf("password-token-CA-secret-sentinel: %w", sentinel)}
		_, _, encryptErr := EncryptCredentials(context.Background(), provider, versionedFixtureAAD, versionedFixtureCredentials())
		_, decryptErr := DecryptCredentials(context.Background(), provider, versionedFixtureAAD, []byte(versionedFixtureV1))
		for _, err := range []error{encryptErr, decryptErr} {
			if ErrorCodeOf(err) != CodeKeyUnavailable || err.Error() != "key_unavailable" || !errors.Is(err, sentinel) || errors.Unwrap(err) != sentinel {
				t.Fatal("provider cancellation must preserve only its safe context sentinel")
			}
		}
	}
}
