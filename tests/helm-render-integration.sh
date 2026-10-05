#!/bin/sh
set -eu
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/input" "$work/chart/templates"
cat > "$work/input/kustomization.yaml" <<'YAML'
resources: [release.yaml]
YAML
cat > "$work/input/release.yaml" <<'YAML'
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: chart
  namespace: apps
spec:
  url: oci://registry.invalid/chart
  ref:
    tag: 1.0.0
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: values
  namespace: apps
data:
  values.yaml: 'replicas: 2'
---
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: sample
  namespace: apps
spec:
  interval: 5m
  chartRef:
    kind: OCIRepository
    name: chart
  valuesFrom:
    - kind: ConfigMap
      name: values
YAML
cat > "$work/chart/Chart.yaml" <<'YAML'
apiVersion: v2
name: sample
version: 1.0.0
YAML
cat > "$work/chart/templates/deployment.yaml" <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: sample
spec:
  replicas: {{ .Values.replicas }}
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
flux-tools render "$work/input" --source-root "$work" --local-artifact chart=chart --output "$work/output.yaml"
[ "$(yq -r 'select(.kind == "Deployment") | .spec.replicas' "$work/output.yaml")" = 2 ]
echo 'OCI chartRef and required Helm valuesFrom: OK'
