package userattachments

import (
	"context"
	"fmt"
)

type remoteStateCertainty uint8

const (
	remoteUnchanged remoteStateCertainty = iota
	remotePossiblyChanged
	remoteChanged
)

type failurePhase uint8

const (
	phaseNone failurePhase = iota
	phaseLocalValidation
	phasePreparation
	phasePolicy
	phaseObjectUpload
	phaseFinalize
)

type batchOutcome struct {
	FinalizedURLs []string
	RemoteState   remoteStateCertainty
	FailedPhase   failurePhase
	Err           error
}

type fileUploadResult struct {
	URL         string
	RemoteState remoteStateCertainty
	FailedPhase failurePhase
	Err         error
}

// batchUpload owns local cardinality, preflight, preparation, and per-file
// upload orchestration. Callers observe paths → batchOutcome only.
// materializeAsset is always the package implementation; tests inject only the
// uploader/prepare seam and exercise TOCTOU via the real filesystem.
type batchUpload struct {
	api             apiClient
	prepareUploader func(context.Context, string, int64, string) (assetUploader, error)
}

// batchOption configures the package-private prepare/uploader seam for tests.
// Production uses newBatchUpload(runner) with the native GitHub adapter only.
type batchOption func(*batchUpload)

func newBatchUpload(runner commandRunner, opts ...batchOption) batchUpload {
	batch := batchUpload{
		api:             apiClient{runner: runner},
		prepareUploader: prepareNativeUploader,
	}
	for _, opt := range opts {
		opt(&batch)
	}
	return batch
}

func (s batchUpload) execute(ctx context.Context, repo string, paths []string) batchOutcome {
	if len(paths) < minFiles || len(paths) > maxFiles {
		return batchOutcome{
			RemoteState: remoteUnchanged,
			FailedPhase: phaseLocalValidation,
			Err:         fmt.Errorf("batch requires between %d and %d files", minFiles, maxFiles),
		}
	}
	assets, err := loadAssets(paths)
	if err != nil {
		return batchOutcome{
			RemoteState: remoteUnchanged,
			FailedPhase: phaseLocalValidation,
			Err:         err,
		}
	}
	repositoryID, login, err := s.getUploadIdentity(ctx, repo)
	if err != nil {
		return batchOutcome{
			RemoteState: remoteUnchanged,
			FailedPhase: phasePreparation,
			Err:         fmt.Errorf("prepare: %w", err),
		}
	}
	uploader, err := s.prepareUploader(ctx, repo, repositoryID, login)
	if err != nil {
		return batchOutcome{
			RemoteState: remoteUnchanged,
			FailedPhase: phasePreparation,
			Err:         fmt.Errorf("session: %w", err),
		}
	}

	urls := make([]string, 0, len(assets))
	remote := remoteUnchanged
	for _, asset := range assets {
		loaded, err := materializeAsset(asset)
		if err != nil {
			phase := phaseLocalValidation
			certainty := remote
			if len(urls) > 0 {
				certainty = maxRemoteState(certainty, remoteChanged)
			}
			return batchOutcome{
				FinalizedURLs: urls,
				RemoteState:   certainty,
				FailedPhase:   phase,
				Err:           fmt.Errorf("file: %w", err),
			}
		}
		result := uploader(ctx, loaded)
		remote = maxRemoteState(remote, result.RemoteState)
		if result.Err != nil {
			return batchOutcome{
				FinalizedURLs: urls,
				RemoteState:   remote,
				FailedPhase:   result.FailedPhase,
				Err:           fmt.Errorf("upload: %w", result.Err),
			}
		}
		if result.FailedPhase != phaseNone || result.RemoteState == remoteUnchanged {
			return batchOutcome{
				FinalizedURLs: urls,
				RemoteState:   maxRemoteState(remote, remoteChanged),
				FailedPhase:   phaseFinalize,
				Err:           fmt.Errorf("upload: inconsistent success result"),
			}
		}
		if err := validateNativeAssetURL(result.URL); err != nil {
			return batchOutcome{
				FinalizedURLs: urls,
				RemoteState:   maxRemoteState(remote, remoteChanged),
				FailedPhase:   phaseFinalize,
				Err:           fmt.Errorf("upload: %w", err),
			}
		}
		urls = append(urls, result.URL)
		remote = remoteChanged
	}
	return batchOutcome{
		FinalizedURLs: urls,
		RemoteState:   remoteChanged,
		FailedPhase:   phaseNone,
	}
}

func (s batchUpload) getUploadIdentity(ctx context.Context, repo string) (int64, string, error) {
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

func exitCodeFor(outcome batchOutcome) int {
	if outcome.Err == nil {
		return exitOK
	}
	if len(outcome.FinalizedURLs) > 0 || outcome.RemoteState != remoteUnchanged {
		return exitPartial
	}
	if outcome.FailedPhase == phaseLocalValidation {
		return exitUsage
	}
	return exitAPI
}

func maxRemoteState(values ...remoteStateCertainty) remoteStateCertainty {
	max := remoteUnchanged
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}
