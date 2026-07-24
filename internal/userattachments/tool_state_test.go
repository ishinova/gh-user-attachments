package userattachments

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func testToolState(t *testing.T) *toolState {
	t.Helper()
	ts, err := openToolStateAt(t.TempDir(), "state")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ts.Close() })
	return ts
}

func TestToolStateRejectsSymlinkStateDirectory(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(outside, "marker")
	if err := os.WriteFile(marker, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	anchor := filepath.Join(root, "anchor")
	if err := os.MkdirAll(anchor, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(anchor, "state")); err != nil {
		t.Fatal(err)
	}

	_, err := openToolStateAt(anchor, "state")
	if err == nil {
		t.Fatal("expected symlink state directory rejection")
	}
	data, readErr := os.ReadFile(marker)
	if readErr != nil || string(data) != "keep\n" {
		t.Fatalf("outside marker changed: %q err=%v", data, readErr)
	}
}

func TestToolStateRejectsSymlinkAnchorFinalComponent(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(outside, "marker")
	if err := os.WriteFile(marker, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bridge := filepath.Join(root, "bridge")
	if err := os.Symlink(outside, bridge); err != nil {
		t.Fatal(err)
	}

	_, err := openToolStateAt(bridge, "state")
	if err == nil {
		t.Fatal("expected final-component symlink anchor rejection")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "marker" {
			t.Fatalf("unexpected outside entry %q", entry.Name())
		}
	}
	data, readErr := os.ReadFile(marker)
	if readErr != nil || string(data) != "keep\n" {
		t.Fatalf("outside marker changed: %q err=%v", data, readErr)
	}
}

// TestToolStateNestedAncestorSymlinkIsOutsideTrustedAnchorContract documents
// that openToolStateAt is not a full-path confinement primitive. A nested
// ancestor symlink (bridge -> outside, anchor = bridge/config) can open and
// write under outside. Production trusts os.UserConfigDir() as an external
// precondition and does not claim to prevent this class of untrusted anchor.
func TestToolStateNestedAncestorSymlinkIsOutsideTrustedAnchorContract(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	config := filepath.Join(outside, "config")
	if err := os.MkdirAll(config, 0o700); err != nil {
		t.Fatal(err)
	}
	bridge := filepath.Join(root, "bridge")
	if err := os.Symlink(outside, bridge); err != nil {
		t.Fatal(err)
	}
	untrustedAnchor := filepath.Join(bridge, "config")

	ts, err := openToolStateAt(untrustedAnchor, "state")
	if err != nil {
		t.Fatalf("untrusted nested-ancestor anchor unexpectedly rejected: %v", err)
	}
	defer ts.Close()
	if err := ts.setSession("escaped"); err != nil {
		t.Fatal(err)
	}
	escapedSession := filepath.Join(config, "state", sessionFileName)
	data, err := os.ReadFile(escapedSession)
	if err != nil || strings.TrimSpace(string(data)) != "escaped" {
		t.Fatalf("expected write under outside via untrusted anchor; data=%q err=%v", data, err)
	}
}

func TestSameFileIdentityRejectsReplacement(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "a")
	replacement := filepath.Join(dir, "b")
	if err := os.WriteFile(original, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(original)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(original)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	after, err := os.Lstat(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if err := sameRegularFile(before, after); err == nil {
		t.Fatal("expected different files to fail identity check")
	}
}

func TestToolStateSessionRoundTrip(t *testing.T) {
	ts := testToolState(t)
	if err := ts.setSession("session-value"); err != nil {
		t.Fatal(err)
	}
	assertPublishedSessionName(t, ts, "session-value")
	if err := ts.setSession("rotated-value"); err != nil {
		t.Fatal(err)
	}
	assertPublishedSessionName(t, ts, "rotated-value")
	if err := ts.deleteSession(); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.getSession(); !errors.Is(err, errSessionNotFound) {
		t.Fatalf("Get after Delete error = %v", err)
	}
}

func TestToolStateSetRemovesLegacyTempSymlinkWithoutFollowing(t *testing.T) {
	ts := testToolState(t)
	target := filepath.Join(t.TempDir(), "outside-secret")
	if err := os.WriteFile(target, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, ts.abs(legacySessionTempName)); err != nil {
		t.Fatal(err)
	}
	if err := ts.setSession("session-value"); err != nil {
		t.Fatal(err)
	}
	assertPublishedSessionName(t, ts, "session-value")
	if _, err := os.Lstat(ts.abs(legacySessionTempName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("legacy temp symlink residual: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "outside\n" {
		t.Fatalf("symlink target rewritten: %q", data)
	}
}

func TestToolStateSetRemovesLegacyTempFileBeforePublication(t *testing.T) {
	ts := testToolState(t)
	if err := os.WriteFile(ts.abs(legacySessionTempName), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ts.setSession("session-value"); err != nil {
		t.Fatal(err)
	}
	assertPublishedSessionName(t, ts, "session-value")
	if _, err := os.Lstat(ts.abs(legacySessionTempName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("legacy temp file residual: %v", err)
	}
}

func TestToolStateSetRejectsNonEmptyLegacyTempDirectory(t *testing.T) {
	ts := testToolState(t)
	if err := ts.setSession("keep-me"); err != nil {
		t.Fatal(err)
	}
	tempDir := ts.abs(legacySessionTempName)
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "nested"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ts.setSession("replacement")
	if err == nil || !strings.Contains(err.Error(), "legacy session temporary") {
		t.Fatalf("error=%v", err)
	}
	assertPublishedSessionName(t, ts, "keep-me")
	assertNoSessionTempResidue(t, ts)
}

func TestToolStateSetRejectsLegacyTempDirectoryWhenNoSession(t *testing.T) {
	ts := testToolState(t)
	tempDir := ts.abs(legacySessionTempName)
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "nested"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ts.setSession("replacement")
	if err == nil || !strings.Contains(err.Error(), "legacy session temporary") {
		t.Fatalf("error=%v", err)
	}
	if _, err := ts.getSession(); !errors.Is(err, errSessionNotFound) {
		t.Fatalf("session unexpectedly present: %v", err)
	}
	assertNoSessionTempResidue(t, ts)
}

func TestToolStateRejectsPermissiveStateDirectoryWithoutRepair(t *testing.T) {
	anchor := t.TempDir()
	statePath := filepath.Join(anchor, "state")
	if err := os.MkdirAll(statePath, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := openToolStateAt(anchor, "state")
	if err == nil {
		t.Fatal("expected permissive state directory rejection")
	}
	if !strings.Contains(err.Error(), "mode 0700") {
		t.Fatalf("error=%v", err)
	}
	info, err := os.Lstat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("state dir was repaired: %o", info.Mode().Perm())
	}
}

func TestToolStateCreatesStateDirectoryMode0700(t *testing.T) {
	anchor := t.TempDir()
	ts, err := openToolStateAt(anchor, "state")
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()
	info, err := os.Lstat(filepath.Join(anchor, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := assertSecureDirectoryMode(info, "tool state directory"); err != nil {
		t.Fatal(err)
	}
}

func TestToolStateRejectsSymlinkStateDirectoryWithoutMutatingTarget(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(outside, "marker")
	if err := os.WriteFile(marker, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	anchor := filepath.Join(root, "anchor")
	if err := os.MkdirAll(anchor, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(anchor, "state")); err != nil {
		t.Fatal(err)
	}

	_, err := openToolStateAt(anchor, "state")
	if err == nil {
		t.Fatal("expected symlink state directory rejection")
	}
	info, err := os.Lstat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("outside target mode changed: %o", info.Mode().Perm())
	}
	data, readErr := os.ReadFile(marker)
	if readErr != nil || string(data) != "keep\n" {
		t.Fatalf("outside marker changed: %q err=%v", data, readErr)
	}
}

func TestToolStateGetRejectsSymlink(t *testing.T) {
	ts := testToolState(t)
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("session-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, ts.abs(sessionFileName)); err != nil {
		t.Fatal(err)
	}
	value, err := ts.getSession()
	if err == nil || value != "" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if errors.Is(err, errSessionNotFound) {
		t.Fatalf("symlink must not look like missing session: %v", err)
	}
	if containsSecret(err.Error(), "session-value") {
		t.Fatalf("error leaked secret: %v", err)
	}
}

func TestToolStateGetRejectsNonRegularFile(t *testing.T) {
	ts := testToolState(t)
	if err := os.Mkdir(ts.abs(sessionFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.getSession(); err == nil {
		t.Fatal("expected non-regular session rejection")
	}
}

func TestToolStateGetRejectsPermissiveMode(t *testing.T) {
	ts := testToolState(t)
	if err := os.WriteFile(ts.abs(sessionFileName), []byte("session-value\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	value, err := ts.getSession()
	if err == nil || value != "" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if containsSecret(err.Error(), "session-value") {
		t.Fatalf("error leaked secret: %v", err)
	}
}

func TestToolStateDeleteReportsLegacyTempFailure(t *testing.T) {
	ts := testToolState(t)
	tempDir := ts.abs(legacySessionTempName)
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "nested"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ts.deleteSession()
	if err == nil {
		t.Fatal("expected legacy temp deletion failure")
	}
	if !containsSecret(err.Error(), "legacy session temporary") {
		t.Fatalf("error=%v", err)
	}
}

func TestToolStateLogoutSymlinkRootLeavesOutsideIntact(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	outsideSession := filepath.Join(outside, "session")
	outsideProfile := filepath.Join(outside, "chrome-profile")
	if err := os.WriteFile(outsideSession, []byte("keep-session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(outsideProfile, "marker")
	if err := os.WriteFile(marker, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	anchor := filepath.Join(root, "anchor")
	if err := os.MkdirAll(anchor, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(anchor, "state")); err != nil {
		t.Fatal(err)
	}
	_, err := openToolStateAt(anchor, "state")
	if err == nil {
		t.Fatal("expected symlink state rejection")
	}
	data, readErr := os.ReadFile(outsideSession)
	if readErr != nil || string(data) != "keep-session\n" {
		t.Fatalf("outside session changed: %q err=%v", data, readErr)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("outside profile changed: %v", err)
	}
}

func TestToolStateConcurrentProfileAcquisitionKeepsActiveOrBusy(t *testing.T) {
	anchor := t.TempDir()
	first, err := openToolStateAt(anchor, "state")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := openToolStateAt(anchor, "state")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	var started sync.WaitGroup
	started.Add(1)
	var firstActive atomic.Bool
	var secondBusy atomic.Bool
	var firstDone sync.WaitGroup
	firstDone.Add(1)

	go func() {
		_ = first.withAuthLock(func() error {
			return first.withEphemeralChromeProfile(func(profileDir string) error {
				firstActive.Store(true)
				started.Done()
				time.Sleep(200 * time.Millisecond)
				info, err := first.root.Lstat(filepath.Base(profileDir))
				if err != nil || !info.IsDir() {
					t.Errorf("active profile missing during first acquisition: %v", err)
				}
				firstActive.Store(false)
				return nil
			})
		})
		firstDone.Done()
	}()

	started.Wait()
	err = second.withAuthLock(func() error {
		return second.withEphemeralChromeProfile(func(string) error { return nil })
	})
	if errors.Is(err, errAuthBusy) {
		secondBusy.Store(true)
	} else if err != nil {
		t.Fatalf("second acquisition error=%v", err)
	}
	firstDone.Wait()
	if !secondBusy.Load() && err != nil {
		t.Fatalf("expected busy or serialized success, got %v", err)
	}
}

func TestToolStateAuthLockContendsAcrossHandles(t *testing.T) {
	anchor := t.TempDir()
	holder, err := openToolStateAt(anchor, "state")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	contender, err := openToolStateAt(anchor, "state")
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()

	holding := make(chan struct{})
	release := make(chan struct{})
	var holderErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		holderErr = holder.withAuthLock(func() error {
			close(holding)
			<-release
			return nil
		})
	}()
	select {
	case <-holding:
	case <-time.After(2 * time.Second):
		t.Fatal("holder did not acquire lock")
	}
	err = contender.withAuthLock(func() error { return nil })
	if !errors.Is(err, errAuthBusy) {
		t.Fatalf("contender error=%v", err)
	}
	close(release)
	wg.Wait()
	if holderErr != nil {
		t.Fatalf("holder error=%v", holderErr)
	}
	info, err := holder.root.Lstat(authLockName)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertSecureLockFileInfo(info); err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
}

func TestToolStateAuthLockCreatesMode0600(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })
	ts := testToolState(t)
	if err := ts.withAuthLock(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	info, err := ts.root.Lstat(authLockName)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertSecureLockFileInfo(info); err != nil {
		t.Fatal(err)
	}
}

func TestToolStateAuthLockRejectsOutsideSymlinkWithoutFollowing(t *testing.T) {
	ts := testToolState(t)
	target := filepath.Join(t.TempDir(), "outside-lock")
	if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, ts.abs(authLockName)); err != nil {
		t.Fatal(err)
	}
	called := false
	err := ts.withAuthLock(func() error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("callback ran or error missing: called=%v err=%v", called, err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != "keep\n" {
		t.Fatalf("outside target changed: %q err=%v", data, readErr)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o666 {
		t.Fatalf("outside target mode changed: %o", info.Mode().Perm())
	}
}

func TestToolStateAuthLockRejectsInsideSymlink(t *testing.T) {
	ts := testToolState(t)
	if err := ts.setSession("session-secret"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sessionFileName, ts.abs(authLockName)); err != nil {
		t.Fatal(err)
	}
	called := false
	err := ts.withAuthLock(func() error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("callback ran or error missing: called=%v err=%v", called, err)
	}
	if !strings.Contains(err.Error(), "symlink") && !strings.Contains(err.Error(), "auth lock") {
		t.Fatalf("error=%v", err)
	}
	assertPublishedSessionName(t, ts, "session-secret")
}

func TestToolStateAuthLockRejectsPermissiveMode(t *testing.T) {
	ts := testToolState(t)
	lockPath := ts.abs(authLockName)
	if err := os.WriteFile(lockPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lockPath, 0o666); err != nil {
		t.Fatal(err)
	}
	called := false
	err := ts.withAuthLock(func() error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("callback ran or error missing: called=%v err=%v", called, err)
	}
	info, err := ts.root.Lstat(authLockName)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o666 {
		t.Fatalf("permissive lock was repaired: %o", info.Mode().Perm())
	}
}

func TestToolStateAuthLockRejectsNonRegular(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		ts := testToolState(t)
		if err := os.Mkdir(ts.abs(authLockName), 0o700); err != nil {
			t.Fatal(err)
		}
		called := false
		err := ts.withAuthLock(func() error {
			called = true
			return nil
		})
		if err == nil || called {
			t.Fatalf("callback ran or error missing: called=%v err=%v", called, err)
		}
		info, err := ts.root.Lstat(authLockName)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Fatalf("non-regular lock was replaced: mode=%v", info.Mode())
		}
	})
	t.Run("fifo", func(t *testing.T) {
		ts := testToolState(t)
		if err := syscall.Mkfifo(ts.abs(authLockName), 0o600); err != nil {
			t.Fatal(err)
		}
		called := false
		err := ts.withAuthLock(func() error {
			called = true
			return nil
		})
		if err == nil || called {
			t.Fatalf("callback ran or error missing: called=%v err=%v", called, err)
		}
		info, err := ts.root.Lstat(authLockName)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Type()&fs.ModeNamedPipe == 0 {
			t.Fatalf("fifo lock was replaced: mode=%v", info.Mode())
		}
	})
}

func TestWithEphemeralChromeProfileCleansUpAfterSuccess(t *testing.T) {
	ts := testToolState(t)
	var seen string
	err := ts.withEphemeralChromeProfile(func(profileDir string) error {
		seen = profileDir
		info, err := os.Lstat(profileDir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("profile perm = %o", info.Mode().Perm())
		}
		if err := os.WriteFile(filepath.Join(profileDir, "marker"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == "" {
		t.Fatal("action was not called")
	}
	if _, err := os.Lstat(seen); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("profile residual after success: %v", err)
	}
}

func TestWithEphemeralChromeProfileCleansUpAfterError(t *testing.T) {
	ts := testToolState(t)
	var seen string
	err := ts.withEphemeralChromeProfile(func(profileDir string) error {
		seen = profileDir
		return errors.New("action failed")
	})
	if err == nil || !containsSecret(err.Error(), "action failed") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Lstat(seen); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("profile residual after error: %v", err)
	}
}

func TestWithEphemeralChromeProfileRemovesLegacyResidueFirst(t *testing.T) {
	ts := testToolState(t)
	legacy := ts.abs(legacyChromeProfileName)
	orphan := ts.abs(chromeProfilePrefix + "old")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ts.setSession("keep"); err != nil {
		t.Fatal(err)
	}
	err := ts.withEphemeralChromeProfile(func(profileDir string) error {
		if _, err := os.Lstat(legacy); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("legacy profile still present during action: %v", err)
		}
		if _, err := os.Lstat(orphan); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("orphan profile still present during action: %v", err)
		}
		if profileDir == legacy || profileDir == orphan {
			t.Fatalf("reused residue path %q", profileDir)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := ts.getSession()
	if err != nil || value != "keep" {
		t.Fatalf("session touched: %q err=%v", value, err)
	}
}

func TestRemoveOwnedNameDoesNotFollowSymlink(t *testing.T) {
	ts := testToolState(t)
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "marker")
	if err := os.WriteFile(marker, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, ts.abs(legacyChromeProfileName)); err != nil {
		t.Fatal(err)
	}
	if err := ts.removeOwnedName(legacyChromeProfileName); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(ts.abs(legacyChromeProfileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("symlink residual: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("symlink target was removed: %v", err)
	}
}

func TestCleanupChromeProfilesLeavesSession(t *testing.T) {
	ts := testToolState(t)
	if err := os.MkdirAll(ts.abs(legacyChromeProfileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ts.setSession("session"); err != nil {
		t.Fatal(err)
	}
	if err := ts.cleanupChromeProfileResidue(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(ts.abs(legacyChromeProfileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("legacy residual: %v", err)
	}
	value, err := ts.getSession()
	if err != nil || value != "session" {
		t.Fatalf("session removed: %q err=%v", value, err)
	}
}

func TestCleanupAuthResidueRemovesOrphanSessionTemps(t *testing.T) {
	ts := testToolState(t)
	if err := ts.setSession("keep-session"); err != nil {
		t.Fatal(err)
	}
	if err := ts.withAuthLock(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	orphanName := ".session-deadbeef01234567"
	if err := os.WriteFile(ts.abs(orphanName), []byte("orphan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside-secret")
	if err := os.WriteFile(target, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkName := ".session-abcdabcdabcdabcd"
	if err := os.Symlink(target, ts.abs(linkName)); err != nil {
		t.Fatal(err)
	}

	// Published session must survive the orphan sweep. Full cleanupAuthResidue
	// also deletes session (logout), so exercise the helper it joins.
	if err := ts.cleanupSessionTempResidue(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(ts.abs(orphanName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("orphan residual: %v", err)
	}
	if _, err := os.Lstat(ts.abs(linkName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("orphan symlink residual: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "outside\n" {
		t.Fatalf("symlink target damaged: %q err=%v", data, err)
	}
	value, err := ts.getSession()
	if err != nil || value != "keep-session" {
		t.Fatalf("session removed: %q err=%v", value, err)
	}
	if _, err := ts.root.Lstat(authLockName); err != nil {
		t.Fatalf("auth.lock missing: %v", err)
	}
}

func assertPublishedSessionName(t *testing.T, ts *toolState, want string) {
	t.Helper()
	info, err := ts.root.Lstat(sessionFileName)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Type()&fs.ModeSymlink != 0 {
		t.Fatal("published session is a symlink")
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("published session mode type = %v", info.Mode().Type())
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("published session perm = %o", info.Mode().Perm())
	}
	value, err := ts.getSession()
	if err != nil || value != want {
		t.Fatalf("value=%q error=%v", value, err)
	}
}

func assertNoSessionTempResidue(t *testing.T, ts *toolState) {
	t.Helper()
	entries, err := ts.readRootNames()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range entries {
		if strings.HasPrefix(name, ".session-") {
			t.Fatalf("unexpected session temp residue %q", name)
		}
	}
}
