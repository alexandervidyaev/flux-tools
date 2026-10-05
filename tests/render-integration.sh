#!/bin/sh
set -eu
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export FLUX_TOOLS_CACHE_DIR="$work/cache"
mkdir -p "$work/entry" "$work/base" "$work/override"
cat > "$work/entry/kustomization.yaml" <<'YAML'
resources: [flux.yaml]
YAML
cat > "$work/entry/flux.yaml" <<'YAML'
apiVersion: v1
kind: ConfigMap
metadata:
  name: variables
  namespace: flux-system
data:
  env: integration
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: base
  namespace: flux-system
spec:
  url: oci://registry.invalid/base
  ref:
    tag: v1
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: override
  namespace: flux-system
spec:
  url: oci://registry.invalid/override
  ref:
    tag: v1
---
apiVersion: source.extensions.fluxcd.io/v1beta1
kind: ArtifactGenerator
metadata:
  name: composed
  namespace: flux-system
spec:
  sources:
    - alias: base
      kind: OCIRepository
      name: base
    - alias: override
      kind: OCIRepository
      name: override
  artifacts:
    - name: composed
      copy:
        - from: '@base/**'
          to: '@artifact/'
        - from: '@override/*.yaml'
          to: '@artifact/'
          strategy: Merge
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: apps
  namespace: flux-system
spec:
  interval: 5m
  path: ./
  prune: true
  sourceRef:
    kind: ExternalArtifact
    name: composed
  targetNamespace: applications
  postBuild:
    substituteFrom:
      - kind: ConfigMap
        name: variables
  images:
    - name: nginx
      newTag: latest
  patches:
    - target:
        kind: Deployment
        name: sample
      patch: '[{"op":"replace","path":"/spec/replicas","value":5}]'
YAML
cat > "$work/base/kustomization.yaml" <<'YAML'
resources: [deployment.yaml]
YAML
cat > "$work/base/deployment.yaml" <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: sample
  annotations:
    environment: ${env}
    default: ${missing:=fallback}
    escaped: $${literal}
spec:
  replicas: 1
  selector:
    matchLabels: {app: sample}
  template:
    metadata:
      labels: {app: sample}
    spec:
      containers:
        - name: sample
          image: nginx:1.0
YAML
cat > "$work/override/deployment.yaml" <<'YAML'
metadata:
  annotations:
    override: present
YAML
flux-tools render "$work/entry" --source-root "$work" \
  --local-artifact base=base --local-artifact override=override \
  --enable-helm=false --output "$work/rendered.yaml"
[ "$(yq -r 'select(.kind == "Deployment") | .spec.replicas' "$work/rendered.yaml")" = "5" ]
[ "$(yq -r 'select(.kind == "Deployment") | .spec.template.spec.containers[0].image' "$work/rendered.yaml")" = "nginx:latest" ]
[ "$(yq -r 'select(.kind == "Deployment") | .metadata.namespace' "$work/rendered.yaml")" = "applications" ]
[ "$(yq -r 'select(.kind == "Deployment") | .metadata.annotations.environment' "$work/rendered.yaml")" = "integration" ]
[ "$(yq -r 'select(.kind == "Deployment") | .metadata.annotations.default' "$work/rendered.yaml")" = "fallback" ]
[ "$(yq -r 'select(.kind == "Deployment") | .metadata.annotations.escaped' "$work/rendered.yaml")" = '${literal}' ]
[ "$(yq -r 'select(.kind == "Deployment") | .metadata.annotations.override' "$work/rendered.yaml")" = "present" ]
printf 'Flux patches, images, substitution, target namespace and artifact Merge: OK\n'
yq -i '(select(.kind == "Kustomization") | .spec.postBuild.substituteFrom[0].name) = "missing"' "$work/entry/flux.yaml"
if flux-tools render "$work/entry" --source-root "$work" \
  --local-artifact base=base --local-artifact override=override \
  --enable-helm=false --output "$work/missing.yaml" > "$work/error.log" 2>&1; then
  echo 'Missing required substituteFrom was accepted' >&2
  exit 1
fi
printf 'Missing substituteFrom fails render: OK\n'
