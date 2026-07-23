package userattachments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

const nativeUploadUserAgent = "gh-user-attachments"

type nativeUploadFailure struct {
	Err error
}

func (e *nativeUploadFailure) Error() string { return e.Err.Error() }
func (e *nativeUploadFailure) Unwrap() error { return e.Err }

type assetUploader func(context.Context, uploadAsset) (string, error)

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

func (c nativeUploadClient) upload(ctx context.Context, asset uploadAsset) (string, error) {
	policy, err := c.requestPolicy(ctx, asset)
	if err != nil {
		return "", fmt.Errorf("request native upload policy: %w", err)
	}
	if err := c.uploadToS3(ctx, policy, asset); err != nil {
		return "", &nativeUploadFailure{
			Err: fmt.Errorf("upload native asset data: %w", err),
		}
	}
	result, err := c.finalize(ctx, policy)
	if err != nil {
		return "", &nativeUploadFailure{
			Err: fmt.Errorf("finalize native upload: %w", err),
		}
	}
	return result, nil
}

func (c nativeUploadClient) requestPolicy(ctx context.Context, asset uploadAsset) (nativePolicy, error) {
	body, contentType, err := multipartBody(nil, []formValue{
		{key: "name", value: asset.Name},
		{key: "size", value: strconv.Itoa(len(asset.Content))},
		{key: "content_type", value: asset.MediaType},
		{key: "authenticity_token", value: c.uploadToken},
		{key: "repository_id", value: strconv.FormatInt(c.repositoryID, 10)},
	})
	if err != nil {
		return nativePolicy{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/upload/policies/assets", body)
	if err != nil {
		return nativePolicy{}, err
	}
	c.setGitHubHeaders(request, contentType)
	response, err := c.github.Do(request)
	if err != nil {
		return nativePolicy{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return nativePolicy{}, httpResponseError(response, http.StatusCreated)
	}
	var policy nativePolicy
	if err := decodeLimitedJSON(response.Body, &policy); err != nil {
		return nativePolicy{}, &nativeUploadFailure{Err: err}
	}
	if err := c.validatePolicy(policy, asset); err != nil {
		return nativePolicy{}, &nativeUploadFailure{Err: err}
	}
	return policy, nil
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
	if err := decodeLimitedJSON(response.Body, &payload); err != nil {
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
	if len(policy.Form) == 0 || policy.AssetUploadAuthenticityToken == "" {
		return fmt.Errorf("GitHub returned an incomplete native upload policy")
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

func decodeLimitedJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}

func httpResponseError(response *http.Response, expected int) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	var payload struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &payload)
	message := safeCommandMessage(payload.Message)
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	return fmt.Errorf("expected HTTP %d: native upload HTTP %d: %s", expected, response.StatusCode, message)
}

func nativeUploadMutated(err error) bool {
	var failure *nativeUploadFailure
	return errors.As(err, &failure)
}
