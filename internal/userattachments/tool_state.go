package userattachments

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	toolStateDirName        = "gh-user-attachments"
	sessionFileName         = "session"
	legacySessionTempName   = "session.tmp"
	authLockName            = "auth.lock"
	legacyChromeProfileName = "chrome-profile"
	chromeProfilePrefix     = "chrome-profile-"
)

var (
	errSessionNotFound = errors.New("no stored GitHub web session")
	errAuthBusy        = errors.New("another auth login or logout is already in progress")
)

// toolState owns the tool state directory through an os.Root handle.
//
// Production trusted-anchor contract:
//   - os.UserConfigDir() is an external OS precondition. This package does not
//     claim to detect or reject ancestor symlinks on the path to that base.
//   - Within the opened root, session / profile / lock / temp operations are
//     root-relative and must not follow child symlinks out of the tool state.
//   - Same-UID processes are trusted not to rename or replace the UserConfigDir
//     or tool-state pathname while an auth login holds an open root and hands
//     an absolute Chrome profile path to the browser. os.Root cannot control
//     what that absolute pathname later resolves to after handoff. This package
//     does not defend against a malicious same-UID actor and does not claim
//     absolute profile pathname confinement.
//
// openToolStateAt is a package helper for tests and for opening the relative
// child under a caller-supplied absolute directory. It is not a portable
// full-path confinement primitive: it only rejects a final-component symlink
// anchor and a symlink state child. Nested ancestor symlink escapes through
// intermediate path components remain outside the production security claim.
type toolState struct {
	root *os.Root
}

// toolStateUserConfigDir is the configuration-directory lookup tool state
// resolution performs. Tests replace it to record whether resolution still
// depends on it; the environment variable names behind it are defined by the
// standard library and differ per platform.
var toolStateUserConfigDir = os.UserConfigDir

func defaultToolState() (*toolState, error) {
	base, err := toolStateUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("locate user config directory: %w", err)
	}
	return openToolStateAt(base, toolStateDirName)
}

// openToolStateAt opens anchorDir and returns a Root scoped to a single
// relative child. The child name must be a single path element. If the child
// already exists as a symlink, opening fails without following it.
//
// The final path component of anchorDir must itself be a real directory
// (not a symlink). This is a local misuse check for tests and misconfigured
// callers; it does not walk ancestors and does not make an arbitrary absolute
// anchor a trusted production boundary. Production callers must pass
// os.UserConfigDir().
func openToolStateAt(anchorDir, name string) (*toolState, error) {
	if err := validateRootRelativeName(name); err != nil {
		return nil, err
	}
	// Lstat only inspects the final path component. Intermediate symlinks in
	// the anchor path (for example bridge/config where bridge -> outside) are
	// resolved by the OS before Lstat and are outside this helper's claim.
	anchorInfo, err := os.Lstat(anchorDir)
	if err != nil {
		return nil, fmt.Errorf("stat tool state anchor: %w", err)
	}
	if anchorInfo.Mode().Type()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("tool state anchor must not be a symlink")
	}
	if !anchorInfo.IsDir() {
		return nil, fmt.Errorf("tool state anchor is not a directory")
	}
	anchor, err := os.OpenRoot(anchorDir)
	if err != nil {
		return nil, fmt.Errorf("open tool state anchor: %w", err)
	}
	defer anchor.Close()

	info, err := anchor.Lstat(name)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := anchor.Mkdir(name, 0o700); err != nil {
			return nil, fmt.Errorf("create tool state directory: %w", err)
		}
		info, err = anchor.Lstat(name)
		if err != nil {
			return nil, fmt.Errorf("stat tool state directory: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("stat tool state directory: %w", err)
	}
	if err := assertSecureDirectoryMode(info, "tool state directory"); err != nil {
		return nil, err
	}
	root, err := anchor.OpenRoot(name)
	if err != nil {
		return nil, fmt.Errorf("open tool state root: %w", err)
	}
	return &toolState{root: root}, nil
}

func (ts *toolState) Close() error {
	if ts == nil || ts.root == nil {
		return nil
	}
	return ts.root.Close()
}

