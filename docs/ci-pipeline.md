# Typical usage

How the commands compose locally and in a CI pipeline. Flags are described in the [command reference](commands.md).

## Local check before a commit

```bash
flux-tools test clusters/dev/kube-dev-1
flux-tools test clusters/dev/kube-dev-1 --enable-helm
```

## Full CI pipeline

```bash
# 1. Validate Flux resources (clusters in parallel) + JUnit for the Tests tab
flux-tools test clusters/dev --junit-report report.xml

# 2. Render manifests (charts are pulled automatically into the cache)
flux-tools build clusters/dev --enable-helm

# 3. Validate against Kubernetes schemas + Code Quality in the merge request
flux-tools kubeconform ./cluster-manifests/ -summary -ignore-missing-schemas \
  --codequality-report gl-codequality.json

# 4. Post-process when a field has to be dropped, not a whole kind
flux-tools yq eval-all 'del(.webhooks[].clientConfig.caBundle)' ./cluster-manifests/
```

Dropping whole kinds needs no yq step at all:

```bash
flux-tools build clusters/dev --enable-helm --skip-kind HelmRelease --output sliced --output-dir ./sliced
```

## The Helm cache in GitLab CI

`build --enable-helm` already fills the cache on its own: it pre-downloads every chart the clusters reference into `charts/`, and stores the rendered output of each one in `template-cache/`. Nothing has to be warmed up in a separate job. Keep that directory between pipelines and the second run does neither the download nor the templating.

Measured on one HelmRelease, same command twice:

| Run | What happened | `helm` stage | Wall clock |
| --- | --- | --- | --- |
| first, empty cache | chart downloaded, chart rendered | 95 ms | 1613 ms |
| second, cache restored | download skipped, render served from cache | 64 ms | 253 ms |

The rendered manifests are byte-identical. Both caches are per chart, so the saving grows with the number of HelmReleases: a fleet with dozens of releases spends most of a cold pipeline downloading and templating, and almost none of a warm one.

### Making GitLab actually keep it

Start with the constraint that silently wastes the most time: **GitLab archives only paths inside `$CI_PROJECT_DIR`.** The default cache directory is `~/.flux-tools/cache`, which is outside it, so a `cache:` block naming it caches nothing. The pipeline still passes, and every run downloads and re-templates every chart. Point the cache into the project first:

```yaml
variables:
  FLUX_TOOLS_CACHE_DIR: $CI_PROJECT_DIR/.flux-tools-cache

render:
  stage: build
  cache:
    # A fixed key, not $CI_COMMIT_REF_SLUG: charts are keyed by name and
    # version, so every branch can share one cache. A per-branch key makes the
    # first pipeline on every branch cold for no benefit.
    key: flux-tools-cache
    paths:
      - .flux-tools-cache/charts/
      - .flux-tools-cache/template-cache/
      - .flux-tools-cache/repo-metadata.json
  script:
    - flux-tools build clusters/ --enable-helm --output-dir ./incoming
  artifacts:
    paths: [incoming/]
```

A job that only reads the cache, such as one rendering the target branch for a diff, can add `policy: pull` so it does not re-upload an archive it did not change. When several jobs write the cache at once the last one to finish wins, which is harmless here: both caches are content-addressed, so a partial cache is still correct, only less complete.

### What to put in `paths`, and what to leave out

| Path | Cache it | Why |
| --- | --- | --- |
| `charts/` | yes | the pulled `.tgz` files, keyed by chart name and version. The largest saving |
| `template-cache/` | yes | rendered charts. The key covers chart, version, values, values files, postRenderers and the helm version, so a hit is always correct |
| `repo-metadata.json` | yes | a few bytes that let `helm repo update` be skipped within the TTL (`--repo-ttl`, 60 minutes) |
| `config/`, `cache/` | optional | Helm's own `repositories.yaml` and repository indexes. Small, and harmless to keep |
| `git/` | only with pinned refs | a clone is reused by (URL, ref) **without fetching**. With `spec.ref.tag` or `spec.ref.commit` that is correct forever; with `spec.ref.branch` a cached clone serves the commit from whenever it was first cloned |

The last row is the one that bites. If any GitRepository in the repository is followed by branch, leave `git/` out of the cache and let each pipeline clone it fresh.

A chart taken from a GitRepository is never stored in `template-cache/`: a working-tree chart has no version to key on. Those releases re-render every time, and that is expected.

### Checking that it actually works

