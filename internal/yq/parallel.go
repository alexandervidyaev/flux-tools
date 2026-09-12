package yq

import (
	"context"

	"github.com/alexandervidyaev/flux-tools/pkg/workerpool"
)

// ProcessFilesParallel processes multiple files in parallel using a worker pool
func ProcessFilesParallel(ctx context.Context, files []string, filter string, outputDir string, workers int) []*ProcessResult {
	return workerpool.Run(ctx, files, workers, func(ctx context.Context, filePath string) *ProcessResult {
		return ProcessFile(ctx, filePath, filter, outputDir)
	})
}
