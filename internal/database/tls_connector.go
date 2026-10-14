package database

import (
	"crypto/tls"
	"database/sql/driver"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// databaseTLSConnectorDeps 只用于逐次构造和无网络测试；不修改全局驱动注册表
// 或进程环境。工厂取得配置的所有权，不能在构造后继续修改配置。
type databaseTLSConnectorDeps struct {
	getenv        func(string) string
	parsePostgres func(string, pgx.ParseConfigOptions) (*pgx.ConnConfig, error)
	newMySQL      func(*mysql.Config) (driver.Connector, error)
	newPostgres   func(pgx.ConnConfig) driver.Connector
}

// newDatabaseTLSConnector 是尚未接入 Manager 的严格构造边界，不拨号。
// 与 T0 编译器不同，nil policy 也是错误；这里绝不回退到旧 DSN/sql.Open。
// 后续调用方必须使用 sql.OpenDB，不能重新序列化配置或使用 ConnString。
func newDatabaseTLSConnector(source Source, credentials Credentials, policy *databaseTLSPolicy) (driver.Connector, error) {
	return databaseTLSConnector(source, credentials, policy, databaseTLSConnectorDeps{
		getenv:        os.Getenv,
		parsePostgres: pgx.ParseConfigWithOptions,
		newMySQL:      mysql.NewConnector,
		newPostgres: func(config pgx.ConnConfig) driver.Connector {
			return stdlib.GetConnector(config)
		},
	})
}

func databaseTLSConnector(source Source, credentials Credentials, policy *databaseTLSPolicy, deps databaseTLSConnectorDeps) (driver.Connector, error) {
	if policy == nil || (source.Driver != DriverMySQL && source.Driver != DriverPostgreSQL) ||
		source.Reachability == "container-internal" || source.Reachability == "unreachable" || source.Evidence == "none" {
		return nil, errDatabaseTLSConfigurationInvalid
	}
	databaseName := effectiveDatabase(source, credentials.Database)
	if credentials.Username == "" || databaseName == "" {
		return nil, errDatabaseTLSConfigurationInvalid
	}
	host, port, _ := endpointParts(source.Location, source.Port, databaseName)
	if strings.TrimSpace(source.Host) != "" {
		host = source.Host
	}
	if port <= 0 || port > 65535 {
		return nil, errDatabaseTLSConfigurationInvalid
	}
	tlsConfig, err := compileDatabaseTLS(source.Driver, host, policy)
	if err != nil {
		return nil, errDatabaseTLSConfigurationInvalid
	}
	if source.Driver == DriverMySQL {
		// 重现旧 literal Config -> FormatDSN -> ParseDSN 的非认证选项，
		// 不能直接换成 NewConfig，后者会改变鉴权和连接活性默认值。
		// 种子不含密码；认证字段最后赋值，也避免 DSN 用户名分隔符歧义。
		seed := mysql.Config{
			Net: "tcp", Addr: net.JoinHostPort(host, strconv.Itoa(port)), DBName: databaseName,
			ParseTime: true, Params: map[string]string{"charset": "utf8mb4"},
		}
		config, err := mysql.ParseDSN(seed.FormatDSN())
		if err != nil {
			return nil, errDatabaseTLSConfigurationInvalid
		}
		config.User, config.Passwd = credentials.Username, credentialSecret(credentials)
		config.TLS, config.TLSConfig = tlsConfig, ""
		config.AllowFallbackToPlaintext = false
		if !databaseTLSConnectorConfigIsStrict(config.TLS) || config.AllowFallbackToPlaintext {
			return nil, errDatabaseTLSConfigurationInvalid
		}
		connector, err := deps.newMySQL(config)
		if err != nil || connector == nil {
			return nil, errDatabaseTLSConfigurationInvalid
		}
		return connector, nil
	}

	// pgx 没有禁用环境读取的解析选项。service 按键存在即尝试读文件，
	// 不能用 service= 禁用；严格分支在解析前拒绝非空 PGSERVICE。
	if deps.getenv("PGSERVICE") != "" {
		return nil, errDatabaseTLSConfigurationInvalid
	}
	values := url.Values{
		"sslmode": {"disable"}, "sslrootcert": {""}, "sslcert": {""}, "sslkey": {""},
		"sslpassword": {""}, "passfile": {""}, "servicefile": {""}, "sslsni": {"1"},
		"sslnegotiation": {"postgres"}, "connect_timeout": {"0"}, "target_session_attrs": {"any"},
		"min_protocol_version": {"3.0"}, "max_protocol_version": {"3.0"},
		"channel_binding": {"prefer"}, "require_auth": {""},
	}
	seed := (&url.URL{
		Scheme: "postgres", User: url.UserPassword(credentials.Username, ""),
		Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: "/" + databaseName,
		RawQuery: values.Encode(),
	}).String()
	allowedKeys := []string{"host", "port", "user", "password", "database"}
	for key := range values {
		allowedKeys = append(allowedKeys, key)
	}
	parsed, err := deps.parsePostgres(seed, pgx.ParseConfigOptions{
		ParseConfigOptions: pgconn.ParseConfigOptions{ConnStringAllowedKeys: allowedKeys},
	})
	if err != nil || parsed == nil {
		// ParseConfigError 保留输入连接串；不包装、不保留原始错误链。
		return nil, errDatabaseTLSConfigurationInvalid
	}
	// 复制保留 pgx 私有的解析标记，同时不修改解析器返回的配置。
	config := parsed.Copy()
	config.Host, config.Port = host, uint16(port)
	config.User, config.Database = credentials.Username, databaseName
	config.Password = credentialSecret(credentials)
	config.RuntimeParams, config.Fallbacks = nil, nil
	config.TLSConfig, config.SSLNegotiation = tlsConfig, "postgres"
	if !databaseTLSConnectorConfigIsStrict(config.TLSConfig) || len(config.Fallbacks) != 0 || config.SSLNegotiation != "postgres" {
		return nil, errDatabaseTLSConfigurationInvalid
	}
	connector := deps.newPostgres(*config)
	if connector == nil {
		return nil, errDatabaseTLSConfigurationInvalid
	}
	return connector, nil
}

func databaseTLSConnectorConfigIsStrict(config *tls.Config) bool {
	return config != nil && config.MinVersion >= tls.VersionTLS12 && config.ServerName != "" &&
		!config.InsecureSkipVerify && config.VerifyPeerCertificate == nil && config.VerifyConnection == nil
}
