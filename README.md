# flux-tools

Render, validate and diff Kubernetes manifests from a Flux CD GitOps repository, without a cluster. Point it at a cluster directory and it does what Flux would do on reconcile: builds every Kustomization, templates every HelmRelease, and gives you the resulting manifests to check, diff and post to a merge request.

The idea comes from [flux-local](https://github.com/allenporter/flux-local). This is an independent Go implementation with a persistent Helm render cache, parallel processing of clusters and a native unified diff.

```bash
# every Kustomization builds, every HelmRelease templates
flux-tools test clusters/prod --enable-helm

# rendered manifests, one file per cluster
flux-tools build clusters/prod --enable-helm
```

## Install

```bash
go install github.com/alexandervidyaev/flux-tools/cmd/flux-tools@latest
```

Binaries for linux/darwin, amd64/arm64 are on the [releases page](https://github.com/alexandervidyaev/flux-tools/releases). A Docker image with every external tool inside:

```bash
docker run --rm -v "$PWD:/app" ghcr.io/alexandervidyaev/flux-tools:latest test clusters/prod
```

The binary itself needs `kustomize`, `helm` and `git` in `PATH`. `kubeconform` and `yq` only for the commands that wrap them.

## How it finds your clusters

flux-tools makes no assumptions about how the repository is organised.

- A directory with `flux-system/` is a cluster, wherever it sits. The repository root is read from the flux-system Kustomization that `flux bootstrap` wrote, so `spec.path` resolves exactly as Flux resolves it.
- No `flux-system/` in git (flux-operator)? Name the entry directories, or mark them and pass `--cluster-marker`.
- A directory without `kustomization.yaml` is built the way kustomize-controller builds it, from a generated one.

```bash
flux-tools build clusters/                      # every cluster under clusters/
flux-tools build operator/clusters/dev         # flux-operator layout
```

Details, including which GitRepository counts as "this repository", in [docs/repository-layout.md](docs/repository-layout.md).

## Everything runs in parallel

A repository with thirty clusters is rendered thirty ways at once, not one after another. Every fan-out sits on one worker pool, so the behaviour and the flag are the same everywhere: `-j` bounds the workers, and the aggregation happens after the pool rather than under a mutex.

| Command | What is parallelised | Flag | Default |
| --- | --- | --- | --- |
| `build` | clusters | `-j` | one worker per cluster, `0` for unbounded |
| `test` | clusters | `--sequential` forces one at a time | 10 workers, or one per cluster when there are fewer |
| `diff` | clusters | `-j` | one worker per cluster |
| `helm-pull` | chart downloads, and cluster discovery before them | `-j` | 3 downloads, discovery capped at 10 |
| `yq` | files | `-j` | one worker per CPU |
| `post-comment` | comments posted to the merge request | `--workers` | 3 |

Two caches make the repeat runs cheap on top of that: `helm template` output is cached across runs, keyed by a hash of every input, and `kustomize build` output is cached within a run, so a path rendered during discovery is not rendered again during the build.

`helm-pull --verbose` drops chart discovery to one worker, because interleaved output from several clusters is unreadable.

## What a pipeline looks like

```bash
flux-tools test clusters/prod --enable-helm --junit-report report.xml
flux-tools build clusters/prod --enable-helm --skip-kind HelmRelease --output sliced --output-dir ./incoming
flux-tools kubeconform ./incoming -summary -ignore-missing-schemas --codequality-report gl-codequality.json
flux-tools diff ./current ./incoming -o ./diffs/     # ./current: the same build on the target branch
flux-tools post-comment ./diffs/                     # summary + one comment per cluster in the GitLab MR
```

The full walkthrough, with caching between pipelines and the diff baseline for a new environment, is in [docs/ci-pipeline.md](docs/ci-pipeline.md).

## Commands

| Command | What it does |
|---|---|
| `test` | Validate every Kustomization and HelmRelease of one or more clusters |
| `build` | Render every object of the given clusters at once, to YAML, JSON or a kubectl-slice compatible tree |
| `helm-pull` | Pre-download the charts the clusters use |
| `kubeconform` | Validate rendered manifests against JSON schemas, and turn the result into a GitLab Code Quality report |
| `yq` | Run yq over every rendered file in parallel, writing to `cleaned/` instead of over the input |
| `diff` | Compare two `build` results, native unified diff, per-cluster reports |
| `post-comment` | Publish the diff to a GitLab merge request |

Every flag and default: [docs/commands.md](docs/commands.md).

## Documentation

- [Repository layout](docs/repository-layout.md): entry directory, repository root, local and remote sources
- [Command reference](docs/commands.md)
- [CI pipeline](docs/ci-pipeline.md): the full flow, diff against the target branch
- [Configuration](docs/configuration.md): caches and environment variables
- [Supported Flux features](docs/flux-features.md)
- [AGENTS.md](AGENTS.md): architecture and contribution rules
- [CHANGELOG.md](CHANGELOG.md)

## Roadmap

- `post-comment` for GitHub pull requests.
- A single `flux-tools ci` command: render, validate, slice and diff in one pass.

## License

[Apache-2.0](LICENSE)
