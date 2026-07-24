package userattachments

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sequenceUploader(outcomes []fileUploadResult) assetUploader {
	index := 0
	return func(context.Context, uploadAsset) fileUploadResult {
		outcome := outcomes[index]
		index++
		return outcome
	}
}

func withUploader(uploader assetUploader) batchOption {
	return func(batch *batchUpload) {
		batch.prepareUploader = func(context.Context, string, int64, string) (assetUploader, error) {
			return uploader, nil
		}
	}
}

func withPrepareUploader(prepare func(context.Context, string, int64, string) (assetUploader, error)) batchOption {
	return func(batch *batchUpload) {
		batch.prepareUploader = prepare
	}
}

func batchTestPaths(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	first := filepath.Join(dir, "first.png")
	second := filepath.Join(dir, "second.mp4")
	writePNG(t, first)
	writeSupportedTestFile(t, second)
	return []string{first, second}
}

func canonicalAssetURL(id int) string {
	return fmt.Sprintf(
		"https://github.com/user-attachments/assets/%08x-%04x-%04x-%04x-%012x",
		id, id, id, id, id,
	)
}

func newTestBatch(t *testing.T, runner *scriptedRunner, uploader assetUploader) batchUpload {
	t.Helper()
	return newBatchUpload(runner.Run, withPrepareUploader(func(_ context.Context, repo string, repoID int64, login string) (assetUploader, error) {
		if repo != "owner/repo" || repoID != 123 || login != "owner" {
			t.Fatalf("prepare repo=%q id=%d login=%q", repo, repoID, login)
		}
		return uploader, nil
	}))
}

func TestBatchUploadReturnsFinalNativeURLsInInputOrder(t *testing.T) {
	firstURL := canonicalAssetURL(0x44)
	secondURL := canonicalAssetURL(0x55)
	paths := batchTestPaths(t)
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"id":123}`},
		{body: `{"login":"owner"}`},
	}}
	batch := newTestBatch(t, runner, sequenceUploader([]fileUploadResult{
		{URL: firstURL, RemoteState: remoteChanged},
		{URL: secondURL, RemoteState: remoteChanged},
	}))

	outcome := batch.execute(context.Background(), "owner/repo", paths)

	if outcome.Err != nil || len(outcome.FinalizedURLs) != 2 ||
		outcome.FinalizedURLs[0] != firstURL || outcome.FinalizedURLs[1] != secondURL ||
		outcome.RemoteState != remoteChanged || outcome.FailedPhase != phaseNone {
		t.Fatalf("outcome=%#v", outcome)
	}
	if code := exitCodeFor(outcome); code != exitOK {
		t.Fatalf("exit=%d", code)
	}
	runner.assertDone()
}

func TestBatchUploadAcceptsTenFilesInInputOrder(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, 0, maxFiles)
	urls := make([]string, 0, maxFiles)
	outcomes := make([]fileUploadResult, 0, maxFiles)
	for i := range maxFiles {
		path := filepath.Join(dir, fmt.Sprintf("file-%02d.png", i))
		writePNG(t, path)
		paths = append(paths, path)
		url := canonicalAssetURL(0x10 + i)
		urls = append(urls, url)
		outcomes = append(outcomes, fileUploadResult{URL: url, RemoteState: remoteChanged})
	}
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"id":123}`},
		{body: `{"login":"owner"}`},
	}}
	batch := newTestBatch(t, runner, sequenceUploader(outcomes))

	outcome := batch.execute(context.Background(), "owner/repo", paths)

	if outcome.Err != nil || len(outcome.FinalizedURLs) != maxFiles ||
		outcome.RemoteState != remoteChanged || outcome.FailedPhase != phaseNone {
		t.Fatalf("outcome=%#v", outcome)
	}
	for i, url := range urls {
		if outcome.FinalizedURLs[i] != url {
			t.Fatalf("url[%d]=%q want %q", i, outcome.FinalizedURLs[i], url)
		}
	}
	if code := exitCodeFor(outcome); code != exitOK {
		t.Fatalf("exit=%d", code)
	}
	runner.assertDone()
}

