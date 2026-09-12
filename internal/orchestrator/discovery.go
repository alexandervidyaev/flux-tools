package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
)

// DiscoverClustersRecursive finds every cluster below rootPath at any depth.
// A cluster is a leaf: its own subdirectories are not searched.
func DiscoverClustersRecursive(rootPath string, markers Markers) ([]string, error) {
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var allClusters []string
	for _, entry := range entries {
		if !entry.IsDir() || isIgnoredDir(entry.Name()) {
			continue
		}

		subPath := filepath.Join(rootPath, entry.Name())
		if markers.IsCluster(subPath) {
			allClusters = append(allClusters, subPath)
			continue
		}

		nested, err := DiscoverClustersRecursive(subPath, markers)
		if err != nil {
			// Don't abort; continue with other directories
			continue
		}
		allClusters = append(allClusters, nested...)
	}

	return allClusters, nil
}
