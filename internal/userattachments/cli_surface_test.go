package userattachments

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The golden files under testdata/cli hold the CLI surface. AGENTS.md keys the
// Release contract on this directory, so "did this change the CLI surface" is
// answered by whether the diff touches these files rather than by reading the
// change. Three rules keep that answer trustworthy, and a section that cannot
// follow them is not a section:
//
//   - Produced by calling the implementation. Transcribing a map, a constant, or
//     hand-written text records the declaration instead of the behavior, and a
//     later change to the behavior then leaves the golden untouched.
//   - Total over its input domain, or labelled "(representative)" in its
//     heading. Finite domains are walked whole, including values no constant
//     names today. Unbounded domains cannot be, so they say so.
//   - Reachable only through a registered section, so a file cannot outlive the
//     section that produced it.
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

// enumWalkBound spans every value a one-byte enum holds. It is a literal rather
// than a bound derived from the enum types so that widening a type cannot turn
// the walk into a hang; the recorded domain bound catches the widening instead.
const enumWalkBound = 255

// fileCountLadder spans the --file cardinalities recorded in the golden. The
// domain is unbounded so the ladder is representative, and its span is a
// literal rather than maxFiles+1: deriving it from the constant under test
// would let the accepted set and the recorded span move together and leave the
// rows identical.
const fileCountLadder = 16

// errSurface stands in for any failure when driving the exit-code mapping. Only
// its presence matters to exitCodeFor, never its text.
var errSurface = errors.New("surface")

