package database

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCompileDatabaseTLSLegacy(t *testing.T) {
	t.Parallel()
	// nil 只表示保留旧路径，连未知 driver/无效 host 也留给原调用方处理。
	for _, driver := range []Driver{DriverSQLite, DriverMySQL, DriverPostgreSQL, "", "unknown"} {
		for _, host := range []string{"", "db.example", "/tmp/database.sock", "https://invalid:5432"} {
			config, err := compileDatabaseTLS(driver, host, nil)
			if config != nil || err != nil {
				t.Fatalf("legacy changed: config=%v err=%v", config, err)
			}
		}
	}
}

func TestCompileDatabaseTLSModeAndDriver(t *testing.T) {
	t.Parallel()
	for _, driver := range []Driver{DriverMySQL, DriverPostgreSQL, DriverSQLite, "", "MYSQL", "postgres", "unknown"} {
		for _, mode := range []databaseTLSMode{databaseTLSVerifyFull, "", "VERIFY-FULL", "verify-Full", " verify-full", "verify-full ", "require", "prefer", "verify-ca", "skip-verify", "disable", "false"} {
			t.Run(string(driver)+"/"+string(mode), func(t *testing.T) {
				config, err := compileDatabaseTLS(driver, "db.example", &databaseTLSPolicy{Mode: mode})
				if (driver == DriverMySQL || driver == DriverPostgreSQL) && mode == databaseTLSVerifyFull {
					if err != nil {
						t.Fatal(err)
					}
					assertDatabaseTLSDefaults(t, config, "db.example")
					if config.RootCAs != nil {
						t.Fatal("default trust must leave RootCAs nil for normal system verification")
					}
				} else {
					assertDatabaseTLSInvalid(t, config, err)
				}
			})
		}
	}
}

func TestCompileDatabaseTLSServerNames(t *testing.T) {
	t.Parallel()
	maxDNS := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	valid := []struct{ name, want string }{
		{"db.example", "db.example"},
		{"DB.Example.", "db.example"},
		{"localhost", "localhost"},
		{"a", "a"},
		{"1-db.example", "1-db.example"},
		{"123.example", "123.example"},
		{"xn--bcher-kva.example", "xn--bcher-kva.example"},
		{strings.Repeat("a", 63), strings.Repeat("a", 63)},
		{maxDNS, maxDNS},
		{maxDNS[:252] + ".", maxDNS[:252]},
		{"127.0.0.1", "127.0.0.1"},
		{"192.0.2.1", "192.0.2.1"},
		{"::1", "::1"},
		{"2001:0DB8:0:0:0:0:0:1", "2001:db8::1"},
		{"::ffff:192.0.2.1", "::ffff:192.0.2.1"},
		// 无方括号的合法 IPv6 不是 host:port；不能猜测最后一段为端口。
		{"2001:db8::1:5432", "2001:db8::1:5432"},
	}
	for _, driver := range []Driver{DriverMySQL, DriverPostgreSQL} {
		for _, tt := range valid {
			t.Run(string(driver)+"/"+tt.name, func(t *testing.T) {
				for _, override := range []bool{false, true} {
					host := tt.name
					policy := databaseTLSPolicy{Mode: databaseTLSVerifyFull}
					if override {
						host, policy.ServerName = "127.0.0.1", tt.name
					}
					beforeHost, beforePolicy := host, policy
					config, err := compileDatabaseTLS(driver, host, &policy)
					if err != nil {
						t.Fatal(err)
					}
					assertDatabaseTLSDefaults(t, config, tt.want)
					if host != beforeHost || policy != beforePolicy {
						t.Fatal("compilation must not change dial host or policy input")
					}
				}
			})
		}
	}
	invalid := []string{
		"", ".", "..", "db..example", ".db.example", "db.example..",
		"-db.example", "db-.example", "db_example", "*.example", "db.*.example",
		" db.example", "db.example ", "db\texample", "db\nexample", "db\rexample", "db\x00example", "db\x7fexample",
		"bücher.example", "db\u00a0example", "db。example", "db\xffexample",
		"https://db.example", "postgres://db.example", "user@db.example", "db.example/path", `C:\db`, "/var/run/postgresql",
		"db.example:5432", "127.0.0.1:3306", "db.example,other.example", "db.example?sslmode=disable", "db.example#x",
		"[::1]", "[::1]:5432", "fe80::1%eth0", "[fe80::1%eth0]", "fe80::1%25eth0", "2001:::1",
		"127.0.0.01", "256.1.2.3", "127.1", "127.0.0.1.", "1234", "1.2.3.4.5",
		strings.Repeat("a", 64) + ".example", maxDNS + "x", maxDNS + ".",
	}
	for i, name := range invalid {
		t.Run(fmt.Sprintf("invalid_%d", i), func(t *testing.T) {
			for _, driver := range []Driver{DriverMySQL, DriverPostgreSQL} {
				for _, override := range []string{"", "db.example"} {
					config, err := compileDatabaseTLS(driver, name, &databaseTLSPolicy{Mode: databaseTLSVerifyFull, ServerName: override})
					assertDatabaseTLSInvalid(t, config, err)
				}
				// 空覆盖有明确的缺省语义；其他非法覆盖一律失败。
				if name != "" {
					config, err := compileDatabaseTLS(driver, "127.0.0.1", &databaseTLSPolicy{Mode: databaseTLSVerifyFull, ServerName: name})
					assertDatabaseTLSInvalid(t, config, err)
				}
			}
		})
	}
}

