package helm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexandervidyaev/flux-tools/pkg/kustomize"
	"github.com/alexandervidyaev/flux-tools/pkg/types"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// TemplateOptions contains options for helm template command
type TemplateOptions struct {
	ReleaseName             string
	Namespace               string
	Values                  map[string]interface{}
	ValuesFiles             []string
	SkipCRDs                bool
	SkipTests               bool
	IncludeCRDs             bool
	DisableSchemaValidation bool
	PostRenderers           []types.PostRenderer
}

// HasChartInCache reports whether the chart .tgz is already in the cache —
// then Template resolves it locally and needs no repository access at all.
func (c *Client) HasChartInCache(chartName, version string) bool {
	return c.getChartFromCache(chartName, version) != ""
}

// getChartFromCache checks if a chart exists in the cache and returns its path
func (c *Client) getChartFromCache(chartName, version string) string {
	if version == "" {
		return ""
	}

	chartPath := filepath.Join(c.cacheDir, "charts", fmt.Sprintf("%s-%s.tgz", chartName, version))

	if _, err := os.Stat(chartPath); err == nil {
		return chartPath
	}

	return ""
}

// Template executes helm template for a HelmRelease
// templateOptions derives what Flux implies for a render: the release name is
// "[targetNamespace-]name" unless the HelmRelease pins one, the namespace falls
// back to the HelmRelease's own, and either install or upgrade opting out of
// schema validation disables it, since which action would run is unknowable
// here.
func templateOptions(hr *types.HelmRelease, valuesData map[string]interface{}) TemplateOptions {
	releaseName := hr.Spec.ReleaseName
	if releaseName == "" {
		releaseName = hr.Name
		if hr.Spec.TargetNamespace != "" {
			releaseName = hr.Spec.TargetNamespace + "-" + hr.Name
		}
	}

	namespace := hr.Spec.TargetNamespace
	if namespace == "" {
		namespace = hr.Namespace
	}

	return TemplateOptions{
		ReleaseName: releaseName,
		Namespace:   namespace,
		Values:      valuesData,
		SkipTests:   true,
		DisableSchemaValidation: (hr.Spec.Install != nil && hr.Spec.Install.DisableSchemaValidation) ||
			(hr.Spec.Upgrade != nil && hr.Spec.Upgrade.DisableSchemaValidation),
		PostRenderers: hr.Spec.PostRenderers,
	}
}

func (c *Client) Template(ctx context.Context, hr *types.HelmRelease, valuesData map[string]interface{}) ([]*unstructured.Unstructured, error) {
	chartSpec := hr.Spec.Chart.Spec
	sourceRef := chartSpec.SourceRef
	options := templateOptions(hr, valuesData)

	// ChartRef — the chart is carried by a source object instead of being
	// pulled from a repository. Only ExternalArtifact can be resolved offline:
	// it is carved by an ArtifactGenerator out of this very repository, so the
	// chart directory is already on disk.
	if hr.Spec.ChartRef != nil {
		if chartSpec.Chart != "" {
			return nil, fmt.Errorf(
				"HelmRelease %s/%s sets both spec.chart and spec.chartRef, which are mutually exclusive",
				hr.Namespace, hr.Name,
			)
		}
		return c.templateChartRef(ctx, hr, options)
	}

	// GitRepository — the chart lives in a Git source: this checkout when the
	// source is the flux-system sync GitRepository, a clone of the declared
	// GitRepository otherwise (see ResolveGitSourcePath).
	if sourceRef.Kind == "GitRepository" {
		refKey := hr.ChartSourceKey()
		basePath, err := c.ResolveGitSourcePath(ctx, refKey)
		if err != nil {
			return nil, fmt.Errorf("HelmRelease %s/%s: failed to resolve GitRepository %s: %w", hr.Namespace, hr.Name, refKey, err)
		}
		basePathLabel := "current repository"
		if basePath != c.rootPath {
			if gitRepo, ok := c.GetGitRepository(refKey); ok {
				basePathLabel = fmt.Sprintf("clone of %s", gitRepo.Spec.URL)
			}
		}

		chartPath := chartSpec.Chart
		if !filepath.IsAbs(chartPath) {
			chartPath = filepath.Join(basePath, chartPath)
		}
		if _, err := os.Stat(chartPath); err != nil {
			return nil, fmt.Errorf("HelmRelease %s/%s: chart path %q (resolved against %s) does not exist: %w", hr.Namespace, hr.Name, chartPath, basePathLabel, err)
		}

		valuesFiles, err := c.resolveValuesFiles(hr, basePath, basePathLabel)
		if err != nil {
			return nil, err
		}
		options.ValuesFiles = valuesFiles

		return c.templateLocalChart(ctx, chartPath, options)
	}

	if len(chartSpec.ValuesFiles) > 0 {
		return nil, fmt.Errorf(
			"HelmRelease %s/%s: spec.chart.spec.valuesFiles is currently only supported for GitRepository sources (got %s); "+
				"either inline the values via spec.values / spec.valuesFrom, or switch the chart source to a GitRepository",
			hr.Namespace, hr.Name, sourceRef.Kind,
		)
	}

	repoKey := hr.ChartSourceKey()

	repo, exists := c.GetRepository(repoKey)
	if !exists {
		return nil, fmt.Errorf("repository %s not found for HelmRelease %s/%s", repoKey, hr.Namespace, hr.Name)
	}

	return c.templateChart(ctx, repo, chartSpec.Chart, chartSpec.Version, options)
}

