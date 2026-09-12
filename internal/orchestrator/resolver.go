package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexandervidyaev/flux-tools/pkg/fsutil"
)

// FluxSystemMarker is the directory `flux bootstrap` leaves in a cluster entry.
const FluxSystemMarker = "flux-system"

// Markers are the names whose presence in a directory marks it as a cluster.
// FluxSystemMarker is always one of them. A flux-operator layout keeps the sync
// source in the cluster instead of in git and therefore has no flux-system/;
// --cluster-marker adds whatever name such a repository uses in its place.
type Markers []string

// DefaultMarkers is the set every run starts from.
func DefaultMarkers() Markers {
	return Markers{FluxSystemMarker}
}

// With adds a marker name to the set. An empty name adds nothing.
func (m Markers) With(name string) Markers {
	if name == "" {
		return m
	}
	return append(append(Markers{}, m...), name)
}

// IsCluster reports whether the directory carries one of the markers. A marker
// counts whether it is a file or a directory, so a repository is free to use an
// empty sentinel file.
func (m Markers) IsCluster(path string) bool {
	for _, name := range m {
		if _, err := os.Stat(filepath.Join(path, name)); err == nil {
			return true
		}
	}
	return false
}

// ResolveInput turns one positional path into the cluster directories to build:
//
//  1. the path carries a marker           -> it is one cluster
//  2. directories below it carry markers  -> that set of clusters
//  3. no marker anywhere below it         -> the path itself, taken as given
//
// Rule 3 is what lets a flux-operator layout work with no marker at all: there
// is nothing in git that says "this is a cluster", so the caller's word is
// taken. It is refused only when the path plainly holds clusters rather than
// manifests of its own, because rendering such a path as one entry produces the
// wrong result without failing.
func ResolveInput(path string, markers Markers) (Mode, []string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return ModeInvalid, nil, fmt.Errorf("invalid path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return ModeInvalid, nil, fmt.Errorf("path does not exist: %s", absPath)
	}
	if !info.IsDir() {
		return ModeInvalid, nil, fmt.Errorf("path is not a directory: %s", absPath)
	}

	if markers.IsCluster(absPath) {
		return ModeSingle, []string{absPath}, nil
	}

	clusters, err := DiscoverClustersRecursive(absPath, markers)
	if err != nil {
		return ModeInvalid, nil, err
	}
	if len(clusters) > 0 {
		return ModeMulti, clusters, nil
	}

	if looksLikeClusterSet(absPath) {
		return ModeInvalid, nil, fmt.Errorf(
			"%s holds no manifests of its own, only subdirectories that do: this looks like a set of clusters rather than one\n"+
				"List the cluster directories explicitly, or mark each of them with a file or directory and name it with --cluster-marker",
			absPath)
	}

	return ModeSingle, []string{absPath}, nil
}

// looksLikeClusterSet reports whether the directory holds no YAML of its own
// while at least one of its subdirectories does. Such a path is a set of
// clusters carrying no marker: rendering it as one entry names the output after
// the wrong directory, and merges the clusters once there is more than one.
func looksLikeClusterSet(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}

	var subdirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			if !isIgnoredDir(entry.Name()) {
				subdirs = append(subdirs, filepath.Join(path, entry.Name()))
			}
			continue
		}
		// Manifests of its own: an entry, however unusual its shape.
		if fsutil.IsYAMLFile(entry.Name()) {
			return false
		}
	}

	for _, dir := range subdirs {
		if holdsYAML(dir) {
			return true
		}
	}
	return false
}

// holdsYAML reports whether the directory has a YAML file directly in it.
func holdsYAML(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && fsutil.IsYAMLFile(entry.Name()) {
			return true
		}
	}
	return false
}

// isIgnoredDir skips hidden directories (.git, .github, .gitlab, ...) when
// walking for clusters. Nothing else is filtered: a directory is a cluster if
// and only if it carries a marker.
func isIgnoredDir(name string) bool {
	return strings.HasPrefix(name, ".")
}
