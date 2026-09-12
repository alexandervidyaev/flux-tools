# Command reference

Every command, flag and default. For the big picture see the [README](../README.md).

## Global flags

| Flag | Description | Default |
|---|---|---|
| `--timeout` | Timeout for the whole run in seconds. When it expires every subprocess (kustomize, helm, git, ...) is cancelled | `0` (none) |

Ctrl+C and SIGTERM cancel every running subprocess the same way.

---

## test

Validates the Flux resources of a cluster: every Kustomization builds, and with `--enable-helm` every HelmRelease templates.

Clusters run in parallel, including helm mode: charts are pre-pulled before the fan-out and templating works from the local cache without touching repositories.

```
flux-tools test [path...]
```

**Flags:**

| Flag | Short | Description | Default |
|---|---|---|---|
| `--enable-helm` | | Process HelmRelease resources | `false` |
| `--verbose` | `-v` | Verbose output | `false` |
| `--strict` | | Strict validation | `false` |
| `--flux-workdir` | | Repository root that `spec.path` resolves against; see [repository layout](repository-layout.md) | `.` |
| `--cluster-marker` | | Extra file or directory name marking a cluster entry, added to `flux-system/` | none |
| `--sequential` | | Force sequential execution (an escape hatch for parallel helm mode) | `false` |
| `--skip-failed-charts` | | Skip charts that fail to template | `false` |
| `--skip-oci` | | Skip Kustomizations with an OCIRepository source instead of failing | `false` |
| `--cache-dir` | | Cache directory | see [caching](configuration.md#caching) |
| `--helm-timeout` | | Timeout of Helm operations in seconds | `300` |
| `--skip-helm-pull` | | Do not pull charts automatically (with `--enable-helm`) | `false` |
| `--no-template-cache` | | Disable the persistent `helm template` cache | `false` |
| `--junit-report` | | Path of a JUnit XML report | not written |

**Examples:**

```bash
# One cluster
flux-tools test clusters/dev/kube-dev-1 --enable-helm

# Every cluster of an environment, in parallel
flux-tools test clusters/dev --enable-helm

# Strict, verbose
flux-tools test clusters/dev --strict -v

# JUnit report for the Tests tab of a merge request
flux-tools test clusters/dev --enable-helm --junit-report report.xml
```

JUnit report: testsuite = cluster, testcase = Kustomization/HelmRelease, failure = the error text. It is written even when tests fail, before the non-zero exit. A GitLab job:

```yaml
test:
  script:
    - flux-tools test clusters/$ENVIRONMENT --junit-report report.xml
  artifacts:
    when: always
    reports:
      junit: report.xml
```

---

## build

Renders every Kubernetes manifest of the given clusters at once: every Kustomization is built, every HelmRelease is templated. There is no per-object build; the cluster is the unit. The result is one file per cluster, or a tree of files in sliced mode.

After the summary a stage timing line is always printed, for example
`Stages: discovery 199ms | kustomize 297ms | helm 6.3s | substitute/filter 0s | serialize 71ms`
(`-v` breaks it down per line).

```
flux-tools build [path...]
```

**Flags:**

| Flag | Short | Description | Default |
|---|---|---|---|
| `--enable-helm` | | Process HelmRelease resources | `false` |
| `--verbose` | `-v` | Verbose output | `false` |
| `--strict` | | Strict mode | `false` |
| `--skip-crds` | | Drop CRDs from the result | `false` |
| `--skip-secrets` | | Drop Secrets from the result | `false` |
| `--skip-flux-system` | | Drop flux-system objects | `false` |
| `--skip-failed-charts` | | Skip charts that fail | `false` |
| `--skip-oci` | | Skip Kustomizations with an OCIRepository source instead of failing | `false` |
| `--skip-kind` | | Drop objects of this kind, case-insensitive (repeatable) | |
| `--flux-workdir` | | Repository root that `spec.path` resolves against; see [repository layout](repository-layout.md) | `.` |
| `--cluster-marker` | | Extra file or directory name marking a cluster entry, added to `flux-system/` | none |
| `--output` | `-o` | Output format: `yaml`, `json`, `sliced` | `yaml` |
| `--slice-template` | | File name template for `--output sliced`, kubectl-slice compatible (fields `.kind`, `.apiVersion`, `.metadata.name`, `.metadata.namespace`; functions `lower`, `dottodash`) | `Namespace:{{.metadata.namespace\|lower}}/Kind:{{.kind\|lower}}/Name:{{.metadata.name\|dottodash}}.yaml` |
| `--output-dir` | | Output directory | `./cluster-manifests` |
| `--cache-dir` | | Cache directory | see [caching](configuration.md#caching) |
| `--helm-timeout` | | Timeout of Helm operations in seconds | `300` |
| `--skip-helm-pull` | | Do not pull charts automatically | `false` |
| `--no-template-cache` | | Disable the persistent `helm template` cache | `false` |
| `--concurrency` | `-j` | Parallel builds | number of clusters |
| `--substitute` | | Post-build substitution variable `key=value` (repeatable), applied to every rendered object | |
| `--allow-missing-path` | | A missing input path produces an empty result instead of an error (for a diff baseline that does not exist yet) | `false` |
| `--cpuprofile` | | Write a CPU profile to this file (`go tool pprof`) | not written |

**Output directory precedence** (highest first):
1. `--output-dir`
2. `FLUX_TOOLS_GENERATED_MANIFESTS_DIR`
3. `./cluster-manifests`

**Sliced mode.** With `--output sliced` every object is written to its own file at
`<output-dir>/<cluster>/<result of --slice-template>` instead of one `<cluster>.yaml`. Each file is one YAML document without a leading `---`; the paths are identical to what kubectl-slice produces for the same objects. Two objects mapping to the same path fail the cluster build. Together with `--skip-kind` this replaces the `kubectl-slice` step in CI with one build call and no second YAML parse.

**Examples:**

```bash
# One cluster
flux-tools build clusters/dev/kube-dev-1 --enable-helm
# Result: ./cluster-manifests/kube-dev-1.yaml

# An environment
flux-tools build clusters/dev --enable-helm
# Result: ./cluster-manifests/*.yaml

# Custom output directory
flux-tools build clusters/dev --enable-helm --output-dir /tmp/manifests

# Without CRDs and Secrets, as JSON
flux-tools build clusters/dev --skip-crds --skip-secrets -o json

# Limit parallelism
flux-tools build clusters/dev --enable-helm -j 4

# Drop HelmRelease objects and split into files
# (the tree is kubectl-slice compatible, so that CI step is not needed)
flux-tools build clusters/dev --enable-helm \
  --skip-kind HelmRelease \
  --output sliced
# Result: ./cluster-manifests/<cluster>/Namespace:<ns>/Kind:<kind>/Name:<name>.yaml

# Profile a slow build
flux-tools build clusters/stable --enable-helm --cpuprofile cpu.prof
go tool pprof cpu.prof
```

---

## helm-pull

Pre-downloads Helm charts. Discovers every HelmRelease in the clusters and pulls every chart they use into the cache. Charts are deduplicated: each is downloaded once.

```
flux-tools helm-pull [path...]
```

**Flags:**

| Flag | Short | Description | Default |
|---|---|---|---|
| `--verbose` | `-v` | Verbose output (discovery runs sequentially) | `false` |
| `--dry-run` | | Show what would be pulled | `false` |
| `--force` | | Re-download even when the chart is cached | `false` |
| `--helm-cache-dir` | | Cache directory | see [caching](configuration.md#caching) |
| `--helm-timeout` | | Timeout of Helm operations in seconds | `300` |
| `--concurrency` | `-j` | Parallel downloads | `3` |
| `--flux-workdir` | | Repository root that `spec.path` resolves against; see [repository layout](repository-layout.md) | `.` |
| `--cluster-marker` | | Extra file or directory name marking a cluster entry, added to `flux-system/` | none |
| `--force-repo-update` | | Force a repository index update | `false` |
| `--repo-ttl` | | Repository metadata cache lifetime in minutes | `60` |

**Examples:**

```bash
flux-tools helm-pull clusters/dev/kube-dev-1
flux-tools helm-pull clusters/
flux-tools helm-pull clusters/dev --dry-run
flux-tools helm-pull clusters/ --force -j 10
```

HelmReleases whose chart comes from `spec.chartRef` are skipped: there is nothing to pull.

---

## kubeconform

Validates manifests against JSON schemas. A wrapper around `kubeconform`; native flags are passed through.

Wrapping it is not about speed: kubeconform walks directories and parallelises on its own, and `-j` here only maps to its `-n` so the flag is spelled the same across these commands. What the wrapper adds is `--codequality-report`, which turns kubeconform's JSON into the GitLab Code Quality format, so a failure shows up in the merge request widget with the resource and the reason instead of only in a job log. kubeconform has no such output of its own. The report is written even when validation fails, which is exactly when it is wanted, and an empty `[]` when there is nothing to report.

Each finding carries the path of the rendered file it came from, not of a file in the repository, so GitLab lists the findings rather than annotating lines in the diff: what was validated is build output, which is not committed.

```
flux-tools kubeconform [path] [kubeconform flags...]
```

**Own flags:**

| Flag | Short | Description | Default |
|---|---|---|---|
| `--concurrency` | `-j` | Goroutines (mapped to kubeconform `-n`) | auto |
| `--codequality-report` | | Path of a GitLab Code Quality report; incompatible with a custom `-output` | not written |
| `--verbose` | `-v` | Verbose output | `false` |

Everything else (`-strict`, `-ignore-missing-schemas`, `-kubernetes-version`, `-schema-location`, `-summary`, `-output`, ...) goes to kubeconform unchanged.

**Examples:**

```bash
flux-tools kubeconform ./cluster-manifests/ -summary

flux-tools kubeconform ./cluster-manifests/ -summary -ignore-missing-schemas

flux-tools kubeconform ./cluster-manifests/ \
  -kubernetes-version 1.28.0 \
  -schema-location default \
  -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
  -skip CustomResourceDefinition \
  -summary \
  -output pretty

# Code Quality report: invalid manifests are highlighted in the merge request
flux-tools kubeconform ./cluster-manifests/ --codequality-report gl-codequality.json
```

With `--codequality-report` the command parses kubeconform's JSON output itself: a short summary goes to the console (invalid/error per file) and a [GitLab Code Quality](https://docs.gitlab.com/ee/ci/testing/code_quality.html#implement-a-custom-tool) report goes to the file (invalid = severity `major`, parse errors = `critical`). The report is written even when validation fails; zero findings give a valid empty report `[]`.

```yaml
validate:
  script:
    - flux-tools kubeconform ./cluster-manifests/ --codequality-report gl-codequality.json
  artifacts:
    when: always
    reports:
      codequality: gl-codequality.json
```

---

## yq

Parallel processing of YAML manifests with a yq filter. Every YAML file in the directory is processed concurrently.

Here the wrapping does buy speed: one `yq` invocation handles one file, so a directory of clusters otherwise needs a shell loop that runs them one after another. Three further things come with it. The filter can arrive in `YQ_FILTER`, which suits a pipeline that configures it centrally. Output goes to `<input>/cleaned/` rather than over the input, and files carrying `.clean.` in the name are skipped, so a second run never reads its own output back. And `--allow-missing-path` turns an absent input into an empty result, which is what the diff baseline of a new environment needs.

```
flux-tools yq eval-all '<filter>' [path]
flux-tools yq [path]                         # filter from YQ_FILTER
```

**Flags:**

| Flag | Short | Description | Default |
|---|---|---|---|
| `--concurrency` | `-j` | Parallel workers | number of CPUs |
| `--output-dir` | `-o` | Output directory | `<input>/cleaned/` |
| `--allow-missing-path` | | A missing or empty input creates the output directory and exits zero | `false` |
| `--verbose` | `-v` | Verbose output | `false` |

**Examples:**

```bash
# Drop HelmRelease objects
flux-tools yq eval-all 'select(.kind != "HelmRelease")' ./cluster-manifests/

# Drop caBundle from webhooks
flux-tools yq eval-all 'del(.webhooks[].clientConfig.caBundle)' -o ./processed ./cluster-manifests/

# Filter from the environment
YQ_FILTER='select(.kind != "HelmRelease")' flux-tools yq ./cluster-manifests/
```

Selecting by kind is cheaper with `build --skip-kind`, which avoids a second YAML parse. The yq step stays useful for `del(...)` of noisy fields (caBundle, checksum/rollme annotations).

---

## diff

Compares rendered manifests between two directories and reports added, deleted and modified resources per cluster. The unified diff is generated natively (a port of git's xdiff, byte-compatible with `git diff`; no git binary is run). Changes are sorted by (namespace, kind, name), so the report is stable between runs.

```
flux-tools diff <current-dir> <incoming-dir>
```

Either shape of `build` output works on each side:

- one directory per cluster holding a file per object, which is what `build --output sliced` writes;
- one `<cluster>.yaml` per cluster, which is what `build` writes by default. Such a side is sliced into the first shape in a temporary directory before the comparison, so no separate slicing step is needed.

The two sides may differ in shape. Comparison is per object either way, which is what makes added, deleted and modified meaningful.

**Flags:**

| Flag | Short | Description | Default |
|---|---|---|---|
| `--concurrency` | `-j` | Parallel workers | number of clusters |
| `--context` | `-U` | Context lines | `3` |
| `--output-dir` | `-o` | Directory for the report files, one set per cluster | |
| `--format` | | Output format: `console`, `markdown` | `console` |

With `-o` these files are written:
- `index.md`: summary of every cluster (metrics + a list with statuses)
- `<cluster>-diff.md`: the human-readable diff
- `<cluster>-diff.json`: the structured diff (used by `post-comment`, usable by other tools)

**Examples:**

```bash
flux-tools diff ./current ./incoming
flux-tools diff ./current ./incoming -o ./diffs/
flux-tools diff --context 20 ./current ./incoming -o ./diffs/
```

---

## post-comment

Publishes the diff reports to a GitLab merge request: first a summary comment from `index.md` (disabled with `--skip-summary` or `FLUX_TOOLS_SKIP_SUMMARY=true`), then one comment per cluster. Comments from the previous run are deleted first (found by a marker that includes the environment; every page of notes is read).

The diff directory must contain the artifacts of this binary's `diff` command: `<cluster>-diff.json` is required next to `<cluster>-diff.md`, because statistics and the compact format for large diffs are built from it.

```
flux-tools post-comment <diff-dir>
```

**Flags:**

| Flag | Description | Default |
|---|---|---|
| `--project-id` | GitLab project ID | `$CI_PROJECT_ID` |
| `--mr-iid` | Merge request IID | `$CI_MERGE_REQUEST_IID` |
| `--gitlab-url` | GitLab API URL | `$CI_API_V4_URL` or `https://gitlab.com/api/v4` |
| `--token` | GitLab API token | `$GITLAB_TOKEN` or `$CI_JOB_TOKEN` |
| `--environment` | Environment name | `$CI_ENVIRONMENT_NAME` or `$ENVIRONMENT` |
| `--workers` | Parallel workers | `3` |
| `--timeout` | HTTP timeout in seconds | `30` or `$GITLAB_TIMEOUT` |
| `--rate-limit` | Delay between posts in ms | `500` or `$GITLAB_RATE_LIMIT_MS` |
| `--delete-rate-limit` | Delay between deletes in ms | `200` or `$GITLAB_DELETE_RATE_LIMIT_MS` |
| `--skip-delete` | Keep the previous run's comments | `false` |
| `--skip-summary` | Do not post the summary comment | `false` or `$FLUX_TOOLS_SKIP_SUMMARY` |

Comments above the GitLab note limit (~65KB) fall back to a compact format: lists of added/deleted resources, full diffs only for modified ones, and a link to the job artifact.

**Examples:**

```bash
# In GitLab CI the variables are picked up automatically
flux-tools post-comment ./diffs/

# Locally
export CI_PROJECT_ID="12345"
export CI_MERGE_REQUEST_IID="42"
export GITLAB_TOKEN="glpat-xxxxxxxxxxxx"
flux-tools post-comment ./diffs/

flux-tools post-comment ./diffs/ --skip-delete
flux-tools post-comment ./diffs/ --skip-summary
```

GitHub pull requests are not supported yet, see [roadmap](../README.md#roadmap).

---