func TestCompileDatabaseTLSRootCAs(t *testing.T) {
	t.Parallel()
	ca := newDatabaseTLSCA(t, nil)
	otherCA := newDatabaseTLSCA(t, nil)
	noKeyUsageCA := newDatabaseTLSCA(t, func(cert *x509.Certificate) { cert.KeyUsage = 0 })
	nonCA := newDatabaseTLSCA(t, func(cert *x509.Certificate) { cert.IsCA = false })
	noConstraints := newDatabaseTLSCA(t, func(cert *x509.Certificate) { cert.BasicConstraintsValid = false })
	noCertSign := newDatabaseTLSCA(t, func(cert *x509.Certificate) { cert.KeyUsage = x509.KeyUsageDigitalSignature })
	emptyKeyUsage := newDatabaseTLSCA(t, func(cert *x509.Certificate) {
		cert.ExtraExtensions = []pkix.Extension{{Id: asn1.ObjectIdentifier{2, 5, 29, 15}, Value: []byte{0x03, 0x01, 0x00}}}
	})
	privateKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("secret sentinel")}))
	headers := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: map[string]string{"Comment": "sentinel"}, Bytes: ca.cert.Raw}))
	badDER := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid DER sentinel")}))
	trailingDER := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: append(append([]byte{}, ca.cert.Raw...), 0)}))
	doubleDER := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: append(append([]byte{}, ca.cert.Raw...), ca.cert.Raw...)}))
	malformed := "-----BEGIN CERTIFICATE-----\n%%%invalid sentinel%%%\n-----END CERTIFICATE-----\n"
	tests := []struct {
		name, input string
		wantCerts   int
	}{
		{"one", ca.pem, 1},
		{"multiple", ca.pem + otherCA.pem, 2},
		{"whitespace", " \r\n\t" + ca.pem + " \t\r\n" + otherCA.pem + "\t\n", 2},
		{"crlf", strings.ReplaceAll(ca.pem, "\n", "\r\n"), 1},
		{"no final newline", strings.TrimSuffix(ca.pem, "\n"), 1},
		{"no key usage extension", noKeyUsageCA.pem, 1},
		{"64KiB boundary", ca.pem + strings.Repeat(" ", databaseTLSMaxCABytes-len(ca.pem)), 1},
		{"32 blocks including duplicates", strings.Repeat(ca.pem, databaseTLSMaxCACerts), 1},
		{"whitespace only", " \r\n\t", 0},
		{"URL", "https://example.test/ca.pem", 0},
		{"file path", "/etc/ssl/certs/sentinel.pem", 0},
		{"raw DER", string(ca.cert.Raw), 0},
		{"invalid DER", badDER, 0},
		{"trailing DER", trailingDER, 0},
		{"two DER certificates in one block", doubleDER, 0},
		{"empty DER", "-----BEGIN CERTIFICATE-----\n-----END CERTIFICATE-----\n", 0},
		{"bad base64", malformed, 0},
		{"bad then good", malformed + ca.pem, 0},
		{"good then bad", ca.pem + malformed, 0},
		{"nested begin", "-----BEGIN CERTIFICATE-----\n" + ca.pem, 0},
		{"missing end", strings.Replace(ca.pem, "-----END CERTIFICATE-----\n", "", 1), 0},
		{"mismatched end then good", strings.Replace(ca.pem, "END CERTIFICATE", "END PRIVATE KEY", 1) + ca.pem, 0},
		{"missing end then good", strings.Replace(ca.pem, "-----END CERTIFICATE-----\n", "", 1) + otherCA.pem, 0},
		{"private key", privateKey, 0},
		{"certificate then private key", ca.pem + privateKey, 0},
		{"private key then certificate", privateKey + ca.pem, 0},
		{"other block", strings.ReplaceAll(ca.pem, "CERTIFICATE", "TRUSTED CERTIFICATE"), 0},
		{"headers", headers, 0},
		{"prefix", "sentinel\n" + ca.pem, 0},
		{"between", ca.pem + "sentinel\n" + otherCA.pem, 0},
		{"trailing", ca.pem + "sentinel", 0},
		{"nul", ca.pem + "\x00", 0},
		{"unicode whitespace", ca.pem + "\u00a0", 0},
		{"extra end line bytes", strings.Replace(ca.pem, "-----END CERTIFICATE-----", "-----END CERTIFICATE-----sentinel", 1), 0},
		{"same line blocks", strings.TrimSuffix(ca.pem, "\n") + otherCA.pem, 0},
		{"non CA", nonCA.pem, 0},
		{"missing basic constraints", noConstraints.pem, 0},
		{"no certificate signing usage", noCertSign.pem, 0},
		{"present empty key usage", emptyKeyUsage.pem, 0},
		{"over byte budget", ca.pem + strings.Repeat(" ", databaseTLSMaxCABytes-len(ca.pem)+1), 0},
		{"over block budget", strings.Repeat(ca.pem, databaseTLSMaxCACerts+1), 0},
	}
	for _, driver := range []Driver{DriverMySQL, DriverPostgreSQL} {
		for _, tt := range tests {
			t.Run(string(driver)+"/"+tt.name, func(t *testing.T) {
				config, err := compileDatabaseTLS(driver, "db.example", &databaseTLSPolicy{Mode: databaseTLSVerifyFull, CAPEM: tt.input})
				if tt.wantCerts == 0 {
					assertDatabaseTLSInvalid(t, config, err)
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				assertDatabaseTLSDefaults(t, config, "db.example")
				if config.RootCAs == nil || len(config.RootCAs.Subjects()) != tt.wantCerts {
					t.Fatal("custom trust must contain exactly the supplied unique certificates")
				}
			})
		}
	}
}

