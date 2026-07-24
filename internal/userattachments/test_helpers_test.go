package userattachments

import (
	"context"
	"strings"
	"testing"
)

func containsSecret(message, secret string) bool {
	return secret != "" && strings.Contains(message, secret)
}

type scriptedCall struct {
	body   string
	stderr string
	code   int
	check  func(*testing.T, []string)
}

type scriptedRunner struct {
	t     *testing.T
	calls []scriptedCall
	index int
}

func (runner *scriptedRunner) Run(_ context.Context, arguments ...string) (commandResult, error) {
	runner.t.Helper()
	if runner.index >= len(runner.calls) {
		runner.t.Fatalf("unexpected gh call %d: %v", runner.index+1, arguments)
	}
	call := runner.calls[runner.index]
	runner.index++
	if call.check != nil {
		call.check(runner.t, arguments)
	}
	return commandResult{
		Stdout: call.body,
		Stderr: call.stderr,
		Code:   call.code,
	}, nil
}

func (runner *scriptedRunner) assertDone() {
	runner.t.Helper()
	if runner.index != len(runner.calls) {
		runner.t.Fatalf("used %d of %d scripted calls", runner.index, len(runner.calls))
	}
}

func requireArgument(t *testing.T, arguments []string, value string) {
	t.Helper()
	for _, argument := range arguments {
		if argument == value {
			return
		}
	}
	t.Fatalf("arguments %v do not contain %q", arguments, value)
}

type memorySessionStore struct {
	value string
	set   bool
}

func (store *memorySessionStore) Get() (string, error) {
	if !store.set {
		return "", errSessionNotFound
	}
	return store.value, nil
}

type toolStateSessionReader struct {
	ts *toolState
}

func (r toolStateSessionReader) Get() (string, error) {
	return r.ts.getSession()
}