func cliSurfaceSections() map[string]func(*testing.T) string {
	return map[string]func(*testing.T) string{
		"top-level-help.golden": func(t *testing.T) string { return runForSurface(t, "--help") },
		"upload-help.golden":    func(t *testing.T) string { return runForSurface(t, "upload", "--help") },
		"auth-help.golden":      func(t *testing.T) string { return runForSurface(t, "auth", "--help") },
		"invocation.golden":     renderInvocations,
		"upload-output.golden":  renderUploadOutput,
		"entrypoint.golden":     renderEntryPoint,
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

// environmentLookups are the standard-library entry points through which this
// package reaches the environment. A golden can only record the names a caller
// may set if every one of these calls is either replaceable by a test or passed
// to something that is.
var environmentLookups = []string{
	"os.Getenv", "os.LookupEnv", "os.Environ", "os.ExpandEnv",
	"os.UserConfigDir", "os.UserHomeDir",
	"syscall.Getenv", "syscall.Environ",
}

// environmentLookupExceptions are the call sites that reach the environment
// without a package-level seam, each because the value is injected or the name
// is already recorded elsewhere.
var environmentLookupExceptions = map[string]string{
	"auth.go:getenv:        os.Getenv,":                                           "injected into authService, which tests replace",
	"auth.go:if strings.TrimSpace(os.Getenv(githubSessionEnvironment)) != \"\" {": "reads the name resolveNativeSession already records",
	"native_session.go:session, err := resolveNativeSession(os.Getenv, store)":    "injected into resolveNativeSession, which the golden drives with a recorder",
}

// TestEnvironmentReadsStayRecordable keeps the recorded environment sections
// complete by construction. Three review rounds found the same defect in
// different paths: a lookup performed directly on the standard library cannot
// be observed, so a name a caller may set never reaches a golden and the
// diff-based Skill trigger misses it. Rather than wait to notice the next path,
// this fails when one appears.
func TestEnvironmentReadsStayRecordable(t *testing.T) {
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list package files: %v", err)
	}
	seam := regexp.MustCompile(`^var (\w+) = (os|syscall)\.(Getenv|LookupEnv|Environ|ExpandEnv|UserConfigDir|UserHomeDir)$`)
	declared := map[string]string{}
	for _, entry := range entries {
		if strings.HasSuffix(entry, "_test.go") {
			continue
		}
		content, err := os.ReadFile(entry)
		if err != nil {
			t.Fatalf("read %s: %v", entry, err)
		}
		for _, line := range strings.Split(string(content), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || !containsAny(trimmed, environmentLookups) {
				continue
			}
			if match := seam.FindStringSubmatch(trimmed); match != nil {
				declared[match[1]] = entry
				continue
			}
			if _, allowed := environmentLookupExceptions[entry+":"+trimmed]; allowed {
				continue
			}
			t.Errorf("%s reads the environment outside a recordable seam:\n\t%s\n"+
				"declare a package-level seam so contract.golden can record it, "+
				"or add the call site to environmentLookupExceptions with its reason", entry, trimmed)
		}
	}

	// A seam that no section replaces records nothing, so declaring one is not
	// enough: the environment it reads still bypasses every golden. Rendering the
	// section that owns them reports which seams it actually drove.
	environmentSeamsMutex.Lock()
	observedEnvironmentSeams = map[string]bool{}
	environmentSeamsMutex.Unlock()
	_ = renderContract(t)
	environmentSeamsMutex.Lock()
	observed := maps.Clone(observedEnvironmentSeams)
	environmentSeamsMutex.Unlock()
	for name, file := range declared {
		if !observed[name] {
			t.Errorf("%s declares the environment seam %s, but no registered section replaces it;\n"+
				"drive it from a renderer so the names it reads reach a golden", file, name)
		}
	}
}

// observedEnvironmentSeams records which environment seams a render run
// replaced. noteEnvironmentSeam is called at each replacement so the guard test
// can compare what the package declares against what a section actually drives.
var (
	environmentSeamsMutex    sync.Mutex
	observedEnvironmentSeams = map[string]bool{}
)

func noteEnvironmentSeam(name string) {
	environmentSeamsMutex.Lock()
	defer environmentSeamsMutex.Unlock()
	observedEnvironmentSeams[name] = true
}

func containsAny(line string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
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

	// auth dispatches only after opening the session store, so the state
	// directory is redirected into the temporary directory normalizePaths
	// rewrites. Which variable moves it is platform-dependent and the resolved
	// base has to exist before the store opens: on darwin os.UserConfigDir
	// ignores XDG_CONFIG_HOME and resolves beneath $HOME/Library/Application
	// Support, which a bare temporary HOME does not contain. Creating whatever
	// the platform resolves keeps these rows identical on every supported host.
	// auth login is deliberately absent from the matrix: it launches a browser.
	t.Setenv("HOME", directory)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(directory, "config"))
	t.Setenv(githubSessionEnvironment, "")
	configBase, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("resolve config directory: %v", err)
	}
	if err := os.MkdirAll(configBase, 0o700); err != nil {
		t.Fatalf("create %s: %v", configBase, err)
	}

	var out strings.Builder
	out.WriteString("# invocations (representative)\n")
	for _, arguments := range [][]string{
		{},
		{"help"},
		{"-h"},
		{"upload"},
		{"upload", "-h"},
		{"auth"},
		{"auth", "-h"},
		{"auth", "help"},
		{"auth", "status"},
		{"auth", "logout"},
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

	// The cardinality domain is unbounded, so this ladder is representative. It
	// runs past the current bound rather than stopping at it, which is what lets
	// an isolated acceptance above the bound show up as a changed row.
	fmt.Fprintf(&out, "\n# --file count (representative, 0..%d)\n", fileCountLadder)
	for count := 0; count <= fileCountLadder; count++ {
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

// renderEntryPoint runs the built binary. Every other section calls run()
// directly, so main.go and the exported Run wrapper are unrecorded: swapping the
// streams they pass, changing which arguments they forward, or dropping the exit
// status would leave every golden unchanged. The argv here is representative and
// limited to forms that return before any gh call, so the section stays offline.
func renderEntryPoint(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "gh-user-attachments")
	build := exec.Command("go", "build", "-o", binary, "../..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, output)
	}

	var out strings.Builder
	out.WriteString("# entry point (representative: argv that returns before any gh call)\n")
	for _, arguments := range [][]string{
		{"--help"},
		{"--version"},
		{"upload"},
		{"bogus"},
	} {
		command := exec.Command(binary, arguments...)
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		err := command.Run()
		code := 0
		var exit *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exit):
			code = exit.ExitCode()
		default:
			t.Fatalf("run %v: %v", arguments, err)
		}
		fmt.Fprintf(&out, "\n$ gh-user-attachments %s\n", strings.Join(arguments, " "))
		fmt.Fprintf(&out, "exit %d\n", code)
		writeStream(&out, "stdout", stdout.String())
		writeStream(&out, "stderr", stderr.String())
	}
	return out.String()
}

