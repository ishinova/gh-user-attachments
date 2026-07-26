package userattachments

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The golden files under testdata/cli hold the CLI surface. AGENTS.md keys the
// Release contract on this directory, so "did this change the CLI surface" is
// answered by whether the diff touches these files rather than by reading the
// change. Two rules keep that answer trustworthy, and a section that cannot
// follow them is not a section:
//
//   - Produced by calling the implementation. Transcribing a map, a constant, or
//     hand-written text records the declaration instead of the behavior, and a
//     later change to the behavior then leaves the golden untouched.
//   - Total over its input domain, or labelled "(representative)" in its
//     heading. Finite domains are walked whole, including values no constant
//     names today. Unbounded domains cannot be, so they say so.
//
// The surface is what a caller can observe: accepted argv, exit codes, stdout,
// stderr, environment variables read, and which local files are admitted.
// Observing something of a kind not on that list means adding a section.
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

func cliSurfaceSections() map[string]func(*testing.T) string {
	return map[string]func(*testing.T) string{
		"top-level-help.golden": func(t *testing.T) string { return runForSurface(t, "--help") },
		"upload-help.golden":    func(t *testing.T) string { return runForSurface(t, "upload", "--help") },
		"auth-help.golden":      func(t *testing.T) string { return runForSurface(t, "auth", "--help") },
		"invocation.golden":     renderInvocations,
		"contract.golden":       renderContract,
		"files.golden":          renderFileAdmission,
	}
}

func TestCLISurfaceMatchesGoldenFiles(t *testing.T) {
	for name, render := range cliSurfaceSections() {
		t.Run(name, func(t *testing.T) {
			assertGolden(t, name, render(t))
		})
	}
}

