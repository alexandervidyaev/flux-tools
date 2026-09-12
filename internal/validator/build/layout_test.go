package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// layout_test.go runs BuildAll against repositories laid out the ways Flux
// users lay them out, each written into a temp dir, with a real kustomize.
// The point is the contract on the two roots: where the entry is and what
// spec.path is relative to, plus which GitRepository counts as this checkout.

func requireKustomize(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not in PATH")
	}
}

// repo is a scratch repository. Every path is relative to its root.
type repo struct {
	t    *testing.T
	root string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	return &repo{t: t, root: t.TempDir()}
}

func (r *repo) path(rel string) string { return filepath.Join(r.root, filepath.FromSlash(rel)) }

func (r *repo) file(rel, content string) *repo {
	writeFixture(r.t, r.path(rel), content)
	return r
}

// bootstrap writes what `flux bootstrap --path=<cluster>` writes.
func (r *repo) bootstrap(cluster string) *repo {
	r.file(cluster+"/flux-system/gotk-sync.yaml", gotkSync("./"+cluster))
	r.file(cluster+"/flux-system/kustomization.yaml", "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - gotk-sync.yaml\n")
	return r
}

func configMap(name string) string {
	return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + name + "\n  namespace: default\n"
}

func fluxKustomization(name, path, sourceKind, sourceName string) string {
	s := `apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: ` + name + `
  namespace: flux-system
spec:
  interval: 10m
  prune: true
`
	if path != "" {
		s += "  path: " + path + "\n"
	}
	s += "  sourceRef:\n    kind: " + sourceKind + "\n    name: " + sourceName + "\n"
	return s
}

func gitRepository(name, url string) string {
	return `apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: ` + name + `
  namespace: flux-system
spec:
  interval: 1m
  url: ` + url + `
  ref:
    branch: main
`
}

func kustomizationFile(resources ...string) string {
	s := "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n"
	for _, res := range resources {
		s += "  - " + res + "\n"
	}
	return s
}

func buildAll(t *testing.T, opts BuildOptions) ([]*unstructured.Unstructured, error) {
	t.Helper()
	opts.CacheDir = t.TempDir()
	opts.SkipFluxSystem = true
	builder, err := NewBuilder(context.Background(), opts)
	if err != nil {
		return nil, err
	}
	return builder.BuildAll(context.Background())
}

func names(objects []*unstructured.Unstructured) []string {
	out := make([]string, 0, len(objects))
	for _, o := range objects {
		if o.GetKind() == "ConfigMap" {
			out = append(out, o.GetName())
		}
	}
	sort.Strings(out)
	return out
}

func assertNames(t *testing.T, objects []*unstructured.Unstructured, want ...string) {
	t.Helper()
	got := names(objects)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ConfigMaps = %v, want %v", got, want)
	}
}

func assertError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
}

// --- bootstrap layouts -----------------------------------------------------

func TestLayoutBootstrapWithoutClusterKustomization(t *testing.T) {
	requireKustomize(t)
	r := newRepo(t).bootstrap("clusters/prod").
		file("clusters/prod/apps.yaml", fluxKustomization("apps", "./apps/prod", "GitRepository", "flux-system")).
		file("clusters/prod/infra.yaml", fluxKustomization("infra", "./infrastructure", "GitRepository", "flux-system")).
		file("clusters/prod/values.yaml", "replicas: 3\n").
		file("apps/prod/cm.yaml", configMap("apps")).
		file("infrastructure/cm.yaml", configMap("infra"))

	objects, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	assertNames(t, objects, "apps", "infra")
}

func TestLayoutBootstrapWithClusterKustomizationListsOnlyWhatItSays(t *testing.T) {
	requireKustomize(t)
	r := newRepo(t).bootstrap("clusters/prod").
		file("clusters/prod/kustomization.yaml", kustomizationFile("flux-system", "apps.yaml")).
		file("clusters/prod/apps.yaml", fluxKustomization("apps", "./apps/prod", "GitRepository", "flux-system")).
		file("clusters/prod/not-listed.yaml", fluxKustomization("unlisted", "./unlisted", "GitRepository", "flux-system")).
		file("apps/prod/cm.yaml", configMap("apps")).
		file("unlisted/cm.yaml", configMap("unlisted"))

	objects, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	assertNames(t, objects, "apps")
}

func TestLayoutBootstrapDeepClusterPathAnyNaming(t *testing.T) {
	requireKustomize(t)
	r := newRepo(t).bootstrap("k8s/fleet/eu/prod-1").
		file("k8s/fleet/eu/prod-1/apps.yaml", fluxKustomization("apps", "./workloads/eu", "GitRepository", "flux-system")).
		file("workloads/eu/cm.yaml", configMap("eu"))

	objects, err := buildAll(t, BuildOptions{Path: r.path("k8s/fleet/eu/prod-1"), RootPath: r.root})
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	assertNames(t, objects, "eu")
}

