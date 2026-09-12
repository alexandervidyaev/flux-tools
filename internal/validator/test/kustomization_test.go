package test

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

// TestKustomizationTestCaseUsesBuildPathResolver verifies that when a
// BuildPathResolver is supplied, KustomizationTestCase delegates path
// resolution to it (which in production knows how to clone external
// GitRepository sources) instead of joining spec.path against a local root.
// This unblocks fluxcd's cross-repo Kustomizations such as kargo-helm.
func TestKustomizationTestCaseUsesBuildPathResolver(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not in PATH")
	}

	// Use the existing source-repo fixture as the "external" repository: the
	// resolver returns the fixture's cluster directory, irrespective of what
	// spec.path or RootPath would compute on their own.
	fixtureCluster, err := filepath.Abs(filepath.Join(
		"..", "..", "orchestrator", "testdata",
		"source-repo", "releases", "stable", "cluster-a",
	))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	tc := &KustomizationTestCase{
		TestName: "flux-system/kargo-helm",
		Kustomization: &types.Kustomization{
			ObjectMeta: types.ObjectMeta{
				Name:      "kargo-helm",
				Namespace: "flux-system",
			},
			Spec: types.KustomizationSpec{
				// Path that would not exist locally — proves the resolver
				// dictates where kustomize build runs.
				Path: "./releases/stable/external-cluster",
				SourceRef: types.CrossNamespaceObjectReference{
					Kind: "GitRepository",
					Name: "kargo",
				},
			},
		},
		RootPath: t.TempDir(),
		BuildPathResolver: func(_ context.Context, ks *types.Kustomization) (string, error) {
			return fixtureCluster, nil
		},
	}

	result := tc.Run(context.Background())

	if result.Status != TestPassed {
		t.Fatalf("status = %v err = %v, want PASSED", result.Status, result.Error)
	}
	if result.Objects == 0 {
		t.Errorf("expected at least one rendered object, got 0")
	}
}

// TestKustomizationTestCaseResolverError surfaces resolver failures as a
// failed test rather than a silent pass.
func TestKustomizationTestCaseResolverError(t *testing.T) {
	resolverErr := errors.New("clone failed")
	tc := &KustomizationTestCase{
		TestName: "flux-system/kargo-helm",
		Kustomization: &types.Kustomization{
			Spec: types.KustomizationSpec{
				SourceRef: types.CrossNamespaceObjectReference{
					Kind: "GitRepository",
					Name: "kargo",
				},
				Path: "./releases/stable/cluster-a",
			},
		},
		RootPath: t.TempDir(),
		BuildPathResolver: func(_ context.Context, ks *types.Kustomization) (string, error) {
			return "", resolverErr
		},
	}

	result := tc.Run(context.Background())

	if result.Status != TestFailed {
		t.Errorf("status = %v, want FAILED", result.Status)
	}
	if result.Error == nil || !errors.Is(result.Error, resolverErr) {
		t.Errorf("error = %v, want chain containing %v", result.Error, resolverErr)
	}
}
