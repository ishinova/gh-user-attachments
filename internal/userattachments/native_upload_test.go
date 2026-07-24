package userattachments

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNativeVideoUploadCompletesGitHubPolicyS3AndFinalizeFlow(t *testing.T) {
	assetURL := "https://github.com/user-attachments/assets/11111111-1111-1111-1111-111111111111"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/upload/policies/assets":
			if request.Method != http.MethodPost {
				t.Fatalf("policy method=%s", request.Method)
			}
			if request.Header.Get("Origin") != server.URL || request.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				t.Fatalf("policy headers=%v", request.Header)
			}
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{
				"name": "demo.mp4", "size": "5", "content_type": "video/mp4",
				"authenticity_token": "upload-token", "repository_id": "123",
			} {
				if got := request.FormValue(key); got != want {
					t.Fatalf("policy %s=%q want=%q", key, got, want)
				}
			}
			writer.Header().Set("Content-Type", "application/json; charset=utf-8")
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"upload_url": server.URL + "/s3",
				"asset":      map[string]any{"id": 7, "content_type": "video/mp4", "href": assetURL},
				"form": map[string]string{
					"key":              "asset-key",
					"policy":           "signed",
					"X-Amz-Algorithm":  "AWS4-HMAC-SHA256",
					"X-Amz-Credential": "cred",
					"X-Amz-Date":       "20260724T000000Z",
					"X-Amz-Signature":  "sig",
					"Content-Type":     "video/mp4",
				},
				"asset_upload_url":                "/upload/assets/7",
				"asset_upload_authenticity_token": "finalize-token",
			})
		case "/s3":
			if _, err := request.Cookie("user_session"); err != http.ErrNoCookie {
				t.Fatalf("GitHub session cookie leaked to S3: %v", err)
			}
			assertS3Multipart(t, request, "video")
			writer.WriteHeader(http.StatusNoContent)
		case "/upload/assets/7":
			if request.Method != http.MethodPut {
				t.Fatalf("finalize method=%s", request.Method)
			}
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if got := request.FormValue("authenticity_token"); got != "finalize-token" {
				t.Fatalf("finalize token=%q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]string{"href": assetURL, "name": "demo.mp4"})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := nativeUploadClient{
		github:       server.Client(),
		s3:           server.Client(),
		baseURL:      server.URL,
		repository:   "owner/repo",
		repositoryID: 123,
		uploadToken:  "upload-token",
	}
	result := client.upload(context.Background(), uploadAsset{
		Name: "demo.mp4", MediaType: "video/mp4", Content: []byte("video"),
	})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.URL != assetURL || result.RemoteState != remoteChanged || result.FailedPhase != phaseNone {
		t.Fatalf("result=%#v", result)
	}
}

func TestNativeVideoUploadRejectsFinalizeURLDifferentFromPolicy(t *testing.T) {
	policyURL := "https://github.com/user-attachments/assets/44444444-4444-4444-4444-444444444444"
	finalURL := "https://github.com/user-attachments/assets/55555555-5555-5555-5555-555555555555"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `{"href":%q}`, finalURL)
	}))
	defer server.Close()
	policy := nativePolicy{
		AssetUploadURL:               "/upload/assets/7",
		AssetUploadAuthenticityToken: "finalize-token",
	}
	policy.Asset.Href = policyURL
	client := nativeUploadClient{github: server.Client(), baseURL: server.URL, repository: "owner/repo"}

	_, err := client.finalize(context.Background(), policy)

	if err == nil || !strings.Contains(err.Error(), "changed native asset URL") {
		t.Fatalf("error=%v", err)
	}
}

func TestNativeVideoUploadRejectsUnexpectedFinalizePath(t *testing.T) {
	assetURL := "https://github.com/user-attachments/assets/99999999-9999-4999-8999-999999999999"
	policy := nativePolicy{
		UploadURL: "https://github-production-user-asset-6210df.s3.amazonaws.com/",
		Form: map[string]string{
			"key": "asset-key", "policy": "signed", "X-Amz-Algorithm": "AWS4-HMAC-SHA256",
			"X-Amz-Credential": "cred", "X-Amz-Date": "20260724T000000Z",
			"X-Amz-Signature": "sig", "Content-Type": "video/mp4",
		},
		AssetUploadURL:               "/upload/assets/8",
		AssetUploadAuthenticityToken: "finalize-token",
	}
	policy.Asset.ID = 7
	policy.Asset.Href = assetURL
	policy.Asset.ContentType = "video/mp4"
	client := nativeUploadClient{
		validateS3URL:    validateNativeS3URL,
		validateAssetURL: validateNativeAssetURL,
	}

	err := client.validatePolicy(policy, uploadAsset{MediaType: "video/mp4"})

	if err == nil || !strings.Contains(err.Error(), "invalid native finalize path") {
		t.Fatalf("error=%v", err)
	}
}

