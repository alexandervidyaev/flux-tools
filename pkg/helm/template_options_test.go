package helm

import (
	"context"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

// argRecorder captures the helm command line so tests can assert on the
// options Template derives, which are otherwise invisible.
type argRecorder struct {
	mu     sync.Mutex
	calls  [][]string
	output []byte
}

func (r *argRecorder) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{name}, args...))
	return r.output, nil
}

func (r *argRecorder) RunWithStdin(ctx context.Context, _ io.Reader, name string, args ...string) ([]byte, error) {
	return r.Run(ctx, name, args...)
}

func (r *argRecorder) LookPath(string) error { return nil }

func (r *argRecorder) templateArgs(t *testing.T) []string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if len(call) > 1 && call[0] == "helm" && call[1] == "template" {
			return call
		}
	}
	t.Fatalf("no `helm template` invocation among %v", r.calls)
	return nil
}

// ociClient wires a client to an OCI repository, the one source kind that
// reaches `helm template` without first adding or refreshing a repository.
func ociClient(t *testing.T) (*Client, *argRecorder) {
	t.Helper()
	c, err := NewClient(t.TempDir(), 60)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	rec := &argRecorder{output: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: rendered\n")}
	c.runner = rec

	repo := &types.HelmRepository{Spec: types.HelmRepositorySpec{URL: "oci://registry.example.com/charts", Type: "oci"}}
	repo.Name = "charts"
	repo.Namespace = "apps"
	c.RegisterRepository(repo)

	return c, rec
}

func ociRelease(namespace, name string) *types.HelmRelease {
	hr := &types.HelmRelease{}
	hr.Namespace = namespace
	hr.Name = name
	hr.Spec.Chart.Spec.Chart = "podinfo"
	hr.Spec.Chart.Spec.Version = "6.0.0"
	hr.Spec.Chart.Spec.SourceRef = types.CrossNamespaceObjectReference{Kind: "HelmRepository", Name: "charts"}
	return hr
}

// Flux derives the release name as "[targetNamespace-]name" unless the
// HelmRelease pins one. The name reaches helm as the second argument, so a
// wrong derivation renames every object the chart templates.
func TestTemplateDerivesReleaseName(t *testing.T) {
	tests := []struct {
		name            string
		releaseName     string
		targetNamespace string
		want            string
	}{
		{name: "an explicit releaseName wins", releaseName: "pinned", targetNamespace: "other", want: "pinned"},
		{name: "targetNamespace prefixes the name", targetNamespace: "other", want: "other-podinfo"},
		{name: "bare name when neither is set", want: "podinfo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := ociClient(t)
			hr := ociRelease("apps", "podinfo")
			hr.Spec.ReleaseName = tt.releaseName
			hr.Spec.TargetNamespace = tt.targetNamespace

			if _, err := c.Template(context.Background(), hr, nil); err != nil {
				t.Fatalf("Template: %v", err)
			}

			args := rec.templateArgs(t)
			if args[2] != tt.want {
				t.Errorf("release name = %q, want %q (args %v)", args[2], tt.want, args)
			}
		})
	}
}

// --namespace follows targetNamespace, falling back to the HelmRelease's own
// namespace; it decides where the rendered objects land.
func TestTemplateDerivesNamespace(t *testing.T) {
	tests := []struct {
		name            string
		targetNamespace string
		want            string
	}{
		{name: "targetNamespace wins", targetNamespace: "target", want: "target"},
		{name: "falls back to the HelmRelease namespace", want: "apps"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := ociClient(t)
			hr := ociRelease("apps", "podinfo")
			hr.Spec.TargetNamespace = tt.targetNamespace

			if _, err := c.Template(context.Background(), hr, nil); err != nil {
				t.Fatalf("Template: %v", err)
			}

			args := rec.templateArgs(t)
			i := slices.Index(args, "--namespace")
			if i < 0 || i+1 >= len(args) {
				t.Fatalf("--namespace missing from %v", args)
			}
			if args[i+1] != tt.want {
				t.Errorf("namespace = %q, want %q", args[i+1], tt.want)
			}
		})
	}
}

// Either install or upgrade opting out of schema validation disables it for
// the render, since flux-tools cannot know which action would run.
func TestTemplateDisableSchemaValidation(t *testing.T) {
	tests := []struct {
		name    string
		install bool
		upgrade bool
		want    bool
	}{
		{name: "neither", want: false},
		{name: "install opts out", install: true, want: true},
		{name: "upgrade opts out", upgrade: true, want: true},
		{name: "both opt out", install: true, upgrade: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := ociClient(t)
			hr := ociRelease("apps", "podinfo")
			if tt.install {
				hr.Spec.Install = &types.Install{DisableSchemaValidation: true}
			}
			if tt.upgrade {
				hr.Spec.Upgrade = &types.Upgrade{DisableSchemaValidation: true}
			}

			if _, err := c.Template(context.Background(), hr, nil); err != nil {
				t.Fatalf("Template: %v", err)
			}

			got := slices.Contains(rec.templateArgs(t), "--skip-schema-validation")
			if got != tt.want {
				t.Errorf("--skip-schema-validation present = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTemplateRejectsUnresolvableReleases(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(hr *types.HelmRelease)
		wantErr string
	}{
		{
			name: "valuesFiles need a GitRepository source",
			mutate: func(hr *types.HelmRelease) {
				hr.Spec.Chart.Spec.ValuesFiles = []string{"values-prod.yaml"}
			},
			wantErr: "only supported for GitRepository sources",
		},
		{
			name: "the repository must be registered",
			mutate: func(hr *types.HelmRelease) {
				hr.Spec.Chart.Spec.SourceRef.Name = "unregistered"
			},
			wantErr: "repository apps/unregistered not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := ociClient(t)
			hr := ociRelease("apps", "podinfo")
			tt.mutate(hr)

			_, err := c.Template(context.Background(), hr, nil)
			if err == nil {
				t.Fatalf("want an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
			// A release that cannot be resolved never reaches helm.
			if len(rec.calls) != 0 {
				t.Errorf("helm was invoked despite the error: %v", rec.calls)
			}
		})
	}
}

// sourceRef.namespace defaults to the HelmRelease namespace, so a repository
// registered in another namespace is only found when the ref names it.
func TestTemplateResolvesRepositoryAcrossNamespaces(t *testing.T) {
	c, _ := ociClient(t)
	hr := ociRelease("other", "podinfo")

	if _, err := c.Template(context.Background(), hr, nil); err == nil {
		t.Fatal("a repository in apps must not resolve from other/ without an explicit namespace")
	}

	hr.Spec.Chart.Spec.SourceRef.Namespace = "apps"
	if _, err := c.Template(context.Background(), hr, nil); err != nil {
		t.Fatalf("an explicit sourceRef.namespace must resolve: %v", err)
	}
}
