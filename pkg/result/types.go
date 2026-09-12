// Package result provides shared result types for file processing operations.
package result

// FileResult represents the outcome of processing a single file.
type FileResult struct {
	InputPath  string
	OutputPath string
	Success    bool
	Error      error
}