// templateChartRef templates a HelmRelease whose chart comes from
// spec.chartRef. The chart directory is resolved through the
// ArtifactGenerator that produces the referenced ExternalArtifact: its copy
// source is a path inside the Flux source, which on disk is rootPath — the
// directory that gets pushed as the OCI artifact. A mismatch between the two
// surfaces as a missing chart directory naming the resolved path.
//
// Local charts deliberately bypass the template cache, exactly like the
// GitRepository path: their content travels with the commit rather than with
// a chart version, so there is no stable key to cache on.
func (c *Client) templateChartRef(ctx context.Context, hr *types.HelmRelease, options TemplateOptions) ([]*unstructured.Unstructured, error) {
	ref := hr.Spec.ChartRef

	if ref.Kind != "ExternalArtifact" {
		return nil, fmt.Errorf(
			"HelmRelease %s/%s takes its chart from spec.chartRef of kind %q; only ExternalArtifact is supported, because it is the only kind whose content is available on disk",
			hr.Namespace, hr.Name, ref.Kind,
		)
	}

	namespace := ref.Namespace
	if namespace == "" {
		namespace = hr.Namespace
	}
	key := types.GetObjectKey(namespace, ref.Name)

	subPath, resolveErr, exists := c.GetExternalArtifactPath(key)
	if !exists {
		return nil, fmt.Errorf(
			"HelmRelease %s/%s references ExternalArtifact %s, but no ArtifactGenerator among the built manifests produces an artifact with that name",
			hr.Namespace, hr.Name, key,
		)
	}
	if resolveErr != nil {
		return nil, fmt.Errorf("HelmRelease %s/%s references ExternalArtifact %s: %w", hr.Namespace, hr.Name, key, resolveErr)
	}

	if c.rootPath == "" {
		return nil, fmt.Errorf(
			"HelmRelease %s/%s references ExternalArtifact %s, but the source root path is not set, so %q cannot be resolved",
			hr.Namespace, hr.Name, key, subPath,
		)
	}

	chartPath := filepath.Join(c.rootPath, subPath)
	if _, err := os.Stat(chartPath); err != nil {
		return nil, fmt.Errorf(
			"HelmRelease %s/%s: chart directory %q for ExternalArtifact %s does not exist: %w",
			hr.Namespace, hr.Name, chartPath, key, err,
		)
	}

	return c.templateLocalChart(ctx, chartPath, options)
}