func TestCompileDatabaseTLSIndependentConfigs(t *testing.T) {
	t.Parallel()
	ca := newDatabaseTLSCA(t, nil)
	otherCA := newDatabaseTLSCA(t, nil)
	for _, custom := range []bool{false, true} {
		policy := databaseTLSPolicy{Mode: databaseTLSVerifyFull, ServerName: "db.example"}
		if custom {
			policy.CAPEM = ca.pem
		}
		first, err := compileDatabaseTLS(DriverMySQL, "127.0.0.1", &policy)
		if err != nil {
			t.Fatal(err)
		}
		second, err := compileDatabaseTLS(DriverPostgreSQL, "127.0.0.1", &policy)
		if err != nil {
			t.Fatal(err)
		}
		if first == second || (custom && first.RootCAs == second.RootCAs) {
			t.Fatal("compiled mutable objects must not be shared")
		}
		policy.Mode, policy.ServerName, policy.CAPEM = "disable", "changed.example", otherCA.pem
		assertDatabaseTLSDefaults(t, first, "db.example")
		assertDatabaseTLSDefaults(t, second, "db.example")
		first.ServerName, first.InsecureSkipVerify = "changed.example", true
		assertDatabaseTLSDefaults(t, second, "db.example")
		if custom {
			first.RootCAs.AddCert(otherCA.cert)
			if len(first.RootCAs.Subjects()) != 2 || len(second.RootCAs.Subjects()) != 1 {
				t.Fatal("changing one root pool must not change another")
			}
		}
	}
}

