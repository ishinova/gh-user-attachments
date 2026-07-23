package userattachments

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const (
	chromePathEnvironment = "GH_USER_ATTACHMENTS_CHROME"
	loginPollInterval     = time.Second
)

// acquireSessionViaCDP opens a headed Chrome with a tool-owned profile, waits
// for the user to sign in to github.com, and extracts the user_session cookie
// through the DevTools protocol. It never touches the user's personal Chrome
// profile. The browser is closed before returning.
func acquireSessionViaCDP(ctx context.Context, stderr io.Writer) (string, error) {
	chromePath, err := findChrome()
	if err != nil {
		return "", err
	}
	stateDir, err := toolStateDir()
	if err != nil {
		return "", err
	}
	profileDir := filepath.Join(stateDir, "chrome-profile")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return "", fmt.Errorf("create chrome profile directory: %w", err)
	}

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
	err = chromedp.Run(browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
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
	if !present {
		return "", nil
	}
	return login, nil
}

func findChrome() (string, error) {
	if override := os.Getenv(chromePathEnvironment); override != "" {
		return override, nil
	}
	candidates := []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "Applications/Google Chrome.app/Contents/MacOS/Google Chrome"))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("find Google Chrome: install Chrome or set %s", chromePathEnvironment)
}