func TestLayoutBootstrapClusterAtRepositoryRoot(t *testing.T) {
	requireKustomize(t)
	r := newRepo(t)
	r.file("flux-system/gotk-sync.yaml", gotkSync("./")).
		file("flux-system/kustomization.yaml", kustomizationFile("gotk-sync.yaml")).
		file("apps.yaml", fluxKustomization("apps", "./apps", "GitRepository", "flux-system")).
		file("apps/cm.yaml", configMap("apps"))

	objects, err := buildAll(t, BuildOptions{Path: r.root, RootPath: r.root})
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	assertNames(t, objects, "apps")
}

// A spec.path that names a directory the repository does not have fails
// naming that directory, rather than rendering something else.
func TestLayoutSpecPathThatDoesNotExistIsAnError(t *testing.T) {
	requireKustomize(t)
	r := newRepo(t)
	r.file("clusters/prod/flux-system/gotk-sync.yaml", gotkSync("./clusters/staging"))

	_, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	assertError(t, err, filepath.Join("clusters", "staging"))
}

func TestLayoutBootstrapWithRootFlagStillKnowsTheSyncSource(t *testing.T) {
	requireKustomize(t)
	// --root replaces the derivation of the root, not the knowledge of which
	// GitRepository is this checkout: another source is still not local.
	r := newRepo(t).bootstrap("clusters/prod").
		file("clusters/prod/apps.yaml", fluxKustomization("apps", "./apps", "GitRepository", "flux-system")).
		file("clusters/prod/other.yaml", fluxKustomization("other", "./apps", "GitRepository", "someone-else")).
		file("apps/cm.yaml", configMap("apps"))

	_, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	assertError(t, err, "neither the flux-system sync source")

	if err := os.Remove(r.path("clusters/prod/other.yaml")); err != nil {
		t.Fatal(err)
	}
	objects, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	assertNames(t, objects, "apps")
}

// --- entry and spec.path without kustomization.yaml -------------------------

func TestLayoutNestedPathWithoutKustomizationIsGenerated(t *testing.T) {
	requireKustomize(t)
	r := newRepo(t).bootstrap("clusters/prod").
		file("clusters/prod/apps.yaml", fluxKustomization("apps", "./apps", "GitRepository", "flux-system")).
		file("apps/a.yaml", configMap("a")).
		file("apps/deep/b.yml", configMap("b")).
		file("apps/README.md", "kind: ConfigMap\n").
		file("apps/values.yaml", "image:\n  tag: 1\n").
		file("apps/with-kustomization/kustomization.yaml", kustomizationFile("c.yaml")).
		file("apps/with-kustomization/c.yaml", configMap("c")).
		file("apps/with-kustomization/not-listed.yaml", configMap("not-listed"))

	objects, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	assertNames(t, objects, "a", "b", "c")
}

func TestLayoutEmptySpecPathMeansRepositoryRoot(t *testing.T) {
	requireKustomize(t)
	r := newRepo(t).bootstrap("clusters/prod").
		file("clusters/prod/all.yaml", fluxKustomization("all", "", "GitRepository", "flux-system")).
		file("root-level.yaml", configMap("root-level"))

	objects, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	// The whole repository is the source: the ConfigMap at the root is in,
	// and so is everything the cluster directory itself carries.
	got := names(objects)
	if len(got) == 0 || got[len(got)-1] != "root-level" {
		t.Errorf("ConfigMaps = %v, want root-level among them", got)
	}
}

func TestLayoutSourceIgnoreAppliesToGeneratedKustomizations(t *testing.T) {
	requireKustomize(t)
	r := newRepo(t).bootstrap("clusters/prod").
		file(".sourceignore", "scratch/\n*.local.yaml\n").
		file("clusters/prod/apps.yaml", fluxKustomization("apps", "./apps", "GitRepository", "flux-system")).
		file("clusters/prod/debug.local.yaml", fluxKustomization("debug", "./debug", "GitRepository", "flux-system")).
		file("apps/a.yaml", configMap("a")).
		file("apps/scratch/draft.yaml", configMap("draft")).
		file("apps/listed/kustomization.yaml", kustomizationFile("x.local.yaml")).
		file("apps/listed/x.local.yaml", configMap("listed-explicitly")).
		file("debug/cm.yaml", configMap("debug"))

	objects, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	// debug.local.yaml and scratch/ are ignored; a file an explicit
	// kustomization.yaml lists is kustomize's business, not the matcher's.
	assertNames(t, objects, "a", "listed-explicitly")
}

// --- which GitRepository is this checkout -----------------------------------

