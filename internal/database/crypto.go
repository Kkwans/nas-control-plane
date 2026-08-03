package database

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	credentialCiphertextVersion   = 1
	credentialCiphertextAlgorithm = "AES-256-GCM"
	deploymentKeyBytes            = 32
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
	p.current++
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
	nextVersion := document.CurrentVersion + 1
	if nextVersion <= 0 {
		nextVersion = 1
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

type credentialPayload struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	Database string `json:"database,omitempty"`
}

func EncryptCredentials(ctx context.Context, provider KeyProvider, aad string, credentials Credentials) ([]byte, int, error) {
	if err := contextError(ctx); err != nil {
		return nil, 0, err
	}
	if provider == nil {
		return nil, 0, &DatabaseError{Code: CodeKeyUnavailable, Operation: "credential_encrypt"}
	}
	material, err := provider.Current(ctx)
	if err != nil {
		return nil, 0, &DatabaseError{Code: CodeKeyUnavailable, Operation: "credential_encrypt", Cause: err}
	}
	if err := validateKeyMaterial(material); err != nil {
		return nil, 0, &DatabaseError{Code: CodeKeyUnavailable, Operation: "credential_encrypt", Cause: err}
	}
	ciphertext, nonce, err := sealCredential(material.Key, []byte(aad), credentials)
	if err != nil {
		return nil, 0, err
	}
	envelope := encryptedCredentialEnvelope{
		Version: credentialCiphertextVersion, Algorithm: credentialCiphertextAlgorithm,
		KeyVersion: material.Version,
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, 0, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_encrypt", Cause: err}
	}
	return encoded, material.Version, nil
}

func DecryptCredentials(ctx context.Context, provider KeyProvider, aad string, encoded []byte) (Credentials, error) {
	if err := contextError(ctx); err != nil {
		return Credentials{}, err
	}
	if provider == nil {
		return Credentials{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "credential_decrypt"}
	}
	var envelope encryptedCredentialEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil || envelope.Version != credentialCiphertextVersion ||
		envelope.Algorithm != credentialCiphertextAlgorithm || envelope.KeyVersion <= 0 {
		return Credentials{}, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt", Cause: errors.New("credential ciphertext version is unsupported")}
	}
	material, err := provider.ForVersion(ctx, envelope.KeyVersion)
	if err != nil {
		return Credentials{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "credential_decrypt", Cause: err}
	}
	if err := validateKeyMaterial(material); err != nil {
		return Credentials{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "credential_decrypt", Cause: err}
	}
	nonce, nonceErr := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	ciphertext, ciphertextErr := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if nonceErr != nil || ciphertextErr != nil {
		return Credentials{}, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt", Cause: errors.New("credential ciphertext encoding is invalid")}
	}
	credentials, err := openCredential(material.Key, []byte(aad), nonce, ciphertext)
	if err != nil {
		return Credentials{}, err
	}
	return credentials, nil
}

func sealCredential(key, aad []byte, credentials Credentials) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_encrypt", Cause: err}
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_encrypt", Cause: err}
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_encrypt", Cause: err}
	}
	payload, err := json.Marshal(credentialPayload{
		Username: credentials.Username, Password: credentials.Password,
		Token: credentials.Token, Database: credentials.Database,
	})
	if err != nil {
		return nil, nil, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_encrypt", Cause: err}
	}
	defer clearBytes(payload)
	return gcm.Seal(nil, nonce, payload, aad), nonce, nil
}

func openCredential(key, aad, nonce, ciphertext []byte) (Credentials, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return Credentials{}, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt", Cause: err}
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return Credentials{}, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt", Cause: errors.New("credential nonce is invalid")}
	}
	payload, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return Credentials{}, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt", Cause: errors.New("credential authentication failed")}
	}
	defer clearBytes(payload)
	var decoded credentialPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return Credentials{}, &DatabaseError{Code: CodeCredentialCorrupt, Operation: "credential_decrypt", Cause: errors.New("credential payload is invalid")}
	}
	return Credentials{Username: decoded.Username, Password: decoded.Password, Token: decoded.Token, Database: decoded.Database}, nil
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
