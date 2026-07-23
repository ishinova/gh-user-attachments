package userattachments

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSessionStoreRoundTripKeepsRestrictivePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "session")
	store := fileSessionStore{path: path}

	if _, err := store.Get(); !errors.Is(err, errSessionNotFound) {
		t.Fatalf("Get before Set error = %v", err)
	}
	if err := store.Set("session-value"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session file perm = %o", info.Mode().Perm())
	}
	value, err := store.Get()
	if err != nil || value != "session-value" {
		t.Fatalf("value=%q error=%v", value, err)
	}

	if err := store.Set("rotated-value"); err != nil {
		t.Fatal(err)
	}
	value, err = store.Get()
	if err != nil || value != "rotated-value" {
		t.Fatalf("value=%q error=%v", value, err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session file perm after rewrite = %o", info.Mode().Perm())
	}

	if err := store.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(); !errors.Is(err, errSessionNotFound) {
		t.Fatalf("Get after Delete error = %v", err)
	}
	if err := store.Delete(); err != nil {
		t.Fatalf("Delete must be idempotent: %v", err)
	}
}
