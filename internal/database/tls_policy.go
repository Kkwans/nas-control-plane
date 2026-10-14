package database

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"net/netip"
	"strings"
)

type databaseTLSMode string

const (
	databaseTLSVerifyFull databaseTLSMode = "verify-full"
	databaseTLSMaxCABytes                 = 64 << 10
	databaseTLSMaxCACerts                 = 32
)

// databaseTLSPolicy 目前只是包内基础类型，尚未接入凭据、RPC 或数据库连接。
// CAPEM 为空表示使用系统根；非空时仅信任这些 CA，不合并系统根。
type databaseTLSPolicy struct {
	Mode       databaseTLSMode
	CAPEM      string
	ServerName string
}

// 不保留原始输入或解析错误链，也不在本阶段增加传输层错误码。
var errDatabaseTLSConfigurationInvalid = errors.New("tls_configuration_invalid")

// compileDatabaseTLS 是纯配置编译器，不解析 DSN、不读文件、不联网。
// nil policy 返回 nil, nil，供未来调用方保留旧路径；这不是 TLS 安全承诺。
// 未来严格 RPC/v2 凭据入口仍须单独要求非 nil policy，不能将缺省当作成功。
// dialHost 必须是实际拨号的单个 host，而非地址、URL 或 socket 路径。
// 即使提供 ServerName 也校验 dialHost；覆盖只改变验证身份，不改变拨号目标。
// 每次调用都新建配置和自定义根池；调用方负责连接器的无明文 fallback 约束。
func compileDatabaseTLS(driver Driver, dialHost string, policy *databaseTLSPolicy) (*tls.Config, error) {
	if policy == nil {
		return nil, nil
	}
	if (driver != DriverMySQL && driver != DriverPostgreSQL) || policy.Mode != databaseTLSVerifyFull {
		return nil, errDatabaseTLSConfigurationInvalid
	}
	serverName, ok := databaseTLSServerName(dialHost)
	if !ok {
		return nil, errDatabaseTLSConfigurationInvalid
	}
	if policy.ServerName != "" {
		serverName, ok = databaseTLSServerName(policy.ServerName)
		if !ok {
			return nil, errDatabaseTLSConfigurationInvalid
		}
	}
	var roots *x509.CertPool
	if policy.CAPEM != "" {
		var err error
		roots, err = databaseTLSRootCAs(policy.CAPEM)
		if err != nil {
			return nil, err
		}
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
		RootCAs:    roots,
	}, nil
}

// DNS 仅接受 ASCII LDH 标签（含单标签及 A-label），每标签至多 63 字节，
// 整个输入至多 253 字节；统一小写并去掉一个尾点。不做 IDNA 转换或 DNS 查询。
// IP 仅接受 netip 的无歧义字面量并规范化；拒绝 IPv6 方括号和 zone，不能
// 静默删除 scope 后验证另一个身份。全数字（含点分）但非合法 IPv4 的值也拒绝。
func databaseTLSServerName(name string) (string, bool) {
	if name == "" || len(name) > 253 {
		return "", false
	}
	if ip, err := netip.ParseAddr(name); err == nil {
		if ip.Zone() != "" {
			return "", false
		}
		return ip.String(), true
	}
	name = strings.TrimSuffix(name, ".")
	allNumeric := true
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if c >= '0' && c <= '9' {
				continue
			}
			allNumeric = false
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '-' {
				return "", false
			}
		}
	}
	if allNumeric {
		return "", false
	}
	return strings.ToLower(name), true
}

func databaseTLSRootCAs(caPEM string) (*x509.CertPool, error) {
	if len(caPEM) > databaseTLSMaxCABytes {
		return nil, errDatabaseTLSConfigurationInvalid
	}
	roots := x509.NewCertPool()
	rest := []byte(caPEM)
	count := 0
	for {
		// 仅允许 ASCII PEM 空白；非空白前缀、间隙和尾部一律拒绝。
		rest = bytes.Trim(rest, " \t\r\n")
		if len(rest) == 0 {
			break
		}
		const begin = "-----BEGIN CERTIFICATE-----"
		if count == databaseTLSMaxCACerts || !bytes.HasPrefix(rest, []byte(begin)) {
			return nil, errDatabaseTLSConfigurationInvalid
		}
		// pem.Decode 会跳过损坏的 block；先隔离首个 END 行，并拒绝嵌套
		// BEGIN，避免坏证书或隐藏私钥被后面的有效证书掩盖。
		end := bytes.Index(rest, []byte("\n-----END "))
		if end < 0 {
			return nil, errDatabaseTLSConfigurationInvalid
		}
		blockEnd := len(rest)
		if newline := bytes.IndexByte(rest[end+1:], '\n'); newline >= 0 {
			blockEnd = end + 1 + newline + 1
		}
		encoded := rest[:blockEnd]
		if bytes.Contains(encoded[len(begin):], []byte("-----BEGIN ")) {
			return nil, errDatabaseTLSConfigurationInvalid
		}
		block, tail := pem.Decode(encoded)
		if block == nil || len(tail) != 0 || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errDatabaseTLSConfigurationInvalid
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.BasicConstraintsValid || !cert.IsCA ||
			(cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0) {
			return nil, errDatabaseTLSConfigurationInvalid
		}
		// KeyUsage=0 既可能是未声明用途，也可能是声明了空用途；仅前者
		// 无约束。存在 KeyUsage 扩展时必须明确允许签发证书。
		if cert.KeyUsage == 0 {
			for _, extension := range cert.Extensions {
				if extension.Id.Equal(asn1.ObjectIdentifier{2, 5, 29, 15}) {
					return nil, errDatabaseTLSConfigurationInvalid
				}
			}
		}
		// 信任锚可以是中间 CA；不要求自签名。有效期及链约束由正常
		// x509 验证处理，编译器不依赖当前时间。
		roots.AddCert(cert)
		count++
		rest = rest[blockEnd:]
	}
	if count == 0 {
		return nil, errDatabaseTLSConfigurationInvalid
	}
	return roots, nil
}
