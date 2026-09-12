package helm

import (
	"errors"
	"strings"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

// helmRepoTyped builds a repository with an explicit spec.type; the empty
// type is a plain HTTP repository, "oci" an OCI registry.
func helmRepoTyped(ns, name, url, repoType string) *types.HelmRepository {
	repo := &types.HelmRepository{}
	repo.Namespace = ns
	repo.Name = name
	repo.Spec.URL = url
	repo.Spec.Type = repoType
	return repo
}

// A HelmRelease taking its chart from spec.chartRef points at an
// ExternalArtifact carved out of the repository itself, so there is nothing to
// fetch. The caller tells that apart from a real failure by errNoChartToPull.
func TestExtractChartRefSkipsChartRef(t *testing.T) {
	hr := helmRelease("apps", "app", "", "", "", "")
	hr.Spec.ChartRef = &types.ChartRef{Kind: "ExternalArtifact", Name: "artifact"}

	if _, err := extractChartRef(hr, nil); !errors.Is(err, errNoChartToPull) {
		t.Fatalf("err = %v, want errNoChartToPull", err)
	}
}

// A malformed or unresolvable HelmRelease must surface as a real error, never
// as errNoChartToPull: the caller only logs the latter and moves on, so
// conflating the two would silently drop charts that should have been pulled.
func TestExtractChartRefErrors(t *testing.T) {
	tests := []struct {
		name    string
		hr      *types.HelmRelease
		repos   map[string]*types.HelmRepository
		wantErr string
	}{
		{
			name:    "chart name is required",
			hr:      helmRelease("apps", "app", "", "HelmRepository", "repo", ""),
			wantErr: "chart name is empty",
		},
		{
			name:    "sourceRef name is required",
			hr:      helmRelease("apps", "app", "podinfo", "HelmRepository", "", ""),
			wantErr: "chart sourceRef.name is empty",
		},
		{
			name:    "an undeclared repository is an error, not a skip",
			hr:      helmRelease("apps", "app", "podinfo", "HelmRepository", "missing", ""),
			repos:   map[string]*types.HelmRepository{},
			wantErr: "helm repository not found: apps/missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := extractChartRef(tt.hr, tt.repos)
			if err == nil {
				t.Fatalf("want error %q, got nil", tt.wantErr)
			}
			if errors.Is(err, errNoChartToPull) {
				t.Fatalf("a malformed HelmRelease must not read as nothing-to-pull: %v", err)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// Every ChartRef field is carried from either the HelmRelease or the resolved
// repository; IsOCI comes from the repository's spec.type and decides whether
// the puller talks to a registry or a chart index.
func TestExtractChartRefResolvedFields(t *testing.T) {
	tests := []struct {
		name string
		repo *types.HelmRepository
		want ChartRef
	}{
		{
			name: "http repository",
			repo: helmRepoTyped("apps", "charts", "https://charts.example.com", ""),
			want: ChartRef{
				Repository: "https://charts.example.com", RepoName: "charts",
				Chart: "podinfo", Version: "1.0.0", IsOCI: false,
				Namespace: "apps", ReleaseName: "app",
			},
		},
		{
			name: "oci repository",
			repo: helmRepoTyped("apps", "charts", "oci://registry.example.com/charts", "oci"),
			want: ChartRef{
				Repository: "oci://registry.example.com/charts", RepoName: "charts",
				Chart: "podinfo", Version: "1.0.0", IsOCI: true,
				Namespace: "apps", ReleaseName: "app",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repos := map[string]*types.HelmRepository{tt.repo.GetKey(): tt.repo}
			got, err := extractChartRef(helmRelease("apps", "app", "podinfo", "HelmRepository", "charts", ""), repos)
			if err != nil {
				t.Fatalf("extractChartRef: %v", err)
			}
			if got != tt.want {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}
