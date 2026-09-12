package kustomize

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func newObj(apiVersion, kind, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]interface{}{"name": name},
	}}
}

func TestFilterSkipKinds(t *testing.T) {
	objects := []*unstructured.Unstructured{
		newObj("helm.toolkit.fluxcd.io/v2", "HelmRelease", "app"),
		newObj("source.toolkit.fluxcd.io/v1", "HelmRepository", "repo"),
		newObj("v1", "ConfigMap", "cm"),
	}

	tests := []struct {
		name      string
		skipKinds []string
		wantKinds []string
	}{
		{
			name:      "no skip kinds keeps everything",
			skipKinds: nil,
			wantKinds: []string{"HelmRelease", "HelmRepository", "ConfigMap"},
		},
		{
			name:      "exact match",
			skipKinds: []string{"HelmRelease"},
			wantKinds: []string{"HelmRepository", "ConfigMap"},
		},
		{
			name:      "case-insensitive match",
			skipKinds: []string{"helmrelease", "CONFIGMAP"},
			wantKinds: []string{"HelmRepository"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewFilter(FilterOptions{SkipKinds: tt.skipKinds})
			got := f.FilterObjects(objects)

			var gotKinds []string
			for _, obj := range got {
				gotKinds = append(gotKinds, obj.GetKind())
			}

			if len(gotKinds) != len(tt.wantKinds) {
				t.Fatalf("got kinds %v, want %v", gotKinds, tt.wantKinds)
			}
			for i := range gotKinds {
				if gotKinds[i] != tt.wantKinds[i] {
					t.Errorf("got kinds %v, want %v", gotKinds, tt.wantKinds)
					break
				}
			}
		})
	}
}