func TestNativeVideoUploadReportsMutationOnPolicyValidationFailure(t *testing.T) {
	assetURL := "https://github.com/user-attachments/assets/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"upload_url": "https://untrusted.invalid/upload",
			"asset":      map[string]any{"id": 7, "content_type": "video/mp4", "href": assetURL},
			"form": map[string]string{
				"key": "asset-key", "policy": "signed", "X-Amz-Algorithm": "AWS4-HMAC-SHA256",
				"X-Amz-Credential": "cred", "X-Amz-Date": "20260724T000000Z",
				"X-Amz-Signature": "sig", "Content-Type": "video/mp4",
			},
			"asset_upload_url":                "/upload/assets/7",
			"asset_upload_authenticity_token": "finalize-token",
		})
	}))
	defer server.Close()
	client := nativeUploadClient{
		github:       server.Client(),
		baseURL:      server.URL,
		repository:   "owner/repo",
		repositoryID: 123,
		uploadToken:  "upload-token",
		validateS3URL: func(*url.URL) error {
			return fmt.Errorf("untrusted upload host")
		},
		validateAssetURL: validateNativeAssetURL,
	}

	result := client.upload(context.Background(), uploadAsset{
		Name: "demo.mp4", MediaType: "video/mp4", Content: []byte("video"),
	})

	if result.Err == nil || result.RemoteState != remoteChanged || result.FailedPhase != phasePolicy {
		t.Fatalf("result=%#v did not preserve the remote mutation", result)
	}
}

func TestNativeVideoUploadReportsCreatedPolicyWithInvalidJSONAsMutation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"asset":`))
	}))
	defer server.Close()
	client := nativeUploadClient{
		github: server.Client(), baseURL: server.URL, repository: "owner/repo",
		repositoryID: 123, uploadToken: "upload-token",
	}

	result := client.upload(context.Background(), uploadAsset{
		Name: "demo.mp4", MediaType: "video/mp4", Content: []byte("video"),
	})

	if result.Err == nil || result.RemoteState != remoteChanged || result.FailedPhase != phasePolicy {
		t.Fatalf("result=%#v did not preserve the remote mutation", result)
	}
}

func TestNativePolicyTransportAmbiguityIsPossiblyChanged(t *testing.T) {
	var received atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received.Store(true)
		_, _ = io.ReadAll(request.Body)
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			t.Fatal("server cannot hijack")
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}))
	defer server.Close()
	client := nativeUploadClient{
		github: server.Client(), baseURL: server.URL, repository: "owner/repo",
		repositoryID: 123, uploadToken: "upload-token",
	}

	result := client.upload(context.Background(), uploadAsset{
		Name: "demo.mp4", MediaType: "video/mp4", Content: []byte("video"),
	})

	if !received.Load() {
		t.Fatal("policy body was not received")
	}
	if result.Err == nil || result.URL != "" ||
		result.RemoteState != remotePossiblyChanged || result.FailedPhase != phasePolicy {
		t.Fatalf("result=%#v", result)
	}
}

func TestValidateNativeS3URLRejectsLookalikeHost(t *testing.T) {
	lookalike, err := url.Parse("https://github-production-user-asset-6210df.s3.amazonaws.com.attacker.invalid/")
	if err != nil {
		t.Fatal(err)
	}

	err = validateNativeS3URL(lookalike)

	if err == nil || !strings.Contains(err.Error(), "untrusted native asset upload host") {
		t.Fatalf("error=%v", err)
	}
}

func TestHTTPResponseErrorDoesNotEchoUnstructuredResponseBody(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader("secret-upload-token")),
	}

	err := httpResponseError(response, http.StatusCreated)

	if strings.Contains(err.Error(), "secret-upload-token") {
		t.Fatalf("response body leaked into error: %v", err)
	}
}

func TestHTTPResponseErrorDoesNotEchoStructuredJSONMessage(t *testing.T) {
	marker := "SYNTHETIC_PRIVATE_MARKER_7f3a9c"
	response := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"message":"` + marker + `"}`)),
	}

	err := httpResponseError(response, http.StatusCreated)

	if err == nil || strings.Contains(err.Error(), marker) {
		t.Fatalf("structured message leaked into error: %v", err)
	}
	if !strings.Contains(err.Error(), "Bad Request") {
		t.Fatalf("error=%v", err)
	}
}

