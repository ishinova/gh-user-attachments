package userattachments

import (
	"context"
	"fmt"
)

type attachmentService struct {
	api                   apiClient
	prepareNativeUploader func(context.Context, string, int64, string) (assetUploader, error)
	materializeAsset      func(localAsset) (uploadAsset, error)
}

func newAttachmentService(runner commandRunner) attachmentService {
	return attachmentService{
		api:                   apiClient{runner: runner},
		prepareNativeUploader: prepareNativeUploader,
		materializeAsset:      materializeAsset,
	}
}

func (s attachmentService) execute(ctx context.Context, repo string, assets []localAsset) ([]string, error) {
	repositoryID, login, err := s.getUploadIdentity(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	uploader, err := s.prepareNativeUploader(ctx, repo, repositoryID, login)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	urls := make([]string, 0, len(assets))
	for _, asset := range assets {
		loaded, err := s.materializeAsset(asset)
		if err != nil {
			return urls, fmt.Errorf("file: %w", err)
		}
		uploadedURL, err := uploader(ctx, loaded)
		if err != nil {
			return urls, fmt.Errorf("upload: %w", err)
		}
		urls = append(urls, uploadedURL)
	}
	return urls, nil
}

func (s attachmentService) getUploadIdentity(ctx context.Context, repo string) (int64, string, error) {
	repositoryResponse, err := s.api.get(ctx, "repos/"+repo)
	if err != nil {
		return 0, "", fmt.Errorf("read repository identity: %w", err)
	}
	var repository struct {
		ID int64 `json:"id"`
	}
	if err := decodeJSON(repositoryResponse, &repository); err != nil || repository.ID <= 0 {
		if err == nil {
			err = fmt.Errorf("GitHub returned an invalid repository ID")
		}
		return 0, "", fmt.Errorf("read repository identity: %w", err)
	}
	login, err := currentLogin(ctx, s.api)
	if err != nil {
		return 0, "", err
	}
	return repository.ID, login, nil
}
