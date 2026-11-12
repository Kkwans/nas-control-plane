package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
)

var errDatabaseTLSInput = errors.New("invalid database TLS input")

type databaseJSONMember struct {
	name  string
	value json.RawMessage
}

// 仅检查根级连接和凭据对象，不递归扫描 SQL 字符串或动态行数据。
// 顺序保留所有成员，防止后一个 credentials/null 掩盖先前的严格策略。
// 没有 TLS 键的旧请求继续使用原强类型解码器的大小写兼容行为。
func validateDatabaseTLSInput(data []byte) error {
	root, _, err := databaseJSONMembers(data)
	if err != nil {
		return err
	}
	var credentials []databaseJSONMember
	credentialCount, sourceCount := 0, 0
	canonical, hasTLS := true, false
	for _, member := range root {
		switch {
		case strings.EqualFold(member.name, "sourceId"):
			sourceCount++
			canonical = canonical && member.name == "sourceId"
		case strings.EqualFold(member.name, "credentials"):
			credentialCount++
			canonical = canonical && member.name == "credentials"
			members, _, err := databaseJSONMembers(member.value)
			if err != nil {
				return err
			}
			for _, field := range members {
				if strings.EqualFold(field.name, "tls") {
					hasTLS = true
				}
			}
			credentials = members
		}
	}
	if !hasTLS {
		return nil
	}
	if !canonical || credentialCount != 1 || sourceCount > 1 {
		return errDatabaseTLSInput
	}
	seen := make(map[string]bool)
	for _, member := range credentials {
		if seen[member.name] {
			return errDatabaseTLSInput
		}
		seen[member.name] = true
		switch member.name {
		case "tls":
			if err := validateDatabaseTLSPolicyInput(member.value); err != nil {
				return err
			}
		case "username", "password", "token", "database":
			if _, err := databaseJSONString(member.value); err != nil {
				return err
			}
		default:
			return errDatabaseTLSInput
		}
	}
	return nil
}

func validateDatabaseTLSPolicyInput(data []byte) error {
	members, object, err := databaseJSONMembers(data)
	if err != nil || !object {
		return errDatabaseTLSInput
	}
	var policy ncpdatabase.TLSPolicy
	seen := make(map[string]bool)
	for _, member := range members {
		if seen[member.name] {
			return errDatabaseTLSInput
		}
		seen[member.name] = true
		value, err := databaseJSONString(member.value)
		if err != nil {
			return err
		}
		switch member.name {
		case "mode":
			policy.Mode = ncpdatabase.TLSMode(value)
		case "caPEM":
			policy.CAPEM = value
		case "serverName":
			policy.ServerName = value
		default:
			return errDatabaseTLSInput
		}
	}
	// 共用字节/证书预算、PEM/CA/用途与服务器身份校验，避免 HTTP 漂移。
	if err := ncpdatabase.ValidateRequiredTLSPolicy(&policy); err != nil {
		return errDatabaseTLSInput
	}
	return nil
}

func databaseJSONString(data []byte) (string, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '"' {
		return "", errDatabaseTLSInput // json.Unmarshal 的 string 目标会静默接受 null。
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", errDatabaseTLSInput
	}
	return value, nil
}

func databaseJSONMembers(data []byte) ([]databaseJSONMember, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return nil, false, errDatabaseTLSInput
	}
	if token != json.Delim('{') {
		return nil, false, nil // 非对象仍由正常的强类型解码器拒绝。
	}
	var members []databaseJSONMember
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, false, errDatabaseTLSInput
		}
		name, ok := token.(string)
		if !ok {
			return nil, false, errDatabaseTLSInput
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false, errDatabaseTLSInput
		}
		members = append(members, databaseJSONMember{name: name, value: value})
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, false, errDatabaseTLSInput
	}
	return members, true, nil
}
