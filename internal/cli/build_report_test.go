package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexandervidyaev/flux-tools/internal/orchestrator"
	"github.com/alexandervidyaev/flux-tools/internal/validator/build"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// reportBuildResults reads opts.Timers unconditionally; runBuildMulti
// guarantees it is set before the call, so every case here sets one too.
func timersWith(stages map[string]time.Duration) *output.StageTimer {
	t := output.NewStageTimer()
	for stage, d := range stages {
		t.Add(stage, d)
	}
	return t
}

func TestReportBuildResultsAllPassed(t *testing.T) {
	var buf bytes.Buffer
	results := &orchestrator.AggregatedResults{
		Clusters: []orchestrator.ClusterResult{
			{Name: "cluster-a", Passed: true},
			{Name: "cluster-b", Passed: true},
		},
		Total:  2,
		Passed: 2,
	}

	err := reportBuildResults(output.NewWithWriter(&buf, false), results, build.BuildOptions{Timers: timersWith(nil)})
	if err != nil {
		t.Fatalf("reportBuildResults: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "2 built ===") {
		t.Errorf("totals line missing from:\n%s", got)
	}
	if strings.Contains(got, "failed") {
		t.Errorf("clean run must not mention failures:\n%s", got)
	}
	// Nothing was recorded, so Summary is empty and the line is omitted.
	if strings.Contains(got, "Stages:") {
		t.Errorf("no stage was recorded, Stages line must be omitted:\n%s", got)
	}
}

func TestReportBuildResultsFailuresAreListedAndReturned(t *testing.T) {
	var buf bytes.Buffer
	results := &orchestrator.AggregatedResults{
		Clusters: []orchestrator.ClusterResult{
			{Name: "cluster-a", Passed: true},
			{Name: "cluster-b", Error: os.ErrPermission},
		},
		Total:  2,
		Passed: 1,
		Failed: 1,
	}

	err := reportBuildResults(output.NewWithWriter(&buf, false), results, build.BuildOptions{Timers: timersWith(nil)})
	if err == nil {
		t.Fatal("a failed cluster must surface as an error")
	}
	if want := "1 cluster(s) failed to build"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}

	got := buf.String()
	for _, want := range []string{"Failed clusters:", "✗ cluster-b", "1 failed, ", "1 built ==="} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// The passing cluster is not named in the failure block.
	if strings.Contains(got, "✗ cluster-a") {
		t.Errorf("passing cluster listed as failed:\n%s", got)
	}
}

func TestReportBuildResultsStageTimings(t *testing.T) {
	stages := map[string]time.Duration{
		build.StageDiscovery: 1500 * time.Millisecond,
		build.StageSerialize: 300 * time.Millisecond,
	}

	t.Run("quiet prints only the totals line", func(t *testing.T) {
		var buf bytes.Buffer
		opts := build.BuildOptions{Timers: timersWith(stages)}
		if err := reportBuildResults(output.NewWithWriter(&buf, false), &orchestrator.AggregatedResults{Passed: 1}, opts); err != nil {
			t.Fatalf("reportBuildResults: %v", err)
		}
		got := buf.String()
		if !strings.Contains(got, "Stages: discovery 1.5s | serialize 300ms\n") {
			t.Errorf("stage summary missing or malformed:\n%s", got)
		}
		if strings.Contains(got, "Stage timings") {
			t.Errorf("per-stage block belongs to verbose mode only:\n%s", got)
		}
	})

	t.Run("verbose adds the per-stage block", func(t *testing.T) {
		var buf bytes.Buffer
		opts := build.BuildOptions{Verbose: true, Timers: timersWith(stages)}
		if err := reportBuildResults(output.NewWithWriter(&buf, true), &orchestrator.AggregatedResults{Passed: 1}, opts); err != nil {
			t.Fatalf("reportBuildResults: %v", err)
		}
		got := buf.String()
		for _, want := range []string{"Stage timings (aggregated across clusters):", "discovery", "1.5s", "Stages: "} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in:\n%s", want, got)
			}
		}
		// Stages never recorded are skipped, not printed as zero.
		if strings.Contains(got, build.StageHelm) {
			t.Errorf("unrecorded stage %q must not appear:\n%s", build.StageHelm, got)
		}
	})
}

