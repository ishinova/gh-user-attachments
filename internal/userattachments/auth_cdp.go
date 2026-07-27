package userattachments

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const (
	chromePathEnvironment = "GH_USER_ATTACHMENTS_CHROME"
	loginPollInterval     = time.Second
)

// acquireSessionViaCDP opens a headed Chrome with a one-shot tool-owned
// profile, waits for the user to sign in to github.com, and extracts the
// user_session cookie through the DevTools protocol. It never touches the
// user's personal Chrome profile. The browser is closed and the ephemeral
// profile is deleted before returning.
//
// Caller must hold the auth lock and pass the locked toolState. This function
// does not acquire or release the lock.
func acquireSessionViaCDP(ctx context.Context, ts *toolState, stderr io.Writer) (string, error) {
	chromePath, err := findChrome()
	if err != nil {
		return "", err
	}
	var session string
	err = ts.withEphemeralChromeProfile(func(profileDir string) error {
		value, err := acquireSessionInProfile(ctx, chromePath, profileDir, stderr)
		if err != nil {
			return err
		}
		session = value
		return nil
	})
	if err != nil {
		return "", err
	}
	return session, nil
}

func acquireSessionInProfile(ctx context.Context, chromePath, profileDir string, stderr io.Writer) (string, error) {
	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.UserDataDir(profileDir),
		chromedp.NoFirstRun,
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-session-crashed-bubble", true),
		chromedp.Flag("hide-crash-restore-bubble", true),
		// Login needs a visible window; false values delete the default flags.
		chromedp.Flag("headless", false),
		chromedp.Flag("hide-scrollbars", false),
		chromedp.Flag("mute-audio", false),
	)
	allocatorCtx, cancelAllocator := chromedp.NewExecAllocator(ctx, options...)
	defer cancelAllocator()
	browserCtx, cancelBrowser := chromedp.NewContext(allocatorCtx)
	defer cancelBrowser()

	if err := chromedp.Run(browserCtx, chromedp.Navigate(githubBaseURL+"/login")); err != nil {
		return "", fmt.Errorf("open Chrome for GitHub sign-in: %w", err)
	}
	fmt.Fprintln(stderr, "gh-user-attachments: complete GitHub sign-in in the opened Chrome window")

	// Unsigned github.com pages still ship meta[name="user-login"] with an empty
	// content attribute, so waiting for the node is not enough; poll until the
	// value becomes non-empty. The caller context supplies the overall deadline.
	for {
		login, err := signedInLogin(browserCtx)
		if err != nil {
			return "", fmt.Errorf("GitHub sign-in did not complete: %w", err)
		}
		if login != "" {
			break
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("GitHub sign-in did not complete: %w", ctx.Err())
		case <-time.After(loginPollInterval):
		}
	}

	var session string
	err := chromedp.Run(browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		cookies, err := network.GetCookies().WithURLs([]string{githubBaseURL}).Do(ctx)
		if err != nil {
			return err
		}
		for _, cookie := range cookies {
			if cookie.Name == "user_session" && cookie.Value != "" {
				session = cookie.Value
			}
		}
		return nil
	}))
	if err != nil {
		return "", fmt.Errorf("read session cookie from Chrome: %w", err)
	}
	if session == "" {
		return "", fmt.Errorf("GitHub sign-in completed but no user_session cookie was found")
	}
	return session, nil
}

func signedInLogin(ctx context.Context) (string, error) {
	var login string
	var present bool
	err := chromedp.Run(ctx, chromedp.AttributeValue(`meta[name="user-login"]`, "content", &login, &present, chromedp.ByQuery))
	if err != nil {
		return "", err
	}
	// Missing or empty content both mean unsigned-in; only chromedp failures
	// (closed browser, etc.) are returned as errors.
	if !present || login == "" {
		return "", nil
	}
	return login, nil
}

// chromeGetenv is the environment lookup Chrome resolution performs. Tests
// replace it to record which names the resolution reads, including the ones it
// only reaches when the override is absent.
var chromeGetenv = os.Getenv

// lookPath is a seam so discovery can be exercised without depending on which
// browsers happen to be installed on the machine running the tests.
var lookPath = exec.LookPath

// chromeCandidates lists the browsers to try for the given platform, in
// preference order. Absolute pathnames are probed as files; bare names are
// resolved through PATH.
//
// chromedp performs its own discovery when no ExecPath option is given, but it
// is not usable here: on Unix it prefers headless_shell and headless-shell over
// a full browser, and this flow needs a visible window for the user to sign in.
// Its fallback also defers the failure to process start, which loses the early
// message naming the override variable.
func chromeCandidates(goos, home string) []string {
	switch goos {
	case "darwin":
		candidates := []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
		if home != "" {
			candidates = append(candidates, filepath.Join(home, "Applications/Google Chrome.app/Contents/MacOS/Google Chrome"))
		}
		return candidates
	case "linux":
		// Every entry here can open a window. Headless-only builds are
		// deliberately absent.
		return []string{
			"google-chrome-stable",
			"google-chrome",
			"chromium",
			"chromium-browser",
		}
	default:
		return nil
	}
}

func findChrome() (string, error) {
	if override := chromeGetenv(chromePathEnvironment); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	if path, ok := selectChrome(chromeCandidates(runtime.GOOS, home)); ok {
		return path, nil
	}
	return "", fmt.Errorf("find Google Chrome: install Chrome or set %s", chromePathEnvironment)
}

// selectChrome returns the first candidate that resolves to an existing
// executable.
func selectChrome(candidates []string) (string, bool) {
	for _, candidate := range candidates {
		if filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, true
			}
			continue
		}
		if path, err := lookPath(candidate); err == nil {
			return path, true
		}
	}
	return "", false
}
