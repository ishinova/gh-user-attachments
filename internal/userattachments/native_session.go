package userattachments

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	githubSessionEnvironment = "GH_USER_ATTACHMENTS_SESSION"
	githubBaseURL            = "https://github.com"
)

var uploadTokenPattern = regexp.MustCompile(`"uploadToken":"([^"]+)"`)
var userLoginPattern = regexp.MustCompile(`<meta name="user-login" content="([^"]+)"`)
var nativeAssetPathPattern = regexp.MustCompile(`^/user-attachments/assets/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func prepareNativeUploader(ctx context.Context, repo string, repositoryID int64, login string) (assetUploader, error) {
	store, err := defaultSessionStore()
	if err != nil {
		return nil, err
	}
	session, err := resolveNativeSession(os.Getenv, store)
	if err != nil {
		return nil, err
	}
	uploader, err := prepareNativeUploaderFromSession(ctx, githubBaseURL, repo, repositoryID, login, session)
	if err != nil {
		return nil, err
	}
	return uploader.upload, nil
}

func prepareNativeUploaderFromSession(ctx context.Context, baseURL, repo string, repositoryID int64, login, session string) (nativeUploadClient, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nativeUploadClient{}, fmt.Errorf("invalid GitHub base URL")
	}
	client := githubSessionClient(base, session)
	uploadToken, sessionLogin, err := fetchNativeUploadToken(ctx, client, baseURL, repo)
	if err != nil {
		return nativeUploadClient{}, err
	}
	if !strings.EqualFold(sessionLogin, login) {
		return nativeUploadClient{}, fmt.Errorf("stored session identity @%s does not match gh authentication @%s", sessionLogin, login)
	}
	uploader := nativeUploadClient{
		github: client,
		s3: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		baseURL:      baseURL,
		repository:   repo,
		repositoryID: repositoryID,
		uploadToken:  uploadToken,
	}
	if baseURL == githubBaseURL {
		uploader.validateS3URL = validateNativeS3URL
		uploader.validateAssetURL = validateNativeAssetURL
	}
	return uploader, nil
}

func githubSessionClient(base *url.URL, session string) *http.Client {
	jar, _ := cookiejar.New(nil)
	secure := base.Scheme == "https"
	jar.SetCookies(base, []*http.Cookie{
		{Name: "user_session", Value: session, Path: "/", Secure: secure, HttpOnly: true},
		{Name: "__Host-user_session_same_site", Value: session, Path: "/", Secure: secure, HttpOnly: true},
	})
	return &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme != base.Scheme || request.URL.Host != base.Host {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

func fetchNativeUploadToken(ctx context.Context, client *http.Client, baseURL, repo string) (string, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/"+repo, nil)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("User-Agent", nativeUploadUserAgent)
	response, err := client.Do(request)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	body, err := readGitHubHTMLPage(response, maxSessionPageBytes)
	if err != nil {
		return "", "", err
	}
	return parseSessionPage(body)
}

// readGitHubHTMLPage owns status, media type, and body-size validation for
// GitHub HTML pages used by auth status/login and upload preparation.
func readGitHubHTMLPage(response *http.Response, limit int64) ([]byte, error) {
	if response == nil {
		return nil, fmt.Errorf("GitHub returned an empty response")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub page returned HTTP %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/html" {
		return nil, fmt.Errorf("GitHub returned a non-HTML response")
	}
	if response.Body == nil {
		return nil, fmt.Errorf("GitHub returned an empty response body")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read GitHub page: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("GitHub page exceeded size limit")
	}
	return body, nil
}

// parseSessionPage extracts the upload token and the logged-in login from a
// GitHub web page. A missing token means the session is expired, lacks write
// access, or misses SSO authorization.
func parseSessionPage(body []byte) (string, string, error) {
	tokenMatch := uploadTokenPattern.FindSubmatch(body)
	loginMatch := userLoginPattern.FindSubmatch(body)
	if len(tokenMatch) != 2 {
		return "", "", fmt.Errorf("native upload token not found; the stored session may be expired, lack write access, or miss SSO authorization (check `gh user-attachments auth status`)")
	}
	if len(loginMatch) != 2 {
		return "", "", fmt.Errorf("authenticated web identity was not present on the GitHub page")
	}
	return string(tokenMatch[1]), string(loginMatch[1]), nil
}

func validateNativeS3URL(value *url.URL) error {
	host := strings.ToLower(value.Hostname())
	if value.Scheme != "https" ||
		value.User != nil ||
		value.Port() != "" ||
		value.RawQuery != "" ||
		value.Fragment != "" ||
		!strings.HasPrefix(host, "github-production-user-asset-") ||
		!strings.HasSuffix(host, ".s3.amazonaws.com") {
		return fmt.Errorf("GitHub returned an untrusted native asset upload host")
	}
	return nil
}

func validateNativeAssetURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil ||
		parsed.Scheme != "https" ||
		parsed.User != nil ||
		parsed.Hostname() != "github.com" ||
		parsed.Port() != "" ||
		!nativeAssetPathPattern.MatchString(parsed.Path) ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return fmt.Errorf("GitHub returned an invalid native asset URL")
	}
	return nil
}