// resolveValuesFiles resolves HelmRelease.spec.chart.spec.valuesFiles paths
// against the chart source basePath and returns absolute filesystem paths
// suitable for `helm template --values`.
//
// Per Flux v2 semantics, valuesFiles entries are paths relative to the
// SourceRef root. For GitRepository sources, that is the (cloned or current)
// repository working tree referenced as basePath. Absolute paths are rejected
// to avoid leaking out of the source artifact.
//
// If a path still contains an unresolved ${var} placeholder, this is treated
// as a configuration error: the parent Kustomization must declare the
// matching postBuild.substitute key for the substitution to take effect
// before the HelmRelease reaches helm templating.
func (c *Client) resolveValuesFiles(hr *types.HelmRelease, basePath, basePathLabel string) ([]string, error) {
	raw := hr.Spec.Chart.Spec.ValuesFiles
	if len(raw) == 0 {
		return nil, nil
	}
	if basePath == "" {
		return nil, fmt.Errorf("HelmRelease %s/%s declares valuesFiles but no chart source basePath could be resolved", hr.Namespace, hr.Name)
	}

	resolved := make([]string, 0, len(raw))
	for _, vf := range raw {
		vf = strings.TrimSpace(vf)
		if vf == "" {
			continue
		}
		if strings.Contains(vf, "${") {
			return nil, fmt.Errorf(
				"HelmRelease %s/%s: valuesFile path %q contains an unresolved ${...} placeholder; "+
					"ensure the parent Kustomization defines the corresponding postBuild.substitute key",
				hr.Namespace, hr.Name, vf,
			)
		}
		if filepath.IsAbs(vf) {
			return nil, fmt.Errorf(
				"HelmRelease %s/%s: valuesFile path %q is absolute; expected a relative path inside the SourceRef",
				hr.Namespace, hr.Name, vf,
			)
		}
		abs := filepath.Join(basePath, filepath.Clean(vf))
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf(
				"HelmRelease %s/%s: valuesFile %q (resolved to %s, against %s) not found: %w",
				hr.Namespace, hr.Name, vf, abs, basePathLabel, err,
			)
		}
		resolved = append(resolved, abs)
	}
	return resolved, nil
}

// templateLocalChart renders a local chart by its absolute filesystem path.
// Used for charts from GitRepository sources that live in the current repo.
//
// Deliberately NOT covered by the persistent template cache (I-3): the chart
// is a plain directory whose content is not versioned, so a sound cache key
// would have to hash every file of the chart tree — expensive and fragile.
// See templatecache.go for the full caching policy.
func (c *Client) templateLocalChart(ctx context.Context, chartPath string, options TemplateOptions) ([]*unstructured.Unstructured, error) {
	args := []string{
		"template",
		options.ReleaseName,
		chartPath,
	}

	if options.Namespace != "" {
		args = append(args, "--namespace", options.Namespace)
	}

	// Order matters: helm merges multiple --values files in declaration order
	// (later overrides earlier). Per Flux v2 semantics, chart.spec.valuesFiles
	// has the lowest precedence among user-supplied values, while inline
	// spec.values and spec.valuesFrom have the highest. valuesFiles therefore
	// come first; the merged inline+valuesFrom temp file comes last.
	for _, vf := range options.ValuesFiles {
		args = append(args, "--values", vf)
	}

	if len(options.Values) > 0 {
		valuesFile, err := c.writeValuesToTempFile(options.Values)
		if err != nil {
			return nil, fmt.Errorf("failed to write values file: %w", err)
		}
		defer os.Remove(valuesFile)
		args = append(args, "--values", valuesFile)
	}

	if options.SkipTests {
		args = append(args, "--skip-tests")
	}

	if options.IncludeCRDs {
		args = append(args, "--include-crds")
	}

	if options.DisableSchemaValidation {
		args = append(args, "--skip-schema-validation")
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.timeout)*time.Second)
	defer cancel()

	output, err := c.runner.Run(ctx, c.helmBin, args...)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("helm template timed out after %d seconds for %s", c.timeout, chartPath)
		}
		return nil, fmt.Errorf("helm template failed for local chart %s: %w", chartPath, err)
	}

	if len(options.PostRenderers) > 0 {
		output, err = c.applyPostRenderers(ctx, output, options.PostRenderers)
		if err != nil {
			return nil, fmt.Errorf("post-render failed for local chart %s: %w", chartPath, err)
		}
	}

	return kustomize.ParseKustomizeOutput(output)
}

// templateChart executes helm template for a chart
func (c *Client) templateChart(ctx context.Context, repo *types.HelmRepository, chartName, version string, options TemplateOptions) ([]*unstructured.Unstructured, error) {
	chartPath := c.getChartFromCache(chartName, version)

	cached, cacheKey := c.lookupTemplateCache(ctx, chartPath, chartName, version, options)
	if cached != nil {
		return cached, nil
	}

	chartRef, err := c.resolveChartRef(ctx, repo, chartName, chartPath)
	if err != nil {
		return nil, err
	}

	args, cleanup, err := c.templateArgs(chartRef, version, options)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.timeout)*time.Second)
	defer cancel()

	output, err := c.runner.Run(ctx, c.helmBin, args...)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("helm template timed out after %d seconds for %s", c.timeout, chartRef)
		}
		return nil, fmt.Errorf("helm template failed for %s: %w", chartRef, err)
	}

	if len(options.PostRenderers) > 0 {
		output, err = c.applyPostRenderers(ctx, output, options.PostRenderers)
		if err != nil {
			return nil, fmt.Errorf("post-render failed for %s: %w", chartRef, err)
		}
	}

	// Cache the final result (after postRenderers): the key already includes
	// the postRenderers spec, so a hit skips both helm and kustomize execs.
	if cacheKey != "" {
		c.templateCacheStore(cacheKey, output)
	}

	return kustomize.ParseKustomizeOutput(output)
}

