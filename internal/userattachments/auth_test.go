package userattachments

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type authFixture struct {
	service   authService
	sawCookie *string
	server    *httptest.Server
	ts        *toolState
}

func reopenToolState(ts *toolState) func() (*toolState, error) {
	anchor := filepath.Dir(ts.root.Name())
	name := filepath.Base(ts.root.Name())
	return func() (*toolState, error) {
		return openToolStateAt(anchor, name)
	}
}

func newAuthFixture(t *testing.T, pageLogin string) *authFixture {
	t.Helper()
	sawCookie := new(string)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if session, err := request.Cookie("user_session"); err == nil {
			*sawCookie = session.Value
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		if pageLogin == "" {
			_, _ = fmt.Fprint(writer, `<html><head></head><body>signed out</body></html>`)
			return
		}
		_, _ = fmt.Fprintf(writer, `<html><head><meta name="user-login" content="%s"></head></html>`, pageLogin)
	}))
	t.Cleanup(server.Close)
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"login":"owner"}`},
	}}
	ts := testToolState(t)
	service := authService{
		api:     apiClient{runner: runner.Run},
		store:   toolStateSessionReader{ts: ts},
		getenv:  func(string) string { return "" },
		baseURL: server.URL,
		stderr:  io.Discard,
		acquireViaCDP: func(context.Context, *toolState, io.Writer) (string, error) {
			return "cdp-acquired-session", nil
		},
		openState: reopenToolState(ts),
	}
	return &authFixture{service: service, sawCookie: sawCookie, server: server, ts: ts}
}

func TestAuthLoginCDPStoresAcquiredSession(t *testing.T) {
	fixture := newAuthFixture(t, "owner")

	login, err := fixture.service.login(context.Background())

	if err != nil || login != "owner" {
		t.Fatalf("login=%q error=%v", login, err)
	}
	value, err := fixture.ts.getSession()
	if err != nil || value != "cdp-acquired-session" {
		t.Fatalf("stored=%q error=%v", value, err)
	}
	if *fixture.sawCookie != "cdp-acquired-session" {
		t.Fatalf("validation did not use the acquired session: %q", *fixture.sawCookie)
	}
}

func TestAuthLoginRejectsIdentityMismatch(t *testing.T) {
	fixture := newAuthFixture(t, "someone-else")

	_, err := fixture.service.login(context.Background())

	if err == nil || !strings.Contains(err.Error(), "someone-else") {
		t.Fatalf("error=%v", err)
	}
	if _, err := fixture.ts.getSession(); !errors.Is(err, errSessionNotFound) {
		t.Fatalf("mismatched session was stored: %v", err)
	}
}

func TestAuthLoginRejectsSignedOutSession(t *testing.T) {
	fixture := newAuthFixture(t, "")

	_, err := fixture.service.login(context.Background())

	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("error=%v", err)
	}
	if _, err := fixture.ts.getSession(); !errors.Is(err, errSessionNotFound) {
		t.Fatalf("signed-out session was stored: %v", err)
	}
}

func TestAuthStatusAcceptsConfiguredMatchingSession(t *testing.T) {
	fixture := newAuthFixture(t, "owner")
	if err := fixture.ts.setSession("stored-session"); err != nil {
		t.Fatal(err)
	}

	login, err := fixture.service.status(context.Background())

	if err != nil || login != "owner" {
		t.Fatalf("login=%q error=%v", login, err)
	}
}

func TestAuthStatusRejectsMissingSession(t *testing.T) {
	fixture := newAuthFixture(t, "owner")

	_, err := fixture.service.status(context.Background())

	if err == nil || !strings.Contains(err.Error(), "no GitHub web session") {
		t.Fatalf("error=%v", err)
	}
}

func TestAuthStatusRejectsExpiredSession(t *testing.T) {
	fixture := newAuthFixture(t, "")
	if err := fixture.ts.setSession("stale-session"); err != nil {
		t.Fatal(err)
	}

	_, err := fixture.service.status(context.Background())

	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("error=%v", err)
	}
}

func TestAuthLogoutDeletesSessionAndProfileResidue(t *testing.T) {
	ts := testToolState(t)
	if err := ts.setSession("stored-session"); err != nil {
		t.Fatal(err)
	}
	if err := ts.root.Mkdir(legacyChromeProfileName, 0o700); err != nil {
		t.Fatal(err)
	}
	service := authService{
		openState: reopenToolState(ts),
	}
	if err := service.logout(); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.getSession(); !errors.Is(err, errSessionNotFound) {
		t.Fatalf("session residual: %v", err)
	}
	if _, err := ts.root.Lstat(legacyChromeProfileName); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("profile residual: %v", err)
	}
	if _, err := ts.root.Lstat(authLockName); err != nil {
		t.Fatalf("auth.lock should remain as reusable flock file: %v", err)
	}
}

func TestAuthLogoutCleansProfilesWhenSessionDeleteFails(t *testing.T) {
	ts := testToolState(t)
	if err := ts.setSession("stored-session"); err != nil {
		t.Fatal(err)
	}
	tempDir := ts.abs(legacySessionTempName)
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "nested"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ts.root.Mkdir(legacyChromeProfileName, 0o700); err != nil {
		t.Fatal(err)
	}
	service := authService{
		openState: reopenToolState(ts),
	}
	err := service.logout()
	if err == nil || !strings.Contains(err.Error(), "legacy session temporary") {
		t.Fatalf("error=%v", err)
	}
	if _, err := ts.getSession(); !errors.Is(err, errSessionNotFound) {
		t.Fatalf("session residual: %v", err)
	}
	if _, err := ts.root.Lstat(legacyChromeProfileName); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("profile residual after session delete failure: %v", err)
	}
	if _, err := ts.root.Lstat(authLockName); err != nil {
		t.Fatalf("auth.lock should remain: %v", err)
	}
}

func TestAuthLoginLockCoversValidationAndStore(t *testing.T) {
	ts := testToolState(t)
	releaseValidate := make(chan struct{})
	acquired := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-releaseValidate
		if session, err := request.Cookie("user_session"); err == nil && session.Value == "cdp-acquired-session" {
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(writer, `<html><head><meta name="user-login" content="owner"></head></html>`)
			return
		}
		http.Error(writer, "unexpected", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)

	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"login":"owner"}`},
	}}
	service := authService{
		api:     apiClient{runner: runner.Run},
		store:   toolStateSessionReader{ts: ts},
		getenv:  func(string) string { return "" },
		baseURL: server.URL,
		stderr:  io.Discard,
		acquireViaCDP: func(context.Context, *toolState, io.Writer) (string, error) {
			close(acquired)
			return "cdp-acquired-session", nil
		},
		openState: reopenToolState(ts),
	}

	var loginErr error
	var loginWG sync.WaitGroup
	loginWG.Add(1)
	go func() {
		defer loginWG.Done()
		_, loginErr = service.login(context.Background())
	}()

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("login did not reach post-acquire validation")
	}

	logoutErr := service.logout()
	if !errors.Is(logoutErr, errAuthBusy) {
		t.Fatalf("logout during validation: %v", logoutErr)
	}

	close(releaseValidate)
	loginWG.Wait()
	if loginErr != nil {
		t.Fatalf("login=%v", loginErr)
	}
	value, err := ts.getSession()
	if err != nil || value != "cdp-acquired-session" {
		t.Fatalf("stored=%q err=%v", value, err)
	}

	if err := service.logout(); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.getSession(); !errors.Is(err, errSessionNotFound) {
		t.Fatalf("session remained after logout: %v", err)
	}
}

