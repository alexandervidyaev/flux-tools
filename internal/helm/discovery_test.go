package helm

import (
	"errors"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

func helmRelease(ns, name, chart, refKind, refName, refNS string) *types.HelmRelease {
	hr := &types.HelmRelease{}
	hr.Namespace = ns
	hr.Name = name
	hr.Spec.Chart.Spec.Chart = chart
	hr.Spec.Chart.Spec.Version = "1.0.0"
	hr.Spec.Chart.Spec.SourceRef = types.CrossNamespaceObjectReference{Kind: refKind, Name: refName, Namespace: refNS}
	return hr
}

func TestExtractChartRefHonoursSourceRefNamespace(t *testing.T) {
	repo := &types.HelmRepository{}
	repo.Namespace = "flux-system"
	repo.Name = "podinfo"
	repo.Spec.URL = "https://stefanprodan.github.io/podinfo"
	repos := map[string]*types.HelmRepository{repo.GetKey(): repo}

	// HelmRelease in another namespace, sourceRef points at flux-system.
	ref, err := extractChartRef(helmRelease("podinfo", "podinfo", "podinfo", "HelmRepository", "podinfo", "flux-system"), repos)
	if err != nil {
		t.Fatalf("extractChartRef: %v", err)
	}
	if ref.Repository != repo.Spec.URL || ref.Chart != "podinfo" {
		t.Errorf("ref = %+v", ref)
	}

	// No namespace on sourceRef: the HelmRelease's own namespace, where there
	// is no such repository.
	_, err = extractChartRef(helmRelease("podinfo", "podinfo", "podinfo", "HelmRepository", "podinfo", ""), repos)
	if err == nil {
		t.Error("expected a lookup in podinfo/podinfo to fail")
	}
}

func TestExtractChartRefSkipsGitRepositoryCharts(t *testing.T) {
	_, err := extractChartRef(helmRelease("apps", "local-app", "./charts/local-app", "GitRepository", "flux-system", "flux-system"), nil)
	if !errors.Is(err, errNoChartToPull) {
		t.Errorf("err = %v, want errNoChartToPull", err)
	}
}
