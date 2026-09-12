package test

import (
	"context"
	"fmt"
)

// DiscoverTests discovers all tests from the repository
func (r *TestRunner) DiscoverTests(ctx context.Context) ([]Test, error) {
	// Use builder to get manifest collection
	collection, err := r.builder.GetManifestCollection(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to discover manifests: %w", err)
	}

	var tests []Test

	// Get root path from builder
	rootPath := r.builder.GetRootPath()

	// Create test for each Kustomization
	for key, kustomization := range collection.Kustomizations {
		test := &KustomizationTestCase{
			TestName:          key,
			Kustomization:     kustomization,
			RootPath:          rootPath,
			Options:           r.options,
			BuildPathResolver: r.builder.ResolveKustomizationBuildPath,
		}
		tests = append(tests, test)
	}

	// If --enable-helm, create tests for HelmRelease
	if r.options.EnableHelm {
		for key, helmRelease := range collection.HelmReleases {
			test := &HelmReleaseTestCase{
				TestName:           key,
				HelmRelease:        helmRelease,
				HelmRepositories:   collection.HelmRepositories,
				ArtifactGenerators: collection.ArtifactGenerators,
				GitRepositories:    collection.GitRepositories,
				SelfSource:         r.builder.SelfSource(),
				RootPath:           rootPath,
				Options:            r.options,
			}
			tests = append(tests, test)
		}
	}

	return tests, nil
}
