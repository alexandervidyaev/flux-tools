package helm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

func TestGetConcurrencyDefaultsToThree(t *testing.T) {
	tests := []struct {
		name string
		opts PullOptions
		want int
	}{
		{name: "explicit value wins", opts: PullOptions{Concurrency: 7}, want: 7},
		{name: "zero falls back", opts: PullOptions{Concurrency: 0}, want: 3},
		{name: "negative falls back", opts: PullOptions{Concurrency: -1}, want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getConcurrency(tt.opts); got != tt.want {
				t.Errorf("getConcurrency = %d, want %d", got, tt.want)
			}
		})
	}
}

// The cache layout is a contract shared with helm.Client, which writes the
// archives: <cacheDir>/charts/<chart>-<version>.tgz.
func TestChartExistsInCacheMatchesTheArchiveLayout(t *testing.T) {
	cacheDir := t.TempDir()
	chartsDir := filepath.Join(cacheDir, "charts")
	if err := os.MkdirAll(chartsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartsDir, "podinfo-6.0.0.tgz"), []byte("x"), 0644); err != nil {
		t.Fatalf("seed archive: %v", err)
	}

	tests := []struct {
		name  string
		chart ChartRef
		want  bool
	}{
		{name: "exact chart and version", chart: ChartRef{Chart: "podinfo", Version: "6.0.0"}, want: true},
		{name: "another version is a miss", chart: ChartRef{Chart: "podinfo", Version: "6.0.1"}, want: false},
		{name: "another chart is a miss", chart: ChartRef{Chart: "other", Version: "6.0.0"}, want: false},
		{name: "an empty version is a miss", chart: ChartRef{Chart: "podinfo"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ChartExistsInCache(cacheDir, tt.chart); got != tt.want {
				t.Errorf("ChartExistsInCache = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("a missing cache directory is simply a miss", func(t *testing.T) {
		if ChartExistsInCache(filepath.Join(t.TempDir(), "absent"), ChartRef{Chart: "podinfo", Version: "6.0.0"}) {
			t.Error("want false when the cache does not exist")
		}
	})
}

// dryRunCheck is what --dry-run reports on, so the split between cached and
// missing charts, and the counters the summary prints, are both load-bearing.
func TestDryRunCheckSplitsCachedFromMissing(t *testing.T) {
	cacheDir := t.TempDir()
	chartsDir := filepath.Join(cacheDir, "charts")
	if err := os.MkdirAll(chartsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartsDir, "cached-1.0.0.tgz"), []byte("x"), 0644); err != nil {
		t.Fatalf("seed archive: %v", err)
	}

	charts := []ChartRef{
		{RepoName: "repo", Chart: "cached", Version: "1.0.0", Repository: "https://charts.example.com"},
		{RepoName: "repo", Chart: "missing", Version: "2.0.0", Repository: "https://charts.example.com"},
	}

	var buf bytes.Buffer
	result := dryRunCheck(charts, PullOptions{CacheDir: cacheDir},
		output.NewWithWriter(&buf, true), &PullResult{TotalCharts: len(charts)})

	if result.Skipped != 1 || result.Pulled != 1 {
		t.Errorf("Skipped = %d, Pulled = %d, want 1 and 1", result.Skipped, result.Pulled)
	}
	if len(result.AlreadyCached) != 1 || result.AlreadyCached[0].Chart != "cached" {
		t.Errorf("AlreadyCached = %+v", result.AlreadyCached)
	}
	if len(result.NeedDownload) != 1 || result.NeedDownload[0].Chart != "missing" {
		t.Errorf("NeedDownload = %+v", result.NeedDownload)
	}

	got := buf.String()
	if !strings.Contains(got, "✓") || !strings.Contains(got, "already in cache") {
		t.Errorf("cached chart was not reported:\n%s", got)
	}
	if !strings.Contains(got, "⬇") || !strings.Contains(got, "needs download") {
		t.Errorf("missing chart was not reported:\n%s", got)
	}

	// Nothing is fetched and TotalCharts is left as the caller set it.
	if result.TotalCharts != 2 || result.Failed != 0 {
		t.Errorf("TotalCharts = %d, Failed = %d", result.TotalCharts, result.Failed)
	}
}

func TestChartRefString(t *testing.T) {
	tests := []struct {
		name  string
		chart ChartRef
		want  string
	}{
		{
			name:  "http charts name their repository",
			chart: ChartRef{RepoName: "repo", Chart: "podinfo", Version: "6.0.0", Repository: "https://charts.example.com"},
			want:  "repo/podinfo@6.0.0 (from https://charts.example.com)",
		},
		{
			name:  "oci charts gain the scheme when it is absent",
			chart: ChartRef{Chart: "kargo", Version: "1.2.3", Repository: "registry.example.com/charts", IsOCI: true},
			want:  "oci://registry.example.com/charts/kargo:1.2.3",
		},
		{
			// The scheme must not be doubled: Flux spells OCI URLs with it.
			name:  "an oci:// prefix is not duplicated",
			chart: ChartRef{Chart: "kargo", Version: "1.2.3", Repository: "oci://registry.example.com/charts", IsOCI: true},
			want:  "oci://registry.example.com/charts/kargo:1.2.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.chart.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPullResultString(t *testing.T) {
	r := &PullResult{TotalCharts: 5, Pulled: 2, Skipped: 2, Failed: 1}
	if want := "Total: 5, Pulled: 2, Skipped: 2, Failed: 1"; r.String() != want {
		t.Errorf("String() = %q, want %q", r.String(), want)
	}
}
