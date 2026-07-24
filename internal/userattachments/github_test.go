package userattachments

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAPIClientDelegatesAuthenticationAndErrorsToGH(t *testing.T) {
	runner := &scriptedRunner{t: t, calls: []scriptedCall{{
		stderr: "gh: Resource not accessible by integration (HTTP 403)\n",
		code:   1,
		check: func(t *testing.T, arguments []string) {
			requireArgument(t, arguments, "api")
			requireArgument(t, arguments, "--hostname")
			requireArgument(t, arguments, "github.com")
			for _, argument := range arguments {
				if argument == "auth" || argument == "token" || argument == "--include" {
					t.Fatalf("unexpected gh argument: %v", arguments)
				}
			}
		},
	}}}

	_, err := (apiClient{runner: runner.Run}).get(context.Background(), "repos/o/r")
	if err == nil || !strings.Contains(err.Error(), "Resource not accessible") {
		t.Fatalf("error = %v", err)
	}
	runner.assertDone()
}

func TestAPIClientReportsCommandFailureWithoutHTTPOutput(t *testing.T) {
	runner := &scriptedRunner{t: t, calls: []scriptedCall{{code: 17}}}

	_, err := (apiClient{runner: runner.Run}).get(context.Background(), "repos/o/r")
	if err == nil || !strings.Contains(err.Error(), "gh command failed with exit code 17") {
		t.Fatalf("error = %v", err)
	}
	runner.assertDone()
}

func TestExecRunnerPreservesCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runGH(ctx, "--version")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}