func TestCompileDatabaseTLSConcurrent(t *testing.T) {
	t.Parallel()
	ca := newDatabaseTLSCA(t, nil)
	policy := &databaseTLSPolicy{Mode: databaseTLSVerifyFull, CAPEM: ca.pem}
	const count = 16
	configs := make([]*tls.Config, count)
	var workers sync.WaitGroup
	for i := range configs {
		workers.Go(func() {
			config, err := compileDatabaseTLS(DriverMySQL, "db.example", policy)
			if err != nil {
				t.Error(err)
				return
			}
			configs[i] = config
		})
	}
	workers.Wait()
	for i, config := range configs {
		assertDatabaseTLSDefaults(t, config, "db.example")
		for _, previous := range configs[:i] {
			if config == previous || config.RootCAs == previous.RootCAs {
				t.Fatal("parallel compilation shared mutable state")
			}
		}
	}
}

// 只用内存证书及 x509.Verify 证明配置的信任/身份语义；没有 TLS 握手、
// listener 或数据库集成，不能据此宣称数据库 TLS 功能已可用。
func TestCompileDatabaseTLSCertificateVerification(t *testing.T) {
	t.Parallel()
	ca := newDatabaseTLSCA(t, nil)
	otherCA := newDatabaseTLSCA(t, nil)
	dnsLeaf := newDatabaseTLSLeaf(t, ca, nil)
	ipLeaf := newDatabaseTLSLeaf(t, ca, func(cert *x509.Certificate) {
		cert.DNSNames = nil
		cert.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	})
	unknownLeaf := newDatabaseTLSLeaf(t, otherCA, nil)
	cnOnly := newDatabaseTLSLeaf(t, ca, func(cert *x509.Certificate) { cert.DNSNames = nil })
	expired := newDatabaseTLSLeaf(t, ca, func(cert *x509.Certificate) { cert.NotAfter = databaseTLSTestTime.Add(-time.Hour) })
	notYetValid := newDatabaseTLSLeaf(t, ca, func(cert *x509.Certificate) { cert.NotBefore = databaseTLSTestTime.Add(time.Hour) })
	clientOnly := newDatabaseTLSLeaf(t, ca, func(cert *x509.Certificate) { cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth} })
	intermediate := newDatabaseTLSLeaf(t, ca, func(cert *x509.Certificate) {
		cert.IsCA, cert.BasicConstraintsValid = true, true
		cert.KeyUsage, cert.ExtKeyUsage = x509.KeyUsageCertSign, nil
	})
	tests := []struct {
		name, host, override, roots string
		leaf                        *x509.Certificate
		want                        string
	}{
		{"DNS SAN", "db.example", "", ca.pem, dnsLeaf, ""},
		{"DNS case and terminal dot", "DB.EXAMPLE.", "", ca.pem, dnsLeaf, ""},
		{"loopback with DNS override", "127.0.0.1", "db.example", ca.pem, dnsLeaf, ""},
		{"IPv4 SAN", "127.0.0.1", "", ca.pem, ipLeaf, ""},
		{"IPv6 SAN", "::1", "", ca.pem, ipLeaf, ""},
		{"IPv4 mapped SAN", "::ffff:127.0.0.1", "", ca.pem, ipLeaf, ""},
		{"multiple roots", "db.example", "", ca.pem + otherCA.pem, unknownLeaf, ""},
		{"wrong DNS SAN", "wrong.example", "", ca.pem, dnsLeaf, "hostname"},
		{"wrong IP SAN", "127.0.0.2", "", ca.pem, ipLeaf, "hostname"},
		{"DNS SAN cannot verify IP", "127.0.0.1", "", ca.pem, dnsLeaf, "hostname"},
		{"override cannot bypass SAN", "db.example", "wrong.example", ca.pem, dnsLeaf, "hostname"},
		{"CN without SAN", "db.example", "", ca.pem, cnOnly, "hostname"},
		{"unknown CA", "db.example", "", ca.pem, unknownLeaf, "authority"},
		{"custom CA excludes other trust", "db.example", "", otherCA.pem, dnsLeaf, "authority"},
		{"expired leaf", "db.example", "", ca.pem, expired, "expired"},
		{"future leaf", "db.example", "", ca.pem, notYetValid, "expired"},
		{"client only leaf", "db.example", "", ca.pem, clientOnly, "usage"},
	}
	for _, driver := range []Driver{DriverMySQL, DriverPostgreSQL} {
		for _, tt := range tests {
			t.Run(string(driver)+"/"+tt.name, func(t *testing.T) {
				config, err := compileDatabaseTLS(driver, tt.host, &databaseTLSPolicy{Mode: databaseTLSVerifyFull, CAPEM: tt.roots, ServerName: tt.override})
				if err != nil {
					t.Fatal(err)
				}
				_, err = tt.leaf.Verify(x509.VerifyOptions{
					Roots: config.RootCAs, DNSName: config.ServerName, CurrentTime: databaseTLSTestTime,
					KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
				})
				var hostname x509.HostnameError
				var authority x509.UnknownAuthorityError
				var invalid x509.CertificateInvalidError
				switch tt.want {
				case "":
					if err != nil {
						t.Fatal(err)
					}
				case "hostname":
					if !errors.As(err, &hostname) {
						t.Fatalf("wanted hostname rejection, got %v", err)
					}
				case "authority":
					if !errors.As(err, &authority) {
						t.Fatalf("wanted trust rejection, got %v", err)
					}
				case "expired", "usage":
					if !errors.As(err, &invalid) ||
						(tt.want == "expired" && invalid.Reason != x509.Expired) ||
						(tt.want == "usage" && invalid.Reason != x509.IncompatibleUsage) {
						t.Fatalf("wanted %s rejection, got %v", tt.want, err)
					}
				}
			})
		}
	}
	// 非自签的 CA 也可由调用方显式选作信任锚；编译时不强制自签名。
	intermediatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: intermediate.Raw}))
	config, err := compileDatabaseTLS(DriverMySQL, "db.example", &databaseTLSPolicy{Mode: databaseTLSVerifyFull, CAPEM: intermediatePEM})
	if err != nil || config == nil || config.RootCAs == nil {
		t.Fatalf("explicit intermediate CA rejected: %v", err)
	}
}

