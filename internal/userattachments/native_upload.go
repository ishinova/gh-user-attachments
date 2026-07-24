package userattachments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const (
	nativeUploadUserAgent = "gh-user-attachments"
	maxJSONResponseBytes  = 1 << 20
	maxSessionPageBytes   = 8 << 20
)

var requiredS3FormKeys = []string{
	"key",
	"policy",
	"X-Amz-Algorithm",
	"X-Amz-Credential",
	"X-Amz-Date",
	"X-Amz-Signature",
	"Content-Type",
}

type assetUploader func(context.Context, uploadAsset) fileUploadResult

type nativeUploadClient struct {
	github           *http.Client
	s3               *http.Client
	baseURL          string
	repository       string
	repositoryID     int64
	uploadToken      string
	validateS3URL    func(*url.URL) error
	validateAssetURL func(string) error
}

type nativePolicy struct {
	UploadURL string `json:"upload_url"`
	Asset     struct {
		ID          int64  `json:"id"`
		Href        string `json:"href"`
		ContentType string `json:"content_type"`
	} `json:"asset"`
	Form                         map[string]string `json:"form"`
	AssetUploadURL               string            `json:"asset_upload_url"`
	AssetUploadAuthenticityToken string            `json:"asset_upload_authenticity_token"`
}

func (c nativeUploadClient) upload(ctx context.Context, asset uploadAsset) fileUploadResult {
	policy, result := c.requestPolicy(ctx, asset)
	if result.Err != nil {
		return result
	}
	if err := c.uploadToS3(ctx, policy, asset); err != nil {
		return fileUploadResult{
			RemoteState: remoteChanged,
			FailedPhase: phaseObjectUpload,
			Err:         fmt.Errorf("upload native asset data: %w", err),
		}
	}
	url, err := c.finalize(ctx, policy)
	if err != nil {
		return fileUploadResult{
			RemoteState: remoteChanged,
			FailedPhase: phaseFinalize,
			Err:         fmt.Errorf("finalize native upload: %w", err),
		}
	}
	return fileUploadResult{URL: url, RemoteState: remoteChanged}
}

func (c nativeUploadClient) requestPolicy(ctx context.Context, asset uploadAsset) (nativePolicy, fileUploadResult) {
	body, contentType, err := multipartBody(nil, []formValue{
		{key: "name", value: asset.Name},
		{key: "size", value: strconv.Itoa(len(asset.Content))},
		{key: "content_type", value: asset.MediaType},
		{key: "authenticity_token", value: c.uploadToken},
		{key: "repository_id", value: strconv.FormatInt(c.repositoryID, 10)},
	})
	if err != nil {
		return nativePolicy{}, fileUploadResult{
			RemoteState: remoteUnchanged,
			FailedPhase: phasePolicy,
			Err:         fmt.Errorf("request native upload policy: %w", err),
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/upload/policies/assets", body)
	if err != nil {
		return nativePolicy{}, fileUploadResult{
			RemoteState: remoteUnchanged,
			FailedPhase: phasePolicy,
			Err:         fmt.Errorf("request native upload policy: %w", err),
		}
	}
	c.setGitHubHeaders(request, contentType)
	response, err := c.github.Do(request)
	if err != nil {
		return nativePolicy{}, fileUploadResult{
			RemoteState: remotePossiblyChanged,
			FailedPhase: phasePolicy,
			Err:         fmt.Errorf("request native upload policy: %w", err),
		}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return nativePolicy{}, fileUploadResult{
			RemoteState: remotePossiblyChanged,
			FailedPhase: phasePolicy,
			Err:         fmt.Errorf("request native upload policy: %w", httpResponseError(response, http.StatusCreated)),
		}
	}
	var policy nativePolicy
	if err := decodeStrictJSONResponse(response, &policy); err != nil {
		return nativePolicy{}, fileUploadResult{
			RemoteState: remoteChanged,
			FailedPhase: phasePolicy,
			Err:         fmt.Errorf("request native upload policy: %w", err),
		}
	}
	if err := c.validatePolicy(policy, asset); err != nil {
		return nativePolicy{}, fileUploadResult{
			RemoteState: remoteChanged,
			FailedPhase: phasePolicy,
			Err:         fmt.Errorf("request native upload policy: %w", err),
		}
	}
	return policy, fileUploadResult{RemoteState: remoteChanged}
}

func (c nativeUploadClient) uploadToS3(ctx context.Context, policy nativePolicy, asset uploadAsset) error {
	knownOrder := []string{
		"key", "acl", "policy", "X-Amz-Algorithm", "X-Amz-Credential",
		"X-Amz-Date", "X-Amz-Signature", "Content-Type", "Cache-Control",
		"x-amz-meta-Surrogate-Control",
	}
	written := make(map[string]struct{}, len(policy.Form))
	fields := make([]formValue, 0, len(policy.Form))
	for _, key := range knownOrder {
		if value, ok := policy.Form[key]; ok {
			fields = append(fields, formValue{key: key, value: value})
			written[key] = struct{}{}
		}
	}
	remaining := make([]string, 0, len(policy.Form)-len(written))
	for key := range policy.Form {
		if _, ok := written[key]; !ok {
			remaining = append(remaining, key)
		}
	}
	sort.Strings(remaining)
	for _, key := range remaining {
		fields = append(fields, formValue{key: key, value: policy.Form[key]})
	}
	body, contentType, err := multipartBody(&formFile{name: asset.Name, content: asset.Content}, fields)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, policy.UploadURL, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Origin", c.baseURL)
	request.Header.Set("User-Agent", nativeUploadUserAgent)
	response, err := c.s3.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return httpResponseError(response, http.StatusNoContent)
	}
	return nil
}

func (c nativeUploadClient) finalize(ctx context.Context, policy nativePolicy) (string, error) {
	body, contentType, err := multipartBody(nil, []formValue{{
		key: "authenticity_token", value: policy.AssetUploadAuthenticityToken,
	}})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+policy.AssetUploadURL, body)
	if err != nil {
		return "", err
	}
	c.setGitHubHeaders(request, contentType)
	response, err := c.github.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", httpResponseError(response, http.StatusOK)
	}
	var payload struct {
		Href string `json:"href"`
	}
	if err := decodeStrictJSONResponse(response, &payload); err != nil {
		return "", err
	}
	if payload.Href == "" {
		return "", fmt.Errorf("GitHub returned an empty native asset URL")
	}
	if payload.Href != policy.Asset.Href {
		return "", fmt.Errorf("GitHub changed native asset URL during finalize")
	}
	if c.validateAssetURL != nil {
		if err := c.validateAssetURL(payload.Href); err != nil {
			return "", err
		}
	}
	return payload.Href, nil
}

