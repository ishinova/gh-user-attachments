package userattachments

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// stubLookPath replaces PATH resolution for one test. Only the listed names
// resolve, each to a distinct pathname so callers can tell which one matched.
func stubLookPath(t *testing.T, resolvable ...string) {
	t.Helper()
	previous := lookPath
	lookPath = func(name string) (string, error) {
		if slices.Contains(resolvable, name) {
			return "/resolved/" + name, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { lookPath = previous })
}

func TestFindChromeUsesTheOverrideBeforeAnyDiscovery(t *testing.T) {
	t.Setenv(chromePathEnvironment, "/custom/chrome")
	stubLookPath(t, "google-chrome-stable")

	path, err := findChrome()
	if err != nil || path != "/custom/chrome" {
		t.Fatalf("findChrome()=%q, %v want the override", path, err)
	}
}

func TestSelectChromeResolvesBareNamesThroughPathInOrder(t *testing.T) {
	stubLookPath(t, "chromium", "google-chrome")

	path, ok := selectChrome([]string{"google-chrome-stable", "google-chrome", "chromium"})
	if !ok || path != "/resolved/google-chrome" {
		t.Fatalf("selectChrome()=%q, %t want the first resolvable candidate", path, ok)
	}
}

func TestSelectChromeProbesAbsoluteCandidatesAsFiles(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "chrome")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	stubLookPath(t)

	path, ok := selectChrome([]string{directory, filepath.Join(directory, "absent"), executable})
	if !ok || path != executable {
		t.Fatalf("selectChrome()=%q, %t want %q", path, ok, executable)
	}
}

func TestSelectChromeReportsNoMatch(t *testing.T) {
	stubLookPath(t)

	if path, ok := selectChrome([]string{"google-chrome", "/absent/chrome"}); ok {
		t.Fatalf("selectChrome()=%q, %t want no match", path, ok)
	}
}

func TestFindChromeWithoutAnyBrowserNamesTheOverrideVariable(t *testing.T) {
	candidates := chromeCandidates(runtime.GOOS, "")
	if slices.ContainsFunc(candidates, filepath.IsAbs) {
		t.Skipf("%s probes absolute pathnames that may exist on this machine", runtime.GOOS)
	}
	t.Setenv(chromePathEnvironment, "")
	stubLookPath(t)

	_, err := findChrome()
	if err == nil || !strings.Contains(err.Error(), chromePathEnvironment) {
		t.Fatalf("findChrome() error=%v want it to name %s", err, chromePathEnvironment)
	}
}

func TestChromeCandidatesOnLinuxExcludeHeadlessOnlyBrowsers(t *testing.T) {
	candidates := chromeCandidates("linux", "/home/example")
	if len(candidates) == 0 {
		t.Fatal("linux has no candidates")
	}
	// The sign-in flow needs a visible window, so a headless-only build must
	// never win discovery.
	for _, candidate := range candidates {
		if strings.Contains(candidate, "headless") {
			t.Fatalf("candidate %q cannot open a window", candidate)
		}
	}
	if candidates[0] != "google-chrome-stable" {
		t.Fatalf("candidates[0]=%q want Chrome to be preferred", candidates[0])
	}
}

func TestChromeCandidatesOnMacOSIncludeTheUserBundleOnlyWhenHomeIsKnown(t *testing.T) {
	withHome := chromeCandidates("darwin", "/Users/example")
	if len(withHome) != 2 || withHome[1] != "/Users/example/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" {
		t.Fatalf("candidates=%v want the user bundle appended", withHome)
	}
	if withoutHome := chromeCandidates("darwin", ""); len(withoutHome) != 1 {
		t.Fatalf("candidates=%v want only the system bundle", withoutHome)
	}
}
