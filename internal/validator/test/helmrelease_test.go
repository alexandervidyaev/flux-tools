package test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

func hrTestCase(t *testing.T, chart, srcName string) *HelmReleaseTestCase {
	t.Helper()
	hr := &types.HelmRelease{}
	hr.Namespace = "apps"
	hr.Name = "podinfo"
	hr.Spec.Chart.Spec.Chart = chart
	hr.Spec.Chart.Spec.Version = "1.0.0"
	hr.Spec.Chart.Spec.SourceRef = types.CrossNamespaceObjectReference{
		Kind: "HelmRepository",
		Name: srcName,
	}
	return &HelmReleaseTestCase{
		TestName:         "apps/podinfo",
		HelmRelease:      hr,
		HelmRepositories: map[string]*types.HelmRepository{},
		Options:          TestOptions{CacheDir: t.TempDir(), HelmTimeout: 30, NoTemplateCache: true},
	}
}

// Every failing path must produce a fully-populated result: the runner and the
// JUnit report read Name, Type, Path and Duration whether the case passed or
// not, so an early return that forgets one of them shows up as a blank row.
func TestHelmReleaseTestCaseValidationFailures(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not in PATH")
	}

	tests := []struct {
		name    string
		chart   string
		srcName string
		wantErr string
	}{
		{
			name:    "chart name is required",
			chart:   "",
			srcName: "charts",
			wantErr: "chart name is empty",
		},
		{
			name:    "sourceRef name is required",
			chart:   "podinfo",
			srcName: "",
			wantErr: "chart source ref name is empty",
		},
		{
			name:    "the referenced repository must be declared",
			chart:   "podinfo",
			srcName: "missing",
			wantErr: "helm repository not found: apps/missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := hrTestCase(t, tt.chart, tt.srcName).Run(context.Background())

			if res.Status != TestFailed {
				t.Fatalf("Status = %q, want %q", res.Status, TestFailed)
			}
			if res.Error == nil || !strings.Contains(res.Error.Error(), tt.wantErr) {
				t.Errorf("Error = %v, want it to contain %q", res.Error, tt.wantErr)
			}
			if res.Name != "apps/podinfo" {
				t.Errorf("Name = %q, want the test name", res.Name)
			}
			if res.Type != HelmReleaseTest {
				t.Errorf("Type = %q, want %q", res.Type, HelmReleaseTest)
			}
			// Path is namespace/name of the release, independent of TestName.
			if res.Path != "apps/podinfo" {
				t.Errorf("Path = %q", res.Path)
			}
			if res.Duration <= 0 {
				t.Error("Duration was not recorded on the failing path")
			}
			if res.Objects != 0 {
				t.Errorf("Objects = %d, want 0 on a failure", res.Objects)
			}
		})
	}
}

// A spec.chartRef release never reaches the chart/sourceRef validation: the
// chart is carried by a source object, so the release is templated from disk.
// With no ArtifactGenerator registered the resolution fails, which is what
// pins the chartRef branch as distinct from the chart-spec one.
func TestHelmReleaseTestCaseChartRefTakesItsOwnPath(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not in PATH")
	}

	tc := hrTestCase(t, "", "")
	tc.HelmRelease.Spec.ChartRef = &types.ChartRef{Kind: "ExternalArtifact", Name: "artifact"}

	res := tc.Run(context.Background())

	if res.Status != TestFailed {
		t.Fatalf("Status = %q, want %q", res.Status, TestFailed)
	}
	// Not the chart-spec validation error: the chartRef branch returned first.
	if res.Error == nil || strings.Contains(res.Error.Error(), "chart name is empty") {
		t.Errorf("Error = %v, want a chartRef resolution failure", res.Error)
	}
	if !strings.Contains(res.Error.Error(), "helm template failed") {
		t.Errorf("Error = %v, want it to come from templating", res.Error)
	}
	if res.Duration <= 0 {
		t.Error("Duration was not recorded")
	}
}
