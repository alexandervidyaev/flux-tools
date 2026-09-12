package test

import (
	"context"
	"fmt"
	"time"

	"github.com/alexandervidyaev/flux-tools/pkg/helm"
)

// Run executes the helm release test
func (t *HelmReleaseTestCase) Run(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{
		Name: t.TestName,
		Type: HelmReleaseTest,
		Path: t.HelmRelease.Namespace + "/" + t.HelmRelease.Name,
	}

	// Every terminal path stamps the duration: the runner and the JUnit report
	// read it whether the case passed or not.
	fail := func(err error) TestResult {
		result.Status = TestFailed
		result.Error = err
		result.Duration = time.Since(start)
		return result
	}

	// All three templating paths (chartRef, GitRepository, HelmRepository) end
	// the same way, so they share one terminal block.
	templated := func(count int, err error) TestResult {
		if err != nil {
			result.Status = TestFailed
			result.Error = fmt.Errorf("helm template failed: %w", err)
		} else {
			result.Status = TestPassed
			result.Objects = count
		}
		result.Duration = time.Since(start)
		return result
	}

	// In tests the cluster Secrets referenced via valuesFrom are not available,
	// so tolerant mode replaces a missing ref with a placeholder at its
	// targetPath instead of failing the case.
	tolerantValues := func() (map[string]interface{}, error) {
		vb := helm.NewValuesBuilder()
		vb.SetTolerateMissing(true)
		return vb.BuildValues(t.HelmRelease)
	}

	// Create Helm client
	helmClient, err := helm.NewClient(t.Options.CacheDir, t.Options.HelmTimeout)
	if err != nil {
		return fail(fmt.Errorf("failed to create helm client: %w", err))
	}

	// Persistent helm template cache (I-3): enabled where rendering actually
	// happens (here and in the builder), off with --no-template-cache.
	helmClient.SetTemplateCacheEnabled(!t.Options.NoTemplateCache)

	// Check helm installed
	if err := helmClient.CheckHelmInstalled(ctx); err != nil {
		return fail(fmt.Errorf("helm not installed: %w", err))
	}

	// Resolve chart reference
	chart := t.HelmRelease.Spec.Chart
	chartSpec := chart.Spec

	// ChartRef — the chart is carried by a source object. For an
	// ExternalArtifact the chart directory lives in this repository, so the
	// release can be templated offline once the producing ArtifactGenerators
	// are registered.
	if t.HelmRelease.Spec.ChartRef != nil {
		helmClient.SetRootPath(t.RootPath)
		for _, ag := range t.ArtifactGenerators {
			helmClient.RegisterArtifactGenerator(ag)
		}

		values, err := tolerantValues()
		if err != nil {
			return fail(fmt.Errorf("failed to build values: %w", err))
		}

		objects, err := helmClient.Template(ctx, t.HelmRelease, values)
		return templated(len(objects), err)
	}

	// Validate chart spec
	if chartSpec.Chart == "" {
		return fail(fmt.Errorf("chart name is empty"))
	}

	if chartSpec.SourceRef.Name == "" {
		return fail(fmt.Errorf("chart source ref name is empty"))
	}

	// GitRepository source: the chart is a directory in this checkout when the
	// GitRepository is the sync source, a clone otherwise. Same rule as the
	// builder (helm.Client.ResolveGitSourcePath).
	if chartSpec.SourceRef.Kind == "GitRepository" {
		helmClient.SetRootPath(t.RootPath)
		helmClient.SetSelfSource(t.SelfSource)
		for _, repo := range t.GitRepositories {
			helmClient.RegisterGitRepository(repo)
		}

		values, err := tolerantValues()
		if err != nil {
			return fail(fmt.Errorf("failed to build values: %w", err))
		}

		objects, err := helmClient.Template(ctx, t.HelmRelease, values)
		return templated(len(objects), err)
	}

	// HelmRepository / OCIRepository — standard path via a registered helm repo.
	repoKey := t.HelmRelease.ChartSourceKey()
	repo, exists := t.HelmRepositories[repoKey]
	if !exists {
		return fail(fmt.Errorf("helm repository not found: %s", repoKey))
	}

	// When the chart .tgz is already in the cache (charts are pre-pulled by
	// autoPullHelmCharts before the fan-out), Template resolves it locally and
	// repository setup is unnecessary — skipping it also makes the test safe
	// to run in parallel with other clusters sharing the helm config.
	if !helmClient.HasChartInCache(chartSpec.Chart, chartSpec.Version) {
		// Add repository
		if err := helmClient.AddRepository(ctx, repo); err != nil {
			return fail(fmt.Errorf("failed to add repository: %w", err))
		}

		// Update repositories if not OCI
		if !repo.IsOCI() {
			if err := helmClient.UpdateRepositories(ctx); err != nil {
				return fail(fmt.Errorf("failed to update repositories: %w", err))
			}
		}
	} else {
		// Template still needs the HelmRepository registered to distinguish
		// OCI from HTTP references
		helmClient.RegisterRepository(repo)
	}

	// Template the chart
	objects, err := helmClient.Template(ctx, t.HelmRelease, nil)
	return templated(len(objects), err)
}
