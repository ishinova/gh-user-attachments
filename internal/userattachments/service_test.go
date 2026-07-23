package userattachments

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func materializeTestAsset(asset localAsset) (uploadAsset, error) {
	return uploadAsset{Name: asset.Name, MediaType: asset.MediaType, Content: []byte(asset.Name)}, nil
}

type uploadOutcome struct {
	url string
	err error
}

func sequenceUploader(outcomes []uploadOutcome) assetUploader {
	index := 0
	return func(context.Context, uploadAsset) (string, error) {
		outcome := outcomes[index]
		index++
		return outcome.url, outcome.err
	}
}

func TestUploadReturnsFinalNativeURLsInInputOrder(t *testing.T) {
	firstURL := "https://github.com/user-attachments/assets/44444444-4444-4444-8444-444444444444"
	secondURL := "https://github.com/user-attachments/assets/55555555-5555-4555-8555-555555555555"
	assets := []localAsset{
		{Name: "first.png", MediaType: "image/png"},
		{Name: "second.mp4", MediaType: "video/mp4"},
	}
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"id":123}`},
		{body: `{"login":"owner"}`},
	}}
	uploader := sequenceUploader([]uploadOutcome{{url: firstURL}, {url: secondURL}})
	service := newAttachmentService(runner.Run)
	service.materializeAsset = materializeTestAsset
	service.prepareNativeUploader = func(_ context.Context, repo string, repoID int64, login string) (assetUploader, error) {
		if repo != "owner/repo" || repoID != 123 || login != "owner" {
			t.Fatalf("prepare repo=%q id=%d login=%q", repo, repoID, login)
		}
		return uploader, nil
	}

	urls, err := service.execute(context.Background(), "owner/repo", assets)

	if err != nil || len(urls) != 2 || urls[0] != firstURL || urls[1] != secondURL {
		t.Fatalf("urls=%v error=%v", urls, err)
	}
	runner.assertDone()
}

func TestUploadReturnsOnlyCompletedURLsWhenLaterUploadFails(t *testing.T) {
	firstURL := "https://github.com/user-attachments/assets/44444444-4444-4444-8444-444444444444"
	assets := []localAsset{
		{Name: "first.png", MediaType: "image/png"},
		{Name: "second.mp4", MediaType: "video/mp4"},
	}
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"id":123}`},
		{body: `{"login":"owner"}`},
	}}
	uploader := sequenceUploader([]uploadOutcome{{url: firstURL}, {err: fmt.Errorf("upload unavailable")}})
	service := newAttachmentService(runner.Run)
	service.materializeAsset = materializeTestAsset
	service.prepareNativeUploader = func(context.Context, string, int64, string) (assetUploader, error) {
		return uploader, nil
	}

	urls, err := service.execute(context.Background(), "owner/repo", assets)

	if len(urls) != 1 || urls[0] != firstURL || err == nil || !strings.Contains(err.Error(), "upload:") || nativeUploadMutated(err) {
		t.Fatalf("urls=%v error=%v", urls, err)
	}
	runner.assertDone()
}

func TestUploadReturnsCompletedURLsWhenALaterFileChangesAfterValidation(t *testing.T) {
	firstURL := "https://github.com/user-attachments/assets/44444444-4444-4444-8444-444444444444"
	assets := []localAsset{{Name: "first.png"}, {Name: "second.mp4"}}
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"id":123}`},
		{body: `{"login":"owner"}`},
	}}
	uploader := sequenceUploader([]uploadOutcome{{url: firstURL}})
	service := newAttachmentService(runner.Run)
	service.prepareNativeUploader = func(context.Context, string, int64, string) (assetUploader, error) {
		return uploader, nil
	}
	service.materializeAsset = func(asset localAsset) (uploadAsset, error) {
		if asset.Name == "second.mp4" {
			return uploadAsset{}, fmt.Errorf("file changed after validation")
		}
		return uploadAsset{Name: asset.Name, Content: []byte("first")}, nil
	}

	urls, err := service.execute(context.Background(), "owner/repo", assets)

	if len(urls) != 1 || urls[0] != firstURL || err == nil || !strings.Contains(err.Error(), "file:") || nativeUploadMutated(err) {
		t.Fatalf("urls=%v error=%v", urls, err)
	}
	runner.assertDone()
}

func TestUploadReportsFirstMutatedFailureAsPartialWithoutURL(t *testing.T) {
	assets := []localAsset{
		{Name: "first.png", MediaType: "image/png"},
		{Name: "second.mp4", MediaType: "video/mp4"},
	}
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"id":123}`},
		{body: `{"login":"owner"}`},
	}}
	uploader := sequenceUploader([]uploadOutcome{{
		err: &nativeUploadFailure{
			Err: fmt.Errorf("S3 unavailable"),
		},
	}})
	service := newAttachmentService(runner.Run)
	service.materializeAsset = materializeTestAsset
	service.prepareNativeUploader = func(context.Context, string, int64, string) (assetUploader, error) {
		return uploader, nil
	}

	urls, err := service.execute(context.Background(), "owner/repo", assets)

	if len(urls) != 0 || err == nil || !strings.Contains(err.Error(), "upload:") || !nativeUploadMutated(err) {
		t.Fatalf("urls=%v error=%v", urls, err)
	}
	runner.assertDone()
}