func (c nativeUploadClient) validatePolicy(policy nativePolicy, asset uploadAsset) error {
	uploadURL, err := url.Parse(policy.UploadURL)
	if err != nil || !uploadURL.IsAbs() {
		return fmt.Errorf("GitHub returned an invalid S3 upload URL")
	}
	if c.validateS3URL != nil {
		if err := c.validateS3URL(uploadURL); err != nil {
			return err
		}
	}
	if policy.Asset.ID <= 0 || policy.Asset.ContentType != asset.MediaType || policy.Asset.Href == "" {
		return fmt.Errorf("GitHub returned incomplete native asset metadata")
	}
	if c.validateAssetURL != nil {
		if err := c.validateAssetURL(policy.Asset.Href); err != nil {
			return err
		}
	}
	if policy.AssetUploadAuthenticityToken == "" {
		return fmt.Errorf("GitHub returned an incomplete native upload policy")
	}
	for _, key := range requiredS3FormKeys {
		if strings.TrimSpace(policy.Form[key]) == "" {
			return fmt.Errorf("GitHub returned an incomplete native upload policy")
		}
	}
	finalizeURL, err := url.Parse(policy.AssetUploadURL)
	expectedFinalizePath := fmt.Sprintf("/upload/assets/%d", policy.Asset.ID)
	if err != nil || finalizeURL.IsAbs() || finalizeURL.Host != "" || finalizeURL.Path != expectedFinalizePath || finalizeURL.RawQuery != "" || finalizeURL.Fragment != "" {
		return fmt.Errorf("GitHub returned an invalid native finalize path")
	}
	return nil
}

func (c nativeUploadClient) setGitHubHeaders(request *http.Request, contentType string) {
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Origin", c.baseURL)
	request.Header.Set("Referer", c.baseURL+"/"+c.repository)
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	request.Header.Set("User-Agent", nativeUploadUserAgent)
}

type formValue struct {
	key   string
	value string
}

type formFile struct {
	name    string
	content []byte
}

func multipartBody(file *formFile, fields []formValue) (*bytes.Buffer, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for _, field := range fields {
		if err := writer.WriteField(field.key, field.value); err != nil {
			return nil, "", fmt.Errorf("write multipart field %s: %w", field.key, err)
		}
	}
	if file != nil {
		part, err := writer.CreateFormFile("file", file.name)
		if err != nil {
			return nil, "", fmt.Errorf("create multipart file: %w", err)
		}
		if _, err := part.Write(file.content); err != nil {
			return nil, "", fmt.Errorf("write multipart file: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart body: %w", err)
	}
	return body, writer.FormDataContentType(), nil
}

func decodeStrictJSONResponse(response *http.Response, target any) error {
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf("GitHub returned a non-JSON response")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxJSONResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read GitHub response: %w", err)
	}
	if len(data) > maxJSONResponseBytes {
		return fmt.Errorf("GitHub response exceeded size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("GitHub response contained trailing data")
	}
	return nil
}

func httpResponseError(response *http.Response, expected int) error {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	statusText := http.StatusText(response.StatusCode)
	if statusText == "" {
		statusText = "unknown status"
	}
	return fmt.Errorf("expected HTTP %d: native upload HTTP %d: %s", expected, response.StatusCode, statusText)
}
