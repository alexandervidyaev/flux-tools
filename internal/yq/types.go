// Package yq provides YAML file processing using the yq command-line tool.
package yq

import "github.com/alexandervidyaev/flux-tools/pkg/result"

// ProcessResult represents the result of processing a single file with yq.
type ProcessResult struct {
	result.FileResult
}
