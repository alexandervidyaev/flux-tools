# Supported Flux features

| Resource / field | Behaviour |
|---|---|
| `Kustomization` | built with `kustomize build`, nested Kustomizations discovered recursively; `dependsOn` ordered; a `spec.path` without `kustomization.yaml` is built from a generated one, as kustomize-controller does |
| `Kustomization.spec.postBuild.substitute` / `substituteFrom` | applied after the build; ConfigMaps and Secrets found in the manifests are used as sources |
| `Kustomization` with an `OCIRepository` source | cannot be rendered offline: an error, or skipped with `--skip-oci` |
| `HelmRelease` + `HelmRepository` (HTTP) | chart pulled to the cache, `helm template` |
| `HelmRelease` + `HelmRepository` (OCI) | `helm registry login` with a `kubernetes.io/dockerconfigjson` Secret when one is referenced, then pull |
| `HelmRelease` + `GitRepository` | chart directory under the repository root when the source is this checkout, otherwise a clone of the declared GitRepository at its ref |
| `HelmRelease.spec.chartRef` → `ExternalArtifact` | chart directory resolved through the `ArtifactGenerator` (flux-operator source-watcher) that produces the artifact; other `chartRef` kinds are an error |
| `HelmRelease.spec.valuesFrom` | ConfigMaps and Secrets from the manifests, `valuesKey`, `targetPath`; `optional` references may be missing |
| `HelmRelease.spec.postRenderers` | kustomize patches and images applied to the templated output |
| `HelmRelease.spec.chart.spec.version` | passed to `helm pull --version`, so exact versions and semver ranges both work |
| `HelmRelease` `disableSchemaValidation` | passed to `helm template` |

---
