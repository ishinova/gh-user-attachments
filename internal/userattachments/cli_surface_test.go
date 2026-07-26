package userattachments

import (
	"bytes"
	"context"
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

	out.WriteString("\n# exit codes\n")
	for _, entry := range []struct {
		code    int
		meaning string
	}{
		{exitOK, "every file finalized"},
		{exitUsage, "options or local validation failed, no remote mutation started"},
		{exitAPI, "failed before the first remote mutation"},
		{exitPartial, "remote state changed or cannot be ruled out"},
	} {
		fmt.Fprintf(&out, "%d\t%s\n", entry.code, entry.meaning)
	}

	out.WriteString("\n# result URL path\n")
	fmt.Fprintf(&out, "%s\n", nativeAssetPathPattern.String())

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
