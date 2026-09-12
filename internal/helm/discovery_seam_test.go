package helm

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/manifest"
	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

type fakeLoader struct {
	collection *manifest.ManifestCollection
	err        error
}

func (f fakeLoader) GetManifestCollection(context.Context) (*manifest.ManifestCollection, error) {
	return f.collection, f.err
}

// stubLoader swaps the package-level loader factory for the duration of one
// test, so discovery runs against a hand-built collection instead of rendering
// a cluster. Tests using it must not run in parallel.
func stubLoader(t *testing.T, fn func(ctx context.Context, clusterPath, rootPath string, verbose bool) (collectionLoader, error)) {
	t.Helper()
	orig := newCollectionLoader
	newCollectionLoader = fn
	t.Cleanup(func() { newCollectionLoader = orig })
}

func collectionWith(releases []*types.HelmRelease, repos []*types.HelmRepository) *manifest.ManifestCollection {
	c := manifest.NewManifestCollection()
	for _, hr := range releases {
		c.HelmReleases[hr.GetKey()] = hr
	}
	for _, repo := range repos {
		c.HelmRepositories[repo.GetKey()] = repo
	}
	return c
}

func chartKeys(charts []ChartRef) []string {
	keys := make([]string, 0, len(charts))
	for i := range charts {
		keys = append(keys, charts[i].Key())
	}
	sort.Strings(keys)
	return keys
}

// Discovery keeps only the HelmReleases whose chart is a package in a Helm
// repository. Charts from a GitRepository or a spec.chartRef are on disk
// already, and a release whose repository was never declared is logged and
// skipped rather than failing the whole cluster.
func TestDiscoverChartsInClusterSelectsPullableCharts(t *testing.T) {
	pullable := helmRelease("apps", "podinfo", "podinfo", "HelmRepository", "charts", "")
	fromGit := helmRelease("apps", "local", "./charts/local", "GitRepository", "flux-system", "")
	undeclared := helmRelease("apps", "orphan", "orphan", "HelmRepository", "nowhere", "")
	viaChartRef := helmRelease("apps", "generated", "", "", "", "")
	viaChartRef.Spec.ChartRef = &types.ChartRef{Kind: "ExternalArtifact", Name: "artifact"}

	repo := helmRepoTyped("apps", "charts", "https://charts.example.com", "")
	collection := collectionWith(
		[]*types.HelmRelease{pullable, fromGit, undeclared, viaChartRef},
		[]*types.HelmRepository{repo},
	)

	stubLoader(t, func(context.Context, string, string, bool) (collectionLoader, error) {
		return fakeLoader{collection: collection}, nil
	})

	charts, err := DiscoverChartsInCluster(context.Background(), "clusters/dev/a", "", false)
	if err != nil {
		t.Fatalf("DiscoverChartsInCluster: %v", err)
	}

	want := []string{"https://charts.example.com/podinfo@1.0.0"}
	if got := chartKeys(charts); !equalStrings(got, want) {
		t.Errorf("charts = %v, want %v", got, want)
	}
}

func TestDiscoverChartsInClusterPropagatesErrors(t *testing.T) {
	boom := errors.New("boom")

	tests := []struct {
		name    string
		loader  func(context.Context, string, string, bool) (collectionLoader, error)
		wantErr string
	}{
		{
			name:    "the builder cannot be constructed",
			loader:  func(context.Context, string, string, bool) (collectionLoader, error) { return nil, boom },
			wantErr: "failed to create builder",
		},
		{
			name: "the collection cannot be rendered",
			loader: func(context.Context, string, string, bool) (collectionLoader, error) {
				return fakeLoader{err: boom}, nil
			},
			wantErr: "failed to get manifest collection",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubLoader(t, tt.loader)

			_, err := DiscoverChartsInCluster(context.Background(), "clusters/dev/a", "", false)
			if err == nil {
				t.Fatalf("want an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
			if !errors.Is(err, boom) {
				t.Errorf("err = %v, want the cause to be wrapped", err)
			}
		})
	}
}

// The same chart declared in several clusters is pulled once: the key is
// repository + chart + version, so two releases differing only by name or
// namespace collapse into one entry.
func TestDiscoverChartsParallelDeduplicates(t *testing.T) {
	repo := helmRepoTyped("apps", "charts", "https://charts.example.com", "")

	stubLoader(t, func(_ context.Context, clusterPath, _ string, _ bool) (collectionLoader, error) {
		shared := helmRelease("apps", "podinfo-"+clusterPath, "podinfo", "HelmRepository", "charts", "")
		unique := helmRelease("apps", "only-"+clusterPath, clusterPath, "HelmRepository", "charts", "")
		return fakeLoader{collection: collectionWith(
			[]*types.HelmRelease{shared, unique},
			[]*types.HelmRepository{repo},
		)}, nil
	})

	charts, err := DiscoverChartsParallel(context.Background(), []string{"a", "b"}, "", false, 2)
	if err != nil {
		t.Fatalf("DiscoverChartsParallel: %v", err)
	}

	want := []string{
		"https://charts.example.com/a@1.0.0",
		"https://charts.example.com/b@1.0.0",
		"https://charts.example.com/podinfo@1.0.0",
	}
	if got := chartKeys(charts); !equalStrings(got, want) {
		t.Errorf("charts = %v, want %v", got, want)
	}
}

// The two discovery modes treat a broken cluster differently, and callers rely
// on it: sequential (verbose) stops at the first failure so the operator sees
// it, parallel reports a warning and returns what the healthy clusters gave.
func TestDiscoverChartsParallelFailureSemanticsDifferByMode(t *testing.T) {
	repo := helmRepoTyped("apps", "charts", "https://charts.example.com", "")
	loader := func(_ context.Context, clusterPath, _ string, _ bool) (collectionLoader, error) {
		if clusterPath == "broken" {
			return nil, errors.New("cannot build")
		}
		return fakeLoader{collection: collectionWith(
			[]*types.HelmRelease{helmRelease("apps", "podinfo", "podinfo", "HelmRepository", "charts", "")},
			[]*types.HelmRepository{repo},
		)}, nil
	}

	t.Run("sequential aborts", func(t *testing.T) {
		stubLoader(t, loader)

		_, err := DiscoverChartsParallel(context.Background(), []string{"ok", "broken"}, "", true, 1)
		if err == nil {
			t.Fatal("sequential discovery must surface a cluster failure")
		}
		if !strings.Contains(err.Error(), "failed to discover charts in broken") {
			t.Errorf("err = %q, want it to name the failing cluster", err.Error())
		}
	})

	t.Run("parallel tolerates and returns the rest", func(t *testing.T) {
		stubLoader(t, loader)

		charts, err := DiscoverChartsParallel(context.Background(), []string{"ok", "broken"}, "", false, 2)
		if err != nil {
			t.Fatalf("parallel discovery must not fail on one broken cluster: %v", err)
		}
		want := []string{"https://charts.example.com/podinfo@1.0.0"}
		if got := chartKeys(charts); !equalStrings(got, want) {
			t.Errorf("charts = %v, want %v", got, want)
		}
	})
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
