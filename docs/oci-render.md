# OCI render and live diff

`render` is an additional workflow for a single artifact entry. The existing
`build` and `test` commands keep their flags and rendering behaviour.

## Requirements

The Go binary invokes external tools through `CommandRunner`:

- `render`: `kustomize`, `flux`; `helm` when Helm rendering is enabled;
- remote OCI sources: `crane`; `git` for source ignore rules;
- `diff-live`: `kubectl` and an explicit kubeconfig;
- `kube login`: `tbot` configured for the Teleport deployment.

The Docker image includes Flux, crane, kubectl and tbot. Set the
`TELEPORT_VERSION` build argument to a version compatible with your Teleport
cluster.

## Render an artifact entry

```sh
flux-tools render entry --flux-workdir . --output incoming/example-cluster.yaml \
  --local-artifact example/base=base \
  --local-artifact example/override=override
```

The entry directory holds Flux resources; `--flux-workdir` is their source
artifact root. `--source-root` is an alias for this flag. A `--local-artifact`
key matches an OCI URL suffix, so `example/base` replaces a source whose URL
ends in `/example/base`. Local paths are relative to the source root. This lets
a merge request render unpublished artifacts from its checkout.

Without a local replacement, OCIRepository digest, tag or semver references are
resolved to a digest before download. Semver filters are supported. Registry
credentials come from crane's normal Docker configuration, not CLI passwords.
`--oci-rewrite prefix=replacement` redirects an OCI URL to a mirror.

ArtifactGenerator supports OCIRepository sources, copy globs, exclusions and
Overwrite/Merge strategies. Unsupported source kinds, Extract operations,
symlinks and unsafe archive/copy paths return an error.

The Flux CLI applies each Kustomization's patches, images, targetNamespace and
postBuild. Required substituteFrom references must exist in the rendered
ConfigMaps or Secrets; inline substitutions take precedence. Required Helm
valuesFrom references also fail when missing. SOPS values must be decrypted
before render. HelmRelease chartRef can use OCIRepository or a composed
ExternalArtifact.

The output has one YAML file per explicit invocation. It can be compared with
the same render of the target Git revision:

```sh
flux-tools diff current/ incoming/ -o reports/git
```

Keep both sides on the same binary version and rendering options. CI can pass
the rendered YAML as an artifact to a separate manifest-policy validator; the
validator does not need to resolve Flux or OCI sources.

## Teleport kubeconfig

```sh
export TELEPORT_PROXY=teleport.example.invalid:443
export TBOT_TOKEN=example-gitlab-bot
# TBOT_GITLAB_JWT contains a GitLab ID token accepted by this Teleport join rule.
flux-tools kube login --cluster example-cluster \
  --out-dir /tmp/identity --config-path /tmp/tbot.yaml
```

The proxy and join token name are mandatory. Cluster names are explicit:
repeat `--cluster` or provide a whitespace-separated `--clusters` value.
One tbot invocation requests all kubeconfigs. The generated configuration
selects the GitLab join method and `TBOT_GITLAB_JWT`; it uses memory storage and
oneshot mode. Config and kubeconfig files have mode 0600.

## Live preview

```sh
flux-tools diff-live incoming/example-cluster.yaml \
  --kubeconfig /tmp/identity/example-cluster/kubeconfig.yaml \
  --output reports/live.diff
```

This executes `kubectl diff --server-side --field-manager kustomize-controller`.
The exit code for differences is accepted; API, access and apply-conflict errors
fail the command. Known literal and base64 Secret values from the input are
masked in stdout, saved reports and errors. Rendered YAML itself may contain
secrets, so restrict artifact access and retention. Do not publish kubeconfigs,
JWTs or tbot output as artifacts.

The live preview covers objects present in the input. It does not model Flux
inventory/prune deletions, Helm hooks, reconciliation order or uninstall.
Empty input is rejected rather than treating a deletion-only change as an empty
diff. Compare the two Git revisions as well to see removals.
