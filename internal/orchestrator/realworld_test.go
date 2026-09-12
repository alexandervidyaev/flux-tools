package orchestrator

import (
	"path/filepath"
	"sort"
	"testing"
)

// realworld_test.go validates discovery against vendored manifest fixtures
// that mirror the layouts of two real repositories:
//
//   - testdata/source-repo — kargo-style source repo with charts/<chart>/ and
//     releases/<env>/<cluster>/kustomization.yaml, used as an external
//     GitRepository by the build and test fixtures.
//   - testdata/cluster-repo — fluxcd-style cluster repo with infrastructure/
//     and clusters/<env>/<cluster>/flux-system/. Targeted by `flux-tools test/build ...`.
//
// The fixtures are intentionally small but structurally faithful so resolver
// regressions in either invocation mode are caught locally without external
// Helm/OCI access.

const clusterFixture = "testdata/cluster-repo"

func TestRealWorldClusterLayout(t *testing.T) {
	clusterA, err := filepath.Abs(filepath.Join(clusterFixture, "clusters", "dev", "cluster-a"))
	if err != nil {
		t.Fatalf("abs cluster-a: %v", err)
	}
	clusterB, err := filepath.Abs(filepath.Join(clusterFixture, "clusters", "dev", "cluster-b"))
	if err != nil {
		t.Fatalf("abs cluster-b: %v", err)
	}

	cases := []struct {
		name         string
		path         string
		wantMode     Mode
		wantClusters []string
	}{
		{
			name:         "cluster directory with flux-system resolves as ModeSingle",
			path:         filepath.Join(clusterFixture, "clusters", "dev", "cluster-a"),
			wantMode:     ModeSingle,
			wantClusters: []string{clusterA},
		},
		{
			name:         "environment directory enumerates clusters",
			path:         filepath.Join(clusterFixture, "clusters", "dev"),
			wantMode:     ModeMulti,
			wantClusters: []string{clusterA, clusterB},
		},
		{
			name:         "clusters root recursively discovers clusters in all envs",
			path:         filepath.Join(clusterFixture, "clusters"),
			wantMode:     ModeMulti,
			wantClusters: []string{clusterA, clusterB},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mode, clusters, err := ResolveInput(c.path, DefaultMarkers())
			if err != nil {
				t.Fatalf("ResolveInput(%s): %v", c.path, err)
			}
			if mode != c.wantMode {
				t.Errorf("mode = %v, want %v", mode, c.wantMode)
			}
			sort.Strings(clusters)
			sort.Strings(c.wantClusters)
			if !equalStrings(clusters, c.wantClusters) {
				t.Errorf("clusters = %v, want %v", clusters, c.wantClusters)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