var databaseTLSTestTime = time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)

type databaseTLSTestCA struct {
	cert *x509.Certificate
	key  ed25519.PrivateKey
	pem  string
}

func newDatabaseTLSCA(t *testing.T, edit func(*x509.Certificate)) databaseTLSTestCA {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "in-memory test CA"},
		NotBefore: databaseTLSTestTime.Add(-24 * time.Hour), NotAfter: databaseTLSTestTime.Add(24 * time.Hour),
		BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	if edit != nil {
		edit(template)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return databaseTLSTestCA{cert: cert, key: private, pem: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
}

func newDatabaseTLSLeaf(t *testing.T, ca databaseTLSTestCA, edit func(*x509.Certificate)) *x509.Certificate {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "db.example"},
		NotBefore: databaseTLSTestTime.Add(-2 * time.Hour), NotAfter: databaseTLSTestTime.Add(2 * time.Hour),
		DNSNames: []string{"db.example"}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if edit != nil {
		edit(template)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, public, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func assertDatabaseTLSDefaults(t *testing.T, config *tls.Config, name string) {
	t.Helper()
	if config == nil || config.ServerName != name || config.MinVersion != tls.VersionTLS12 || config.MaxVersion != 0 ||
		config.InsecureSkipVerify || config.VerifyPeerCertificate != nil || config.VerifyConnection != nil ||
		config.GetClientCertificate != nil || len(config.Certificates) != 0 || config.KeyLogWriter != nil {
		t.Fatal("compiled config must use TLS1.2+ and normal server certificate verification without credentials or bypass callbacks")
	}
}

func assertDatabaseTLSInvalid(t *testing.T, config *tls.Config, err error) {
	t.Helper()
	if config != nil || err != errDatabaseTLSConfigurationInvalid || !errors.Is(err, errDatabaseTLSConfigurationInvalid) ||
		err.Error() != "tls_configuration_invalid" || fmt.Sprintf("%+v", err) != "tls_configuration_invalid" || errors.Unwrap(err) != nil {
		t.Fatalf("invalid input must return nil config and only the fixed safe error: config=%v err=%v", config, err)
	}
}