func TestBatchUploadReturnsOnlyCompletedURLsWhenLaterUploadFails(t *testing.T) {
	firstURL := canonicalAssetURL(0x44)
	paths := batchTestPaths(t)
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"id":123}`},
		{body: `{"login":"owner"}`},
	}}
	batch := newTestBatch(t, runner, sequenceUploader([]fileUploadResult{
		{URL: firstURL, RemoteState: remoteChanged},
		{RemoteState: remoteChanged, FailedPhase: phaseObjectUpload, Err: fmt.Errorf("upload unavailable")},
	}))

	outcome := batch.execute(context.Background(), "owner/repo", paths)

	if len(outcome.FinalizedURLs) != 1 || outcome.FinalizedURLs[0] != firstURL ||
		outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "upload:") ||
		outcome.RemoteState != remoteChanged || outcome.FailedPhase != phaseObjectUpload {
		t.Fatalf("outcome=%#v", outcome)
	}
	if code := exitCodeFor(outcome); code != exitPartial {
		t.Fatalf("exit=%d", code)
	}
	runner.assertDone()
}

func TestBatchUploadReturnsCompletedURLsWhenALaterFileChangesAfterValidation(t *testing.T) {
	firstURL := canonicalAssetURL(0x44)
	paths := batchTestPaths(t)
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"id":123}`},
		{body: `{"login":"owner"}`},
	}}
	uploads := 0
	batch := newBatchUpload(runner.Run, withUploader(func(context.Context, uploadAsset) fileUploadResult {
		uploads++
		if uploads != 1 {
			t.Fatalf("unexpected upload count %d", uploads)
		}
		if err := os.WriteFile(paths[1], []byte("changed-after-preflight"), 0o600); err != nil {
			t.Fatal(err)
		}
		return fileUploadResult{URL: firstURL, RemoteState: remoteChanged}
	}))

	outcome := batch.execute(context.Background(), "owner/repo", paths)

	if len(outcome.FinalizedURLs) != 1 || outcome.FinalizedURLs[0] != firstURL ||
		outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "file:") ||
		!strings.Contains(outcome.Err.Error(), "file changed after validation") ||
		outcome.RemoteState != remoteChanged || outcome.FailedPhase != phaseLocalValidation {
		t.Fatalf("outcome=%#v", outcome)
	}
	if code := exitCodeFor(outcome); code != exitPartial {
		t.Fatalf("exit=%d", code)
	}
	runner.assertDone()
}

func TestBatchUploadReportsAmbiguousPolicyFailureWithoutURL(t *testing.T) {
	paths := batchTestPaths(t)
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"id":123}`},
		{body: `{"login":"owner"}`},
	}}
	batch := newTestBatch(t, runner, sequenceUploader([]fileUploadResult{{
		RemoteState: remotePossiblyChanged,
		FailedPhase: phasePolicy,
		Err:         fmt.Errorf("connection reset"),
	}}))

	outcome := batch.execute(context.Background(), "owner/repo", paths)

	if len(outcome.FinalizedURLs) != 0 || outcome.Err == nil ||
		outcome.RemoteState != remotePossiblyChanged || outcome.FailedPhase != phasePolicy {
		t.Fatalf("outcome=%#v", outcome)
	}
	if code := exitCodeFor(outcome); code != exitPartial {
		t.Fatalf("exit=%d", code)
	}
	runner.assertDone()
}

func TestBatchUploadLabelsIdentityFailureAsPrepare(t *testing.T) {
	paths := batchTestPaths(t)
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"message":"Not Found"}`, code: 1, stderr: "Not Found"},
	}}
	batch := newBatchUpload(runner.Run, withPrepareUploader(func(context.Context, string, int64, string) (assetUploader, error) {
		t.Fatal("prepareUploader must not run after identity failure")
		return nil, nil
	}))

	outcome := batch.execute(context.Background(), "owner/repo", paths)

	if outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "prepare:") ||
		strings.Contains(outcome.Err.Error(), "session:") ||
		outcome.FailedPhase != phasePreparation || outcome.RemoteState != remoteUnchanged ||
		exitCodeFor(outcome) != exitAPI {
		t.Fatalf("outcome=%#v", outcome)
	}
	runner.assertDone()
}

