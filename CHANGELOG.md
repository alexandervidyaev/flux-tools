# Changelog

## 1.0.5

First public release. Everything below is what the tool does as of this version.

### Commands

- `test` validates Kustomization and HelmRelease resources of one cluster or a whole environment, clusters in parallel, and writes a JUnit report with `--junit-report`. `--sequential` falls back to one cluster at a time, `--skip-failed-charts` turns a template error into a skip instead of a failure, and `--skip-oci` passes over Kustomizations with an OCIRepository source.
- `build` renders every object of every cluster at once into one YAML/JSON file or a kubectl-slice compatible tree, with a persistent `helm template` cache and an in-run kustomize render cache. `--skip-kind` drops objects by kind and replaces the `yq 'select(.kind != ...)'` step of a pipeline, `--substitute` supplies post-build variables from outside the repository, and `--cpuprofile` writes a pprof profile.
- `helm-pull` pre-downloads charts, deduplicated across clusters, with a TTL cache of repository metadata.
- `kubeconform` and `yq` wrap the external tools and run them in parallel over the rendered manifests; `kubeconform` emits a GitLab Code Quality report.
- `diff` compares two `build` results with a native unified diff (a port of git xdiff, byte-compatible with `git diff`) and writes `index.md`, `<cluster>-diff.md` and `<cluster>-diff.json`. Either output shape works on each side: a sliced tree, or the default one file per cluster, which is sliced internally so no separate step is needed.
- `post-comment` posts the diff to a GitLab merge request: one summary comment plus one comment per cluster, replacing the previous run's comments. Every option falls back to the GitLab CI environment, and `--skip-summary` drops the summary comment.

### Flux resources

- HelmRelease sources: HelmRepository (HTTP and OCI), GitRepository (local and external), `spec.chartRef` to an ExternalArtifact produced by an ArtifactGenerator.
- `postBuild.substitute`, `substituteFrom`, `postRenderers`, `valuesFrom` and `disableSchemaValidation` are honoured.
- `helm-pull` and `test` look the chart's HelmRepository up by `spec.chart.spec.sourceRef.namespace`, as `build` does; a chart from a GitRepository is not something to pull.
- A Kustomization or HelmRelease is rendered from this checkout only when its `GitRepository` is the flux-system sync source; any other GitRepository is cloned from its declared URL, and an undeclared or uncloneable one fails the build instead of falling back to a local directory of the same path. Without `flux-system/` an undeclared GitRepository is the checkout itself.

### Repository layout

- No assumptions about repository layout. A cluster is a directory carrying a marker: `flux-system/` always, plus whatever `--cluster-marker` names; a path with no marker below it is one entry taken as given, which is how flux-operator layouts are built. `test`, `build` and `helm-pull` accept several paths.
- The repository root that `spec.path` resolves against is `--flux-workdir`, `.` by default. It is stated rather than inferred, because the root decides what every `spec.path` points at.
- An entry or `spec.path` without `kustomization.yaml` is rendered from a generated one following kustomize-controller's rules, honouring `.sourceignore`, so `flux bootstrap` repositories build as they are.
- Generated kustomizations reference manifests by real path, so a symlinked temporary directory (macOS `/var` to `/private/var`) does not break the build.
- An object rendered by more than one Kustomization appears once in the output, the last rendering winning.

### Running it

- `--allow-missing-path` on `build` and `yq` turns a missing input into an empty result, which is what a diff baseline of a new environment needs.
- A global `--timeout` bounds the whole run, and `build` reports per-stage timings on a `Stages:` line.
- Every external program runs through one command runner, and Helm is pointed at the tool's own cache directory through `HELM_CACHE_HOME`, `HELM_CONFIG_HOME` and `HELM_DATA_HOME`, so the user's Helm configuration is never touched.
- `flux-tools --version` reports the module version for a binary built with `go install module@version`, where no ldflags are injected.
