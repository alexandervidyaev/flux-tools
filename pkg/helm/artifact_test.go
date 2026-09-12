package helm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

func generator(copies ...types.ArtifactGeneratorCopy) *types.ArtifactGenerator {
	return &types.ArtifactGenerator{
		ObjectMeta: types.ObjectMeta{Name: "gen", Namespace: "app"},
		Spec: types.ArtifactGeneratorSpec{
			Sources: []types.ArtifactGeneratorSource{
				{Alias: "bundle", Kind: "OCIRepository", Name: "app", Namespace: "flux-system"},
			},
			Artifacts: []types.ArtifactGeneratorArtifact{
				{Name: "app-chart", Copy: copies},
			},
		},
	}
}

func TestResolveArtifactPath(t *testing.T) {
	tests := []struct {
		name    string
		copies  []types.ArtifactGeneratorCopy
		want    string
		wantErr string
	}{
		{
			name:   "chart subdirectory into artifact root",
			copies: []types.ArtifactGeneratorCopy{{From: "@bundle/charts/extend/", To: "@artifact/"}},
			want:   "charts/extend",
		},
		{
			name:   "no trailing slashes",
			copies: []types.ArtifactGeneratorCopy{{From: "@bundle/charts/extend", To: "@artifact"}},
			want:   "charts/extend",
		},
		{
			name:    "unknown alias",
			copies:  []types.ArtifactGeneratorCopy{{From: "@other/charts/extend/", To: "@artifact/"}},
			wantErr: "not declared in spec.sources",
		},
		{
			name:    "destination is not the artifact root",
			copies:  []types.ArtifactGeneratorCopy{{From: "@bundle/charts/extend/", To: "@artifact/sub/"}},
			wantErr: "only @artifact/ can be mapped",
		},
		{
			name: "assembled from several copies",
			copies: []types.ArtifactGeneratorCopy{
				{From: "@bundle/charts/extend/", To: "@artifact/"},
				{From: "@bundle/common/", To: "@artifact/"},
			},
			wantErr: "assembled from 2 copy operations",
		},
		{
			name:    "no copy operations",
			copies:  nil,
			wantErr: "has no copy operations",
		},
		{
			name:    "whole source, no chart subdirectory",
			copies:  []types.ArtifactGeneratorCopy{{From: "@bundle/", To: "@artifact/"}},
			wantErr: "copies the whole source",
		},
		{
			name:    "path escapes the source",
			copies:  []types.ArtifactGeneratorCopy{{From: "@bundle/../secrets/", To: "@artifact/"}},
			wantErr: "not of the form @<alias>/<path>",
		},
		{
			name:    "missing alias prefix",
			copies:  []types.ArtifactGeneratorCopy{{From: "charts/extend/", To: "@artifact/"}},
			wantErr: "not of the form @<alias>/<path>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ag := generator(tc.copies...)
			got, err := resolveArtifactPath(ag, ag.Spec.Artifacts[0])

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got path %q", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected path %q, got %q", tc.want, got)
			}
		})
	}
}

func TestRegisterArtifactGeneratorKeepsFirstAndCarriesError(t *testing.T) {
	c := &Client{externalArtifacts: make(map[string]externalArtifactSource)}

	c.RegisterArtifactGenerator(generator(types.ArtifactGeneratorCopy{From: "@bundle/charts/extend/", To: "@artifact/"}))
	// A second generator claiming the same artifact name must not win.
	c.RegisterArtifactGenerator(generator(types.ArtifactGeneratorCopy{From: "@bundle/charts/other/", To: "@artifact/"}))

	path, err, ok := c.GetExternalArtifactPath("app/app-chart")
	if !ok {
		t.Fatal("expected the artifact to be registered")
	}
	if err != nil {
		t.Fatalf("unexpected resolution error: %v", err)
	}
	if path != "charts/extend" {
		t.Fatalf("expected first registration to win, got %q", path)
	}

	if _, _, ok := c.GetExternalArtifactPath("app/missing"); ok {
		t.Fatal("expected lookup of an unknown artifact to report not found")
	}

	// A generator that cannot be mapped is still registered, so the failure is
	// reported against the HelmRelease that references it.
	broken := generator(types.ArtifactGeneratorCopy{From: "@nope/charts/x/", To: "@artifact/"})
	broken.Spec.Artifacts[0].Name = "broken-chart"
	c.RegisterArtifactGenerator(broken)

	if _, err, ok := c.GetExternalArtifactPath("app/broken-chart"); !ok || err == nil {
		t.Fatalf("expected a registered artifact carrying a resolution error, got ok=%v err=%v", ok, err)
	}
}

func TestTemplateChartRefErrors(t *testing.T) {
	chartDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(chartDir, "charts", "extend"), 0o755); err != nil {
		t.Fatal(err)
	}

	newClient := func(root string) *Client {
		c := &Client{externalArtifacts: make(map[string]externalArtifactSource), rootPath: root}
		c.RegisterArtifactGenerator(generator(types.ArtifactGeneratorCopy{From: "@bundle/charts/extend/", To: "@artifact/"}))
		return c
	}

	hrWithRef := func(kind, name string) *types.HelmRelease {
		return &types.HelmRelease{
			ObjectMeta: types.ObjectMeta{Name: "extend", Namespace: "app"},
			Spec: types.HelmReleaseSpec{
				ChartRef: &types.ChartRef{Kind: kind, Name: name},
			},
		}
	}

	tests := []struct {
		name    string
		client  *Client
		hr      *types.HelmRelease
		wantErr string
	}{
		{
			name:    "unsupported chartRef kind",
			client:  newClient(chartDir),
			hr:      hrWithRef("OCIRepository", "app-chart"),
			wantErr: "only ExternalArtifact is supported",
		},
		{
			name:    "artifact not produced by any generator",
			client:  newClient(chartDir),
			hr:      hrWithRef("ExternalArtifact", "unknown-chart"),
			wantErr: "no ArtifactGenerator among the built manifests produces",
		},
		{
			name:    "root path not set",
			client:  newClient(""),
			hr:      hrWithRef("ExternalArtifact", "app-chart"),
			wantErr: "source root path is not set",
		},
		{
			name:    "chart directory missing on disk",
			client:  newClient(filepath.Join(chartDir, "elsewhere")),
			hr:      hrWithRef("ExternalArtifact", "app-chart"),
			wantErr: "does not exist",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.client.templateChartRef(context.Background(), tc.hr, TemplateOptions{ReleaseName: "extend"})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestTemplateRejectsChartAndChartRefTogether(t *testing.T) {
	c := &Client{externalArtifacts: make(map[string]externalArtifactSource)}
	hr := &types.HelmRelease{
		ObjectMeta: types.ObjectMeta{Name: "extend", Namespace: "app"},
		Spec: types.HelmReleaseSpec{
			Chart:    types.HelmChartTemplate{Spec: types.HelmChartTemplateSpec{Chart: "extend"}},
			ChartRef: &types.ChartRef{Kind: "ExternalArtifact", Name: "app-chart"},
		},
	}

	_, err := c.Template(context.Background(), hr, nil)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutual-exclusivity error, got %v", err)
	}
}