func TestBatchUploadLabelsPrepareUploaderFailureAsSession(t *testing.T) {
	paths := batchTestPaths(t)
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"id":123}`},
		{body: `{"login":"owner"}`},
	}}
	batch := newBatchUpload(runner.Run, withPrepareUploader(func(context.Context, string, int64, string) (assetUploader, error) {
		return nil, fmt.Errorf("stored session missing")
	}))

	outcome := batch.execute(context.Background(), "owner/repo", paths)

	if outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "session:") ||
		outcome.FailedPhase != phasePreparation || outcome.RemoteState != remoteUnchanged ||
		exitCodeFor(outcome) != exitAPI {
		t.Fatalf("outcome=%#v", outcome)
	}
	runner.assertDone()
}

func TestBatchUploadLocalValidationFailsBeforeRemote(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first.png")
	writePNG(t, first)
	runner := &scriptedRunner{t: t}
	called := false
	batch := newBatchUpload(runner.Run, withPrepareUploader(func(context.Context, string, int64, string) (assetUploader, error) {
		called = true
		return nil, fmt.Errorf("should not prepare")
	}))

	outcome := batch.execute(context.Background(), "owner/repo", []string{first, "missing.png"})

	if called || outcome.Err == nil || outcome.FailedPhase != phaseLocalValidation ||
		outcome.RemoteState != remoteUnchanged || exitCodeFor(outcome) != exitUsage {
		t.Fatalf("outcome=%#v called=%v", outcome, called)
	}
}

func TestBatchUploadRejectsCardinalityBeforeRemote(t *testing.T) {
	dir := t.TempDir()
	files := make([]string, 0, 11)
	for i := range 11 {
		path := filepath.Join(dir, fmt.Sprintf("file-%02d.png", i))
		writePNG(t, path)
		files = append(files, path)
	}
	runner := &scriptedRunner{t: t}
	remoteCalls := 0
	batch := newBatchUpload(runner.Run, withPrepareUploader(func(context.Context, string, int64, string) (assetUploader, error) {
		remoteCalls++
		return nil, fmt.Errorf("should not prepare")
	}))

	for _, paths := range [][]string{nil, files[:1], files[:11]} {
		outcome := batch.execute(context.Background(), "owner/repo", paths)
		if remoteCalls != 0 || outcome.Err == nil || outcome.FailedPhase != phaseLocalValidation ||
			outcome.RemoteState != remoteUnchanged || exitCodeFor(outcome) != exitUsage {
			t.Fatalf("paths=%d outcome=%#v remoteCalls=%d", len(paths), outcome, remoteCalls)
		}
	}
}

func TestBatchUploadRejectsSuccessWithEmptyOrInvalidURL(t *testing.T) {
	paths := batchTestPaths(t)
	tests := []struct {
		name string
		url  string
	}{
		{name: "empty", url: ""},
		{name: "invalid host", url: "https://example.invalid/user-attachments/assets/44444444-4444-4444-8444-444444444444"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &scriptedRunner{t: t, calls: []scriptedCall{
				{body: `{"id":123}`},
				{body: `{"login":"owner"}`},
			}}
			batch := newTestBatch(t, runner, sequenceUploader([]fileUploadResult{
				{URL: test.url, RemoteState: remoteChanged},
			}))

			outcome := batch.execute(context.Background(), "owner/repo", paths)

			if len(outcome.FinalizedURLs) != 0 || outcome.Err == nil ||
				!strings.Contains(outcome.Err.Error(), "invalid native asset URL") ||
				outcome.RemoteState != remoteChanged || outcome.FailedPhase != phaseFinalize ||
				exitCodeFor(outcome) != exitPartial {
				t.Fatalf("outcome=%#v", outcome)
			}
			if strings.Contains(outcome.Err.Error(), test.url) && test.url != "" {
				t.Fatalf("error leaked url: %v", outcome.Err)
			}
			runner.assertDone()
		})
	}
}

func TestBatchUploadRejectsInconsistentSuccessResult(t *testing.T) {
	firstURL := canonicalAssetURL(0x44)
	paths := batchTestPaths(t)
	tests := []struct {
		name   string
		result fileUploadResult
	}{
		{
			name: "failed phase set",
			result: fileUploadResult{
				URL:         firstURL,
				RemoteState: remoteChanged,
				FailedPhase: phaseFinalize,
			},
		},
		{
			name: "remote unchanged",
			result: fileUploadResult{
				URL:         firstURL,
				RemoteState: remoteUnchanged,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &scriptedRunner{t: t, calls: []scriptedCall{
				{body: `{"id":123}`},
				{body: `{"login":"owner"}`},
			}}
			batch := newTestBatch(t, runner, sequenceUploader([]fileUploadResult{test.result}))

			outcome := batch.execute(context.Background(), "owner/repo", paths)

			if len(outcome.FinalizedURLs) != 0 || outcome.Err == nil ||
				!strings.Contains(outcome.Err.Error(), "inconsistent success result") ||
				outcome.RemoteState != remoteChanged || outcome.FailedPhase != phaseFinalize ||
				exitCodeFor(outcome) != exitPartial {
				t.Fatalf("outcome=%#v", outcome)
			}
			runner.assertDone()
		})
	}
}

func TestBatchUploadPreservesPriorURLsWhenLaterSuccessURLInvalid(t *testing.T) {
	firstURL := canonicalAssetURL(0x44)
	paths := batchTestPaths(t)
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"id":123}`},
		{body: `{"login":"owner"}`},
	}}
	batch := newTestBatch(t, runner, sequenceUploader([]fileUploadResult{
		{URL: firstURL, RemoteState: remoteChanged},
		{URL: "", RemoteState: remoteChanged},
	}))

	outcome := batch.execute(context.Background(), "owner/repo", paths)

	if len(outcome.FinalizedURLs) != 1 || outcome.FinalizedURLs[0] != firstURL ||
		outcome.Err == nil || outcome.RemoteState != remoteChanged ||
		outcome.FailedPhase != phaseFinalize || exitCodeFor(outcome) != exitPartial {
		t.Fatalf("outcome=%#v", outcome)
	}
	runner.assertDone()
}

