// Package exec runs the external programs the tool shells out to. Every helm,
// kustomize, yq, kubeconform, kubectl-slice and git invocation goes through a
// CommandRunner, which tests replace with a mock.
package exec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// CommandRunner abstracts command execution for testability.
type CommandRunner interface {
	// Run executes a command and returns its combined stdout.
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	// RunWithStdin executes a command with stdin and returns combined stdout.
	RunWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error)
	// LookPath checks whether a binary is available in PATH.
	LookPath(name string) error
}

// RealRunner executes commands via os/exec.
type RealRunner struct {
	Env []string // Additional environment variables.
}

func (r *RealRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return r.RunWithStdin(ctx, nil, name, args...)
}

func (r *RealRunner) RunWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(r.Env) > 0 {
		cmd.Env = append(os.Environ(), r.Env...)
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("%w: %s", err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func (r *RealRunner) LookPath(name string) error {
	_, err := exec.LookPath(name)
	return err
}
