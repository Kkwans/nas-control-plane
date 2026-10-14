package database

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	credentialCiphertextVersion    = 1
	credentialCiphertextTLSVersion = 2
	credentialCiphertextAlgorithm  = "AES-256-GCM"
	deploymentKeyBytes             = 32
)

// KeyMaterial is a versioned deployment key. Key is copied on ingress and
// egress so callers cannot mutate the provider's active key accidentally.
type KeyMaterial struct {
	Version int
	Key     []byte
}

type KeyProvider interface {
	Current(context.Context) (KeyMaterial, error)
	ForVersion(context.Context, int) (KeyMaterial, error)
}

type RotatingKeyProvider interface {
	KeyProvider
	Rotate(context.Context) (KeyMaterial, error)
}

func validateKeyMaterial(material KeyMaterial) error {
	if material.Version <= 0 || len(material.Key) != deploymentKeyBytes {
		return errors.New("invalid deployment key material")
	}
	return nil
}

func copyKeyMaterial(material KeyMaterial) KeyMaterial {
	return KeyMaterial{Version: material.Version, Key: append([]byte(nil), material.Key...)}
}

// MemoryKeyProvider is useful for tests and for an embedding process that
// already owns a deployment key. Production Server wiring should use
// FileKeyProvider so the key is independent from the SQLite database.
type MemoryKeyProvider struct {
	mu      sync.RWMutex
	current int
	keys    map[int][]byte
}

func NewMemoryKeyProvider(version int, key []byte) (*MemoryKeyProvider, error) {
	material := KeyMaterial{Version: version, Key: append([]byte(nil), key...)}
	if err := validateKeyMaterial(material); err != nil {
		return nil, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_init", Cause: err}
	}
	return &MemoryKeyProvider{current: version, keys: map[int][]byte{version: material.Key}}, nil
}

