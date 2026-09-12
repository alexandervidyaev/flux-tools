package build

import (
	"context"
	"fmt"

	"github.com/alexandervidyaev/flux-tools/pkg/manifest"
)

// GetRootPath returns the repository root path
func (b *Builder) GetRootPath() string {
	return b.rootPath
}

// SelfSource is the namespace/name of the GitRepository that is this checkout
// (the flux-system sync source), or empty when there is no flux-system/.
func (b *Builder) SelfSource() string {
	return b.selfSource
}

// GetManifestCollection returns the manifest collection without building Helm releases
// This is useful for test discovery where we just need to know what resources exist
func (b *Builder) GetManifestCollection(ctx context.Context) (*manifest.ManifestCollection, error) {
	b.printer.Verbose("Discovering Flux manifests from: %s\n", b.options.Path)

	// Step 1: Run kustomize build on the input path to get all Flux objects
	fluxObjects, err := b.buildAndParseCached(ctx, b.options.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to build kustomizations: %w", err)
	}

	// Step 2: Parse Flux objects into collection
	collection := manifest.NewManifestCollection()
	b.parseFluxObjects(fluxObjects, collection)

	b.printer.Verbose("Initial scan: Found %d Kustomizations, %d HelmReleases, %d HelmRepositories\n",
		len(collection.Kustomizations), len(collection.HelmReleases), len(collection.HelmRepositories))

	// Step 2.5: Recursively discover nested Kustomizations
	if err := b.discoverNestedKustomizations(ctx, collection); err != nil {
		return nil, fmt.Errorf("failed to discover nested Kustomizations: %w", err)
	}

	b.printer.Verbose("After recursive discovery: %d Kustomizations total\n", len(collection.Kustomizations))

	// Step 3: Validate dependencies if in strict mode
	if b.options.Strict {
		if err := manifest.ValidateDependencies(collection); err != nil {
			return nil, fmt.Errorf("dependency validation failed: %w", err)
		}
	}

	return collection, nil
}
