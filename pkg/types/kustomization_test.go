package types

import "testing"

func TestKustomizationSourceRefKey(t *testing.T) {
	cases := []struct {
		name string
		ks   *Kustomization
		want string
	}{
		{
			name: "explicit sourceRef.namespace is preserved",
			ks: &Kustomization{
				ObjectMeta: ObjectMeta{Namespace: "flux-system", Name: "kargo-helm"},
				Spec: KustomizationSpec{SourceRef: CrossNamespaceObjectReference{
					Kind: "GitRepository", Name: "kargo", Namespace: "flux-system",
				}},
			},
			want: "flux-system/kargo",
		},
		{
			name: "empty sourceRef.namespace defaults to Kustomization namespace",
			ks: &Kustomization{
				ObjectMeta: ObjectMeta{Namespace: "flux-system", Name: "kargo-helm"},
				Spec: KustomizationSpec{SourceRef: CrossNamespaceObjectReference{
					Kind: "GitRepository", Name: "kargo",
				}},
			},
			want: "flux-system/kargo",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.ks.SourceRefKey(); got != c.want {
				t.Errorf("SourceRefKey() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestHelmReleaseChartSourceKey(t *testing.T) {
	hr := &HelmRelease{}
	hr.Namespace = "podinfo"
	hr.Spec.Chart.Spec.SourceRef = CrossNamespaceObjectReference{Kind: "HelmRepository", Name: "podinfo"}
	if got := hr.ChartSourceKey(); got != "podinfo/podinfo" {
		t.Errorf("default namespace: %q", got)
	}
	hr.Spec.Chart.Spec.SourceRef.Namespace = "flux-system"
	if got := hr.ChartSourceKey(); got != "flux-system/podinfo" {
		t.Errorf("explicit namespace: %q", got)
	}
}
