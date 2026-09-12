# AGENTS.md

Context for AI agents and contributors. This file is the single source of truth about how the repository is organised; `CLAUDE.md` only points here.

## What this project is

`flux-tools` is a Go CLI (Cobra) that validates, renders and diffs Kubernetes manifests from Flux CD GitOps repositories without a cluster. The idea comes from [flux-local](https://github.com/allenporter/flux-local); the implementation is independent.

The tool assumes nothing about repository layout. A cluster is an entry directory plus a repository root:

- **entries**: one rule, and no flag changes it. A marker in the path makes it a cluster; markers below it make a set; no marker anywhere makes the path itself one entry as given. `flux-system/` is always a marker, `--cluster-marker` names another for layouts that have none in git (`internal/orchestrator/resolver.go`, `ResolveInput`).
- **root**: always explicit. `--flux-workdir` defaults to the working directory, and is never derived: a wrong root renders the wrong manifests, so it is not guessed (`internal/cli/input.go`, `resolveEntries`).

An entry or `spec.path` without `kustomization.yaml` is built from a generated one (`pkg/kustomize/generator.go`), following kustomize-controller's rules, with `<root>/.sourceignore` applied (`pkg/kustomize/sourceignore.go`, a gitignore subset). Which GitRepository is this checkout is decided once, in `helm.Client.ResolveGitSourcePath`: the flux-system sync source (`build.DetectSelfSource`) resolves to the root, every other GitRepository is cloned, an undeclared one is an error. Without `flux-system/` the self source is unknown and an undeclared GitRepository is taken as local. A path that exists locally never turns a remote source into a local one. Empty `spec.path` = root.

## Build and checks

```bash
go build -o flux-tools ./cmd/flux-tools/   # build the binary
go build ./... && go vet ./...              # compile + static analysis
gofmt -l .                                  # must print nothing
staticcheck ./...                           # must be clean (pinned to 2025.1.1 in CI)
go test -race ./...                         # ~420 tests across all packages
```

CI (`.github/workflows/ci.yml`) runs exactly this set on every pull request. Run it locally before pushing. The same workflow validates `.goreleaser.yaml` with a snapshot build and builds the Docker image. A `v*` tag triggers `release.yml`: goreleaser publishes binaries to GitHub Releases and the image goes to `ghcr.io`.

## Layout

```
cmd/flux-tools/main.go          entry point, calls cli.Execute()

internal/
  cli/                          Cobra commands (root, test, build, helm-pull,
                                kubeconform, yq, diff, post-comment)
                                root.go: signal.NotifyContext + ExecuteContext + global --timeout
                                argflags.go: shared parser for passthrough commands (extractOwnFlags)
                                input.go: positional paths + --flux-workdir -> entries and root
                                emptyinput.go: what --allow-missing-path does
  validator/build/              manifest rendering (builder.go is the main file, ~1000 lines;
                                paths.go: DetectSelfSource from the flux-system Kustomization;
                                slice.go: sliced output; output.go: streaming serialisation;
                                in-run kustomize render cache lives in builder.go)
  validator/test/               resource tests (kustomization + helmrelease)
  helm/                         chart discovery and download (pull.go: repo setup, then parallel
                                pull), repository metadata cache (repo_cache.go)
  diff/                         directory comparison; unified.go is a native unified diff
                                (port of git xdiff, git is never executed);
                                json.go: the structured <cluster>-diff.json artifact
  gitlab/                       posting comments to a GitLab MR (poster.go), httpDoer client (client.go)
  orchestrator/                 cluster/environment discovery; runner.go: RunOverClusters
                                (test and build are both expressed through it); junit.go: JUnit report
  kubeconform/                  kubeconform wrapper + Code Quality report (codequality.go)
  yq/                           yq wrapper

pkg/
  config/env.go                 configuration from env (LoadDefaults)
  exec/runner.go                CommandRunner interface + RealRunner (every external command)
  output/                       Printer (stderr: Info/Verbose/Warn); stagetimer.go: build stage timing
  result/types.go               FileResult, the base type of a per-file result
  workerpool/pool.go            generic worker pool Run[T, R], the ONLY concurrency primitive
  fsutil/                       IsYAMLFile, FindYAMLFiles, ResolvePath, HasKustomizationFile, SplitYAMLDocuments
  manifest/                     ManifestCollection, dependency graph
  helm/                         Helm client (client.go), templating (template.go), persistent render
                                cache (templatecache.go), values (values.go), GitRepository clones with
                                a global singleflight (git.go), ArtifactGenerator resolution (artifact.go)
  kustomize/                    kustomize Builder (BuildDir renders a directory with or without
                                kustomization.yaml), generator.go: kustomize-controller-style
                                generated kustomization, object filtering (filter.go, incl. SkipKinds),
                                variable substitution (substitute.go)
  types/                        Go structs of Flux resources: Kustomization, HelmRelease,
                                HelmRepository, GitRepository, ArtifactGenerator
```

## Architecture

### Context
The root `context.Context` is created in `cli.Execute()` (`signal.NotifyContext`) and passed through `rootCmd.ExecuteContext`; the global `--timeout` flag layers on top in `PersistentPreRun`. Every function that leads to an exec takes `ctx` as its first argument. Never create `context.Background()` outside tests.

### External commands
Every external program (helm, kustomize, yq, kubeconform, git) is run through `pkg/exec.CommandRunner`, never `os/exec` directly. The runner is swapped in tests.

- Packages `yq`, `kubeconform`: package-level `var runner exec.CommandRunner`
- `pkg/helm.Client` and `pkg/kustomize.Builder`: struct field `runner`
- `RealRunner.Run` wraps errors; check the type with `errors.As()`, not a cast
- `diff` runs no external command: the unified diff is generated natively (`internal/diff/unified.go`)

### Concurrency
Two levels, both on top of `pkg/workerpool.Run[T, R]`:
- `orchestrator.RunOverClusters(ctx, clusters, concurrency, verbose, fn)`: run a function over clusters, stream progress, aggregate. `test` (sequential = concurrency 1) and `build` are both expressed through it. New cross-cutting behaviour (fail-fast, reports) belongs here.
- Direct `workerpool.Run` calls for non-cluster fan-outs (files in yq, charts in helm-pull, clusters in diff, comments in poster).

Workers never write shared state: each returns a typed result and aggregation happens after the pool.

### Caches
| Cache | Location | Scope |
|---|---|---|
| Chart `.tgz` | `{cacheDir}/charts/` | across runs |
| Helm repo metadata (TTL) | `{cacheDir}/repo-metadata.json` | across runs; `Mark*` in memory, one `Flush()` |
| Rendered charts | `{cacheDir}/template-cache/` | across runs; key is sha256 of every input (`pkg/helm/templatecache.go`); only `.tgz` renders are cached |
| GitRepository clones | `{cacheDir}/git/` | global singleflight in `pkg/helm/git.go` |
| `kustomize build` output | Builder memory | one run; raw bytes, parsed on every access |

### Output
All user-facing output goes through `pkg/output.Printer` (stderr): `Info()`, `Verbose()`, `Warn()`. Results go to stdout or files. Build stage timing is `pkg/output.StageTimer` (the `Stages:` line at the end of build).

### Configuration
`pkg/config.LoadDefaults()` reads `FLUX_TOOLS_CACHE_DIR` and `FLUX_TOOLS_HELM_TIMEOUT`. Defaults are applied both in the CLI (needed before the builder exists, for `autoPullHelmCharts`) and in `build.NewBuilder` (a guard for internal callers). The duplication is deliberate.

### Flux resource types
`pkg/types/`: `Kustomization`, `HelmRelease`, `HelmRepository` (`IsOCI()`), `GitRepository`, `ArtifactGenerator`. Keys are `GetObjectKey(namespace, name)`, i.e. `namespace/name`.

## Data flows

### build
```
paths -> cli.resolveEntries (orchestrator.ResolveInput; root from --flux-workdir)
     -> RunOverClusters, per cluster:
        -> build.NewBuilder (root: --flux-workdir) -> BuildAll(ctx):
           -> kustomize BuildDir of the entry (through the render cache)
           -> recursive discovery of nested Kustomizations (fills the cache)
           -> processKustomization for each (cache hits)
           -> processHelmReleases: values -> helm.Template (template-cache first, then exec)
           -> substitute/filter (SkipKinds, SkipCRDs, ...)
        -> serialisation: one file (streamed) or a sliced tree
     -> Stages: line + summary
```

### test
```
path -> clusters -> (pre-pull charts via autoPullHelmCharts when --enable-helm)
     -> RunOverClusters (parallel; --sequential = concurrency 1):
        -> TestRunner: discovery -> Kustomization tests (kustomize build)
                                 -> HelmRelease tests (chart in cache -> no repo access -> helm template)
     -> summary (+ --junit-report)
```

### diff pipeline (as consumers run it in CI)
```
build (both sides) -> diff (native unified diff, sorted changes)
   # diff slices build's default output itself; yq only when a field has to be dropped -> index.md + <cluster>-diff.md + <cluster>-diff.json
-> post-comment: summary comment from index.md (off with --skip-summary) + one per cluster;
   stats and the compact format come from the JSON
```

## CLI commands

| Command | Arguments | Purpose |
|---|---|---|
| `test` | paths | Validate Flux resources (parallel, incl. helm; `--flux-workdir`, `--cluster-marker`, `--junit-report`) |
| `build` | paths | Render manifests (`--flux-workdir`, `--cluster-marker`, `--skip-kind`, `--output sliced`, `--cpuprofile`, `--allow-missing-path`) |
| `helm-pull` | paths | Pre-download Helm charts (`--flux-workdir`, `--cluster-marker`) |
| `kubeconform` | path + flags | JSON schema validation (passthrough; `--codequality-report`) |
| `yq` | filter + path | Parallel YAML processing (`--allow-missing-path`) |
| `diff` | two paths | Compare directories (native, no git) |
| `post-comment` | diff dir | Publish to a GitLab MR (needs `<cluster>-diff.json`) |

## Dependencies (go.mod, direct)

- `github.com/spf13/cobra`: CLI framework
- `k8s.io/apimachinery`: Kubernetes types (Unstructured)
- `sigs.k8s.io/yaml`: YAML marshalling
- `gopkg.in/yaml.v3`: streaming YAML parsing (kustomize output)

Add dependencies reluctantly. The whole diff, for example, is hand-written to stay byte-compatible with git.

## External tools

Invoked through `exec.CommandRunner`: `kustomize build`, `helm template/repo/pull/registry login/version`, `kubeconform`, `yq eval-all`, `git clone/checkout` (GitRepository clones only; diff does not use git). Tool versions for the Docker image are pinned in `Dockerfile` through `ARG`.

## Code style

- Comments in English; every package has a `// Package ...` godoc, `cmd/` a `// Command ...` one
- Errors are wrapped: `fmt.Errorf("context: %w", err)`
- Single-letter receivers (`b`, `c`, `p`, `r`, `f`, `d`)
- No comments that restate the line. A comment earns its place only for a trap, an external fact, a tooling directive or an inherently unreadable line

## Makefile and Docker

```bash
make build            # build the binary (version from git describe via ldflags)
make test             # go test -v ./...
make release-check    # goreleaser check
make release-snapshot # multi-arch binaries into dist/
make docker-build     # Docker image with every external tool
make clean            # remove artifacts
```

The Docker image (alpine) contains kustomize, helm, kubeconform, yq and git.

## Rules when changing code

- External calls only through `pkg/exec.CommandRunner`; pass `ctx` down, never create `context.Background()`
- User output through `pkg/output.Printer`, not `fmt.Print*`; results on stdout, status on stderr
- Concurrency through `workerpool.Run` or `orchestrator.RunOverClusters`; no hand-rolled channel pools
- Workers return results; they do not mutate shared state under a mutex
- Do not duplicate helpers from `pkg/fsutil`
- Changes to the diff report format must be mirrored in `post-comment` (it reads `<cluster>-diff.json`; there is no markdown reverse parser)
- Changes to `internal/diff/unified.go` are verified by golden tests against real git. Do not weaken them
- Default CLI behaviour must not change: consumers compare its output byte for byte in their e2e tests
- Before a PR: `go build ./... && go vet ./... && gofmt -l . && staticcheck ./... && go test -race ./...`

## Adding a wrapper command

Pattern, as in `yq` and `kubeconform`: a thin wrapper around an external tool.

1. Create `internal/<name>/`:
   - `types.go`: result types (embed `result.FileResult`)
   - `discovery.go`: input file discovery
   - `processor.go`: `var runner exec.CommandRunner`, processing of one file (takes `ctx`)
   - `parallel.go`: fan-out through `workerpool.Run`
2. Create `internal/cli/<name>.go`: the Cobra command; extract own flags with `extractOwnFlags` (`internal/cli/argflags.go`), pass the rest through
3. Register in `internal/cli/root.go` with `rootCmd.AddCommand()`

## Writing tests

`pkg/exec.CommandRunner` is ready to mock:

```go
type mockRunner struct {
    runFunc func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (m *mockRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
    return m.runFunc(ctx, name, args...)
}

func (m *mockRunner) RunWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error) {
    return m.runFunc(ctx, name, args...)
}

func (m *mockRunner) LookPath(name string) error { return nil }
```

- Packages with `var runner` (yq, kubeconform): replace the variable in the test
- Struct-level runners (helm.Client, kustomize.Builder): pass the mock through the field
- The GitLab client is mocked through the `httpDoer` interface (see `internal/gitlab/client_test.go`, a stub with a response queue)
- Chart discovery is mocked through the package-level `newCollectionLoader` in `internal/helm`: return your own `collectionLoader` and discovery runs against a hand-built `manifest.ManifestCollection` instead of a rendered cluster (see `discovery_seam_test.go`)
- To assert on user-facing output, build the printer with `output.NewWithWriter(&buf, verbose)` instead of capturing stderr
- Integration tests use the fixtures in `internal/orchestrator/testdata/` (cluster-repo, and source-repo as an external GitRepository); tests that need kustomize/helm/kubeconform in PATH skip when they are missing

## Where to look for typical tasks

| Task | Files |
|---|---|
| Add a CLI flag | `internal/cli/<command>.go` (passthrough commands: `argflags.go`) |
| Change rendering logic | `internal/validator/build/builder.go` |
| Change sliced output / serialisation | `internal/validator/build/slice.go`, `output.go` |
| Change test logic | `internal/validator/test/runner.go`, `helmrelease.go` |
| Cluster orchestration, progress, summaries | `internal/orchestrator/runner.go`, `sequential.go` |
| Add a Flux type | `pkg/types/` + `internal/validator/build/builder.go` (parseFluxObjects) |
| Change object filtering | `pkg/kustomize/filter.go` |
| Change Helm templating / render cache | `pkg/helm/template.go`, `templatecache.go`, `values.go` |
| chartRef / ArtifactGenerator resolution | `pkg/helm/artifact.go`, `pkg/types/artifactgenerator.go` |
| Change cluster discovery | `internal/orchestrator/resolver.go`, `discovery.go`, `internal/cli/input.go` |
| Change how entries and the root are resolved | `internal/orchestrator/resolver.go` (`ResolveInput`), `internal/cli/input.go` (`resolveEntries`) |
| Change the generated kustomization rules | `pkg/kustomize/generator.go`, `sourceignore.go` (mirror kustomize-controller and source-controller) |
| Change the diff algorithm | `internal/diff/unified.go` (golden tests against git!) |
| Change the diff report format | `internal/diff/formatter_markdown.go`, `json.go`, `writer.go` + `internal/gitlab/poster.go` |
| Change the GitLab integration | `internal/gitlab/poster.go`, `client.go` |
| JUnit / Code Quality reports | `internal/orchestrator/junit.go`, `internal/kubeconform/codequality.go` |
| Stage timing / profiling | `pkg/output/stagetimer.go`, `--cpuprofile` in `internal/cli/build.go` |
| Add an env variable | `pkg/config/env.go` |

## Roadmap

- `post-comment` for GitHub pull requests. Today only GitLab merge requests are supported; the poster is behind an `httpDoer` seam, so a second provider is a new package next to `internal/gitlab`.
- A single `flux-tools ci` command (render + validate + slice + diff in one pass) would need a built-in equivalent of the yq `del(...)` step.
