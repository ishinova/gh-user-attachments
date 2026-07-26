package userattachments

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The golden files under testdata/cli hold the CLI surface: the commands and
// flags callers may invoke, the exit codes they may branch on, the shape of a
// result URL, and the files the tool accepts. AGENTS.md keys the Release
// contract on this directory, so "did this change the CLI surface" is answered
// by whether the diff touches these files rather than by reading the change.
//
// Regenerate with:
//
//	go test ./internal/userattachments -update
//
// Then review the diff. Regenerating to silence a failure defeats the purpose.
var updateCLISurface = flag.Bool("update", false, "rewrite the CLI surface golden files")

// goldenVersion stands in for the real version so the golden records the shape
// of the --version line without pinning the value. The value differs per build
// by design; the line format is what callers parse.
const goldenVersion = "0.0.0-golden"

const cliSurfaceDir = "testdata/cli"

// errSurface stands in for any failure when driving the exit-code mapping. Only
// its presence matters to exitCodeFor, never its text.
var errSurface = errors.New("surface")

func TestCLISurfaceMatchesGoldenFiles(t *testing.T) {
	for name, render := range map[string]func(*testing.T) string{
		"top-level-help.golden": renderTopLevelHelp,
		"upload-help.golden":    renderUploadHelp,
		"auth-help.golden":      renderAuthHelp,
		"contract.golden":       renderContract,
	} {
		t.Run(name, func(t *testing.T) {
			assertGolden(t, name, render(t))
		})
	}
}

func assertGolden(t *testing.T, name, actual string) {
	t.Helper()
	path := filepath.Join(cliSurfaceDir, name)
	if *updateCLISurface {
		if err := os.WriteFile(path, []byte(actual), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run go test ./internal/userattachments -update)", path, err)
	}
	if string(expected) != actual {
		t.Fatalf("%s is stale.\n--- want ---\n%s\n--- got ---\n%s", path, expected, actual)
	}
}

func renderTopLevelHelp(t *testing.T) string {
	t.Helper()
	return runForSurface(t, "--help")
}

func renderUploadHelp(t *testing.T) string {
	t.Helper()
	return runForSurface(t, "upload", "--help")
}

func renderAuthHelp(t *testing.T) string {
	t.Helper()
	return runForSurface(t, "auth", "--help")
}

// runForSurface captures what a caller sees on both streams for one invocation.
func runForSurface(t *testing.T, arguments ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), arguments, &stdout, &stderr, forbidGH(t))

	var out strings.Builder
	fmt.Fprintf(&out, "$ gh-user-attachments %s\n", strings.Join(arguments, " "))
	fmt.Fprintf(&out, "exit %d\n", code)
	writeStream(&out, "stdout", stdout.String())
	writeStream(&out, "stderr", stderr.String())
	return out.String()
}

func writeStream(out *strings.Builder, label, content string) {
	if content == "" {
		fmt.Fprintf(out, "--- %s (empty) ---\n", label)
		return
	}
	fmt.Fprintf(out, "--- %s ---\n%s", label, content)
	if !strings.HasSuffix(content, "\n") {
		out.WriteString("\n")
	}
}

// renderContract records the parts of the surface that are not visible in help
// output but that callers still depend on.
func renderContract(t *testing.T) string {
	t.Helper()
	var out strings.Builder

	previous := Version
	Version = goldenVersion
	t.Cleanup(func() { Version = previous })
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--version"}, &stdout, &stderr, forbidGH(t)); code != exitOK {
		t.Fatalf("--version exit=%d stderr=%q", code, stderr.String())
	}
	out.WriteString("# version line\n")
	out.WriteString(stdout.String())

	// Exit codes come from exitCodeFor rather than from the constants, so a
	// change to the mapping moves the golden even when the constants stay.
	out.WriteString("\n# exit codes\n")
	for _, entry := range []struct {
		outcome     batchOutcome
		description string
	}{
		{batchOutcome{FinalizedURLs: []string{"a"}}, "every file finalized"},
		{batchOutcome{FailedPhase: phaseLocalValidation, RemoteState: remoteUnchanged, Err: errSurface}, "local validation failed"},
		{batchOutcome{FailedPhase: phasePolicy, RemoteState: remoteUnchanged, Err: errSurface}, "failed before the first remote mutation"},
		{batchOutcome{FailedPhase: phaseObjectUpload, RemoteState: remoteChanged, Err: errSurface}, "failed after remote state changed"},
		{batchOutcome{FinalizedURLs: []string{"a"}, FailedPhase: phaseObjectUpload, Err: errSurface}, "failed after finalizing at least one URL"},
	} {
		fmt.Fprintf(&out, "%d\t%s\n", exitCodeFor(entry.outcome), entry.description)
	}

	// The result URL contract is the whole of validateNativeAssetURL, not just
	// the path pattern: scheme, userinfo, host, port, query, and fragment are
	// all part of what a caller may trust.
	out.WriteString("\n# result URL\n")
	for _, candidate := range []string{
		"https://github.com/user-attachments/assets/00000000-1111-2222-3333-444444444444",
		"http://github.com/user-attachments/assets/00000000-1111-2222-3333-444444444444",
		"https://user@github.com/user-attachments/assets/00000000-1111-2222-3333-444444444444",
		"https://github.com:443/user-attachments/assets/00000000-1111-2222-3333-444444444444",
		"https://example.com/user-attachments/assets/00000000-1111-2222-3333-444444444444",
		"https://github.com/user-attachments/assets/not-a-uuid",
		"https://github.com/user-attachments/assets/00000000-1111-2222-3333-444444444444?x=1",
		"https://github.com/user-attachments/assets/00000000-1111-2222-3333-444444444444#x",
	} {
		verdict := "accept"
		if err := validateNativeAssetURL(candidate); err != nil {
			verdict = "reject"
		}
		fmt.Fprintf(&out, "%s\t%s\n", verdict, candidate)
	}

	out.WriteString("\n# file count\n")
	fmt.Fprintf(&out, "min\t%d\nmax\t%d\n", minFiles, maxFiles)

	out.WriteString("\n# accepted files\n")
	extensions := make([]string, 0, len(supportedFileTypes))
	for extension := range supportedFileTypes {
		extensions = append(extensions, extension)
	}
	sort.Strings(extensions)
	for _, extension := range extensions {
		fileType := supportedFileTypes[extension]
		fmt.Fprintf(&out, "%s\t%s\t%d\n", extension, fileType.mediaType, fileType.maxBytes)
	}

	return out.String()
}
