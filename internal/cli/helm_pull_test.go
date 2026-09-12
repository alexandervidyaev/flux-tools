package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	internalHelm "github.com/alexandervidyaev/flux-tools/internal/helm"
	"github.com/alexandervidyaev/flux-tools/internal/orchestrator"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

func TestPrintPullTargets(t *testing.T) {
	tests := []struct {
		name     string
		mode     orchestrator.Mode
		clusters []string
		want     []string
		notWant  []string
	}{
		{
			name:     "single cluster is named, not counted",
			mode:     orchestrator.ModeSingle,
			clusters: []string{"/repo/clusters/dev/kube-dev"},
			want:     []string{"Pulling charts for single cluster: kube-dev\n"},
			notWant:  []string{"cluster(s)", "/repo/clusters"},
		},
		{
			name:     "multi cluster is numbered by base name",
			mode:     orchestrator.ModeMulti,
			clusters: []string{"/repo/clusters/dev/a", "/repo/clusters/dev/b"},
			want:     []string{"Pulling charts for 2 cluster(s):\n", "  1. a\n", "  2. b\n"},
			notWant:  []string{"/repo/clusters"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printPullTargets(output.NewWithWriter(&buf, false), tt.mode, tt.clusters)

			got := buf.String()
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
			for _, no := range tt.notWant {
				if strings.Contains(got, no) {
					t.Errorf("unexpected %q in:\n%s", no, got)
				}
			}
		})
	}
}

func TestPrintPullSummarySuccess(t *testing.T) {
	var buf bytes.Buffer
	result := &internalHelm.PullResult{TotalCharts: 3, Pulled: 2, Skipped: 1}

	if err := printPullSummary(output.NewWithWriter(&buf, false), result, false); err != nil {
		t.Fatalf("printPullSummary: %v", err)
	}

	got := buf.String()
	for _, want := range []string{
		"Total: 3 unique chart(s)\n",
		"✓ Pulled: 2\n",
		"⊙ Skipped (already in cache): 1\n",
		"All charts pulled successfully!\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[DRY RUN]") || strings.Contains(got, "Failed") {
		t.Errorf("clean run must not mention dry-run or failures:\n%s", got)
	}
}

func TestPrintPullSummaryDryRunListsCharts(t *testing.T) {
	result := &internalHelm.PullResult{
		TotalCharts: 2,
		Pulled:      1,
		Skipped:     1,
		Charts: []internalHelm.ChartRef{
			{RepoName: "repo-a", Chart: "podinfo", Version: "6.0.0"},
			{RepoName: "repo-b", Chart: "cached", Version: "1.0.0"},
		},
		NeedDownload:  []internalHelm.ChartRef{{RepoName: "repo-a", Chart: "podinfo", Version: "6.0.0", IsOCI: true}},
		AlreadyCached: []internalHelm.ChartRef{{RepoName: "repo-b", Chart: "cached", Version: "1.0.0"}},
	}

	t.Run("quiet lists only what would be downloaded", func(t *testing.T) {
		var buf bytes.Buffer
		if err := printPullSummary(output.NewWithWriter(&buf, false), result, true); err != nil {
			t.Fatalf("printPullSummary: %v", err)
		}
		got := buf.String()
		for _, want := range []string{
			"[DRY RUN] ",
			"⊙ Already in cache: 1\n",
			"⬇ Need to download: 1\n",
			"Charts to be downloaded:\n",
			"  1. repo-a/podinfo@6.0.0\n",
			"Dry run complete.\n",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in:\n%s", want, got)
			}
		}
		// The cached list and the per-chart detail are verbose-only.
		for _, no := range []string{"Charts already in cache:", "Type: OCI", "Repository:"} {
			if strings.Contains(got, no) {
				t.Errorf("verbose-only line %q leaked into quiet output:\n%s", no, got)
			}
		}
		if strings.Contains(got, "Pulled:") {
			t.Errorf("dry run must not claim anything was pulled:\n%s", got)
		}
	})

	t.Run("verbose adds the cached list and chart detail", func(t *testing.T) {
		var buf bytes.Buffer
		if err := printPullSummary(output.NewWithWriter(&buf, true), result, true); err != nil {
			t.Fatalf("printPullSummary: %v", err)
		}
		got := buf.String()
		for _, want := range []string{
			"Charts already in cache:\n",
			"  1. repo-b/cached@1.0.0\n",
			"Type: OCI\n",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in:\n%s", want, got)
			}
		}
	})
}

func TestPrintPullSummaryFailuresAreReturned(t *testing.T) {
	var buf bytes.Buffer
	result := &internalHelm.PullResult{
		TotalCharts: 2,
		Pulled:      1,
		Failed:      1,
		Failures:    map[string]error{"repo-a/podinfo": errors.New("404 not found")},
	}

	err := printPullSummary(output.NewWithWriter(&buf, false), result, false)
	if err == nil {
		t.Fatal("a failed chart must surface as an error")
	}
	if want := "1 chart(s) failed to pull"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}

	got := buf.String()
	for _, want := range []string{"✗ Failed: 1\n", "Failed charts:\n", "repo-a/podinfo: 404 not found\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// The success line belongs to the non-failing path only.
	if strings.Contains(got, "All charts pulled successfully") {
		t.Errorf("success line printed despite a failure:\n%s", got)
	}
}
