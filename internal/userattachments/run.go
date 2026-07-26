package userattachments

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

const (
	exitOK      = 0
	exitUsage   = 2
	exitAPI     = 3
	exitPartial = 4
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// developmentVersion marks a build the release pipeline did not stamp. It is a
// constant string expression so -ldflags -X keeps working on Version.
const developmentVersion = "dev"

// Version is replaced by the release build through -ldflags.
var Version = developmentVersion

// readBuildInfo is a seam. Tests cannot control how the test binary itself was
// built, so they replace this instead of depending on the surrounding checkout.
var readBuildInfo = debug.ReadBuildInfo

// resolveVersion reports the most specific identifier available for this build.
// The release pipeline injects the tag, so that value always wins. Every other
// build path leaves developmentVersion behind, which identifies nothing; there
// the main module version the toolchain stamps from version control is used
// instead. Go 1.24 and later derive it from the tag or commit and append
// "+dirty" for uncommitted changes, so it is traceable on its own.
func resolveVersion() string {
	if Version != developmentVersion {
		return Version
	}
	info, ok := readBuildInfo()
	if !ok {
		return developmentVersion
	}
	// "(devel)" is what the toolchain reports when it has no version control
	// data to stamp, which carries no more information than the default.
	if version := info.Main.Version; version != "" && version != "(devel)" {
		return version
	}
	return developmentVersion
}

// newRunBatchUpload builds the upload batch used by run(). Tests may replace
// this to inject prepare/uploader seams without changing production behavior.
var newRunBatchUpload = newBatchUpload

type options struct {
	repository string
	files      []string
}

func Run(arguments []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if timeout := commandTimeout(arguments); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return run(ctx, arguments, stdout, stderr, runGH)
}

func commandTimeout(arguments []string) time.Duration {
	if len(arguments) >= 2 && arguments[0] == "auth" && arguments[1] == "login" {
		// Interactive sign-in must still terminate if the browser flow is abandoned.
		return 10 * time.Minute
	}
	return 0
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer, runner commandRunner) int {
	if len(arguments) == 0 || arguments[0] == "help" || arguments[0] == "--help" || arguments[0] == "-h" {
		printTopLevelUsage(stderr)
		return exitOK
	}
	if len(arguments) == 1 && arguments[0] == "--version" {
		fmt.Fprintf(stdout, "gh-user-attachments %s\n", resolveVersion())
		return exitOK
	}
	if arguments[0] == "auth" {
		return handleAuth(ctx, arguments[1:], stderr, runner)
	}
	if arguments[0] != "upload" {
		fmt.Fprintf(stderr, "gh-user-attachments: unknown command %q\n", arguments[0])
		printTopLevelUsage(stderr)
		return exitUsage
	}
	args, err := parseOptions(arguments[1:], stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		fmt.Fprintf(stderr, "gh-user-attachments: %s\n", safeCommandMessage(err.Error()))
		return exitUsage
	}
	outcome := newRunBatchUpload(runner).execute(ctx, args.repository, args.files)
	for _, url := range outcome.FinalizedURLs {
		fmt.Fprintln(stdout, url)
	}
	if outcome.Err != nil {
		code := exitCodeFor(outcome)
		fmt.Fprintf(stderr, "gh-user-attachments: %s\n", safeCommandMessage(outcome.Err.Error()))
		if code == exitPartial {
			fmt.Fprintln(stderr, "gh-user-attachments: stdout contains only completed result URLs; GitHub may also have created incomplete remote state")
		}
		return code
	}
	return exitOK
}

func parseOptions(arguments []string, stderr io.Writer) (options, error) {
	var value options
	flags := flag.NewFlagSet("gh-user-attachments upload", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&value.repository, "repo", "", "target repository in OWNER/REPO form (required)")
	flags.Func("file", "local attachment file path (required; repeat 2 to 10 times)", func(path string) error {
		value.files = append(value.files, path)
		return nil
	})
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: gh-user-attachments upload --repo OWNER/REPO --file PATH --file PATH [--file PATH ...]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(arguments); err != nil {
		return value, err
	}
	if flags.NArg() != 0 {
		return value, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if !repositoryPattern.MatchString(value.repository) {
		return value, fmt.Errorf("--repo must use OWNER/REPO form")
	}
	if len(value.files) < minFiles || len(value.files) > maxFiles {
		return value, fmt.Errorf("--file must be specified between %d and %d times", minFiles, maxFiles)
	}
	for _, file := range value.files {
		if strings.TrimSpace(file) == "" {
			return value, fmt.Errorf("--file paths must not be empty")
		}
	}
	return value, nil
}

func printTopLevelUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Upload multiple files as native GitHub user attachments.")
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  gh-user-attachments upload --repo OWNER/REPO --file PATH --file PATH [--file PATH ...]")
	fmt.Fprintln(stderr, "  gh-user-attachments auth <login|logout|status>")
}
