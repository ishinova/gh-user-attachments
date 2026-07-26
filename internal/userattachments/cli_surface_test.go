package userattachments

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
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

	// exitCodeFor branches on four inputs whose representable domains are finite:
	// two booleans and two uint8 enums. The golden walks every value of all four
	// and emits a row wherever the result changes, so the table is total over the
	// representable domain rather than over the constants named today. A state or
	// phase added later cannot escape it, and adding one that no branch reads
	// leaves this file untouched, which is correct because nothing observable
	// changed.
	out.WriteString("\n# exit codes (first row of each run; total over the domain)\n")
	out.WriteString("err\turls\tremote>=\tphase>=\texit\n")
	for _, failed := range []bool{false, true} {
		for _, finalized := range []bool{false, true} {
			previous := -1
			for state := 0; state <= math.MaxUint8; state++ {
				for phase := 0; phase <= math.MaxUint8; phase++ {
					outcome := batchOutcome{
						RemoteState: remoteStateCertainty(state),
						FailedPhase: failurePhase(phase),
					}
					if failed {
						outcome.Err = errSurface
					}
					if finalized {
						outcome.FinalizedURLs = []string{"url"}
					}
					code := exitCodeFor(outcome)
					if code == previous {
						continue
					}
					previous = code
					fmt.Fprintf(&out, "%t\t%t\t%d\t%d\t%d\n", failed, finalized, state, phase, code)
				}
			}
		}
	}

	// The result URL domain is every string, so this matrix is representative,
	// not exhaustive. It records what validateNativeAssetURL accepts today,
	// including raw forms that decode to the canonical shape; hardening any of
	// them later moves this file.
	out.WriteString("\n# result URL (representative)\n")
	for _, candidate := range []string{
		"https://github.com/user-attachments/assets/00000000-1111-2222-3333-444444444444",
		"http://github.com/user-attachments/assets/00000000-1111-2222-3333-444444444444",
		"https://user@github.com/user-attachments/assets/00000000-1111-2222-3333-444444444444",
		"https://github.com:443/user-attachments/assets/00000000-1111-2222-3333-444444444444",
		"https://example.com/user-attachments/assets/00000000-1111-2222-3333-444444444444",
		"https://GITHUB.com/user-attachments/assets/00000000-1111-2222-3333-444444444444",
		"https://github.com/user-attachments/assets/not-a-uuid",
		"https://github.com/user-attachments/assets/00000000-1111-2222-3333-444444444444?x=1",
		"https://github.com/user-attachments/assets/00000000-1111-2222-3333-444444444444?",
		"https://github.com/user-attachments/assets/00000000-1111-2222-3333-444444444444#x",
		"https://github.com/user-attachments/assets/%300000000-1111-2222-3333-444444444444",
		"https://github.com/user-attachments/assets/00000000%2D1111-2222-3333-444444444444",
		"https://github.com/user-attachments/../user-attachments/assets/00000000-1111-2222-3333-444444444444",
	} {
		verdict := "accept"
		if err := validateNativeAssetURL(candidate); err != nil {
			verdict = "reject"
		}
		fmt.Fprintf(&out, "%s\t%s\n", verdict, candidate)
	}

	out.WriteString("\n# file count\n")
	fmt.Fprintf(&out, "min\t%d\nmax\t%d\n", minFiles, maxFiles)

	// Every accepted name goes through supportedType rather than being read out
	// of the map, so the golden records the acceptance path and not just its
	// table. The name set is the whole map plus the forms that exercise
	// normalization and rejection, which makes it total over the map.
	out.WriteString("\n# accepted files\n")
	extensions := make([]string, 0, len(supportedFileTypes))
	for extension := range supportedFileTypes {
		extensions = append(extensions, extension)
	}
	sort.Strings(extensions)
	names := make([]string, 0, len(extensions)+4)
	for _, extension := range extensions {
		names = append(names, "file"+extension)
	}
	names = append(names,
		"FILE.PNG",         // upper case extension
		"File.Png",         // mixed case extension
		"archive.tar.gz",   // multi-dot name
		"file.unsupported", // unknown extension
		"file",             // no extension
		".png",             // dotfile that looks like an extension
	)
	for _, name := range names {
		extension, fileType, err := supportedType(name)
		if err != nil {
			fmt.Fprintf(&out, "%s\treject\n", name)
			continue
		}
		fmt.Fprintf(&out, "%s\taccept\t%s\t%s\t%d\n", name, extension, fileType.mediaType, fileType.maxBytes)
	}

	return out.String()
}
