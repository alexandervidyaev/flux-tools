package manifest

import "github.com/alexandervidyaev/flux-tools/pkg/types"

// ManifestCollection holds Flux resources organized by type. It is populated by
// the build package directly (see Builder.parseFluxObjects).
type ManifestCollection struct {
	Kustomizations   map[string]*types.Kustomization
	HelmReleases     map[string]*types.HelmRelease
	HelmRepositories map[string]*types.HelmRepository
	GitRepositories  map[string]*types.GitRepository
	// ArtifactGenerators are indexed by namespace/name of the generator, not of
	// the artifacts it produces
	ArtifactGenerators map[string]*types.ArtifactGenerator
}

// NewManifestCollection creates a new ManifestCollection
func NewManifestCollection() *ManifestCollection {
	return &ManifestCollection{
		Kustomizations:     make(map[string]*types.Kustomization),
		HelmReleases:       make(map[string]*types.HelmRelease),
		HelmRepositories:   make(map[string]*types.HelmRepository),
		GitRepositories:    make(map[string]*types.GitRepository),
		ArtifactGenerators: make(map[string]*types.ArtifactGenerator),
	}
}
