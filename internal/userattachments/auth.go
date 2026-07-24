package userattachments

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type authService struct {
	api     apiClient
	store   sessionReader
	getenv  func(string) string
	baseURL string
	stderr  io.Writer
	// acquireViaCDP runs while the auth lock is already held and must not
	// acquire the lock itself.
	acquireViaCDP func(context.Context, *toolState, io.Writer) (string, error)
	openState     func() (*toolState, error)
}

func handleAuth(ctx context.Context, arguments []string, stderr io.Writer, runner commandRunner) int {
	if len(arguments) == 0 || arguments[0] == "help" || arguments[0] == "--help" || arguments[0] == "-h" {
		printAuthUsage(stderr)
		return exitOK
	}
	subcommand := arguments[0]
	switch {
	case (subcommand == "login" || subcommand == "logout" || subcommand == "status") && len(arguments) == 1:
	default:
		fmt.Fprintf(stderr, "gh-user-attachments: unknown auth command %q\n", strings.Join(arguments, " "))
		printAuthUsage(stderr)
		return exitUsage
	}

	store, err := defaultSessionStore()
	if err != nil {
		fmt.Fprintf(stderr, "gh-user-attachments: auth: %s\n", safeCommandMessage(err.Error()))
		return exitAPI
	}
	service := authService{
		api:           apiClient{runner: runner},
		store:         store,
		getenv:        os.Getenv,
		baseURL:       githubBaseURL,
		stderr:        stderr,
		acquireViaCDP: acquireSessionViaCDP,
		openState:     defaultToolState,
	}

	switch subcommand {
	case "login":
		login, err := service.login(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "gh-user-attachments: auth: %s\n", safeCommandMessage(err.Error()))
			return exitAPI
		}
		fmt.Fprintf(stderr, "gh-user-attachments: stored a valid GitHub web session for @%s\n", login)
		return exitOK
	case "logout":
		if err := service.logout(); err != nil {
			fmt.Fprintf(stderr, "gh-user-attachments: auth: %s\n", safeCommandMessage(err.Error()))
			return exitAPI
		}
		fmt.Fprintln(stderr, "gh-user-attachments: deleted the stored GitHub web session and tool-owned Chrome profile residue")
		if strings.TrimSpace(os.Getenv(githubSessionEnvironment)) != "" {
			fmt.Fprintf(stderr, "gh-user-attachments: %s remains set in the environment and will continue to override the stored session\n", githubSessionEnvironment)
		}
		return exitOK
	case "status":
		login, err := service.status(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "gh-user-attachments: auth: %s\n", safeCommandMessage(err.Error()))
			return exitAPI
		}
		fmt.Fprintf(stderr, "gh-user-attachments: valid GitHub web session for @%s\n", login)
		return exitOK
	}
	return exitUsage
}

func printAuthUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Manage the GitHub web session used for native attachment upload.")
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  gh-user-attachments auth login            Sign in via a tool-owned Chrome window (CDP) and store the session as a 0600 file")
	fmt.Fprintln(stderr, "  gh-user-attachments auth status           Show whether a usable session is configured")
	fmt.Fprintln(stderr, "  gh-user-attachments auth logout           Delete the stored session and tool-owned Chrome profile residue")
}

func (s authService) openToolState() (*toolState, error) {
	if s.openState != nil {
		return s.openState()
	}
	return defaultToolState()
}

func (s authService) logout() error {
	ts, err := s.openToolState()
	if err != nil {
		return err
	}
	defer ts.Close()
	return ts.withAuthLock(func() error {
		return ts.cleanupAuthResidue()
	})
}

func (s authService) login(ctx context.Context) (string, error) {
	login, err := currentLogin(ctx, s.api)
	if err != nil {
		return "", err
	}
	ts, err := s.openToolState()
	if err != nil {
		return "", err
	}
	defer ts.Close()

	var storedLogin string
	err = ts.withAuthLock(func() error {
		secret, err := s.acquireViaCDP(ctx, ts, s.stderr)
		if err != nil {
			return err
		}
		sessionLogin, err := s.validateSession(ctx, secret)
		if err != nil {
			return err
		}
		if !strings.EqualFold(sessionLogin, login) {
			return fmt.Errorf("session belongs to @%s, but gh is authenticated as @%s", sessionLogin, login)
		}
		if err := ts.setSession(secret); err != nil {
			return err
		}
		storedLogin = login
		return nil
	})
	if err != nil {
		return "", err
	}
	return storedLogin, nil
}

func (s authService) status(ctx context.Context) (string, error) {
	login, err := currentLogin(ctx, s.api)
	if err != nil {
		return "", err
	}
	session, err := resolveNativeSession(s.getenv, s.store)
	if err != nil {
		return "", err
	}
	sessionLogin, err := s.validateSession(ctx, session)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(sessionLogin, login) {
		return "", fmt.Errorf("session belongs to @%s, but gh is authenticated as @%s", sessionLogin, login)
	}
	return login, nil
}

// validateSession fetches a GitHub web page with the session and returns the
// login it identifies as. The cookie value is never written anywhere.
func (s authService) validateSession(ctx context.Context, session string) (string, error) {
	base, err := url.Parse(s.baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("invalid GitHub base URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", nativeUploadUserAgent)
	response, err := githubSessionClient(base, session).Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := readGitHubHTMLPage(response, maxSessionPageBytes)
	if err != nil {
		if response.StatusCode != http.StatusOK {
			return "", fmt.Errorf("%w; the session may be invalid or expired", err)
		}
		return "", err
	}
	loginMatch := userLoginPattern.FindSubmatch(body)
	if len(loginMatch) != 2 {
		return "", fmt.Errorf("the session is not logged in to github.com")
	}
	return string(loginMatch[1]), nil
}

func currentLogin(ctx context.Context, api apiClient) (string, error) {
	response, err := api.get(ctx, "user")
	if err != nil {
		return "", fmt.Errorf("read authenticated identity: %w", err)
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := decodeJSON(response, &user); err != nil || user.Login == "" {
		if err == nil {
			err = fmt.Errorf("GitHub returned an empty authenticated login")
		}
		return "", fmt.Errorf("read authenticated identity: %w", err)
	}
	return user.Login, nil
}
