package userattachments

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var errSessionNotFound = errors.New("no stored GitHub web session")

// sessionStore keeps a GitHub web session in a tool-owned location so the CLI
// never reads browser cookie stores or other applications' credentials.
type sessionStore interface {
	Set(value string) error
	Get() (string, error)
	Delete() error
}

// fileSessionStore stores the session as a 0600 file in the tool state
// directory, the same acquisition-then-file pattern used by other
// CDP-based CLIs.
type fileSessionStore struct {
	path string
}

func defaultSessionStore() (fileSessionStore, error) {
	dir, err := toolStateDir()
	if err != nil {
		return fileSessionStore{}, err
	}
	return fileSessionStore{path: filepath.Join(dir, "session")}, nil
}

func toolStateDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config directory: %w", err)
	}
	return filepath.Join(base, "gh-user-attachments"), nil
}

func (s fileSessionStore) Set(value string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, []byte(value+"\n"), 0o600); err != nil {
		return fmt.Errorf("write stored session: %w", err)
	}
	if err := os.Rename(temporary, s.path); err != nil {
		return fmt.Errorf("publish stored session: %w", err)
	}
	return nil
}

func (s fileSessionStore) Get() (string, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", errSessionNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read stored session: %w", err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", errSessionNotFound
	}
	return value, nil
}

func (s fileSessionStore) Delete() error {
	err := os.Remove(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete stored session: %w", err)
	}
	return nil
}

// resolveNativeSession uses the environment override for headless and bot use
// first, then the stored session file.
func resolveNativeSession(getenv func(string) string, store sessionStore) (string, error) {
	if value := strings.TrimSpace(getenv(githubSessionEnvironment)); value != "" {
		return value, nil
	}
	value, err := store.Get()
	if errors.Is(err, errSessionNotFound) {
		return "", fmt.Errorf("no GitHub web session found; run `gh user-attachments auth login` or set %s", githubSessionEnvironment)
	}
	if err != nil {
		return "", err
	}
	return value, nil
}