// abs builds an absolute pathname for same-user Chrome handoff. It is not a
// confinement boundary against concurrent same-UID rename or replace of the
// trusted state root path after the os.Root handle was opened.
func (ts *toolState) abs(name string) string {
	return filepath.Join(ts.root.Name(), name)
}

func assertSecureDirectoryMode(info fs.FileInfo, label string) error {
	if info.Mode().Type()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink", label)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", label)
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("%s must have mode 0700", label)
	}
	return nil
}

func validateRootRelativeName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid tool state name")
	}
	if strings.Contains(name, string(filepath.Separator)) || strings.Contains(name, "/") || strings.Contains(name, `\`) {
		return fmt.Errorf("invalid tool state name")
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("invalid tool state name")
	}
	return nil
}

func (ts *toolState) setSession(value string) (err error) {
	if err := ts.removeLegacySessionTempLinkOrFile(); err != nil {
		return err
	}

	temporaryName, temporary, err := ts.createExclusiveTemp(".session-", 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = temporary.Close()
			_ = ts.root.Remove(temporaryName)
		}
	}()

	if _, err := io.WriteString(temporary, value+"\n"); err != nil {
		return fmt.Errorf("write stored session: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync stored session: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close stored session: %w", err)
	}
	if err := ts.assertSecureRegularFile(temporaryName); err != nil {
		return err
	}
	if err := ts.root.Rename(temporaryName, sessionFileName); err != nil {
		return fmt.Errorf("publish stored session: %w", err)
	}
	if err := ts.assertSecureRegularFile(sessionFileName); err != nil {
		return err
	}
	return nil
}

func (ts *toolState) getSession() (string, error) {
	before, err := ts.root.Lstat(sessionFileName)
	if errors.Is(err, os.ErrNotExist) {
		return "", errSessionNotFound
	}
	if err != nil {
		return "", fmt.Errorf("stat stored session: %w", err)
	}
	if err := assertSecureRegularFileInfo(before); err != nil {
		return "", err
	}

	file, err := ts.root.Open(sessionFileName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errSessionNotFound
		}
		return "", fmt.Errorf("read stored session: %w", err)
	}
	defer file.Close()

	after, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat opened session: %w", err)
	}
	if err := sameRegularFile(before, after); err != nil {
		return "", err
	}
	if err := assertSecureRegularFileInfo(after); err != nil {
		return "", err
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("read stored session: %w", err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", errSessionNotFound
	}
	return value, nil
}

func (ts *toolState) deleteSession() error {
	var errs []error
	if err := ts.root.Remove(sessionFileName); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("delete stored session: %w", err))
	}
	if err := ts.removeLegacySessionTempLinkOrFile(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// removeLegacySessionTempLinkOrFile deletes a legacy session.tmp symlink or
// regular file only. Directories are rejected without recursion. Missing is OK.
func (ts *toolState) removeLegacySessionTempLinkOrFile() error {
	info, err := ts.root.Lstat(legacySessionTempName)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat legacy session temporary: %w", err)
	}
	if info.Mode().Type()&fs.ModeSymlink == 0 && info.IsDir() {
		return fmt.Errorf("delete legacy session temporary: is a directory")
	}
	if err := ts.root.Remove(legacySessionTempName); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete legacy session temporary: %w", err)
	}
	return nil
}

func (ts *toolState) createExclusiveTemp(prefix string, perm fs.FileMode) (string, *os.File, error) {
	for range 10000 {
		suffix, err := randomHex(8)
		if err != nil {
			return "", nil, fmt.Errorf("create temporary session file: %w", err)
		}
		name := prefix + suffix
		file, err := ts.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
		if err == nil {
			return name, file, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", nil, fmt.Errorf("create temporary session file: %w", err)
		}
	}
	return "", nil, fmt.Errorf("create temporary session file: exhausted unique names")
}

func (ts *toolState) assertSecureRegularFile(name string) error {
	info, err := ts.root.Lstat(name)
	if err != nil {
		return fmt.Errorf("stat session file: %w", err)
	}
	return assertSecureRegularFileInfo(info)
}

func assertSecureRegularFileInfo(info fs.FileInfo) error {
	if info.Mode().Type()&fs.ModeSymlink != 0 {
		return fmt.Errorf("session file must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("session file must be a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("session file must have mode 0600")
	}
	return nil
}

func sameRegularFile(before, after fs.FileInfo) error {
	if !os.SameFile(before, after) {
		return fmt.Errorf("session file changed during read")
	}
	return nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// withEphemeralChromeProfile creates one owned profile under the root, runs
// action with an absolute profile path, and deletes only that owned name.
// Absolute path handoff to Chrome assumes no concurrent same-UID rename or
// replace of the trusted UserConfigDir/state pathname; that race is outside
// the security claim. Legacy residue cleanup runs first and must be called
// only while the auth lock is held.
func (ts *toolState) withEphemeralChromeProfile(action func(profileDir string) error) (err error) {
	if err := ts.cleanupChromeProfileResidue(); err != nil {
		return err
	}
	name, err := ts.mkdirUnique(chromeProfilePrefix, 0o700)
	if err != nil {
		return err
	}
	profileDir := ts.abs(name)
	defer func() {
		if cleanupErr := ts.removeOwnedName(name); cleanupErr != nil {
			cleanupErr = fmt.Errorf("delete chrome profile directory: %w", cleanupErr)
			err = errors.Join(err, cleanupErr)
		}
	}()
	return action(profileDir)
}

func (ts *toolState) mkdirUnique(prefix string, perm fs.FileMode) (string, error) {
	for range 10000 {
		suffix, err := randomHex(8)
		if err != nil {
			return "", fmt.Errorf("create chrome profile directory: %w", err)
		}
		name := prefix + suffix
		err = ts.root.Mkdir(name, perm)
		if err == nil {
			info, statErr := ts.root.Lstat(name)
			if statErr != nil {
				_ = ts.removeOwnedName(name)
				return "", fmt.Errorf("stat chrome profile directory: %w", statErr)
			}
			if verifyErr := assertSecureDirectoryMode(info, "chrome profile directory"); verifyErr != nil {
				_ = ts.removeOwnedName(name)
				return "", verifyErr
			}
			return name, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create chrome profile directory: %w", err)
		}
	}
	return "", fmt.Errorf("create chrome profile directory: exhausted unique names")
}

func (ts *toolState) cleanupChromeProfileResidue() error {
	entries, err := ts.readRootNames()
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range entries {
		if name != legacyChromeProfileName && !strings.HasPrefix(name, chromeProfilePrefix) {
			continue
		}
		if err := ts.removeOwnedName(name); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("delete chrome profile residue: %w", errors.Join(errs...))
	}
	return nil
}

// cleanupAuthResidue is the authoritative logout cleanup for tool-owned state:
// session, legacy session temporary, orphan .session-* publication temps, and
// chrome profile residue. auth.lock is a reusable flock file and is
// intentionally retained.
func (ts *toolState) cleanupAuthResidue() error {
	return errors.Join(
		ts.deleteSession(),
		ts.cleanupSessionTempResidue(),
		ts.cleanupChromeProfileResidue(),
	)
}

// cleanupSessionTempResidue deletes orphan exclusive publication temps whose
// names start with ".session-". Symlink targets outside the root are not
// followed; removeOwnedName deletes the link itself.
func (ts *toolState) cleanupSessionTempResidue() error {
	entries, err := ts.readRootNames()
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range entries {
		if !strings.HasPrefix(name, ".session-") {
			continue
		}
		if err := ts.removeOwnedName(name); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("delete session temp residue: %w", errors.Join(errs...))
	}
	return nil
}

func (ts *toolState) readRootNames() ([]string, error) {
	entries, err := fs.ReadDir(ts.root.FS(), ".")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list tool state directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, nil
}

// removeOwnedName deletes a root-relative entry without following a final
// symlink target outside the root. Root.Remove/RemoveAll already refuse
// escape; Lstat-first keeps symlink-to-outside as link-only deletion.
func (ts *toolState) removeOwnedName(name string) error {
	if err := validateRootRelativeName(name); err != nil {
		return err
	}
	info, err := ts.root.Lstat(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode().Type()&fs.ModeSymlink != 0 || !info.IsDir() {
		return ts.root.Remove(name)
	}
	return ts.root.RemoveAll(name)
}

func (ts *toolState) withAuthLock(fn func() error) (err error) {
	file, err := ts.openAuthLock()
	if err != nil {
		return err
	}
	defer func() {
		unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		closeErr := file.Close()
		err = errors.Join(err, unlockErr, closeErr)
	}()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return errAuthBusy
		}
		return fmt.Errorf("acquire auth lock: %w", err)
	}
	return fn()
}

// openAuthLock opens auth.lock as a non-symlink regular file with mode exactly
// 0600. os.Root may follow an in-root final symlink, so type/mode/identity are
// validated with Lstat and the opened fd before the lock is used. Invalid
// existing locks are rejected without chmod repair. Creation uses O_EXCL and
// O_NOFOLLOW, then Chmod(0600) so a new lock is 0600 despite umask.
func (ts *toolState) openAuthLock() (*os.File, error) {
	for range 10000 {
		info, err := ts.root.Lstat(authLockName)
		switch {
		case errors.Is(err, os.ErrNotExist):
			file, err := ts.root.OpenFile(authLockName, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
			if errors.Is(err, os.ErrExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("open auth lock: %w", err)
			}
			if err := file.Chmod(0o600); err != nil {
				_ = file.Close()
				return nil, fmt.Errorf("restrict auth lock: %w", err)
			}
			if err := ts.validateOpenAuthLock(file); err != nil {
				_ = file.Close()
				return nil, err
			}
			return file, nil
		case err != nil:
			return nil, fmt.Errorf("stat auth lock: %w", err)
		}
		if err := assertSecureLockFileInfo(info); err != nil {
			return nil, err
		}
		file, err := ts.root.OpenFile(authLockName, os.O_RDWR|syscall.O_NOFOLLOW, 0)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("open auth lock: %w", err)
		}
		if err := ts.validateOpenAuthLock(file); err != nil {
			_ = file.Close()
			return nil, err
		}
		return file, nil
	}
	return nil, fmt.Errorf("open auth lock: exhausted retries")
}

func (ts *toolState) validateOpenAuthLock(file *os.File) error {
	before, err := ts.root.Lstat(authLockName)
	if err != nil {
		return fmt.Errorf("stat auth lock: %w", err)
	}
	if err := assertSecureLockFileInfo(before); err != nil {
		return err
	}
	after, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat opened auth lock: %w", err)
	}
	if err := assertSecureLockFileInfo(after); err != nil {
		return err
	}
	if !os.SameFile(before, after) {
		return fmt.Errorf("auth lock changed during open")
	}
	return nil
}

func assertSecureLockFileInfo(info fs.FileInfo) error {
	if info.Mode().Type()&fs.ModeSymlink != 0 {
		return fmt.Errorf("auth lock must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("auth lock must be a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("auth lock must have mode 0600")
	}
	return nil
}

// sessionReader loads a stored GitHub web session for status and upload.
// Durable login/logout mutations are owned exclusively by toolState while the
// auth lock is held; this interface is intentionally read-only.
type sessionReader interface {
	Get() (string, error)
}

// toolSessionStore is the production sessionReader for status/upload.
// Login and logout mutate durable session state only through a locked toolState.
type toolSessionStore struct{}

func defaultSessionStore() (sessionReader, error) {
	return toolSessionStore{}, nil
}

func (toolSessionStore) Get() (string, error) {
	ts, err := defaultToolState()
	if err != nil {
		return "", err
	}
	defer ts.Close()
	return ts.getSession()
}

// resolveNativeSession uses the environment override for headless and bot use
// first, then the stored session file.
func resolveNativeSession(getenv func(string) string, store sessionReader) (string, error) {
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