func TestLayoutOtherDeclaredSourceIsClonedNotReadLocally(t *testing.T) {
	requireKustomize(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	// A second repository on disk, reachable through file://, holding the
	// same relative path with a different object. The build must show the
	// remote one.
	remote := newRepo(t).file("apps/cm.yaml", configMap("from-remote"))
	gitInit(t, remote.root)

	r := newRepo(t).bootstrap("clusters/prod").
		file("clusters/prod/shared.yaml", gitRepository("shared", "file://"+remote.root)).
		file("clusters/prod/apps.yaml", fluxKustomization("apps", "./apps", "GitRepository", "shared")).
		file("apps/cm.yaml", configMap("from-local"))

	objects, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	assertNames(t, objects, "from-remote")
}

func TestLayoutOtherUndeclaredSourceIsAnError(t *testing.T) {
	requireKustomize(t)
	r := newRepo(t).bootstrap("clusters/prod").
		file("clusters/prod/apps.yaml", fluxKustomization("apps", "./apps", "GitRepository", "shared")).
		file("apps/cm.yaml", configMap("local"))

	_, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	assertError(t, err, "neither the flux-system sync source")
}

func TestLayoutOtherSourceCloneFailureIsAnError(t *testing.T) {
	requireKustomize(t)
	r := newRepo(t).bootstrap("clusters/prod").
		file("clusters/prod/shared.yaml", gitRepository("shared", "file:///nonexistent/shared.git")).
		file("clusters/prod/apps.yaml", fluxKustomization("apps", "./apps", "GitRepository", "shared")).
		file("apps/cm.yaml", configMap("local"))

	_, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	assertError(t, err, "clone")
}

func TestLayoutSyncSourceWithAnyName(t *testing.T) {
	requireKustomize(t)
	r := newRepo(t)
	r.file("clusters/prod/flux-system/gotk-sync.yaml", strings.ReplaceAll(gotkSync("./clusters/prod"), "name: flux-system\n", "name: fleet\n")).
		file("clusters/prod/flux-system/kustomization.yaml", kustomizationFile("gotk-sync.yaml")).
		file("clusters/prod/apps.yaml", fluxKustomization("apps", "./apps", "GitRepository", "fleet")).
		file("apps/cm.yaml", configMap("apps"))

	objects, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	assertNames(t, objects, "apps")
}

func TestLayoutOCISourceErrorsUnlessSkipped(t *testing.T) {
	requireKustomize(t)
	r := newRepo(t).bootstrap("clusters/prod").
		file("clusters/prod/apps.yaml", fluxKustomization("apps", "./apps", "GitRepository", "flux-system")).
		file("clusters/prod/oci.yaml", fluxKustomization("oci", "./whatever", "OCIRepository", "charts")).
		file("apps/cm.yaml", configMap("apps"))

	_, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	assertError(t, err, "OCIRepository")

	objects, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), SkipOCI: true, RootPath: r.root})
	if err != nil {
		t.Fatalf("BuildAll with SkipOCI: %v", err)
	}
	assertNames(t, objects, "apps")
}

// --- flux-operator layouts (--root) ------------------------------------------

func TestLayoutOperatorEntryWithRoot(t *testing.T) {
	requireKustomize(t)
	r := newRepo(t).
		file("clusters/prod/apps.yaml", fluxKustomization("apps", "./apps/prod", "GitRepository", "flux-system")).
		file("clusters/prod/infra.yaml", fluxKustomization("infra", "./infrastructure", "GitRepository", "flux-system")).
		file("apps/prod/cm.yaml", configMap("apps")).
		file("infrastructure/cm.yaml", configMap("infra"))

	objects, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	assertNames(t, objects, "apps", "infra")
}

func TestLayoutOperatorDeclaredSourceIsCloned(t *testing.T) {
	requireKustomize(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	remote := newRepo(t).file("apps/cm.yaml", configMap("from-remote"))
	gitInit(t, remote.root)

	r := newRepo(t).
		file("clusters/prod/shared.yaml", gitRepository("shared", "file://"+remote.root)).
		file("clusters/prod/apps.yaml", fluxKustomization("apps", "./apps", "GitRepository", "shared")).
		file("clusters/prod/own.yaml", fluxKustomization("own", "./own", "GitRepository", "flux-system")).
		file("apps/cm.yaml", configMap("from-local")).
		file("own/cm.yaml", configMap("own"))

	objects, err := buildAll(t, BuildOptions{Path: r.path("clusters/prod"), RootPath: r.root})
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	assertNames(t, objects, "from-remote", "own")
}

func TestLayoutOperatorEntryIsRepositoryRoot(t *testing.T) {
	requireKustomize(t)
	r := newRepo(t).
		file("sync.yaml", fluxKustomization("apps", "./apps", "GitRepository", "flux-system")).
		file("apps/cm.yaml", configMap("apps"))

	objects, err := buildAll(t, BuildOptions{Path: r.root, RootPath: r.root})
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	assertNames(t, objects, "apps")
}

// gitInit turns dir into a git repository with one commit on main, so that
// file://<dir> can be cloned.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("add", "-A")
	run("commit", "-q", "-m", "init")
}