```bash
flux-tools build clusters/ --enable-helm -v
# Pre-pull complete: Total: 12, Pulled: 0, Skipped: 12, Failed: 0
# template cache: 12 hits, 0 misses
# Stages: discovery 210ms | kustomize 180ms | helm 64ms | substitute/filter 0s | serialize 12ms
```

Two lines say the cache is alive: `Pulled: 0, Skipped: N` means the archives were restored, and `N hits, 0 misses` means the renders were. All misses on the second pipeline in a row means the cache is not being restored between jobs, and the usual cause is the first paragraph of this section.

`--skip-helm-pull` is for a pipeline that pulled the charts in an earlier job; on its own, with nothing in `charts/`, it disables both caches instead of saving time.

To rule the cache out while debugging a rendering difference, pass `--no-template-cache`; charts stay cached, only the rendered output is recomputed.

## Diff in a merge request

```bash
# On the feature branch:
flux-tools build clusters/dev --enable-helm --output-dir ./incoming

# On the target branch (or from artifacts):
flux-tools build clusters/dev --enable-helm --output-dir ./current

# Compare and publish (summary + one comment per cluster)
flux-tools diff -U 10 ./current ./incoming -o ./diffs/
flux-tools post-comment ./diffs/
```

With a persistent `FLUX_TOOLS_CACHE_DIR` the second build is almost free: charts and their rendered output come from the cache.

When the target branch does not contain the environment yet (a new environment), `--allow-missing-path` on `build` and `yq` turns the missing side into an empty tree, so the diff shows everything as added instead of the pipeline failing.


### What the reports look like

`diff` writes three kinds of file into the output directory: one `index.md` summary, and a `<cluster>-diff.md` plus a `<cluster>-diff.json` per cluster. Below is a real run over five clusters where one ConfigMap changed, one Deployment appeared and one ConfigMap was removed.

`index.md` is the map: which clusters to look at, and which to ignore.

````markdown
# 🔄 Changes Summary

| Metric | Value |
|--------|-------|
| Total clusters | 5 |
| Clusters with changes | 2 |
| Total changes | 3 |
| ➕ Added | 1 |
| ➖ Deleted | 1 |
| ⚙️ Modified | 1 |

## 📦 Clusters

- ✅ **cluster** - No changes
- ✅ **dev** - No changes
- ⚙️ **prod** - 2 changes
- ✅ **qa** - No changes
- ⚙️ **staging** - 1 changes
````

`prod-diff.md` groups by what happened to the object. An added or deleted object is shown whole; a modified one is a unified diff with `-U` lines of context. Every heading carries the full identity, so a resource is searchable by name in the merge request.

````markdown
### ➕ Added (1):

#### 1. Namespace:default | Kind:deployment | Name:web

```diff
+ apiVersion: apps/v1
+ kind: Deployment
+ metadata:
+   name: web
+   namespace: default
+ spec:
+   replicas: 2
```

### ⚙️ Modified (1):

#### 1. Namespace:default | Kind:configmap | Name:app-prod

```diff
 apiVersion: v1
 data:
-  who: app-prod
+  replicas: "3"
+  tier: frontend
+  who: app-prod-v2
 kind: ConfigMap
 metadata:
   name: app-prod
   namespace: default
```
````

A cluster that did not change still gets a file, so a missing report is a real failure rather than an empty diff:

````markdown
## ✅ No changes

All manifests are identical between branches.
````

The `.json` next to each report carries the same changes structurally: a `Resource` with namespace, kind and name, a `Status` of `ADDED`, `DELETED` or `MODIFIED`, and the raw unified diff. `post-comment` reads that file, never the markdown.

### What lands in the merge request

`post-comment` posts the summary first, then one comment per cluster **that changed** — clusters reporting no changes are skipped, so the merge request stays readable on a fleet of thirty.

The summary is `index.md` with one line prepended:

```
-- Diff summary for environment: **prod** --

# 🔄 Changes Summary
...
```

Each cluster comment puts the counts up front and folds the body away, so a reviewer scrolls a list of clusters rather than a wall of YAML:

```
-- Diff for environment: **prod** cluster: **prod** --

➕ Added: 1   ➖ Deleted: 0   ⚙️ Modified: 1

<details>
<summary>Click to see details</summary>

...the contents of prod-diff.md...

</details>
```

When a cluster's diff is too large for a GitLab comment, that comment is replaced by a compact one: added and deleted objects become a plain list of names, modified objects keep their diffs, and a link points at the full `<cluster>-diff.md` in the job artifacts. Nothing is silently dropped.

`--skip-summary` (or `FLUX_TOOLS_SKIP_SUMMARY`) drops the first comment when the per-cluster ones are enough.

---
