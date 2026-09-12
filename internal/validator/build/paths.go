package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/alexandervidyaev/flux-tools/pkg/fsutil"
	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

// syncSource is a Flux Kustomization found in <cluster>/flux-system/ that
// syncs from a GitRepository: the one `flux bootstrap` writes.
type syncSource struct {
	path      string // spec.path, relative to the GitRepository root
	sourceKey string // namespace/name of the GitRepository
}

// findSyncSources parses <cluster>/flux-system/*.yaml for Flux Kustomizations
// with a GitRepository source. It returns nil, nil when there is no
// flux-system/ directory at all.
func findSyncSources(clusterPath string) ([]syncSource, error) {
	fluxSystem := filepath.Join(clusterPath, "flux-system")
	if info, err := os.Stat(fluxSystem); err != nil || !info.IsDir() {
		return nil, nil
	}

	files, err := fsutil.FindYAMLFiles(fluxSystem, fsutil.FindOptions{Recursive: false})
	if err != nil {
		return nil, fmt.Errorf("failed to list %s: %w", fluxSystem, err)
	}

	var sources []syncSource
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", file, err)
		}
		for _, doc := range fsutil.SplitYAMLDocuments(data) {
			var ks types.Kustomization
			if err := yaml.Unmarshal(doc, &ks); err != nil {
				continue
			}
			if ks.Kind != "Kustomization" || !strings.HasPrefix(ks.APIVersion, "kustomize.toolkit.fluxcd.io") {
				continue
			}
			if ks.Spec.SourceRef.Kind != "" && ks.Spec.SourceRef.Kind != "GitRepository" {
				continue
			}
			sources = append(sources, syncSource{path: ks.Spec.Path, sourceKey: ks.SourceRefKey()})
		}
	}
	return sources, nil
}

// DetectSelfSource returns the namespace/name of the GitRepository that the
// cluster's flux-system Kustomization syncs from, i.e. the GitRepository that
// is this checkout. Empty when the cluster has no flux-system/ (flux-operator
// keeps the sync source in the cluster, not in git).
func DetectSelfSource(clusterPath string) (string, error) {
	abs, err := filepath.Abs(clusterPath)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}
	sources, err := findSyncSources(abs)
	if err != nil || len(sources) == 0 {
		return "", err
	}
	return sources[0].sourceKey, nil
}
