// Package helm is the Helm integration for Flux HelmRelease resources: chart
// resolution, helm template, values assembly and a persistent render cache.
//
// # Cache layout
//
// The cache directory comes from --cache-dir, then FLUX_TOOLS_CACHE_DIR, then
// ~/.flux-tools/cache (or /tmp/flux-tools when HOME is unavailable).
//
//	{cacheDir}/
//	├── cache/               HELM_CACHE_HOME: repository indexes
//	├── config/              HELM_CONFIG_HOME: repositories.yaml
//	├── data/                HELM_DATA_HOME: plugins
//	├── charts/              pulled chart .tgz files
//	├── git/                 clones of external GitRepository sources
//	├── template-cache/      rendered charts keyed by sha256 of every input
//	└── repo-metadata.json   TTL cache of added repositories
//
// Helm is pointed at the cache with HELM_CACHE_HOME, HELM_CONFIG_HOME and
// HELM_DATA_HOME, so the user's own Helm configuration is never touched.
//
// # Chart sources
//
// How a chart is resolved depends on what the HelmRelease points at:
//
//   - HelmRepository over HTTP: pulled into charts/ by helm pull, then
//     templated from the .tgz.
//   - HelmRepository over OCI: helm registry login when a dockerconfigjson
//     Secret is present, then helm pull.
//   - GitRepository that is this checkout: the chart directory under the
//     repository root.
//   - GitRepository declared elsewhere: cloned into git/ behind a global
//     singleflight, and the chart directory is taken inside the clone.
//   - spec.chartRef to an ExternalArtifact: the directory named by the
//     producing ArtifactGenerator, relative to the repository root.
//
// Only renders from .tgz charts are stored in template-cache/, because a chart
// taken from the working tree has no stable version to key on.
//
// # Usage
//
//	client, err := helm.NewClient(cacheDir, helmTimeout)
//	client.SetRootPath(repoRoot)
//	client.RegisterRepository(repo)
//
//	values, err := helm.NewValuesBuilder().BuildValues(release)
//	objects, err := client.Template(ctx, release, values)
//
// External commands go through [github.com/alexandervidyaev/flux-tools/pkg/exec.CommandRunner];
// tests inject a mock runner through the runner field.
package helm
