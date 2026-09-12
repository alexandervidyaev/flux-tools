package kubeconform

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	fluxexec "github.com/alexandervidyaev/flux-tools/pkg/exec"
)

// runner is the package-level command runner, replaceable for testing.
var runner fluxexec.CommandRunner = &fluxexec.RealRunner{}

// ValidateFile validates a single manifest file using kubeconform binary
// It passes through all additional arguments to kubeconform
func ValidateFile(ctx context.Context, filePath string, args []string) *ValidationResult {
	start := time.Now()

	result := &ValidationResult{
		File: filepath.Base(filePath),
	}

	if err := runner.LookPath("kubeconform"); err != nil {
		result.Err = fmt.Errorf("kubeconform binary not found in PATH: %w", err)
		result.ExitCode = -1
		result.Duration = time.Since(start)
		return result
	}

	cmdArgs := append(args, filePath)
	output, err := runner.Run(ctx, "kubeconform", cmdArgs...)
	result.Output = string(output)
	result.Duration = time.Since(start)

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.Err = fmt.Errorf("failed to execute kubeconform: %w", err)
			result.ExitCode = -1
			return result
		}
	} else {
		result.ExitCode = 0
	}

	return result
}
