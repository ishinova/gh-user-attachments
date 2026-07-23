package userattachments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const apiVersion = "2022-11-28"

type commandResult struct {
	Stdout string
	Stderr string
	Code   int
}

type commandRunner func(context.Context, ...string) (commandResult, error)

func runGH(ctx context.Context, arguments ...string) (commandResult, error) {
	command := exec.CommandContext(ctx, "gh", arguments...)
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := commandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.Code = exitError.ExitCode()
		return result, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return result, fmt.Errorf("gh executable was not found")
	}
	return result, err
}

type apiClient struct {
	runner commandRunner
}

func (c apiClient) get(ctx context.Context, endpoint string) ([]byte, error) {
	arguments := []string{
		"api",
		"--hostname", "github.com",
		"--method", "GET",
		"-H", "Accept: application/vnd.github+json",
		"-H", "X-GitHub-Api-Version: " + apiVersion,
		endpoint,
	}

	result, runErr := c.runner(ctx, arguments...)
	if runErr != nil {
		return nil, runErr
	}
	if result.Code != 0 {
		message := safeCommandMessage(result.Stderr)
		if message == "" {
			message = fmt.Sprintf("gh command failed with exit code %d", result.Code)
		}
		return nil, fmt.Errorf("GitHub API: %s", message)
	}
	return []byte(result.Stdout), nil
}

func safeCommandMessage(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.TrimPrefix(value, "gh: ")
	if len(value) > 500 {
		value = value[:500] + "…"
	}
	return value
}

func decodeJSON(response []byte, target any) error {
	if len(strings.TrimSpace(string(response))) == 0 {
		return fmt.Errorf("GitHub returned an empty JSON response")
	}
	if err := json.Unmarshal(response, target); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}
