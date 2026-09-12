# Configuration

Caches and environment variables.

## Caching

Every cache lives under one directory (`FLUX_TOOLS_CACHE_DIR`, flags `--cache-dir`/`--helm-cache-dir`). In CI keep it between pipelines (`cache:` in GitLab, `actions/cache` on GitHub); a worked GitLab example, including the directory that has to sit inside `$CI_PROJECT_DIR`, is in [the CI pipeline guide](ci-pipeline.md#the-helm-cache-in-gitlab-ci).

| What | Where | Invalidation |
|---|---|---|
| Pulled charts (`.tgz`) | `charts/` | by chart name + version; `helm-pull --force` |
| Helm repository metadata | `repo-metadata.json` | TTL (`--repo-ttl`, 60 min) |
| Clones of external GitRepository sources | `git/` | by (URL, ref); usually cleaned before a CI run |
| Rendered Helm charts | `template-cache/` | none needed: the key covers every input (chart + version, values, values file contents, postRenderers, helm version); `--no-template-cache` disables it |

Within one `build` run the output of `kustomize build` is cached too (every Kustomization is rendered once instead of twice for discovery + build); `-v` shows `kustomize render cache: N entries, M hits`.

---

## Environment variables

### General

| Variable | Description | Default |
|---|---|---|
| `FLUX_TOOLS_CACHE_DIR` | Cache directory (charts, repo metadata, git clones, template cache) | `~/.flux-tools/cache` or `/tmp/flux-tools` |
| `FLUX_TOOLS_HELM_TIMEOUT` | Timeout of Helm operations in seconds | `300` |
| `FLUX_TOOLS_GENERATED_MANIFESTS_DIR` | Output directory of `build` | `./cluster-manifests` |

### yq

| Variable | Description |
|---|---|
| `YQ_FILTER` | yq filter (alternative to the argument) |

### GitLab (post-comment)

| Variable | Description |
|---|---|
| `GITLAB_TOKEN` | GitLab API token (preferred) |
| `CI_JOB_TOKEN` | CI job token (when `GITLAB_TOKEN` is unset) |
| `CI_PROJECT_ID` | Project ID |
| `CI_MERGE_REQUEST_IID` | Merge request IID |
| `CI_API_V4_URL` | GitLab API URL |
| `CI_ENVIRONMENT_NAME` | Environment name |
| `ENVIRONMENT` | Environment name (when `CI_ENVIRONMENT_NAME` is unset) |
| `GITLAB_TIMEOUT` | HTTP timeout in seconds |
| `GITLAB_RATE_LIMIT_MS` | Delay between posts in ms |
| `GITLAB_DELETE_RATE_LIMIT_MS` | Delay between deletes in ms |
| `FLUX_TOOLS_SKIP_SUMMARY` | `true` disables the summary comment |
| `CI_PROJECT_PATH` | Project path (for artifact links) |
| `CI_JOB_ID` | Job ID (for artifact links) |

---