// TestCLISurfaceHasNoOrphanGoldenFiles fails when a golden outlives the section
// that produced it. Without this the registry could drop an invocation, leave
// its file in place, and report no surface change for a removed invocation.
func TestCLISurfaceHasNoOrphanGoldenFiles(t *testing.T) {
	entries, err := os.ReadDir(cliSurfaceDir)
	if err != nil {
		t.Fatalf("read %s: %v", cliSurfaceDir, err)
	}
	sections := cliSurfaceSections()
	for _, entry := range entries {
		if _, registered := sections[entry.Name()]; registered {
			continue
		}
		path := filepath.Join(cliSurfaceDir, entry.Name())
		if *updateCLISurface {
			if err := os.Remove(path); err != nil {
				t.Fatalf("remove orphan %s: %v", path, err)
			}
			continue
		}
		t.Errorf("%s has no section that produces it", path)
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

// renderInvocations drives argv forms that a caller can reach without any remote
// call, recording the exit code and the diagnostic. Help output has its own
// sections, so this one covers routing, option parsing, and the local validation
// that decides exit 2, including which --repo values are accepted. The argv
// domain is unbounded, so the matrix is representative.
func renderInvocations(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	first := filepath.Join(directory, "first.png")
	second := filepath.Join(directory, "second.png")
	writePNG(t, first)
	writePNG(t, second)

	var out strings.Builder
	out.WriteString("# invocations (representative)\n")
	for _, arguments := range [][]string{
		{},
		{"upload"},
		{"auth"},
		{"auth", "status", "extra"},
		{"auth", "unknown"},
		{"unknown"},
		{"--version", "extra"},
		{"upload", "--file", first, "--file", second},
		{"upload", "--repo", "owner", "--file", first, "--file", second},
		{"upload", "--repo", "owner/repo/extra", "--file", first, "--file", second},
		{"upload", "--repo", "owner/re po", "--file", first, "--file", second},
		{"upload", "--repo", "owner/repo?x", "--file", first, "--file", second},
		{"upload", "--repo", "/repo", "--file", first, "--file", second},
		{"upload", "--repo", "owner/", "--file", first, "--file", second},
		{"upload", "--repo", "OWNER/repo.name-1_2", "--file", first, "--file", second, "--file", first},
		{"upload", "--repo", "owner/repo", "--file", first},
		{"upload", "--repo", "owner/repo", "--file", first, "--file", ""},
		{"upload", "--repo", "owner/repo", "--file", first, "--file", second, "trailing"},
		{"upload", "--repo", "owner/repo", "--unknown", "x"},
	} {
		// A runner that records reaching gh, rather than one that aborts the
		// test there. Whether an argv gets past local validation is itself part
		// of the surface, so loosening validation has to produce a line here
		// instead of killing the section before later rows run.
		reachedGH := false
		runner := func(context.Context, ...string) (commandResult, error) {
			reachedGH = true
			return commandResult{}, errSurface
		}
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), arguments, &stdout, &stderr, runner)
		fmt.Fprintf(&out, "\n$ gh-user-attachments %s\n", normalizePaths(strings.Join(arguments, " "), directory))
		fmt.Fprintf(&out, "exit %d\treached gh: %t\n", code, reachedGH)
		writeStream(&out, "stdout", normalizePaths(stdout.String(), directory))
		writeStream(&out, "stderr", normalizePaths(stderr.String(), directory))
	}

	// Driving run() records only what it rejects: an argument that starts being
	// accepted proceeds to the network instead of producing a line here. Option
	// parsing is therefore recorded at parseOptions, which is the gate itself, so
	// loosening and tightening both land in this file.
	out.WriteString("\n# option parsing (representative)\n")
	for _, arguments := range [][]string{
		{"--repo", "owner/repo", "--file", "a.png", "--file", "b.png"},
		{"--repo", "OWNER/repo.name-1_2", "--file", "a.png", "--file", "b.png"},
		{"--repo", "owner", "--file", "a.png", "--file", "b.png"},
		{"--repo", "owner/", "--file", "a.png", "--file", "b.png"},
		{"--repo", "/repo", "--file", "a.png", "--file", "b.png"},
		{"--repo", "owner/repo/extra", "--file", "a.png", "--file", "b.png"},
		{"--repo", "owner/re po", "--file", "a.png", "--file", "b.png"},
		{"--repo", "owner/repo?x", "--file", "a.png", "--file", "b.png"},
		{"--repo", "owner/repo#x", "--file", "a.png", "--file", "b.png"},
		{"--repo", "owner/repo", "--file", "a.png"},
		{"--repo", "owner/repo", "--file", "a.png", "--file", " "},
		{"--repo", "owner/repo", "--file", "a.png", "--file", "a.png"},
	} {
		var discarded bytes.Buffer
		parsed, err := parseOptions(arguments, &discarded)
		if err != nil {
			fmt.Fprintf(&out, "reject\t%s\t%s\n", strings.Join(arguments, " "), err)
			continue
		}
		fmt.Fprintf(&out, "accept\t%s\trepo=%s files=%d\n", strings.Join(arguments, " "), parsed.repository, len(parsed.files))
	}

	// The upper bound is finite and cheap to walk, so the file count is total
	// rather than sampled at its edges.
	out.WriteString("\n# --file count (total over 0..maxFiles+1)\n")
	for count := 0; count <= maxFiles+1; count++ {
		arguments := []string{"--repo", "owner/repo"}
		for index := 0; index < count; index++ {
			arguments = append(arguments, "--file", fmt.Sprintf("%d.png", index))
		}
		var discarded bytes.Buffer
		verdict := "accept"
		if _, err := parseOptions(arguments, &discarded); err != nil {
			verdict = "reject"
		}
		fmt.Fprintf(&out, "%d\t%s\n", count, verdict)
	}
	return out.String()
}

// normalizePaths keeps machine-specific temporary directories out of a golden.
func normalizePaths(content, directory string) string {
	return strings.ReplaceAll(content, directory, "<dir>")
}

