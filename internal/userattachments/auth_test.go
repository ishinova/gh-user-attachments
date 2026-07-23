package userattachments

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type authFixture struct {
	service   authService
	store     *memorySessionStore
	sawCookie *string
}

func newAuthFixture(t *testing.T, pageLogin string) *authFixture {
	t.Helper()
	sawCookie := new(string)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if session, err := request.Cookie("user_session"); err == nil {
			*sawCookie = session.Value
		}
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
	store := &memorySessionStore{}
	service := authService{
		api:     apiClient{runner: runner.Run},
		store:   store,
		getenv:  func(string) string { return "" },
		baseURL: server.URL,
		stderr:  io.Discard,
		acquireViaCDP: func(context.Context, io.Writer) (string, error) {
			return "cdp-acquired-session", nil
		},
	}
	return &authFixture{service: service, store: store, sawCookie: sawCookie}
}

func TestAuthLoginCDPStoresAcquiredSession(t *testing.T) {
	fixture := newAuthFixture(t, "owner")

	login, err := fixture.service.login(context.Background())

	if err != nil || login != "owner" || !fixture.store.set || fixture.store.value != "cdp-acquired-session" {
		t.Fatalf("login=%q error=%v store=%#v", login, err, fixture.store)
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
	if fixture.store.set {
		t.Fatal("mismatched session was stored")
	}
}

func TestAuthLoginRejectsSignedOutSession(t *testing.T) {
	fixture := newAuthFixture(t, "")

	_, err := fixture.service.login(context.Background())

	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("error=%v", err)
	}
	if fixture.store.set {
		t.Fatal("signed-out session was stored")
	}
}

func TestAuthStatusAcceptsConfiguredMatchingSession(t *testing.T) {
	fixture := newAuthFixture(t, "owner")
	fixture.store.set = true
	fixture.store.value = "stored-session"

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
	fixture.store.set = true
	fixture.store.value = "stale-session"

	_, err := fixture.service.status(context.Background())

	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("error=%v", err)
	}
}
