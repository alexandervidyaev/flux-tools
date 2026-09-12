// Package fsutil holds the filesystem helpers shared across the tool: YAML file
// discovery, path resolution and multi-document splitting.
package fsutil

import (
	"path/filepath"
	"strings"
)

// ResolvePath resolves targetPath relative to basePath.
// If targetPath is empty, basePath is returned.
// If targetPath is absolute, it is returned as-is.
func ResolvePath(basePath, targetPath string) string {
	if targetPath == "" {
		return basePath
	}

	if filepath.IsAbs(targetPath) {
		return targetPath
	}

	targetPath = strings.TrimPrefix(targetPath, "./")

	return filepath.Join(basePath, targetPath)
}
