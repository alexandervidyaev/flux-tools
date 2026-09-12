package yq

import "github.com/alexandervidyaev/flux-tools/pkg/fsutil"

// FindManifests recursively finds all YAML files in the given path,
// excluding files containing ".clean." in their name.
func FindManifests(path string) ([]string, error) {
	return fsutil.FindYAMLFiles(path, fsutil.FindOptions{ExcludeClean: true, Recursive: true})
}