func assertS3Multipart(t *testing.T, request *http.Request, wantContent string, wantOrder ...string) {
	t.Helper()
	reader, err := request.MultipartReader()
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		order = append(order, part.FormName())
		content, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		if part.FormName() == "file" && string(content) != wantContent {
			t.Fatalf("file=%q", content)
		}
	}
	if len(wantOrder) == 0 {
		wantOrder = []string{
			"key", "policy", "X-Amz-Algorithm", "X-Amz-Credential",
			"X-Amz-Date", "X-Amz-Signature", "Content-Type", "file",
		}
	}
	if !slices.Equal(order, wantOrder) {
		t.Fatalf("multipart order=%v want=%v", order, wantOrder)
	}
}

func TestUploadToS3AppendsUnknownFormKeysInSortedOrder(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assertS3Multipart(t, request, "video",
			"key", "policy", "X-Amz-Algorithm", "X-Amz-Credential",
			"X-Amz-Date", "X-Amz-Signature", "Content-Type",
			"x-unknown-a", "x-unknown-b", "file",
		)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := nativeUploadClient{s3: server.Client(), baseURL: server.URL}
	err := client.uploadToS3(context.Background(), nativePolicy{
		UploadURL: server.URL + "/s3",
		Form: map[string]string{
			"key":              "asset-key",
			"policy":           "signed",
			"X-Amz-Algorithm":  "AWS4-HMAC-SHA256",
			"X-Amz-Credential": "cred",
			"X-Amz-Date":       "20260724T000000Z",
			"X-Amz-Signature":  "sig",
			"Content-Type":     "video/mp4",
			"x-unknown-b":      "second",
			"x-unknown-a":      "first",
		},
	}, uploadAsset{Name: "demo.mp4", MediaType: "video/mp4", Content: []byte("video")})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDecodeStrictJSONResponseAcceptsCharsetAndTrailingWhitespace(t *testing.T) {
	response := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:   io.NopCloser(strings.NewReader("{\"href\":\"ok\"}\n  \n")),
	}
	var payload struct {
		Href string `json:"href"`
	}
	if err := decodeStrictJSONResponse(response, &payload); err != nil || payload.Href != "ok" {
		t.Fatalf("payload=%#v err=%v", payload, err)
	}
}

func TestDecodeStrictJSONResponseRejectsMediaTypeTrailingAndOversize(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		want        string
	}{
		{name: "text plain", contentType: "text/plain", body: `{"href":"ok"}`, want: "non-JSON"},
		{name: "missing", contentType: "", body: `{"href":"ok"}`, want: "non-JSON"},
		{name: "trailing value", contentType: "application/json", body: "{\"href\":\"ok\"}\n{\"x\":1}", want: "trailing"},
		{name: "trailing junk", contentType: "application/json", body: `{"href":"ok"}trailing`, want: "trailing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := &http.Response{
				Header: http.Header{"Content-Type": []string{test.contentType}},
				Body:   io.NopCloser(strings.NewReader(test.body)),
			}
			var payload struct {
				Href string `json:"href"`
			}
			err := decodeStrictJSONResponse(response, &payload)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want substring %q", err, test.want)
			}
		})
	}

	oversize := strings.Repeat("a", maxJSONResponseBytes+1)
	response := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(strings.NewReader(`{"x":"` + oversize + `"}`)),
	}
	var payload map[string]string
	if err := decodeStrictJSONResponse(response, &payload); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("error=%v", err)
	}
}

func TestNativePolicyRejectsTextPlainJSONBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"upload_url":"https://example"}`))
	}))
	defer server.Close()
	client := nativeUploadClient{
		github: server.Client(), baseURL: server.URL, repository: "owner/repo",
		repositoryID: 123, uploadToken: "upload-token",
	}
	result := client.upload(context.Background(), uploadAsset{
		Name: "demo.mp4", MediaType: "video/mp4", Content: []byte("video"),
	})
	if result.Err == nil || result.RemoteState != remoteChanged || result.FailedPhase != phasePolicy {
		t.Fatalf("result=%#v", result)
	}
	if strings.Contains(result.Err.Error(), "https://example") {
		t.Fatalf("response leaked into error: %v", result.Err)
	}
}

func TestValidateNativeS3URLRejectsUserinfoPortAndQuery(t *testing.T) {
	tests := []string{
		"https://user:pass@github-production-user-asset-6210df.s3.amazonaws.com/",
		"https://github-production-user-asset-6210df.s3.amazonaws.com:443/",
		"https://github-production-user-asset-6210df.s3.amazonaws.com/?x=1",
		"https://github-production-user-asset-6210df.s3.amazonaws.com/#frag",
	}
	for _, raw := range tests {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateNativeS3URL(parsed); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