// lookupTemplateCache returns the cached render and the key to store a fresh
// one under. Only renders from the locally cached .tgz are cached — the archive
// content is uniquely identified by chart name + version. Renders by repository
// reference (chartPath == "") are NOT cached: the source is not pinned to a
// concrete artifact. An empty key means "do not cache". See templatecache.go
// for the full policy and the key composition.
func (c *Client) lookupTemplateCache(ctx context.Context, chartPath, chartName, version string, options TemplateOptions) ([]*unstructured.Unstructured, string) {
	if chartPath == "" || !c.templateCacheIsEnabled() {
		return nil, ""
	}

	cacheKey, err := c.templateCacheKey(ctx, chartName, version, options)
	if err != nil {
		return nil, ""
	}

	if data, ok := c.templateCacheLookup(cacheKey); ok {
		// The cached value is the raw YAML after postRenderers.
		// A corrupt entry parses to zero objects and falls through
		// to a normal render (miss), never to an error.
		if objects, parseErr := kustomize.ParseKustomizeOutput(data); parseErr == nil && len(objects) > 0 {
			c.countTemplateCacheHit()
			return objects, cacheKey
		}
	}

	c.countTemplateCacheMiss()
	return nil, cacheKey
}

// resolveChartRef turns a chart into the reference helm template accepts: the
// cached archive path when there is one, an OCI URL, or a repository-qualified
// name, adding and refreshing the repository first when it was not known yet.
func (c *Client) resolveChartRef(ctx context.Context, repo *types.HelmRepository, chartName, chartPath string) (string, error) {
	if chartPath != "" {
		return chartPath, nil
	}

	if repo.IsOCI() {
		return fmt.Sprintf("%s/%s", repo.Spec.URL, chartName), nil
	}

	added, err := c.EnsureRepositoryAdded(ctx, repo)
	if err != nil {
		return "", fmt.Errorf("failed to add repository %s: %w", repo.Name, err)
	}
	if added {
		c.printer.Info("  → Adding Helm repository: %s (%s)\n", repo.Name, repo.Spec.URL)
		if err := c.UpdateRepositories(ctx); err != nil {
			return "", fmt.Errorf("failed to update repositories: %w", err)
		}
	}

	return fmt.Sprintf("%s/%s", repo.Name, chartName), nil
}

// templateArgs assembles the helm template argument list. The returned cleanup
// removes the temporary values file and must not run before helm has read it.
func (c *Client) templateArgs(chartRef, version string, options TemplateOptions) ([]string, func(), error) {
	cleanup := func() {}

	args := []string{
		"template",
		options.ReleaseName,
		chartRef,
	}

	if version != "" {
		args = append(args, "--version", version)
	}

	if options.Namespace != "" {
		args = append(args, "--namespace", options.Namespace)
	}

	// See templateLocalChart for the rationale on flag ordering: valuesFiles
	// first (lowest precedence), inline+valuesFrom temp file last.
	for _, vf := range options.ValuesFiles {
		args = append(args, "--values", vf)
	}

	if len(options.Values) > 0 {
		valuesFile, err := c.writeValuesToTempFile(options.Values)
		if err != nil {
			return nil, cleanup, fmt.Errorf("failed to write values file: %w", err)
		}
		cleanup = func() { os.Remove(valuesFile) }
		args = append(args, "--values", valuesFile)
	}

	if options.SkipTests {
		args = append(args, "--skip-tests")
	}

	if options.IncludeCRDs {
		args = append(args, "--include-crds")
	}

	if options.DisableSchemaValidation {
		args = append(args, "--skip-schema-validation")
	}

	return args, cleanup, nil
}

// writeValuesToTempFile writes values to a temporary YAML file
func (c *Client) writeValuesToTempFile(values map[string]interface{}) (string, error) {
	tmpFile, err := os.CreateTemp(c.cacheDir, "values-*.yaml")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer tmpFile.Close()

	yamlData, err := yaml.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("failed to marshal values: %w", err)
	}

	if _, err := tmpFile.Write(yamlData); err != nil {
		return "", fmt.Errorf("failed to write values file: %w", err)
	}

	return tmpFile.Name(), nil
}
