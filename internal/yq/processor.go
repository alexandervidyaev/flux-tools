package yq

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	fluxexec "github.com/alexandervidyaev/flux-tools/pkg/exec"
)

// runner is the package-level command runner, replaceable for testing.
var runner fluxexec.CommandRunner = &fluxexec.RealRunner{}

// ProcessFile processes a single YAML file with yq using the provided filter
// Output is saved to outputDir/filename.yaml
func ProcessFile(ctx context.Context, filePath string, filter string, outputDir string) *ProcessResult {
	result := &ProcessResult{}
	result.InputPath = filePath

	fileName := filepath.Base(filePath)
	outputPath := filepath.Join(outputDir, fileName)
	result.OutputPath = outputPath

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		result.Success = false
		result.Error = fmt.Errorf("failed to create output directory: %w", err)
		return result
	}

	if err := runner.LookPath("yq"); err != nil {
		result.Success = false
		result.Error = fmt.Errorf("yq binary not found in PATH")
		return result
	}

	output, err := runner.Run(ctx, "yq", "eval-all", filter, filePath)
	if err != nil {
		result.Success = false
		result.Error = fmt.Errorf("yq command failed: %w", err)
		return result
	}

	if err := os.WriteFile(outputPath, output, 0644); err != nil {
		result.Success = false
		result.Error = fmt.Errorf("failed to write output file: %w", err)
		return result
	}

	result.Success = true
	return result
}
