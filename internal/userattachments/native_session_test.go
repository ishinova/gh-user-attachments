package userattachments

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrepareNativeUploaderFromSessionUsesStoredSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/owner/repo" {
			http.NotFound(writer, request)
			return
		}
		session, err := request.Cookie("user_session")
		if err != nil || session.Value != "stored-session" {
			t.Fatalf("user_session cookie=%v error=%v", session, err)
		}
		sameSite, err := request.Cookie("__Host-user_session_same_site")
		if err != nil || sameSite.Value != session.Value {
			t.Fatalf("same-site cookie=%v error=%v", sameSite, err)
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(writer, `<meta name="user-login" content="owner"><script>"uploadToken":"token-123"</script>`)
	}))
	defer server.Close()

	uploader, err := prepareNativeUploaderFromSession(context.Background(), server.URL, "owner/repo", 123, "owner", "stored-session")
	if err != nil {
		t.Fatal(err)
	}
	if uploader.uploadToken != "token-123" || uploader.repositoryID != 123 {
		t.Fatalf("client=%#v", uploader)
	}
	if uploader.s3.Timeout != 0 {
		t.Fatalf("S3 client timeout=%s; large file transfer must not use a fixed deadline", uploader.s3.Timeout)
	}
}

func TestPrepareNativeUploaderFromSessionRejectsIdentityMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(writer, `<meta name="user-login" content="someone-else"><script>"uploadToken":"token-123"</script>`)
	}))
	defer server.Close()

	_, err := prepareNativeUploaderFromSession(context.Background(), server.URL, "owner/repo", 123, "owner", "stored-session")
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveNativeSessionPrefersEnvironmentThenStore(t *testing.T) {
	store := &memorySessionStore{value: "stored-session", set: true}

	session, err := resolveNativeSession(func(string) string { return " env-session " }, store)
	if err != nil || session != "env-session" {
		t.Fatalf("session=%q error=%v", session, err)
	}

	session, err = resolveNativeSession(func(string) string { return "" }, store)
	if err != nil || session != "stored-session" {
		t.Fatalf("session=%q error=%v", session, err)
	}

	_, err = resolveNativeSession(func(string) string { return "" }, &memorySessionStore{})
	if err == nil || !strings.Contains(err.Error(), "auth login") {
		t.Fatalf("error = %v", err)
	}
}

func TestReadGitHubHTMLPageRejectsNilBody(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/html"}},
		Body:       nil,
	}
	body, err := readGitHubHTMLPage(response, maxSessionPageBytes)
	if err == nil || body != nil {
		t.Fatalf("body=%q err=%v", body, err)
	}
	if !strings.Contains(err.Error(), "empty response body") {
		t.Fatalf("error=%v", err)
	}
}

func TestValidateNativeAssetURLRequiresCanonicalUUID(t *testing.T) {
	valid := "https://github.com/user-attachments/assets/66666666-6666-4666-8666-666666666666"
	if err := validateNativeAssetURL(valid); err != nil {
		t.Fatalf("valid URL rejected: %v", err)
	}
	invalid := "https://github.com/user-attachments/assets/------------------------------------"
	if err := validateNativeAssetURL(invalid); err == nil {
		t.Fatal("malformed UUID was accepted")
	}
}

func TestValidateNativeAssetURLRejectsUserinfoPortQueryFragment(t *testing.T) {
	valid := "https://github.com/user-attachments/assets/66666666-6666-4666-8666-666666666666"
	tests := []string{
		"https://user@" + strings.TrimPrefix(valid, "https://"),
		"https://github.com:443/user-attachments/assets/66666666-6666-4666-8666-666666666666",
		valid + "?x=1",
		valid + "#frag",
	}
	for _, raw := range tests {
		if err := validateNativeAssetURL(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestFetchNativeUploadTokenRejectsOversizedPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(strings.Repeat("a", maxSessionPageBytes+1)))
	}))
	defer server.Close()
	client := server.Client()
	_, _, err := fetchNativeUploadToken(context.Background(), client, server.URL, "owner/repo")
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("error=%v", err)
	}
}

func TestFetchNativeUploadTokenRejectsTextPlain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(writer, `<meta name="user-login" content="owner"><script>"uploadToken":"token-123"</script>`)
	}))
	defer server.Close()
	_, _, err := fetchNativeUploadToken(context.Background(), server.Client(), server.URL, "owner/repo")
	if err == nil || !strings.Contains(err.Error(), "non-HTML") {
		t.Fatalf("error=%v", err)
	}
}

func TestFetchNativeUploadTokenOmitsSessionHintOnNonOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "missing", http.StatusNotFound)
	}))
	defer server.Close()
	_, _, err := fetchNativeUploadToken(context.Background(), server.Client(), server.URL, "owner/repo")
	if err == nil {
		t.Fatal("expected error")
	}
	message := err.Error()
	if !strings.Contains(message, "GitHub page returned HTTP 404") {
		t.Fatalf("error=%v", err)
	}
	if strings.Contains(message, "the session may be invalid or expired") {
		t.Fatalf("unexpected session hint: %v", err)
	}
}

func TestParseSessionPageRejectsMissingIdentityOrToken(t *testing.T) {
	if _, _, err := parseSessionPage([]byte(`<meta name="user-login" content="owner">`)); err == nil {
		t.Fatal("missing token accepted")
	}
	if _, _, err := parseSessionPage([]byte(`"uploadToken":"token-123"`)); err == nil {
		t.Fatal("missing login accepted")
	}
}
