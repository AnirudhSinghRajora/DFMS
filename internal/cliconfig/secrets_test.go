package cliconfig

import (
	"errors"
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestFileStore_RoundTripAndPermissions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store, err := newFileStore()
	if err != nil {
		t.Fatalf("newFileStore: %v", err)
	}

	if _, getErr := store.Get("prod"); !errors.Is(getErr, ErrNotFound) {
		t.Errorf("Get on empty store = %v, want ErrNotFound", getErr)
	}

	secret := []byte(`{"access_token":"a","refresh_token":"b"}`)
	if setErr := store.Set("prod", secret); setErr != nil {
		t.Fatalf("Set: %v", setErr)
	}

	info, err := os.Stat(store.path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != filePerm {
		t.Errorf("tokens file perm = %v, want %v", perm, filePerm)
	}

	got, err := store.Get("prod")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(secret) {
		t.Errorf("Get = %q, want %q", got, secret)
	}

	if err := store.Delete("prod"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get("prod"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := store.Delete("prod"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete of missing = %v, want ErrNotFound", err)
	}
}

func TestFileStore_IsolatesContexts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store, err := newFileStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("prod", []byte("p")); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("local", []byte("l")); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get("prod"); string(got) != "p" {
		t.Errorf("prod = %q", got)
	}
	if got, _ := store.Get("local"); string(got) != "l" {
		t.Errorf("local = %q", got)
	}
}

func TestKeyringStore_WithFakeBackend(t *testing.T) {
	store := &keyringStore{backend: newFakeKeyring()}

	if _, err := store.Get("prod"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get on empty = %v, want ErrNotFound", err)
	}
	if err := store.Set("prod", []byte("secret")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get("prod")
	if err != nil || string(got) != "secret" {
		t.Errorf("Get = %q, %v", got, err)
	}
	if err := store.Delete("prod"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get("prod"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

// fakeKeyring is an in-memory keyringBackend that mimics go-keyring's ErrNotFound.
type fakeKeyring struct {
	items map[string]string
}

func newFakeKeyring() *fakeKeyring { return &fakeKeyring{items: map[string]string{}} }

func (f *fakeKeyring) key(service, user string) string { return service + "\x00" + user }

func (f *fakeKeyring) Set(service, user, secret string) error {
	f.items[f.key(service, user)] = secret
	return nil
}

func (f *fakeKeyring) Get(service, user string) (string, error) {
	v, ok := f.items[f.key(service, user)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeKeyring) Delete(service, user string) error {
	k := f.key(service, user)
	if _, ok := f.items[k]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.items, k)
	return nil
}
