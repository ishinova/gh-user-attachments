package userattachments

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func forbidGH(t *testing.T) commandRunner {
	return func(context.Context, ...string) (commandResult, error) {
		t.Fatal("gh must not run after local validation failure")
		return commandResult{}, nil
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--help"}, &stdout, &stderr, forbidGH(t))
	if code != 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "Upload multiple files") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunUploadHelpDescribesGenericAttachmentFiles(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"upload", "--help"}, &stdout, &stderr, forbidGH(t))
	if code != 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "local attachment file path") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunVersion(t *testing.T) {
	previous := Version
	Version = "1.2.3"
	t.Cleanup(func() { Version = previous })

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--version"}, &stdout, &stderr, forbidGH(t))
	if code != 0 || stdout.String() != "gh-user-attachments 1.2.3\n" || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCommandTimeoutDoesNotLimitTheWholeUploadBatch(t *testing.T) {
	if timeout := commandTimeout([]string{"upload"}); timeout != 0 {
		t.Fatalf("upload timeout=%s want no global deadline", timeout)
	}
	if timeout := commandTimeout([]string{"auth", "login"}); timeout != 10*time.Minute {
		t.Fatalf("auth login timeout=%s want=10m", timeout)
	}
}

func TestRunValidationFailureDoesNotWriteAResultURL(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first.png")
	writePNG(t, first)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"upload", "--repo", "owner/repo",
		"--file", first,
		"--file", "also-missing.png",
	}, &stdout, &stderr, forbidGH(t))
	if code != exitUsage || stdout.Len() != 0 || !strings.Contains(stderr.String(), "also-missing.png") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunUploadWritesFinalURLsInOrderAndExitsZero(t *testing.T) {
	previous := newRunBatchUpload
	t.Cleanup(func() { newRunBatchUpload = previous })

	firstURL := canonicalAssetURL(0x44)
	secondURL := canonicalAssetURL(0x55)
	paths := batchTestPaths(t)
	newRunBatchUpload = func(runner commandRunner, _ ...batchOption) batchUpload {
		return newBatchUpload(runner, withUploader(sequenceUploader([]fileUploadResult{
			{URL: firstURL, RemoteState: remoteChanged},
			{URL: secondURL, RemoteState: remoteChanged},
		})))
	}
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"id":123}`},
		{body: `{"login":"owner"}`},
	}}

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"upload", "--repo", "owner/repo",
		"--file", paths[0],
		"--file", paths[1],
	}, &stdout, &stderr, runner.Run)

	if code != exitOK || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	want := firstURL + "\n" + secondURL + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout=%q want=%q", stdout.String(), want)
	}
	runner.assertDone()
}

func TestRunUploadPartialFailureWritesCompletedURLsAndExitFourNote(t *testing.T) {
	previous := newRunBatchUpload
	t.Cleanup(func() { newRunBatchUpload = previous })

	firstURL := canonicalAssetURL(0x44)
	paths := batchTestPaths(t)
	newRunBatchUpload = func(runner commandRunner, _ ...batchOption) batchUpload {
		return newBatchUpload(runner, withUploader(sequenceUploader([]fileUploadResult{
			{URL: firstURL, RemoteState: remoteChanged},
			{RemoteState: remoteChanged, FailedPhase: phaseObjectUpload, Err: fmt.Errorf("upload unavailable")},
		})))
	}
	runner := &scriptedRunner{t: t, calls: []scriptedCall{
		{body: `{"id":123}`},
		{body: `{"login":"owner"}`},
	}}

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"upload", "--repo", "owner/repo",
		"--file", paths[0],
		"--file", paths[1],
	}, &stdout, &stderr, runner.Run)

	if code != exitPartial {
		t.Fatalf("code=%d", code)
	}
	if stdout.String() != firstURL+"\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "upload unavailable") {
		t.Fatalf("stderr=%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "stdout contains only completed result URLs; GitHub may also have created incomplete remote state") {
		t.Fatalf("missing exit 4 note: stderr=%q", stderr.String())
	}
	runner.assertDone()
}

func TestParseOptionsRequiresExplicitTarget(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseOptions([]string{"--file", "a.png", "--file", "b.png"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "OWNER/REPO") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseOptionsRequiresTwoToTenFiles(t *testing.T) {
	elevenFiles := []string{"--repo", "owner/repo"}
	for index := 0; index < 11; index++ {
		elevenFiles = append(elevenFiles, "--file", fmt.Sprintf("%d.png", index))
	}
	tests := [][]string{
		{"--repo", "owner/repo"},
		{"--repo", "owner/repo", "--file", "a.png"},
		elevenFiles,
	}
	for _, arguments := range tests {
		var stderr bytes.Buffer
		_, err := parseOptions(arguments, &stderr)
		if err == nil || !strings.Contains(err.Error(), "between 2 and 10") {
			t.Fatalf("arguments=%v error=%v", arguments, err)
		}
	}
}

func TestParseOptionsAcceptsMultipleFilesInOrder(t *testing.T) {
	var stderr bytes.Buffer
	options, err := parseOptions([]string{
		"--repo", "owner/repo",
		"--file", "first.png",
		"--file", "second.mp4",
	}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if len(options.files) != 2 || options.files[0] != "first.png" || options.files[1] != "second.mp4" {
		t.Fatalf("files=%v", options.files)
	}
}