// renderContract records the parts of the surface that no single invocation
// shows on its own.
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

	// exitCodeFor branches on four inputs whose domains are finite: two booleans
	// and two unsigned enums. The walk covers every value of all four and emits a
	// row wherever the result changes, so the table is total over the domain
	// rather than over the constants named today. The domain bound is recorded
	// too: widening either enum type moves this file and forces the walk to be
	// reconsidered rather than silently leaving values uncovered.
	stateBound := int(^remoteStateCertainty(0))
	phaseBound := int(^failurePhase(0))
	fmt.Fprintf(&out, "\n# exit codes (total over remote 0..%d, phase 0..%d)\n", stateBound, phaseBound)
	out.WriteString("err\turls\tremote>=\tphase>=\texit\n")
	for _, failed := range []bool{false, true} {
		for _, finalized := range []bool{false, true} {
			last := -1
			for state := 0; state <= stateBound; state++ {
				for phase := 0; phase <= phaseBound; phase++ {
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
					if code == last {
						continue
					}
					last = code
					fmt.Fprintf(&out, "%t\t%t\t%d\t%d\t%d\n", failed, finalized, state, phase, code)
				}
			}
		}
	}

	// The result URL domain is every string, so this matrix is representative.
	// It records what validateNativeAssetURL accepts today, including raw forms
	// that decode to the canonical shape; hardening any of them moves this file.
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

	// The environment names come from a recording lookup rather than from the
	// constants, so renaming one moves this file. Session resolution is the path
	// a caller's agent depends on; SKILL.md names the variable in its guardrails.
	out.WriteString("\n# environment read during session resolution\n")
	var requested []string
	recording := func(name string) string {
		requested = append(requested, name)
		return ""
	}
	_, _ = resolveNativeSession(recording, absentSessionStore{})
	sort.Strings(requested)
	requested = slices.Compact(requested)
	for _, name := range requested {
		fmt.Fprintf(&out, "%s\n", name)
	}

	return out.String()
}

// absentSessionStore reports no stored session so resolveNativeSession takes the
// environment branch and then the not-found branch, without touching the disk.
type absentSessionStore struct{}

func (absentSessionStore) Get() (string, error) { return "", errSessionNotFound }

// renderFileAdmission drives real files through the admission path a caller
// reaches, not through the extension table alone. loadAssets decides duplicates,
// symlinks, extensions, and size; materializeAsset decides whether the content
// matches the extension. The domain is every file, so the set is representative.
func renderFileAdmission(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()

	valid := filepath.Join(directory, "valid.png")
	writePNG(t, valid)
	mismatched := filepath.Join(directory, "mismatched.png")
	writeFile(t, mismatched, []byte("not a png"))
	text := filepath.Join(directory, "notes.txt")
	writeFile(t, text, []byte("plain text\n"))
	unsupported := filepath.Join(directory, "payload.bin")
	writeFile(t, unsupported, []byte("binary"))
	oversize := filepath.Join(directory, "oversize.png")
	writeFile(t, oversize, make([]byte, maxImageFileBytes+1))
	empty := filepath.Join(directory, "empty.png")
	writeFile(t, empty, nil)
	uppercase := filepath.Join(directory, "UPPER.PNG")
	writePNG(t, uppercase)

	link := filepath.Join(directory, "link.png")
	if err := os.Symlink(valid, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	hardlink := filepath.Join(directory, "hardlink.png")
	if err := os.Link(valid, hardlink); err != nil {
		t.Fatalf("hardlink: %v", err)
	}
	missing := filepath.Join(directory, "missing.png")

	var out strings.Builder
	out.WriteString("# file admission (representative)\n")
	for _, group := range [][]string{
		{valid, text},
		{valid, uppercase},
		{valid, mismatched},
		{valid, unsupported},
		{valid, oversize},
		{valid, empty},
		{valid, link},
		{valid, hardlink},
		{valid, missing},
		{valid, valid},
		{valid, directory},
	} {
		fmt.Fprintf(&out, "\n%s\n", normalizePaths(strings.Join(group, " "), directory))
		assets, err := loadAssets(group)
		if err != nil {
			fmt.Fprintf(&out, "load\treject\t%s\n", normalizePaths(err.Error(), directory))
			continue
		}
		out.WriteString("load\taccept\n")
		for _, asset := range assets {
			loaded, err := materializeAsset(asset)
			if err != nil {
				fmt.Fprintf(&out, "materialize\treject\t%s\n", normalizePaths(err.Error(), directory))
				continue
			}
			fmt.Fprintf(&out, "materialize\taccept\t%s\t%s\t%d\n", loaded.Name, loaded.MediaType, len(loaded.Content))
		}
	}
	return out.String()
}

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
