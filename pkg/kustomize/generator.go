package kustomize

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/alexandervidyaev/flux-tools/pkg/fsutil"
)

// GenerateKustomization writes a kustomization.yaml for a directory that has
// none, the way kustomize-controller does before building a Kustomization
// whose spec.path lacks one (fluxcd/pkg kustomize/kustomize_generator.go):
//
//   - every *.yaml / *.yml file under dir is a resource, walked recursively
//     in lexical order;
//   - a subdirectory that carries its own kustomization file is added as a
//     resource and not descended into;
//   - a file that holds no document with apiVersion and kind is skipped, as
//     the controller skips files that do not parse as Kubernetes manifests;
//   - with nothing to add the controller emits a placeholder Namespace so the
//     build does not fail on an empty resource list; here an empty list is
//     written instead, since the caller filters objects anyway.
//
// Files and directories that ignore (a .sourceignore matcher, nil for none)
// leaves out of the artifact are skipped, as source-controller never ships
// them.
//
// The file is written into a fresh temporary directory rather than into dir,
// so the working tree is never touched; resources are referenced by relative
// path, which kustomize accepts under LoadRestrictionsNone. The caller must
// call cleanup.
func GenerateKustomization(dir string, ignore *SourceIgnore) (kustomizationDir string, cleanup func(), err error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	tmp, err := os.MkdirTemp("", "flux-tools-kustomization-")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temporary directory: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	// kustomize resolves symlinks in the kustomization directory before
	// following relative resource paths (macOS: /var/folders -> /private/var).
	// Relative paths must therefore be computed between real paths, or they
	// land beside the wrong ancestor.
	abs, tmp = realPath(abs), realPath(tmp)

	resources, err := scanManifests(abs, ignore)
	if err != nil {
		cleanup()
		return "", nil, err
	}

	relResources := make([]string, 0, len(resources))
	for _, r := range resources {
		rel, err := filepath.Rel(tmp, r)
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("failed to relativise %s: %w", r, err)
		}
		relResources = append(relResources, rel)
	}

	doc := map[string]interface{}{
		"apiVersion": "kustomize.config.k8s.io/v1beta1",
		"kind":       "Kustomization",
		"resources":  relResources,
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to marshal generated kustomization: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "kustomization.yaml"), data, 0o644); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to write generated kustomization: %w", err)
	}

	return tmp, cleanup, nil
}

// scanManifests returns the absolute paths kustomize-controller would list as
// resources for base: manifest files, and directories with their own
// kustomization file.
func scanManifests(base string, ignore *SourceIgnore) ([]string, error) {
	var paths []string
	err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == base {
			return nil
		}
		if ignore.Ignored(path, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			if fsutil.HasKustomizationFile(path) {
				paths = append(paths, path)
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		ok, err := isKubernetesManifest(path)
		if err != nil {
			return err
		}
		if ok {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan %s: %w", base, err)
	}
	return paths, nil
}

// isKubernetesManifest reports whether the file holds at least one YAML
// document with apiVersion and kind.
func isKubernetesManifest(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("failed to read %s: %w", path, err)
	}
	for _, doc := range fsutil.SplitYAMLDocuments(data) {
		if strings.TrimSpace(string(doc)) == "" {
			continue
		}
		var head struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
		}
		if err := yaml.Unmarshal(doc, &head); err != nil {
			continue
		}
		if head.APIVersion != "" && head.Kind != "" {
			return true, nil
		}
	}
	return false, nil
}

// realPath resolves symlinks. A path that does not exist (yet) is resolved
// through its nearest existing ancestor, so that paths under a symlinked
// directory compare equal whether or not they exist.
func realPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent, base := filepath.Split(filepath.Clean(path))
	parent = filepath.Clean(parent)
	if parent == path || parent == "." || parent == string(filepath.Separator) {
		return path
	}
	return filepath.Join(realPath(parent), base)
}