func TestExitCodeForBatchOutcome(t *testing.T) {
	tests := []struct {
		name    string
		outcome batchOutcome
		want    int
	}{
		{
			name:    "success",
			outcome: batchOutcome{FinalizedURLs: []string{canonicalAssetURL(0x11)}, RemoteState: remoteChanged},
			want:    exitOK,
		},
		{
			name:    "local validation",
			outcome: batchOutcome{RemoteState: remoteUnchanged, FailedPhase: phaseLocalValidation, Err: fmt.Errorf("bad file")},
			want:    exitUsage,
		},
		{
			name:    "preparation",
			outcome: batchOutcome{RemoteState: remoteUnchanged, FailedPhase: phasePreparation, Err: fmt.Errorf("repo")},
			want:    exitAPI,
		},
		{
			name:    "policy before send",
			outcome: batchOutcome{RemoteState: remoteUnchanged, FailedPhase: phasePolicy, Err: fmt.Errorf("build")},
			want:    exitAPI,
		},
		{
			name:    "policy ambiguity",
			outcome: batchOutcome{RemoteState: remotePossiblyChanged, FailedPhase: phasePolicy, Err: fmt.Errorf("eof")},
			want:    exitPartial,
		},
		{
			name:    "policy changed",
			outcome: batchOutcome{RemoteState: remoteChanged, FailedPhase: phasePolicy, Err: fmt.Errorf("bad json")},
			want:    exitPartial,
		},
		{
			name:    "prior url",
			outcome: batchOutcome{FinalizedURLs: []string{canonicalAssetURL(0x22)}, RemoteState: remoteChanged, FailedPhase: phaseObjectUpload, Err: fmt.Errorf("s3")},
			want:    exitPartial,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := exitCodeFor(test.outcome); got != test.want {
				t.Fatalf("exit=%d want=%d", got, test.want)
			}
		})
	}
}
