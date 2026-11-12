package database

import (
	"context"
	"crypto/tls"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestDatabaseTLSConnectorRejectsInvalidBeforeParsing(t *testing.T) {
	source := databaseTLSConnectorTestSource(DriverPostgreSQL)
	credentials := databaseTLSConnectorTestCredentials()
	valid := &databaseTLSPolicy{Mode: databaseTLSVerifyFull}
	tests := []struct {
		name   string
		change func(*Source, *Credentials)
		policy *databaseTLSPolicy
	}{
		{name: "nil policy"},
		{name: "empty policy", policy: &databaseTLSPolicy{}},
		{name: "prefer", policy: &databaseTLSPolicy{Mode: "prefer"}},
		{name: "require", policy: &databaseTLSPolicy{Mode: "require"}},
		{name: "wrong mode case", policy: &databaseTLSPolicy{Mode: "VERIFY-FULL"}},
		{name: "bad CA", policy: &databaseTLSPolicy{Mode: databaseTLSVerifyFull, CAPEM: "CA-secret-sentinel"}},
		{name: "bad override", policy: &databaseTLSPolicy{Mode: databaseTLSVerifyFull, ServerName: "*.example"}},
		{name: "sqlite", policy: valid, change: func(s *Source, _ *Credentials) { s.Driver = DriverSQLite }},
		{name: "unknown driver", policy: valid, change: func(s *Source, _ *Credentials) { s.Driver = "unknown" }},
		{name: "internal", policy: valid, change: func(s *Source, _ *Credentials) { s.Reachability = "container-internal" }},
		{name: "unreachable", policy: valid, change: func(s *Source, _ *Credentials) { s.Reachability = "unreachable" }},
		{name: "no evidence", policy: valid, change: func(s *Source, _ *Credentials) { s.Evidence = "none" }},
		{name: "no user", policy: valid, change: func(_ *Source, c *Credentials) { c.Username = "" }},
		{name: "no database", policy: valid, change: func(s *Source, c *Credentials) { s.DefaultDatabase, c.Database = "", "" }},
		{name: "zero port", policy: valid, change: func(s *Source, _ *Credentials) { s.Port = 0 }},
		{name: "negative port", policy: valid, change: func(s *Source, _ *Credentials) { s.Port = -1 }},
		{name: "large port", policy: valid, change: func(s *Source, _ *Credentials) { s.Port = 65536 }},
		{name: "no host", policy: valid, change: func(s *Source, _ *Credentials) { s.Host = "" }},
		{name: "multiple hosts", policy: valid, change: func(s *Source, _ *Credentials) { s.Host = "db.example,other.example" }},
		{name: "socket", policy: valid, change: func(s *Source, _ *Credentials) { s.Host = "/tmp/fake-socket" }},
		{name: "bracketed IPv6", policy: valid, change: func(s *Source, _ *Credentials) { s.Host = "[::1]" }},
		{name: "spaced host", policy: valid, change: func(s *Source, _ *Credentials) { s.Host = " db.example" }},
	}
	for _, driverName := range []Driver{DriverMySQL, DriverPostgreSQL} {
		for _, tt := range tests {
			t.Run(string(driverName)+"/"+tt.name, func(t *testing.T) {
				s, c := source, credentials
				s.Driver = driverName
				if tt.change != nil {
					tt.change(&s, &c)
				}
				// nil hooks panic if a rejection accidentally enters a parser or factory.
				connector, err := databaseTLSConnector(s, c, tt.policy, databaseTLSConnectorDeps{})
				assertDatabaseTLSConnectorInvalid(t, connector, err)
			})
		}
	}
}

func TestDatabaseTLSConnectorMySQLEffectiveOptions(t *testing.T) {
	credentials := databaseTLSConnectorTestCredentials()
	for _, host := range []string{"db.example", "127.0.0.1", "::1"} {
		t.Run(host, func(t *testing.T) {
			source := databaseTLSConnectorTestSource(DriverMySQL)
			source.Host = host
			h := newDatabaseTLSConnectorHarness(t)
			connector, err := databaseTLSConnector(source, credentials,
				&databaseTLSPolicy{Mode: databaseTLSVerifyFull, ServerName: "CERT.Example."}, h.deps)
			if err != nil {
				t.Fatal(err)
			}
			assertDatabaseTLSDefaults(t, h.mysql.TLS, "cert.example")
			if h.mysql.TLSConfig != "" || h.mysql.AllowFallbackToPlaintext {
				t.Fatal("MySQL must have instance TLS and no plaintext fallback or profile registration")
			}
			_, legacyDSN, err := connectionString(source, credentials)
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := mysql.ParseDSN(legacyDSN)
			if err != nil {
				t.Fatal("synthetic legacy DSN must parse")
			}
			actual := h.mysql.Clone()
			actual.TLS = nil
			if !reflect.DeepEqual(actual, legacy) {
				t.Fatal("strict MySQL non-TLS options differ from the effective legacy options")
			}
			if actual.AllowNativePasswords || actual.CheckConnLiveness || !actual.ParseTime || actual.MaxAllowedPacket != 0 {
				t.Fatal("literal-config legacy defaults changed")
			}
			h.assertNoDial(t, connector)
		})
	}
}

func TestDatabaseTLSConnectorPostgresControlledSeed(t *testing.T) {
	clearDatabaseTLSPostgresTestEnvironment(t)
	t.Setenv("PGPASSWORD", "fake-env-password-sentinel")
	source := databaseTLSConnectorTestSource(DriverPostgreSQL)
	source.Host = "127.0.0.1"
	credentials := databaseTLSConnectorTestCredentials()
	credentials.Username = "user:name@with spaces?x=1&service=evil"
	credentials.Database = "/db?sslmode=allow&service=evil#name"
	h := newDatabaseTLSConnectorHarness(t)
	var seed string
	h.deps.parsePostgres = func(input string, options pgx.ParseConfigOptions) (*pgx.ConnConfig, error) {
		seed = input
		for _, secret := range []string{credentials.Password, credentials.Token, "fake-env-password-sentinel"} {
			if strings.Contains(input, secret) {
				t.Fatal("parser seed contains a credential sentinel")
			}
		}
		u, err := url.Parse(input)
		if err != nil {
			t.Fatal("controlled seed is not a URL")
		}
		if password, present := u.User.Password(); !present || password != "" {
			t.Fatal("controlled seed must explicitly override PGPASSWORD with an empty password")
		}
		if u.User.Username() != credentials.Username || u.Host != "127.0.0.1:5432" {
			t.Fatal("structural input escaped incorrectly")
		}
		values := u.Query()
		wantValues := url.Values{
			"sslmode": {"disable"}, "sslrootcert": {""}, "sslcert": {""}, "sslkey": {""},
			"sslpassword": {""}, "passfile": {""}, "servicefile": {""}, "sslsni": {"1"},
			"sslnegotiation": {"postgres"}, "connect_timeout": {"0"}, "target_session_attrs": {"any"},
			"min_protocol_version": {"3.0"}, "max_protocol_version": {"3.0"},
			"channel_binding": {"prefer"}, "require_auth": {""},
		}
		if !reflect.DeepEqual(values, wantValues) {
			t.Fatal("controlled seed options changed")
		}
		wantAllowed := []string{"host", "port", "user", "password", "database"}
		for key := range wantValues {
			wantAllowed = append(wantAllowed, key)
		}
		slices.Sort(wantAllowed)
		actualAllowed := slices.Clone(options.ConnStringAllowedKeys)
		slices.Sort(actualAllowed)
		if !slices.Equal(actualAllowed, wantAllowed) || options.GetSSLPassword != nil {
			t.Fatal("parser must allow only generated options without a credential callback")
		}
		parsed, err := pgx.ParseConfigWithOptions(input, options)
		if err == nil && (parsed.Password != "" || parsed.TLSConfig != nil || len(parsed.Fallbacks) != 0) {
			t.Fatal("password-free seed inherited authentication or TLS")
		}
		return parsed, err
	}
	connector, err := databaseTLSConnector(source, credentials,
		&databaseTLSPolicy{Mode: databaseTLSVerifyFull, ServerName: "CERT.Example."}, h.deps)
	if err != nil {
		t.Fatal(err)
	}
	assertDatabaseTLSPostgresConfig(t, h.postgres, source, credentials, "cert.example")
	if h.postgres.ConnString() != seed || strings.Contains(h.postgres.ConnString(), credentials.Password) || strings.Contains(h.postgres.ConnString(), credentials.Token) {
		t.Fatal("retained seed must be unchanged and free of real credential values")
	}
	h.assertNoDial(t, connector)
}

func TestDatabaseTLSConnectorPostgresEnvironmentNeutralized(t *testing.T) {
	clearDatabaseTLSPostgresTestEnvironment(t)
	path := filepath.Join(t.TempDir(), "synthetic-passfile")
	if err := os.WriteFile(path, []byte("*:*:*:*:fake-file-password-sentinel\n"), 0600); err != nil {
		t.Fatal(err)
	}
	environments := []struct{ key, value string }{
		{"PGSSLMODE", "prefer"}, {"PGSSLMODE", "allow"}, {"PGSSLMODE", "disable"},
		{"PGSSLROOTCERT", "system"}, {"PGSSLROOTCERT", path}, {"PGSSLROOTCERT", "/nonexistent/fake-ca-sentinel"},
		{"PGSSLCERT", path}, {"PGSSLKEY", path}, {"PGSSLPASSWORD", "fake-client-password-sentinel"},
		{"PGPASSFILE", path}, {"PGPASSWORD", "fake-env-password-sentinel"}, {"PGSERVICEFILE", path},
		{"PGSSLNEGOTIATION", "direct"}, {"PGSSLSNI", "0"},
		{"PGHOST", "/fake/socket"}, {"PGHOST", "evil.example,other.example"},
		{"PGPORT", "invalid-port-sentinel"}, {"PGDATABASE", "fake-env-database-sentinel"},
		{"PGUSER", "fake-env-user-sentinel"}, {"PGOPTIONS", "-c search_path=fake_schema"},
		{"PGTZ", "Fake/Zone"}, {"PGAPPNAME", "fake-app-sentinel"},
		{"PGCONNECT_TIMEOUT", "invalid-timeout-sentinel"}, {"PGTARGETSESSIONATTRS", "read-only"},
		{"PGMINPROTOCOLVERSION", "3.2"}, {"PGMAXPROTOCOLVERSION", "invalid-protocol-sentinel"},
		{"PGCHANNELBINDING", "disable"}, {"PGREQUIREAUTH", "invalid-auth-sentinel"},
	}
	ca := newDatabaseTLSCA(t, nil)
	for i, env := range environments {
		t.Run(fmt.Sprintf("%s_%d", env.key, i), func(t *testing.T) {
			t.Setenv(env.key, env.value)
			for _, customCA := range []string{"", ca.pem} {
				for _, secret := range []string{"", "fake-real-password-sentinel"} {
					h := newDatabaseTLSConnectorHarness(t)
					source := databaseTLSConnectorTestSource(DriverPostgreSQL)
					credentials := databaseTLSConnectorTestCredentials()
					credentials.Password, credentials.Token = secret, ""
					connector, err := databaseTLSConnector(source, credentials,
						&databaseTLSPolicy{Mode: databaseTLSVerifyFull, CAPEM: customCA}, h.deps)
					if err != nil {
						t.Fatal(err)
					}
					assertDatabaseTLSPostgresConfig(t, h.postgres, source, credentials, source.Host)
					if (h.postgres.TLSConfig.RootCAs == nil) != (customCA == "") {
						t.Fatal("environment replaced the selected CA trust")
					}
					if customCA != "" && len(h.postgres.TLSConfig.RootCAs.Subjects()) != 1 {
						t.Fatal("custom CA trust was not retained")
					}
					if strings.Contains(h.postgres.ConnString(), "sentinel") {
						t.Fatal("environment or credential sentinel entered retained seed")
					}
					h.assertNoDial(t, connector)
				}
			}
		})
	}
	// 所有已知 PG* 变量同时污染时仍不继承；不修改生产构造器的环境。
	t.Run("combined", func(t *testing.T) {
		for _, env := range environments {
			t.Setenv(env.key, env.value)
		}
		h := newDatabaseTLSConnectorHarness(t)
		source, credentials := databaseTLSConnectorTestSource(DriverPostgreSQL), databaseTLSConnectorTestCredentials()
		connector, err := databaseTLSConnector(source, credentials, &databaseTLSPolicy{Mode: databaseTLSVerifyFull}, h.deps)
		if err != nil {
			t.Fatal(err)
		}
		assertDatabaseTLSPostgresConfig(t, h.postgres, source, credentials, source.Host)
		h.assertNoDial(t, connector)
	})
}

func TestDatabaseTLSConnectorPostgresServiceFailsClosed(t *testing.T) {
	clearDatabaseTLSPostgresTestEnvironment(t)
	for _, service := range []string{"fake-service-sentinel", " "} {
		t.Run(service, func(t *testing.T) {
			h := newDatabaseTLSConnectorHarness(t)
			h.deps.getenv = func(key string) string {
				if key != "PGSERVICE" {
					t.Fatal("unexpected environment read")
				}
				return service
			}
			h.deps.parsePostgres = func(string, pgx.ParseConfigOptions) (*pgx.ConnConfig, error) {
				t.Fatal("PGSERVICE must reject before parsing")
				return nil, nil
			}
			connector, err := databaseTLSConnector(databaseTLSConnectorTestSource(DriverPostgreSQL), databaseTLSConnectorTestCredentials(),
				&databaseTLSPolicy{Mode: databaseTLSVerifyFull}, h.deps)
			assertDatabaseTLSConnectorInvalid(t, connector, err)
			if h.postgres != nil {
				t.Fatal("service rejection reached connector")
			}
		})
	}
	t.Run("environment changes after guard", func(t *testing.T) {
		// 已知假文件若被读取会给出可用配置；空 servicefile 必须使解析失败。
		path := filepath.Join(t.TempDir(), "synthetic-service")
		if err := os.WriteFile(path, []byte("[fake-service]\nhost=evil.example\npassword=fake-file-password-sentinel\nsslmode=disable\n"), 0600); err != nil {
			t.Fatal(err)
		}
		h := newDatabaseTLSConnectorHarness(t)
		h.deps.parsePostgres = func(seed string, options pgx.ParseConfigOptions) (*pgx.ConnConfig, error) {
			t.Setenv("PGSERVICE", "fake-service")
			t.Setenv("PGSERVICEFILE", path)
			return pgx.ParseConfigWithOptions(seed, options)
		}
		connector, err := databaseTLSConnector(databaseTLSConnectorTestSource(DriverPostgreSQL), databaseTLSConnectorTestCredentials(),
			&databaseTLSPolicy{Mode: databaseTLSVerifyFull}, h.deps)
		assertDatabaseTLSConnectorInvalid(t, connector, err)
		if h.postgres != nil {
			t.Fatal("late service configuration reached connector")
		}
	})
}

func TestDatabaseTLSConnectorErrorsAreFixed(t *testing.T) {
	clearDatabaseTLSPostgresTestEnvironment(t)
	credentials := databaseTLSConnectorTestCredentials()
	for _, testCase := range []string{"parser error", "nil parsed config", "mysql factory error", "nil mysql connector", "nil postgres connector"} {
		t.Run(testCase, func(t *testing.T) {
			h := newDatabaseTLSConnectorHarness(t)
			source := databaseTLSConnectorTestSource(DriverPostgreSQL)
			rawError := fmt.Errorf("fake raw error: postgres://user:%s@secret-endpoint-sentinel/db: %s", credentials.Password, credentials.Token)
			switch testCase {
			case "parser error", "nil parsed config":
				h.deps.parsePostgres = func(seed string, _ pgx.ParseConfigOptions) (*pgx.ConnConfig, error) {
					if strings.Contains(seed, credentials.Password) || strings.Contains(seed, credentials.Token) {
						t.Fatal("credential leaked before parse success")
					}
					if testCase == "parser error" {
						return nil, rawError
					}
					return nil, nil
				}
			case "mysql factory error", "nil mysql connector":
				source.Driver = DriverMySQL
				h.deps.newMySQL = func(*mysql.Config) (driver.Connector, error) {
					if testCase == "mysql factory error" {
						return h.connector, rawError
					}
					return nil, nil
				}
			case "nil postgres connector":
				h.deps.newPostgres = func(pgx.ConnConfig) driver.Connector { return nil }
			}
			connector, err := databaseTLSConnector(source, credentials, &databaseTLSPolicy{Mode: databaseTLSVerifyFull}, h.deps)
			assertDatabaseTLSConnectorInvalid(t, connector, err)
			if errors.Is(err, rawError) || strings.Contains(fmt.Sprintf("%+v", err), "sentinel") {
				t.Fatal("raw error or secret was retained")
			}
		})
	}
}

func TestDatabaseTLSConnectorExplicitInputsAndOwnership(t *testing.T) {
	clearDatabaseTLSPostgresTestEnvironment(t)
	ca, otherCA := newDatabaseTLSCA(t, nil), newDatabaseTLSCA(t, nil)
	for _, driverName := range []Driver{DriverMySQL, DriverPostgreSQL} {
		t.Run(string(driverName), func(t *testing.T) {
			policy := &databaseTLSPolicy{Mode: databaseTLSVerifyFull, CAPEM: ca.pem, ServerName: "CERT.Example."}
			source := databaseTLSConnectorTestSource(driverName)
			source.Host, source.Location = "::1", "ignored.example:15432/ignored"
			credentials := databaseTLSConnectorTestCredentials()
			credentials.Username, credentials.Password, credentials.Database = "user:literal@name", "", ""
			source.DefaultDatabase = " default_db "
			var configs []*tls.Config
			for i := 0; i < 2; i++ {
				h := newDatabaseTLSConnectorHarness(t)
				connector, err := databaseTLSConnector(source, credentials, policy, h.deps)
				if err != nil {
					t.Fatal(err)
				}
				if driverName == DriverMySQL {
					if h.mysql.User != credentials.Username || h.mysql.Passwd != credentials.Token || h.mysql.DBName != "default_db" || h.mysql.Addr != "[::1]:15432" {
						t.Fatal("MySQL explicit authentication or endpoint changed")
					}
					configs = append(configs, h.mysql.TLS)
				} else {
					if h.postgres.Host != "::1" || h.postgres.Port != 15432 || h.postgres.User != credentials.Username || h.postgres.Password != credentials.Token || h.postgres.Database != "default_db" {
						t.Fatal("PostgreSQL explicit authentication or endpoint changed")
					}
					configs = append(configs, h.postgres.TLSConfig)
				}
				h.assertNoDial(t, connector)
			}
			if configs[0] == configs[1] || configs[0].RootCAs == configs[1].RootCAs {
				t.Fatal("connector TLS configs or CA pools alias")
			}
			policy.Mode, policy.CAPEM, policy.ServerName = "prefer", "changed", "changed.example"
			configs[0].ServerName = "mutated.example"
			configs[0].RootCAs.AddCert(otherCA.cert)
			assertDatabaseTLSDefaults(t, configs[1], "cert.example")
			if len(configs[1].RootCAs.Subjects()) != 1 {
				t.Fatal("mutation affected another connector's trust")
			}
		})
	}
}

func TestDatabaseTLSConnectorPostgresClearsFallbacksAndCopiesParserConfig(t *testing.T) {
	clearDatabaseTLSPostgresTestEnvironment(t)
	h := newDatabaseTLSConnectorHarness(t)
	var parsed *pgx.ConnConfig
	h.deps.parsePostgres = func(seed string, options pgx.ParseConfigOptions) (*pgx.ConnConfig, error) {
		var err error
		parsed, err = pgx.ParseConfigWithOptions(seed, options)
		if err != nil {
			return nil, err
		}
		parsed.Host, parsed.Port, parsed.User, parsed.Database, parsed.Password = "evil.example", 1234, "evil", "evil", "fake-parser-secret"
		parsed.RuntimeParams = map[string]string{"options": "-c search_path=evil"}
		parsed.Fallbacks = []*pgconn.FallbackConfig{{Host: "evil.example", Port: 1234}}
		parsed.TLSConfig = &tls.Config{InsecureSkipVerify: true}
		parsed.SSLNegotiation = "direct"
		return parsed, nil
	}
	source, credentials := databaseTLSConnectorTestSource(DriverPostgreSQL), databaseTLSConnectorTestCredentials()
	connector, err := databaseTLSConnector(source, credentials, &databaseTLSPolicy{Mode: databaseTLSVerifyFull}, h.deps)
	if err != nil {
		t.Fatal(err)
	}
	assertDatabaseTLSPostgresConfig(t, h.postgres, source, credentials, source.Host)
	if parsed == h.postgres || parsed.Password != "fake-parser-secret" || parsed.Host != "evil.example" || len(parsed.Fallbacks) != 1 || len(parsed.RuntimeParams) != 1 || !parsed.TLSConfig.InsecureSkipVerify {
		t.Fatal("builder mutated the parser-owned config")
	}
	parsed.Host, parsed.Password = "changed.example", "changed"
	parsed.Fallbacks[0].Host = "changed.example"
	parsed.RuntimeParams["options"] = "changed"
	assertDatabaseTLSPostgresConfig(t, h.postgres, source, credentials, source.Host)
	h.assertNoDial(t, connector)
}

func TestDatabaseTLSConnectorRealFactoriesDoNotDial(t *testing.T) {
	clearDatabaseTLSPostgresTestEnvironment(t)
	for _, driverName := range []Driver{DriverMySQL, DriverPostgreSQL} {
		source := databaseTLSConnectorTestSource(driverName)
		source.Host = "192.0.2.1"
		connector, err := newDatabaseTLSConnector(source, databaseTLSConnectorTestCredentials(), &databaseTLSPolicy{Mode: databaseTLSVerifyFull})
		if err != nil || connector == nil {
			t.Fatalf("connector construction failed: %v", err)
		}
		// OpenDB 和 Close 不执行 Connect；不调用 Ping/查询，也不监听端口。
		db := sql.OpenDB(connector)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDatabaseTLSConnectorStrictInvariant(t *testing.T) {
	good := func() *tls.Config { return &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "db.example"} }
	if !databaseTLSConnectorConfigIsStrict(good()) || databaseTLSConnectorConfigIsStrict(nil) {
		t.Fatal("strict TLS invariant incorrect")
	}
	for _, change := range []func(*tls.Config){
		func(c *tls.Config) { c.MinVersion = tls.VersionTLS11 },
		func(c *tls.Config) { c.ServerName = "" },
		func(c *tls.Config) { c.InsecureSkipVerify = true },
		func(c *tls.Config) { c.VerifyConnection = func(tls.ConnectionState) error { return nil } },
	} {
		c := good()
		change(c)
		if databaseTLSConnectorConfigIsStrict(c) {
			t.Fatal("weakened TLS config passed invariant")
		}
	}
}

func databaseTLSConnectorTestSource(driverName Driver) Source {
	return Source{Driver: driverName, Host: "db.example", Port: 5432, DefaultDatabase: "default_db", Reachability: "published", Evidence: "test"}
}

func databaseTLSConnectorTestCredentials() Credentials {
	return Credentials{Username: "test_user", Password: "fake-real-password-sentinel", Token: "fake-token-sentinel", Database: "test_db"}
}

func clearDatabaseTLSPostgresTestEnvironment(t *testing.T) {
	t.Helper()
	// 仅测试设置已知假环境，不枚举或读取实际凭据/证书文件。
	for _, key := range []string{
		"PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGPASSFILE", "PGAPPNAME", "PGCONNECT_TIMEOUT",
		"PGSSLMODE", "PGSSLKEY", "PGSSLCERT", "PGSSLSNI", "PGSSLROOTCERT", "PGSSLPASSWORD", "PGSSLNEGOTIATION",
		"PGTARGETSESSIONATTRS", "PGSERVICE", "PGSERVICEFILE", "PGTZ", "PGOPTIONS", "PGMINPROTOCOLVERSION",
		"PGMAXPROTOCOLVERSION", "PGCHANNELBINDING", "PGREQUIREAUTH",
	} {
		t.Setenv(key, "")
	}
}

func assertDatabaseTLSConnectorInvalid(t *testing.T, connector driver.Connector, err error) {
	t.Helper()
	if connector != nil || err != errDatabaseTLSConfigurationInvalid || err.Error() != "tls_configuration_invalid" || errors.Unwrap(err) != nil {
		t.Fatalf("expected fixed unwrapped configuration error, got connector=%T err=%v", connector, err)
	}
}

func assertDatabaseTLSPostgresConfig(t *testing.T, config *pgx.ConnConfig, source Source, credentials Credentials, serverName string) {
	t.Helper()
	if config == nil {
		t.Fatal("PostgreSQL connector factory was not called")
	}
	if config.Host != source.Host || config.Port != uint16(source.Port) || config.User != credentials.Username || config.Password != credentialSecret(credentials) || config.Database != effectiveDatabase(source, credentials.Database) {
		t.Fatal("explicit PostgreSQL endpoint or authentication changed")
	}
	assertDatabaseTLSDefaults(t, config.TLSConfig, serverName)
	if config.Fallbacks != nil || config.RuntimeParams != nil || config.SSLNegotiation != "postgres" || config.ValidateConnect != nil || config.ConnectTimeout != 0 {
		t.Fatal("PostgreSQL fallback, runtime parameters, negotiation, or target-session environment survived")
	}
	if config.MinProtocolVersion != "3.0" || config.MaxProtocolVersion != "3.0" || config.ChannelBinding != "prefer" || config.RequireAuth != "" {
		t.Fatal("PostgreSQL protocol/authentication environment survived")
	}
	if config.StatementCacheCapacity != 512 || config.DescriptionCacheCapacity != 512 || config.DefaultQueryExecMode != pgx.QueryExecModeCacheStatement {
		t.Fatal("pinned pgx execution defaults changed")
	}
}

type databaseTLSConnectorHarness struct {
	deps      databaseTLSConnectorDeps
	mysql     *mysql.Config
	postgres  *pgx.ConnConfig
	connector *databaseTLSFakeConnector
	dials     int
}

func newDatabaseTLSConnectorHarness(t *testing.T) *databaseTLSConnectorHarness {
	t.Helper()
	h := &databaseTLSConnectorHarness{connector: &databaseTLSFakeConnector{}}
	h.deps = databaseTLSConnectorDeps{
		getenv: os.Getenv,
		parsePostgres: func(seed string, options pgx.ParseConfigOptions) (*pgx.ConnConfig, error) {
			config, err := pgx.ParseConfigWithOptions(seed, options)
			if err == nil {
				config.DialFunc = func(context.Context, string, string) (net.Conn, error) {
					h.dials++
					return nil, errors.New("fake dial must not be called")
				}
				config.LookupFunc = func(context.Context, string) ([]string, error) {
					h.dials++
					return nil, errors.New("fake lookup must not be called")
				}
			}
			return config, err
		},
		newMySQL: func(config *mysql.Config) (driver.Connector, error) {
			if h.mysql != nil || h.postgres != nil {
				t.Fatal("more than one connector constructed")
			}
			h.mysql = config
			return h.connector, nil
		},
		newPostgres: func(config pgx.ConnConfig) driver.Connector {
			if h.mysql != nil || h.postgres != nil {
				t.Fatal("more than one connector constructed")
			}
			h.postgres = &config
			return h.connector
		},
	}
	return h
}

func (h *databaseTLSConnectorHarness) assertNoDial(t *testing.T, connector driver.Connector) {
	t.Helper()
	if connector != h.connector {
		t.Fatal("constructor did not return the per-instance connector")
	}
	db := sql.OpenDB(connector)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if h.dials != 0 || h.connector.connects != 0 {
		t.Fatal("construction or unused pool close attempted a connection")
	}
}

type databaseTLSFakeConnector struct{ connects int }

func (c *databaseTLSFakeConnector) Connect(context.Context) (driver.Conn, error) {
	c.connects++
	return nil, errors.New("fake connector must not connect")
}

func (*databaseTLSFakeConnector) Driver() driver.Driver { return databaseTLSFakeDriver{} }

type databaseTLSFakeDriver struct{}

func (databaseTLSFakeDriver) Open(string) (driver.Conn, error) {
	panic("strict connector must never use legacy DSN Open")
}