func TestAuthLoginLoginConcurrentBusyOrSerialized(t *testing.T) {
	ts := testToolState(t)
	releaseFirst := make(chan struct{})
	firstAcquired := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(writer, `<html><head><meta name="user-login" content="owner"></head></html>`)
	}))
	t.Cleanup(server.Close)

	var acquireCount int
	var acquireMu sync.Mutex
	service := authService{
		api: apiClient{runner: func(context.Context, ...string) (commandResult, error) {
			return commandResult{Stdout: `{"login":"owner"}`}, nil
		}},
		store:   toolStateSessionReader{ts: ts},
		getenv:  func(string) string { return "" },
		baseURL: server.URL,
		stderr:  io.Discard,
		acquireViaCDP: func(context.Context, *toolState, io.Writer) (string, error) {
			acquireMu.Lock()
			acquireCount++
			n := acquireCount
			acquireMu.Unlock()
			if n == 1 {
				close(firstAcquired)
				<-releaseFirst
			}
			return fmt.Sprintf("session-%d", n), nil
		},
		openState: reopenToolState(ts),
	}

	var firstErr, secondErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, firstErr = service.login(context.Background())
	}()
	select {
	case <-firstAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("first login did not acquire")
	}
	go func() {
		defer wg.Done()
		_, secondErr = service.login(context.Background())
	}()
	time.Sleep(50 * time.Millisecond)
	close(releaseFirst)
	wg.Wait()

	busy := errors.Is(firstErr, errAuthBusy) || errors.Is(secondErr, errAuthBusy)
	serialized := firstErr == nil && secondErr == nil
	if !busy && !serialized {
		t.Fatalf("first=%v second=%v", firstErr, secondErr)
	}
	if firstErr == nil || secondErr == nil {
		value, err := ts.getSession()
		if err != nil || value == "" {
			t.Fatalf("stored=%q err=%v", value, err)
		}
	}
}

