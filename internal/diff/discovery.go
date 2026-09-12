// Package diff provides cluster manifest comparison and diff generation.
package diff

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/alexandervidyaev/flux-tools/pkg/fsutil"
)

// FindClustersUnion returns the sorted union of cluster directory names from
// the supplied paths. A non-existent path contributes nothing rather than
// erroring out — this lets the diff command correctly enumerate clusters that
// appear only on the incoming side (or only on the current side) instead of
// missing additions or deletions when a layout changes between branches.
func FindClustersUnion(paths ...string) ([]string, error) {
	seen := make(map[string]struct{})
	for _, p := range paths {
		entries, err := os.ReadDir(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				seen[entry.Name()] = struct{}{}
			}
		}
	}

	clusters := make([]string, 0, len(seen))
	for name := range seen {
		clusters = append(clusters, name)
	}
	sort.Strings(clusters)
	return clusters, nil
}

// FindManifests finds all YAML files in the given directory, keyed by their
// path relative to dirPath. A non-existent directory yields an empty map (not
// an error) so the diff command can compare against a side that does not exist.
func FindManifests(dirPath string) (map[string]string, error) {
	manifests := make(map[string]string)

	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return manifests, nil
	}

	files, err := fsutil.FindYAMLFiles(dirPath)
	if err != nil {
		return nil, err
	}

	for _, path := range files {
		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			return nil, err
		}
		manifests[relPath] = path
	}

	return manifests, nil
}
