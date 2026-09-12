package helm

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fluxexec "github.com/alexandervidyaev/flux-tools/pkg/exec"
	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

// fakeRunner records git invocations and simulates a successful clone by
// creating the target directory with a .git marker.
type fakeRunner struct {
	calls [][]string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if name == "git" && len(args) > 0 && args[0] == "clone" {
		// Last positional arg is the destination directory.
		dst := args[len(args)-1]
		if err := os.MkdirAll(filepath.Join(dst, ".git"), 0o755); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (f *fakeRunner) RunWithStdin(ctx context.Context, _ io.Reader, name string, args ...string) ([]byte, error) {
	return f.Run(ctx, name, args...)
}

func (f *fakeRunner) LookPath(string) error { return nil }

func newTestClient(t *testing.T) (*Client, *fakeRunner) {
	t.Helper()
	cacheDir := t.TempDir()
	c, err := NewClient(cacheDir, 30)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	fr := &fakeRunner{}
	c.runner = fr
	return c, fr
}

func TestGitRefPrecedence(t *testing.T) {
	cases := []struct {
		name string
		ref  *types.GitRepositoryRef
		want string
	}{
		{"nil ref", nil, ""},
		{"branch only", &types.GitRepositoryRef{Branch: "master"}, "master"},
		{"tag overrides branch", &types.GitRepositoryRef{Branch: "master", Tag: "1.0.0"}, "1.0.0"},
		{"semver overrides tag", &types.GitRepositoryRef{Tag: "1.0.0", SemVer: ">=1.0.0"}, ">=1.0.0"},
		{"name overrides semver", &types.GitRepositoryRef{SemVer: ">=1.0.0", Name: "release-1"}, "release-1"},
		{"commit overrides everything", &types.GitRepositoryRef{Branch: "master", Tag: "1", Name: "n", Commit: "abc"}, "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gr := &types.GitRepository{Spec: types.GitRepositorySpec{Reference: tc.ref}}
			if got := gitRef(gr); got != tc.want {
				t.Errorf("gitRef = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGitCacheKeyDeterministic(t *testing.T) {
	a := gitCacheKey("https://gitlab.example.com/org/repo.git", "1.0.0")
	b := gitCacheKey("https://gitlab.example.com/org/repo.git", "1.0.0")
	if a != b {
		t.Errorf("expected deterministic key, got %q vs %q", a, b)
	}
	if !strings.HasSuffix(a, "_1.0.0") {
		t.Errorf("key should encode ref, got %q", a)
	}
	if !strings.HasPrefix(a, "gitlab.example.com_") {
		t.Errorf("key should encode host, got %q", a)
	}
}

func TestNormalizeGitURL(t *testing.T) {
	cases := []struct{ a, b string }{
		{"https://gitlab.example.com/org/repo.git", "git@gitlab.example.com:org/repo.git"},
		{"https://gitlab.example.com/org/repo", "https://gitlab.example.com/org/repo.git/"},
		{"git@gitlab.example.com:org/repo", "git@gitlab.example.com:org/repo.git"},
	}
	for _, tc := range cases {
		if normalizeGitURL(tc.a) != normalizeGitURL(tc.b) {
			t.Errorf("expected %q == %q after normalize, got %q vs %q",
				tc.a, tc.b, normalizeGitURL(tc.a), normalizeGitURL(tc.b))
		}
	}
}

func TestWithCITokenInjects(t *testing.T) {
	t.Setenv("CI_JOB_TOKEN", "secret-token")
	out, err := withCIToken("https://gitlab.example.com/org/repo.git")
	if err != nil {
		t.Fatalf("withCIToken: %v", err)
	}
	if !strings.Contains(out, "gitlab-ci-token:secret-token@") {
		t.Errorf("expected CI token injection, got %q", out)
	}
}

func TestWithCITokenLeavesSSHUntouched(t *testing.T) {
	t.Setenv("CI_JOB_TOKEN", "secret-token")
	in := "git@gitlab.example.com:org/repo.git"
	out, err := withCIToken(in)
	if err != nil {
		t.Fatalf("withCIToken: %v", err)
	}
	if out != in {
		t.Errorf("ssh URL must not be modified, got %q", out)
	}
}

func TestWithCITokenLeavesURLWithUserInfoUntouched(t *testing.T) {
	t.Setenv("CI_JOB_TOKEN", "secret-token")
	in := "https://oauth2:abc@gitlab.example.com/org/repo.git"
	out, err := withCIToken(in)
	if err != nil {
		t.Fatalf("withCIToken: %v", err)
	}
	if out != in {
		t.Errorf("URL with existing userinfo must not be modified, got %q", out)
	}
}

func TestResolveGitRepositoryPathExternalClones(t *testing.T) {
	c, fr := newTestClient(t)
	c.SetRootPath(t.TempDir()) // not the same URL
	external := &types.GitRepository{
		ObjectMeta: types.ObjectMeta{Name: "demo", Namespace: "demo-ns"},
		Spec: types.GitRepositorySpec{
			URL:       "https://gitlab.example.com/org/external.git",
			Reference: &types.GitRepositoryRef{Tag: "1.2.3"},
		},
	}
	got, err := c.ResolveGitRepositoryPath(context.Background(), external)
	if err != nil {
		t.Fatalf("ResolveGitRepositoryPath: %v", err)
	}
	if !strings.Contains(got, "/git/") {
		t.Errorf("expected clone path under cache/git, got %q", got)
	}
	if len(fr.calls) == 0 || fr.calls[0][0] != "git" {
		t.Fatalf("expected git clone, got %v", fr.calls)
	}
	cloneArgs := fr.calls[0]
	if cloneArgs[1] != "clone" {
		t.Errorf("expected clone, got %v", cloneArgs)
	}
	hasBranch := false
	for i, a := range cloneArgs {
		if a == "--branch" && i+1 < len(cloneArgs) && cloneArgs[i+1] == "1.2.3" {
			hasBranch = true
		}
	}
	if !hasBranch {
		t.Errorf("expected --branch 1.2.3 in args: %v", cloneArgs)
	}
	// Second resolution must hit the cache and not call git again.
	if _, err := c.ResolveGitRepositoryPath(context.Background(), external); err != nil {
		t.Fatalf("second ResolveGitRepositoryPath: %v", err)
	}
	if len(fr.calls) != 1 {
		t.Errorf("expected cached resolution to skip git, got %d calls", len(fr.calls))
	}
}

// guard against accidental import cleanup
var _ fluxexec.CommandRunner = (*fakeRunner)(nil)

func TestResolveGitSourcePathSelfSource(t *testing.T) {
	c, fr := newTestClient(t)
	c.SetRootPath("/some/root")
	c.SetSelfSource("flux-system/flux-system")

	got, err := c.ResolveGitSourcePath(context.Background(), "flux-system/flux-system")
	if err != nil {
		t.Fatalf("ResolveGitSourcePath: %v", err)
	}
	if got != "/some/root" || len(fr.calls) != 0 {
		t.Errorf("self source must resolve to rootPath without git, got %q, calls %v", got, fr.calls)
	}

	_, err = c.ResolveGitSourcePath(context.Background(), "flux-system/other")
	if err == nil || !strings.Contains(err.Error(), "neither the flux-system sync source") {
		t.Errorf("undeclared GitRepository with a known self source must fail, got %v", err)
	}
}

func TestResolveGitSourcePathUndeclaredIsLocalWhenSelfUnknown(t *testing.T) {
	c, fr := newTestClient(t)
	c.SetRootPath("/some/root")
	c.SetSelfSource("")

	got, err := c.ResolveGitSourcePath(context.Background(), "flux-system/anything")
	if err != nil {
		t.Fatalf("ResolveGitSourcePath: %v", err)
	}
	if got != "/some/root" || len(fr.calls) != 0 {
		t.Errorf("got %q, calls %v", got, fr.calls)
	}
}

func TestResolveGitSourcePathDeclaredIsCloned(t *testing.T) {
	c, fr := newTestClient(t)
	c.SetRootPath(t.TempDir())
	c.SetSelfSource("flux-system/flux-system")
	c.RegisterGitRepository(&types.GitRepository{
		ObjectMeta: types.ObjectMeta{Name: "shared", Namespace: "flux-system"},
		Spec:       types.GitRepositorySpec{URL: "https://example.com/org/shared.git", Reference: &types.GitRepositoryRef{Branch: "main"}},
	})

	got, err := c.ResolveGitSourcePath(context.Background(), "flux-system/shared")
	if err != nil {
		t.Fatalf("ResolveGitSourcePath: %v", err)
	}
	if !strings.Contains(got, "/git/") || len(fr.calls) == 0 || fr.calls[0][1] != "clone" {
		t.Errorf("declared GitRepository must be cloned, got %q, calls %v", got, fr.calls)
	}
}