func TestNewSlicer(t *testing.T) {
	tests := []struct {
		name      string
		opts      build.BuildOptions
		wantNil   bool
		wantError bool
	}{
		{
			name:    "yaml output needs no slicer",
			opts:    build.BuildOptions{OutputFormat: "yaml"},
			wantNil: true,
		},
		{
			name:    "json output needs no slicer",
			opts:    build.BuildOptions{OutputFormat: "json"},
			wantNil: true,
		},
		{
			name: "sliced falls back to the default template",
			opts: build.BuildOptions{OutputFormat: string(build.OutputFormatSliced)},
		},
		{
			name: "sliced honours a custom template",
			opts: build.BuildOptions{OutputFormat: string(build.OutputFormatSliced), SliceTemplate: "{{.kind}}.yaml"},
		},
		{
			name:      "a broken template fails up front",
			opts:      build.BuildOptions{OutputFormat: string(build.OutputFormatSliced), SliceTemplate: "{{.kind"},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slicer, err := newSlicer(tt.opts)
			if tt.wantError {
				if err == nil {
					t.Fatal("want an error for a malformed template")
				}
				return
			}
			if err != nil {
				t.Fatalf("newSlicer: %v", err)
			}
			if tt.wantNil != (slicer == nil) {
				t.Errorf("slicer == nil is %v, want %v", slicer == nil, tt.wantNil)
			}
		})
	}
}

func TestGetManifestsDirPriority(t *testing.T) {
	const env = "FLUX_TOOLS_GENERATED_MANIFESTS_DIR"

	t.Run("flag wins over env", func(t *testing.T) {
		t.Setenv(env, "/from/env")
		if got := getManifestsDir("/from/flag"); got != "/from/flag" {
			t.Errorf("got %q, want the flag value", got)
		}
	})

	t.Run("env is used when the flag is empty", func(t *testing.T) {
		t.Setenv(env, "/from/env")
		if got := getManifestsDir(""); got != "/from/env" {
			t.Errorf("got %q, want the env value", got)
		}
	})

	t.Run("default when neither is set", func(t *testing.T) {
		t.Setenv(env, "")
		if got := getManifestsDir(""); got != "./cluster-manifests" {
			t.Errorf("got %q, want the default", got)
		}
	})
}

func TestWriteClusterManifests(t *testing.T) {
	objects := []*unstructured.Unstructured{
		{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]any{"name": "first", "namespace": "default"},
		}},
		{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata":   map[string]any{"name": "second", "namespace": "default"},
		}},
	}

	t.Run("yaml holds every object", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "out.yaml")
		if err := writeClusterManifests(path, objects, build.OutputFormatYAML); err != nil {
			t.Fatalf("writeClusterManifests: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		for _, want := range []string{"kind: ConfigMap", "kind: Secret", "name: first", "name: second"} {
			if !strings.Contains(string(data), want) {
				t.Errorf("missing %q in:\n%s", want, data)
			}
		}
	})

	t.Run("json holds every object", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "out.json")
		if err := writeClusterManifests(path, objects, build.OutputFormatJSON); err != nil {
			t.Fatalf("writeClusterManifests: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if !strings.Contains(string(data), `"ConfigMap"`) || !strings.Contains(string(data), `"Secret"`) {
			t.Errorf("json output lost an object:\n%s", data)
		}
	})

	t.Run("an unwritable path is an error, not a panic", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "no-such-dir", "out.yaml")
		if err := writeClusterManifests(path, objects, build.OutputFormatYAML); err == nil {
			t.Fatal("want an error for a path whose directory does not exist")
		}
	})
}
