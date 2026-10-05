// Package build renders Kubernetes manifests from Flux resources.
package build

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexandervidyaev/flux-tools/pkg/helm"
	"github.com/alexandervidyaev/flux-tools/pkg/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func (b *Builder) templateNativeHelmReleases(ctx context.Context, helmReleaseObjects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	var helmObjects []*unstructured.Unstructured
	valuesBuilder := helm.NewValuesBuilder()
	if b.options.TolerateMissingValues {
		valuesBuilder.SetTolerateMissing(true)
	}

	for key, data := range extractConfigMaps(helmReleaseObjects) {
		ns, name, _ := strings.Cut(key, "/")
		valuesBuilder.AddConfigMap(ns, name, data)
	}
	for key, data := range extractSecrets(helmReleaseObjects) {
		ns, name, _ := strings.Cut(key, "/")
		valuesBuilder.AddSecret(ns, name, data)
	}
	for _, hrObj := range helmReleaseObjects {
		if hrObj.GetKind() != "HelmRelease" || !strings.HasPrefix(hrObj.GetAPIVersion(), "helm.toolkit.fluxcd.io") {
			continue
		}
		hr := &types.HelmRelease{}
		data, err := hrObj.MarshalJSON()
		if err == nil {
			err = json.Unmarshal(data, hr)
		}
		if err != nil {
			return nil, fmt.Errorf("invalid HelmRelease: %w", err)
		}

		b.printer.Verbose("  Templating HelmRelease: %s/%s (chart: %s, version: %s)\n",
			hr.Namespace, hr.Name, hr.Spec.Chart.Spec.Chart, hr.Spec.Chart.Spec.Version)

		values, err := valuesBuilder.BuildValues(hr)
		if err != nil {
			return nil, fmt.Errorf("values for HelmRelease %s/%s: %w", hr.Namespace, hr.Name, err)
		}

		var chartObjects []*unstructured.Unstructured
		if ref := hr.Spec.ChartRef; ref != nil && (ref.Kind == "OCIRepository" || ref.Kind == "ExternalArtifact") {
			ns := ref.Namespace
			if ns == "" {
				ns = hr.Namespace
			}
			dir, resolveErr := b.sources.Resolve(ref.Kind, ns, ref.Name)
			if resolveErr != nil {
				return nil, resolveErr
			}
			dir, resolveErr = chartDirectory(dir)
			if resolveErr != nil {
				return nil, resolveErr
			}
			chartObjects, err = b.helmClient.TemplateLocal(ctx, dir, hr, values)
		} else {
			chartObjects, err = b.helmClient.Template(ctx, hr, values)
		}

		if err != nil {
			if !b.options.SkipFailedCharts {
				return nil, fmt.Errorf("failed to template HelmRelease %s/%s: %w", hr.Namespace, hr.Name, err)
			}
			b.printer.Verbose("    Warning: Failed to template HelmRelease %s/%s: %v\n", hr.Namespace, hr.Name, err)
			continue
		}

		targetNamespace := hr.Spec.TargetNamespace
		if targetNamespace == "" {
			targetNamespace = hr.Namespace
		}
		for _, obj := range chartObjects {
			if obj.GetNamespace() == "" && isNamespacedResource(obj) {
				obj.SetNamespace(targetNamespace)
			}
		}

		helmObjects = append(helmObjects, chartObjects...)

		b.printer.Verbose("    Generated %d objects from HelmRelease\n", len(chartObjects))
	}

	return helmObjects, nil
}
func extractSecrets(objects []*unstructured.Unstructured) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, obj := range objects {
		if obj.GetKind() != "Secret" {
			continue
		}
		data := map[string]string{}
		raw, _, _ := unstructured.NestedStringMap(obj.Object, "data")
		for k, v := range raw {
			decoded, err := base64.StdEncoding.DecodeString(v)
			if err == nil {
				data[k] = string(decoded)
			} else {
				data[k] = v
			}
		}
		plain, _, _ := unstructured.NestedStringMap(obj.Object, "stringData")
		for k, v := range plain {
			data[k] = v
		}
		out[types.GetObjectKey(obj.GetNamespace(), obj.GetName())] = data
	}
	return out
}

func chartDirectory(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "Chart.yaml")); err == nil {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var charts []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if _, err := os.Stat(filepath.Join(path, "Chart.yaml")); err == nil {
			charts = append(charts, path)
		}
	}
	if len(charts) != 1 {
		return "", fmt.Errorf("artifact must contain exactly one chart, found %d", len(charts))
	}
	return charts[0], nil
}
