package cliconfig

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

// ErrNotFound is returned by a SecretStore when no secret exists for a context.
var ErrNotFound = errors.New("secret not found")

// keyringService is the service name under which secrets are filed in the OS
// keyring. The per-context name is used as the keyring "user".
const keyringService = "dfmsctl"

// envTokenStore forces a particular secret backend, bypassing auto-detection.
// Accepted values: "keyring" or "file". Useful for headless/CI environments and
// for hermetic tests.
const envTokenStore = "DFMSCTL_TOKEN_STORE"

// SecretStore securely persists an opaque secret (such as a token pair) per
// context name. Implementations keep secrets out of the world-readable config
// file: in the OS keyring when available, otherwise an owner-only file.
type SecretStore interface {
	// Get returns the secret for the named context, or ErrNotFound if absent.
	Get(context string) ([]byte, error)
	// Set stores (or replaces) the secret for the named context.
	Set(context string, secret []byte) error
	// Delete removes the secret for the named context. Removing a missing
	// secret returns ErrNotFound.
	Delete(context string) error
}

// NewSecretStore selects a backend: the OS keyring when it is usable, otherwise
// an owner-only file beside the config. The DFMSCTL_TOKEN_STORE environment
// variable forces "keyring" or "file".
func NewSecretStore() (SecretStore, error) {
	switch os.Getenv(envTokenStore) {
	case "keyring":
		return &keyringStore{backend: osKeyring{}}, nil
	case "file":
		return newFileStore()
	}

	if keyringUsable() {
		return &keyringStore{backend: osKeyring{}}, nil
	}
	return newFileStore()
}

// keyringUsable probes the OS keyring with a throwaway write/delete. This
// detects not just unsupported platforms but also environments where a keyring
// daemon is absent (e.g. headless Linux without a Secret Service).
func keyringUsable() bool {
	const probe = "__dfmsctl_probe__"
	if err := keyring.Set(keyringService, probe, "1"); err != nil {
		return false
	}
	_ = keyring.Delete(keyringService, probe)
	return true
}

// ── Keyring backend ──────────────────────────────────────────

// keyringBackend abstracts the OS keyring so the store can be unit-tested with
// an in-memory fake.
type keyringBackend interface {
	Set(service, user, secret string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

// osKeyring is the production keyringBackend backed by github.com/zalando/go-keyring.
type osKeyring struct{}

func (osKeyring) Set(service, user, secret string) error { return keyring.Set(service, user, secret) }
func (osKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}
func (osKeyring) Delete(service, user string) error { return keyring.Delete(service, user) }

type keyringStore struct {
	backend keyringBackend
}

func (s *keyringStore) Get(context string) ([]byte, error) {
	secret, err := s.backend.Get(keyringService, context)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading keyring: %w", err)
	}
	return []byte(secret), nil
}

func (s *keyringStore) Set(context string, secret []byte) error {
	if err := s.backend.Set(keyringService, context, string(secret)); err != nil {
		return fmt.Errorf("writing keyring: %w", err)
	}
	return nil
}

func (s *keyringStore) Delete(context string) error {
	err := s.backend.Delete(keyringService, context)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("deleting from keyring: %w", err)
	}
	return nil
}

// ── File backend ─────────────────────────────────────────────

// fileStore keeps secrets in an owner-only JSON file beside the config. Secrets
// are base64-encoded so the store can hold arbitrary bytes regardless of their
// content.
type fileStore struct {
	path string
}

func newFileStore() (*fileStore, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	return &fileStore{path: filepath.Join(dir, "tokens.json")}, nil
}

func (s *fileStore) read() (map[string]string, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading secrets file: %w", err)
	}
	secrets := map[string]string{}
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("parsing secrets file: %w", err)
	}
	return secrets, nil
}

func (s *fileStore) write(secrets map[string]string) error {
	data, err := json.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("marshaling secrets: %w", err)
	}
	return writeFileAtomic(s.path, data, filePerm)
}

func (s *fileStore) Get(context string) ([]byte, error) {
	secrets, err := s.read()
	if err != nil {
		return nil, err
	}
	enc, ok := secrets[context]
	if !ok {
		return nil, ErrNotFound
	}
	secret, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, fmt.Errorf("decoding stored secret: %w", err)
	}
	return secret, nil
}

func (s *fileStore) Set(context string, secret []byte) error {
	secrets, err := s.read()
	if err != nil {
		return err
	}
	secrets[context] = base64.StdEncoding.EncodeToString(secret)
	return s.write(secrets)
}

func (s *fileStore) Delete(context string) error {
	secrets, err := s.read()
	if err != nil {
		return err
	}
	if _, ok := secrets[context]; !ok {
		return ErrNotFound
	}
	delete(secrets, context)
	return s.write(secrets)
}
