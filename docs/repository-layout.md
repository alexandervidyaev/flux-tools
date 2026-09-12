# Repository layout

How flux-tools decides what to build and what `spec.path` is relative to.

flux-tools knows nothing about how a repository is organised. Two things are needed to render a cluster, and they are separate questions:

- **the entry directory**: what the cluster's sync Kustomization points at. It is built the way kustomize-controller builds it: with its own `kustomization.yaml` when there is one, otherwise from a generated one that lists every manifest under it (recursively, a subdirectory with its own `kustomization.yaml` becomes one resource). A `.sourceignore` at the root is honoured the way source-controller honours it: what it excludes never reaches the generated list;
- **the repository root**: the directory every Kustomization's `spec.path` is relative to. Flux resolves `spec.path` against the root of the GitRepository artifact, so this is the repository checkout.

Entries are positional arguments. The root is `--flux-workdir`, and it defaults to the working directory, so running from the repository root needs no flag.

## Finding the entries

One rule, and no flag changes it:

| What the path holds | How it is read |
| --- | --- |
| a marker in the path itself | one cluster |
| markers in directories below it, at any depth | that set of clusters, built in parallel |
| no marker anywhere below it | the path itself, one entry taken as given |

`flux-system/` is always a marker. `--cluster-marker` names another, for repositories that have no `flux-system/` in git. A cluster is a leaf: once a directory is recognised, its own subdirectories are not searched.

Several paths can be given at once, and the clusters found under each are concatenated.

## Flux bootstrap

`flux bootstrap --path=clusters/prod` leaves a `flux-system/` directory in the entry. That is the marker, so nothing has to be configured:

```
<repository root>/            <- --flux-workdir, "." when you run from here
  clusters/
    prod/                     <- cluster: has flux-system/
      flux-system/
        gotk-components.yaml
        gotk-sync.yaml
        kustomization.yaml
      infrastructure.yaml     <- Flux Kustomization -> ./infrastructure
      apps.yaml               <- Flux Kustomization -> ./apps/prod
    staging/
      flux-system/
      ...
  infrastructure/
  apps/
```

```bash
flux-tools test clusters/prod
flux-tools build clusters/           # every cluster under clusters/
flux-tools build .                   # every cluster in the repository
```

## flux-operator, or no flux-system/ in git

With flux-operator the sync Kustomization lives in the cluster rather than in git, so there is no marker to find. Either name the entry directories, which is the third rule above:

```bash
flux-tools build operator/clusters/dev
flux-tools build operator/clusters/dev operator/clusters/qa
```

or put a marker of your own in each cluster directory and name it, after which discovery works exactly as it does for a bootstrapped repository:

```bash
flux-tools build --cluster-marker .flux-cluster operator/clusters/
```

The marker may be a file or a directory, and it is added to `flux-system/` rather than replacing it, so a repository holding both layouts is discovered in one pass.

A directory that holds no manifests of its own while its subdirectories do is refused rather than built as a single entry: it is a set of clusters carrying no marker, and rendering it as one would name the output after the wrong directory and merge the clusters into it.

## Running from outside the repository

`--flux-workdir` defaults to the working directory, so an entry given from somewhere else needs the flag:

```bash
cd /anywhere
flux-tools build --flux-workdir ~/src/fleet ~/src/fleet/clusters/prod
```

An entry outside the root is refused: `spec.path` is resolved against the root, so both have to be the same checkout.

## Which Kustomizations are local

A Kustomization or HelmRelease with a `GitRepository` source is rendered from this checkout only when that GitRepository **is** this checkout:

- with `flux-system/` present, it is the GitRepository the `flux-system` Kustomization syncs from (named in `flux-system/gotk-sync.yaml`). Any other GitRepository must be declared in the manifests and is cloned at its `ref`; an undeclared one is an error, and a clone that fails is an error. A Kustomization that points at another repository is never built from this root, even when the same relative path exists here;
- without `flux-system/`, the sync GitRepository lives in the cluster, so nothing in git names it. A GitRepository no manifest declares is taken to be this checkout; a declared one is cloned.

This is decided per cluster from the manifests, independently of `--flux-workdir`.

An empty `spec.path` is the root itself, as in Flux. `OCIRepository` and `Bucket` sources cannot be rendered offline (`--skip-oci`).

When two Kustomizations render the same object (a sync Kustomization at the repository root plus a nested one under it), the output holds it once, the last rendering winning, as the cluster would.