// renderUploadOutput drives run() past local validation with a scripted
// uploader so the success and partial-failure output reaches a golden. No other
// section records it: every row in the invocation matrix stops at validation and
// renderContract calls exitCodeFor directly, so moving completed URLs to stderr,
// changing their delimiter or order, or dropping the exit 4 note would leave
// every registered file unchanged. The uploader outcomes are representative.
func renderUploadOutput(t *testing.T) string {
	t.Helper()
	previous := newRunBatchUpload
	t.Cleanup(func() { newRunBatchUpload = previous })

	paths := batchTestPaths(t)
	directory := filepath.Dir(paths[0])
	firstURL := canonicalAssetURL(0x44)
	secondURL := canonicalAssetURL(0x55)

	var out strings.Builder
	out.WriteString("# upload output (representative)\n")
	for _, scenario := range []struct {
		name     string
		outcomes []fileUploadResult
	}{
		{"both files finalize", []fileUploadResult{
			{URL: firstURL, RemoteState: remoteChanged},
			{URL: secondURL, RemoteState: remoteChanged},
		}},
		{"second file fails after remote change", []fileUploadResult{
			{URL: firstURL, RemoteState: remoteChanged},
			{RemoteState: remoteChanged, FailedPhase: phaseObjectUpload, Err: errSurface},
		}},
		{"first file fails before remote change", []fileUploadResult{
			{FailedPhase: phasePreparation, Err: errSurface},
		}},
	} {
		outcomes := scenario.outcomes
		newRunBatchUpload = func(runner commandRunner, _ ...batchOption) batchUpload {
			return newBatchUpload(runner, withUploader(sequenceUploader(outcomes)))
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
		fmt.Fprintf(&out, "\n## %s\nexit %d\n", scenario.name, code)
		writeStream(&out, "stdout", normalizePaths(stdout.String(), directory))
		writeStream(&out, "stderr", normalizePaths(stderr.String(), directory))
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

	// The enum domain bound is recorded rather than used as the loop bound.
	// Deriving the bound from the type would turn a widening to uint16 into
	// 17 billion exitCodeFor calls before assertGolden could report anything, so
	// the promised guard would hang mise run check instead of failing it. Here a
	// widening moves these two rows and fails in a second, and the walk itself
	// stays at the fixed span below.
	fmt.Fprintf(&out, "\n# enum domain bound (the walk below is total while both are %d)\n", enumWalkBound)
	fmt.Fprintf(&out, "remoteStateCertainty\t%d\nfailurePhase\t%d\n", uint64(^remoteStateCertainty(0)), uint64(^failurePhase(0)))

	// exitCodeFor branches on four inputs: two booleans and two unsigned enums.
	// The walk covers every value both enums hold at their current width and
	// emits a row wherever the result changes, so the table is total over that
	// span rather than over the constants named today.
	fmt.Fprintf(&out, "\n# exit codes (total over remote 0..%d, phase 0..%d)\n", enumWalkBound, enumWalkBound)
	out.WriteString("err\turls\tremote>=\tphase>=\texit\n")
	for _, failed := range []bool{false, true} {
		for _, finalized := range []bool{false, true} {
			last := -1
			for state := 0; state <= enumWalkBound; state++ {
				for phase := 0; phase <= enumWalkBound; phase++ {
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

	// The accepted --file cardinalities are recorded in invocation.golden, where
	// parseOptions decides each row. Serializing minFiles and maxFiles here would
	// record the declarations instead, and would move this file when a constant
	// that validation no longer consults is edited.

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

	// Chrome resolution reads names resolveNativeSession never touches. The
	// override is recorded through behavior, by asking whether findChrome returns
	// the sentinel it was given, and the remaining names through a recording
	// lookup with no override set, which is the only run that reaches the
	// fallback.
	out.WriteString("\n# environment read during Chrome resolution\n")
	sentinel := filepath.Join(t.TempDir(), "chrome")
	t.Setenv(chromePathEnvironment, sentinel)
	resolved, err := findChrome()
	fmt.Fprintf(&out, "override honored\t%t\n", err == nil && resolved == sentinel)

	previousGetenv := chromeGetenv
	previousHome := chromeUserHomeDir
	t.Cleanup(func() {
		chromeGetenv = previousGetenv
		chromeUserHomeDir = previousHome
	})
	var chromeRequested []string
	noteEnvironmentSeam("chromeGetenv")
	chromeGetenv = func(name string) string {
		chromeRequested = append(chromeRequested, name)
		return ""
	}
	homeConsulted := false
	noteEnvironmentSeam("chromeUserHomeDir")
	chromeUserHomeDir = func() (string, error) {
		homeConsulted = true
		return "", nil
	}
	_, _ = findChrome()
	chromeGetenv = previousGetenv
	chromeUserHomeDir = previousHome
	sort.Strings(chromeRequested)
	for _, name := range slices.Compact(chromeRequested) {
		fmt.Fprintf(&out, "%s\n", name)
	}
	fmt.Fprintf(&out, "home directory consulted\t%t\n", homeConsulted)

	// Tool state resolution reaches the environment through the configuration
	// directory, whose variable names the standard library owns and which differ
	// per platform. What is recorded is that the dependency exists.
	previousConfigDir := toolStateUserConfigDir
	t.Cleanup(func() { toolStateUserConfigDir = previousConfigDir })
	configConsulted := false
	noteEnvironmentSeam("toolStateUserConfigDir")
	toolStateUserConfigDir = func() (string, error) {
		configConsulted = true
		return "", errSurface
	}
	store, storeErr := defaultSessionStore()
	if storeErr == nil {
		_, _ = store.Get()
	}
	toolStateUserConfigDir = previousConfigDir
	out.WriteString("\n# environment read during tool state resolution\n")
	fmt.Fprintf(&out, "configuration directory consulted\t%t\n", configConsulted)

	// The fallback also depends on the home directory and on PATH, whose
	// variable names the standard library owns rather than this package. What
	// this package decides is which of them each platform's candidates need, and
	// that is recorded by asking chromeCandidates directly: a candidate carrying
	// the sentinel home needs the home directory, and one that is not an absolute
	// path is resolved through PATH. The run above takes the host platform's
	// branch only, so every branch is walked here instead.
	out.WriteString("\n# candidate resolution per platform (total over the branches chromeCandidates names)\n")
	previousLookPath := lookPath
	t.Cleanup(func() { lookPath = previousLookPath })
	for _, platform := range []string{"darwin", "linux", "windows"} {
		// The sentinel is absolute so that a candidate built from it stays
		// absolute, which is what separates a home-relative candidate from one
		// selection has to resolve through PATH.
		const homeSentinel = "/home-sentinel"
		candidates := chromeCandidates(platform, homeSentinel)
		usesHome := false
		for _, candidate := range candidates {
			if strings.Contains(candidate, homeSentinel) {
				usesHome = true
			}
		}
		// Whether PATH is read is recorded by running the selection with a
		// recording lookPath rather than by inspecting the candidate strings: a
		// selection that stopped consulting PATH would leave an inferred row
		// unchanged.
		usesPath := false
		noteEnvironmentSeam("lookPath")
		lookPath = func(string) (string, error) {
			usesPath = true
			return "", errSurface
		}
		_, _ = selectChrome(candidates)
		lookPath = previousLookPath
		fmt.Fprintf(&out, "%s\thome=%t\tPATH=%t\n", platform, usesHome, usesPath)
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

	out.WriteString(renderSupportedTypes(t))
	return out.String()
}

// sizeSearchCeiling bounds the bisection that finds each extension's admitted
// size. It is a literal above every documented limit rather than a value read
// from the size table, so lowering a limit moves the discovered cutoff instead
// of moving the search with it.
const sizeSearchCeiling = 128 << 20

// renderSupportedTypes walks every extension through loadAsset and records what
// the validator does with it. The candidate domain is the union of the set
// GitHub documents and the set the implementation advertises, so dropping an
// extension flips its row to a rejection instead of removing the row, and
// adding one appends a row. The extension domain is every string, so this is
// not total over it: an extension reached only through future normalization
// would not appear.
//
// The admitted size is discovered by bisection through loadAsset rather than
// probed at the documented limits. Fixed probes at the limits cannot see a
// limit that moves between two of them; a discovered cutoff moves whenever the
// limit does.
func renderSupportedTypes(t *testing.T) string {
	t.Helper()
	candidates := make(map[string]struct{}, len(documentedFileTypes)+len(supportedFileTypes))
	for extension := range documentedFileTypes {
		candidates[extension] = struct{}{}
	}
	for extension := range supportedFileTypes {
		candidates[extension] = struct{}{}
	}
	extensions := make([]string, 0, len(candidates))
	for extension := range candidates {
		extensions = append(extensions, extension)
	}
	sort.Strings(extensions)

	var out strings.Builder
	out.WriteString("\n# extensions (representative: the documented and advertised sets)\n")
	out.WriteString("extension\tmedia type\tlargest admitted bytes\n")
	var known []int64
	for _, extension := range extensions {
		directory := t.TempDir()
		path := filepath.Join(directory, "attachment"+extension)
		writeSupportedTestFile(t, path)

		asset, err := loadAsset(path)
		if err != nil {
			fmt.Fprintf(&out, "%s\treject\t%s\n", extension, normalizePaths(err.Error(), directory))
			continue
		}
		limit, ok := discoverSizeLimit(t, path, asset.Size, known)
		if !ok {
			fmt.Fprintf(&out, "%s\t%s\tundetermined\n", extension, asset.MediaType)
			continue
		}
		if !slices.Contains(known, limit) {
			known = append(known, limit)
		}
		fmt.Fprintf(&out, "%s\t%s\t%d\n", extension, asset.MediaType, limit)
	}
	return out.String()
}

// discoverSizeLimit returns the largest byte count loadAsset admits for path.
// Limits already seen are tried first because extensions share them, and the
// bisection runs only when none of them fits. It reports false when the file is
// rejected one byte past the found cutoff for a reason other than its size,
// which would make the cutoff meaningless.
func discoverSizeLimit(t *testing.T, path string, admitted int64, known []int64) (int64, bool) {
	t.Helper()
	for _, candidate := range known {
		if candidate >= admitted && admitsSize(t, path, candidate) && !admitsSize(t, path, candidate+1) {
			return candidate, rejectedForSize(t, path, candidate+1)
		}
	}
	low, high := admitted, int64(sizeSearchCeiling)
	if admitsSize(t, path, high) {
		t.Fatalf("%s admits %d bytes; raise sizeSearchCeiling", path, high)
	}
	for high-low > 1 {
		middle := low + (high-low)/2
		if admitsSize(t, path, middle) {
			low = middle
			continue
		}
		high = middle
	}
	return low, rejectedForSize(t, path, low+1)
}

func admitsSize(t *testing.T, path string, size int64) bool {
	t.Helper()
	if err := os.Truncate(path, size); err != nil {
		t.Fatalf("truncate %s: %v", path, err)
	}
	_, err := loadAsset(path)
	return err == nil
}

// rejectedForSize reports whether loadAsset rejects the file at this size for
// exceeding the limit rather than for its content, which trailing zero bytes
// could invalidate for some containers.
func rejectedForSize(t *testing.T, path string, size int64) bool {
	t.Helper()
	if err := os.Truncate(path, size); err != nil {
		t.Fatalf("truncate %s: %v", path, err)
	}
	_, err := loadAsset(path)
	return err != nil && strings.Contains(err.Error(), "maximum is")
}

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