func (p *MemoryKeyProvider) Current(ctx context.Context) (KeyMaterial, error) {
	if err := contextError(ctx); err != nil {
		return KeyMaterial{}, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	key, ok := p.keys[p.current]
	if !ok {
		return KeyMaterial{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_current"}
	}
	return KeyMaterial{Version: p.current, Key: append([]byte(nil), key...)}, nil
}

func (p *MemoryKeyProvider) ForVersion(ctx context.Context, version int) (KeyMaterial, error) {
	if err := contextError(ctx); err != nil {
		return KeyMaterial{}, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	key, ok := p.keys[version]
	if !ok {
		return KeyMaterial{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_lookup", Cause: errors.New("key version unavailable")}
	}
	return KeyMaterial{Version: version, Key: append([]byte(nil), key...)}, nil
}

func (p *MemoryKeyProvider) Rotate(ctx context.Context) (KeyMaterial, error) {
	if err := contextError(ctx); err != nil {
		return KeyMaterial{}, err
	}
	key := make([]byte, deploymentKeyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return KeyMaterial{}, &DatabaseError{Code: CodeKeyRotationFailed, Operation: "key_rotate", Cause: err}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current <= 0 || p.current == math.MaxInt {
		return KeyMaterial{}, &DatabaseError{Code: CodeKeyRotationFailed, Operation: "key_rotate"}
	}
	nextVersion := p.current + 1
	if _, exists := p.keys[nextVersion]; exists {
		return KeyMaterial{}, &DatabaseError{Code: CodeKeyRotationFailed, Operation: "key_rotate"}
	}
	p.current = nextVersion
	p.keys[p.current] = key
	return KeyMaterial{Version: p.current, Key: append([]byte(nil), key...)}, nil
}

type keyFileDocument struct {
	CurrentVersion int               `json:"currentVersion"`
	Keys           map[string]string `json:"keys"`
}

// FileKeyProvider stores the deployment key ring in a separate root-managed
// file. It never creates a replacement key implicitly when the file is
// missing, because that would make existing credentials unrecoverable.
type FileKeyProvider struct {
	path string
	mu   sync.Mutex
}

func NewFileKeyProvider(path string) (*FileKeyProvider, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_open", Cause: errors.New("deployment key path is required")}
	}
	provider := &FileKeyProvider{path: filepath.Clean(path)}
	if _, err := provider.read(); err != nil {
		return nil, err
	}
	return provider, nil
}

// CreateFileKeyProvider explicitly provisions an independent deployment key
// file. Callers should perform this during installation, not on a missing-key
// recovery path.
func CreateFileKeyProvider(path string) (*FileKeyProvider, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_create", Cause: errors.New("deployment key path is required")}
	}
	provider := &FileKeyProvider{path: filepath.Clean(path)}
	if _, err := os.Stat(provider.path); err == nil {
		return nil, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_create", Cause: errors.New("deployment key already exists")}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_create", Cause: err}
	}
	key := make([]byte, deploymentKeyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_create", Cause: err}
	}
	document := keyFileDocument{CurrentVersion: 1, Keys: map[string]string{"1": base64.RawStdEncoding.EncodeToString(key)}}
	if err := provider.write(document); err != nil {
		return nil, err
	}
	return provider, nil
}

func (p *FileKeyProvider) Current(ctx context.Context) (KeyMaterial, error) {
	if err := contextError(ctx); err != nil {
		return KeyMaterial{}, err
	}
	document, err := p.read()
	if err != nil {
		return KeyMaterial{}, err
	}
	return decodeKeyDocument(document, document.CurrentVersion)
}

func (p *FileKeyProvider) ForVersion(ctx context.Context, version int) (KeyMaterial, error) {
	if err := contextError(ctx); err != nil {
		return KeyMaterial{}, err
	}
	document, err := p.read()
	if err != nil {
		return KeyMaterial{}, err
	}
	return decodeKeyDocument(document, version)
}

func (p *FileKeyProvider) Rotate(ctx context.Context) (KeyMaterial, error) {
	if err := contextError(ctx); err != nil {
		return KeyMaterial{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	document, err := p.readLocked()
	if err != nil {
		return KeyMaterial{}, &DatabaseError{Code: CodeKeyRotationFailed, Operation: "key_rotate", Cause: err}
	}
	key := make([]byte, deploymentKeyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return KeyMaterial{}, &DatabaseError{Code: CodeKeyRotationFailed, Operation: "key_rotate", Cause: err}
	}
	if document.CurrentVersion == math.MaxInt {
		return KeyMaterial{}, &DatabaseError{Code: CodeKeyRotationFailed, Operation: "key_rotate"}
	}
	nextVersion := document.CurrentVersion + 1
	if _, exists := document.Keys[strconv.Itoa(nextVersion)]; exists {
		return KeyMaterial{}, &DatabaseError{Code: CodeKeyRotationFailed, Operation: "key_rotate"}
	}
	if document.Keys == nil {
		document.Keys = make(map[string]string)
	}
	document.Keys[strconv.Itoa(nextVersion)] = base64.RawStdEncoding.EncodeToString(key)
	document.CurrentVersion = nextVersion
	if err := p.writeLocked(document); err != nil {
		return KeyMaterial{}, &DatabaseError{Code: CodeKeyRotationFailed, Operation: "key_rotate", Cause: err}
	}
	return KeyMaterial{Version: nextVersion, Key: key}, nil
}

func (p *FileKeyProvider) read() (keyFileDocument, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.readLocked()
}

func (p *FileKeyProvider) readLocked() (keyFileDocument, error) {
	info, err := os.Stat(p.path)
	if err != nil {
		return keyFileDocument{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_read", Cause: err}
	}
	if info.Mode().Perm()&0o077 != 0 {
		return keyFileDocument{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_read", Cause: errors.New("deployment key file permissions are too broad")}
	}
	contents, err := os.ReadFile(p.path)
	if err != nil {
		return keyFileDocument{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_read", Cause: err}
	}
	var document keyFileDocument
	if err := json.Unmarshal(contents, &document); err != nil {
		return keyFileDocument{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_read", Cause: errors.New("deployment key file is invalid")}
	}
	if document.CurrentVersion <= 0 || len(document.Keys) == 0 {
		return keyFileDocument{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_read", Cause: errors.New("deployment key ring is empty")}
	}
	return document, nil
}

func (p *FileKeyProvider) write(document keyFileDocument) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writeLocked(document)
}

func (p *FileKeyProvider) writeLocked(document keyFileDocument) error {
	contents, err := json.Marshal(document)
	if err != nil {
		return &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_write", Cause: errors.New("deployment key file cannot be encoded")}
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_write", Cause: err}
	}
	temporary, err := os.CreateTemp(filepath.Dir(p.path), ".ncp-database-key-")
	if err != nil {
		return &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_write", Cause: err}
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_write", Cause: err}
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_write", Cause: err}
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_write", Cause: err}
	}
	if err := temporary.Close(); err != nil {
		return &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_write", Cause: err}
	}
	if err := os.Rename(temporaryPath, p.path); err != nil {
		return &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_write", Cause: err}
	}
	return nil
}

func decodeKeyDocument(document keyFileDocument, version int) (KeyMaterial, error) {
	encoded, ok := document.Keys[strconv.Itoa(version)]
	if !ok {
		return KeyMaterial{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_lookup", Cause: errors.New("key version unavailable")}
	}
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(key) != deploymentKeyBytes {
		return KeyMaterial{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_lookup", Cause: errors.New("key material is invalid")}
	}
	return KeyMaterial{Version: version, Key: key}, nil
}

type encryptedCredentialEnvelope struct {
	Version    int    `json:"version"`
	Algorithm  string `json:"algorithm"`
	KeyVersion int    `json:"keyVersion"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// credentialValue 是 codec 的规范化载体：legacyCredentials.TLS 恒为 nil。
// TLS 只由 tls 保存，转换函数深复制并清除公开字段，避免两个策略真值。
type credentialValue struct {
	legacyCredentials Credentials
	tls               *databaseTLSPolicy
}

func credentialValueFromCredentials(credentials Credentials) credentialValue {
	credentials = CloneCredentials(credentials)
	policy := credentials.TLS
	credentials.TLS = nil
	return credentialValue{legacyCredentials: credentials, tls: policy}
}

func (value credentialValue) credentials() Credentials {
	credentials := value.legacyCredentials
	credentials.TLS = value.tls
	return CloneCredentials(credentials)
}

func cloneCredentialValue(value credentialValue) credentialValue {
	if value.tls != nil {
		policy := *value.tls
		value.tls = &policy
	}
	return value
}

type credentialPayloadV1 struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	Database string `json:"database,omitempty"`
}

type credentialPayloadV2 struct {
	credentialPayloadV1
	TLS *databaseTLSPolicy `json:"tls"`
}

func EncryptCredentials(ctx context.Context, provider KeyProvider, aad string, credentials Credentials) ([]byte, int, error) {
	value := credentialValueFromCredentials(credentials)
	if err := contextError(ctx); err != nil {
		return nil, 0, err
	}
	if provider == nil {
		return nil, 0, &DatabaseError{Code: CodeKeyUnavailable, Operation: "credential_encrypt"}
	}
	material, err := provider.Current(ctx)
	if err != nil {
		return nil, 0, credentialCodecKeyError("credential_encrypt", err)
	}
	return encryptCredentialValueWithKey(ctx, material, aad, value)
}

// v1/v2 共享认证入口；v2 策略必须完整返回，不能静默降为旧凭据。
func DecryptCredentials(ctx context.Context, provider KeyProvider, aad string, encoded []byte) (Credentials, error) {
	value, err := decryptCredentialValue(ctx, provider, aad, encoded)
	if err != nil {
		return Credentials{}, err
	}
	return value.credentials(), nil
}

// 普通加密与 rotation 共用版本、payload、AAD 和 envelope 的唯一构造路径。
func encryptCredentialValueWithKey(ctx context.Context, material KeyMaterial, baseAAD string, value credentialValue) ([]byte, int, error) {
	if err := contextError(ctx); err != nil {
		return nil, 0, err
	}
	if err := validateKeyMaterial(material); err != nil {
		return nil, 0, &DatabaseError{Code: CodeKeyUnavailable, Operation: "credential_encrypt"}
	}
	if value.legacyCredentials.TLS != nil {
		return nil, 0, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_encrypt"}
	}
	value = cloneCredentialValue(value)
	version := credentialCiphertextVersion
	credentials := value.legacyCredentials
	legacy := credentialPayloadV1{
		Username: credentials.Username, Password: credentials.Password,
		Token: credentials.Token, Database: credentials.Database,
	}
	var plain any = legacy
	if value.tls != nil {
		if err := validateRequiredDatabaseTLSPolicy(value.tls); err != nil {
			return nil, 0, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_encrypt"}
		}
		version = credentialCiphertextTLSVersion
		plain = credentialPayloadV2{credentialPayloadV1: legacy, TLS: value.tls}
	}
	payload, err := json.Marshal(plain)
	if err != nil {
		return nil, 0, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_encrypt"}
	}
	defer clearBytes(payload)
	aad, err := credentialEnvelopeAAD(baseAAD, version)
	if err != nil {
		return nil, 0, err
	}
	ciphertext, nonce, err := sealCredentialPayload(material.Key, aad, payload)
	if err != nil {
		return nil, 0, err
	}
	envelope := encryptedCredentialEnvelope{
		Version: version, Algorithm: credentialCiphertextAlgorithm, KeyVersion: material.Version,
		Nonce: base64.RawStdEncoding.EncodeToString(nonce), Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, 0, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_encrypt"}
	}
	return encoded, material.Version, nil
}

// baseAAD 保留旧绑定，v2 另加域、版本和字节长度；没有旧 AAD 重试路径。
func credentialEnvelopeAAD(baseAAD string, version int) ([]byte, error) {
	switch version {
	case credentialCiphertextVersion:
		return []byte(baseAAD), nil
	case credentialCiphertextTLSVersion:
		aad := []byte("ncp/database-credentials/envelope\x00")
		aad = binary.BigEndian.AppendUint32(aad, uint32(credentialCiphertextTLSVersion))
		aad = binary.BigEndian.AppendUint64(aad, uint64(len(baseAAD)))
		return append(aad, baseAAD...), nil
	default:
		return nil, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_aad"}
	}
}

func decryptCredentialValue(ctx context.Context, provider KeyProvider, baseAAD string, encoded []byte) (credentialValue, error) {
	return decryptCredentialValueVersions(ctx, provider, baseAAD, encoded, true)
}

func decryptCredentialValueVersions(ctx context.Context, provider KeyProvider, baseAAD string, encoded []byte, allowTLS bool) (credentialValue, error) {
	if err := contextError(ctx); err != nil {
		return credentialValue{}, err
	}
	if provider == nil {
		return credentialValue{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "credential_decrypt"}
	}
	var envelope encryptedCredentialEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil ||
		(envelope.Version != credentialCiphertextVersion && (!allowTLS || envelope.Version != credentialCiphertextTLSVersion)) ||
		envelope.Algorithm != credentialCiphertextAlgorithm || envelope.KeyVersion <= 0 {
		return credentialValue{}, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt"}
	}
	aad, err := credentialEnvelopeAAD(baseAAD, envelope.Version)
	if err != nil {
		return credentialValue{}, err
	}
	material, err := provider.ForVersion(ctx, envelope.KeyVersion)
	if err != nil {
		return credentialValue{}, credentialCodecKeyError("credential_decrypt", err)
	}
	if err := validateKeyMaterial(material); err != nil || material.Version != envelope.KeyVersion {
		return credentialValue{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "credential_decrypt"}
	}
	nonce, nonceErr := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	ciphertext, ciphertextErr := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if nonceErr != nil || ciphertextErr != nil {
		return credentialValue{}, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt"}
	}
	payload, err := openCredentialPayload(material.Key, aad, nonce, ciphertext)
	if err != nil {
		return credentialValue{}, err
	}
	defer clearBytes(payload)
	var legacy credentialPayloadV1
	var policy *databaseTLSPolicy
	if envelope.Version == credentialCiphertextVersion {
		// v1 保持 plain Unmarshal 的旧未知字段容忍行为。
		if err := json.Unmarshal(payload, &legacy); err != nil {
			return credentialValue{}, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt"}
		}
	} else {
		var decoded credentialPayloadV2
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&decoded); err != nil {
			return credentialValue{}, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt"}
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return credentialValue{}, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt"}
		}
		if err := validateRequiredDatabaseTLSPolicy(decoded.TLS); err != nil {
			return credentialValue{}, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt"}
		}
		legacy, policy = decoded.credentialPayloadV1, decoded.TLS
	}
	return credentialValue{
		legacyCredentials: Credentials{Username: legacy.Username, Password: legacy.Password, Token: legacy.Token, Database: legacy.Database},
		tls:               policy,
	}, nil
}

func sealCredentialPayload(key, aad, payload []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_encrypt"}
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_encrypt"}
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_encrypt"}
	}
	return gcm.Seal(nil, nonce, payload, aad), nonce, nil
}

func openCredentialPayload(key, aad, nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt"}
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return nil, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt"}
	}
	payload, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt"}
	}
	return payload, nil
}

// 只保留可判定的 context sentinel，不让 provider 的原始诊断携带秘密。
func credentialCodecKeyError(operation string, err error) error {
	var cause error
	switch {
	case errors.Is(err, context.Canceled):
		cause = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		cause = context.DeadlineExceeded
	}
	return &DatabaseError{Code: CodeKeyUnavailable, Operation: operation, Cause: cause}
}

func credentialAAD(sourceID string, driver Driver, endpoint string) string {
	return "ncp/database-credentials/v1/" + sourceID + "/" + string(driver) + "/" + endpoint
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return &DatabaseError{Code: CodeTimeout, Operation: "context", Cause: err}
	}
	return nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