func TestAuthLogoutLogoutConcurrentBusyOrSerialized(t *testing.T) {
	ts := testToolState(t)
	if err := ts.setSession("stored-session"); err != nil {
		t.Fatal(err)
	}
	service := authService{openState: reopenToolState(ts)}

	holding := make(chan struct{})
	release := make(chan struct{})
	var holderErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		holder, err := reopenToolState(ts)()
		if err != nil {
			holderErr = err
			close(holding)
			return
		}
		defer holder.Close()
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
	busyErr := service.logout()
	if !errors.Is(busyErr, errAuthBusy) {
		t.Fatalf("logout while locked: %v", busyErr)
	}
	close(release)
	wg.Wait()
	if holderErr != nil {
		t.Fatalf("holder error=%v", holderErr)
	}

	if err := service.logout(); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.getSession(); !errors.Is(err, errSessionNotFound) {
		t.Fatalf("session residual: %v", err)
	}
}

func TestAuthLoginUsesSingleToolStateOwner(t *testing.T) {
	ts := testToolState(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(writer, `<html><head><meta name="user-login" content="owner"></head></html>`)
	}))
	t.Cleanup(server.Close)
	var sawLockedState *toolState
	service := authService{
		api: apiClient{runner: (&scriptedRunner{t: t, calls: []scriptedCall{
			{body: `{"login":"owner"}`},
		}}).Run},
		store:   toolStateSessionReader{ts: ts},
		getenv:  func(string) string { return "" },
		baseURL: server.URL,
		stderr:  io.Discard,
		acquireViaCDP: func(_ context.Context, locked *toolState, _ io.Writer) (string, error) {
			sawLockedState = locked
			return "owned-session", nil
		},
		openState: reopenToolState(ts),
	}
	login, err := service.login(context.Background())
	if err != nil || login != "owner" {
		t.Fatalf("login=%q err=%v", login, err)
	}
	if sawLockedState == nil {
		t.Fatal("acquire did not receive locked toolState")
	}
	value, err := ts.getSession()
	if err != nil || value != "owned-session" {
		t.Fatalf("value=%q err=%v", value, err)
	}
}

func TestAuthValidateSessionRejectsTextPlain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(writer, `<meta name="user-login" content="owner">`)
	}))
	t.Cleanup(server.Close)
	service := authService{baseURL: server.URL}
	_, err := service.validateSession(context.Background(), "session")
	if err == nil || !strings.Contains(err.Error(), "non-HTML") {
		t.Fatalf("error=%v", err)
	}
}

func TestAuthValidateSessionHintsExpiredOnNonOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "missing", http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	service := authService{baseURL: server.URL}
	_, err := service.validateSession(context.Background(), "session")
	if err == nil {
		t.Fatal("expected error")
	}
	message := err.Error()
	if !strings.Contains(message, "GitHub page returned HTTP 404") {
		t.Fatalf("error=%v", err)
	}
	if !strings.Contains(message, "the session may be invalid or expired") {
		t.Fatalf("missing session hint: %v", err)
	}
}

func TestAuthValidateSessionAcceptsExactLimitAndRejectsOversize(t *testing.T) {
	meta := []byte(`<meta name="user-login" content="owner">`)
	exact := append(bytesRepeat('a', int(maxSessionPageBytes)-len(meta)), meta...)
	oversize := append(bytesRepeat('a', int(maxSessionPageBytes)+1-len(meta)), meta...)

	t.Run("exact", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "text/html")
			_, _ = writer.Write(exact)
		}))
		t.Cleanup(server.Close)
		service := authService{baseURL: server.URL}
		login, err := service.validateSession(context.Background(), "session")
		if err != nil || login != "owner" {
			t.Fatalf("login=%q err=%v", login, err)
		}
	})
	t.Run("oversize", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "text/html")
			_, _ = writer.Write(oversize)
		}))
		t.Cleanup(server.Close)
		service := authService{baseURL: server.URL}
		_, err := service.validateSession(context.Background(), "session")
		if err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestGitHubSessionClientRejectsCrossOriginRedirect(t *testing.T) {
	var external *httptest.Server
	external = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(writer, `<meta name="user-login" content="owner">`)
	}))
	t.Cleanup(external.Close)

	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, external.URL+"/", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	base, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := githubSessionClient(base, "session")
	response, err := client.Get(origin.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("status=%d", response.StatusCode)
	}
	_, err = readGitHubHTMLPage(response, maxSessionPageBytes)
	if err == nil {
		t.Fatal("cross-origin redirect body accepted")
	}
}

func TestGitHubSessionClientAllowsSameOriginRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/" {
			http.Redirect(writer, request, "/home", http.StatusFound)
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(writer, `<meta name="user-login" content="owner">`)
	}))
	t.Cleanup(server.Close)
	service := authService{baseURL: server.URL}
	login, err := service.validateSession(context.Background(), "session")
	if err != nil || login != "owner" {
		t.Fatalf("login=%q err=%v", login, err)
	}
}

func bytesRepeat(b byte, n int) []byte {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = b
	}
	return buf
}
