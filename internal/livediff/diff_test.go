package livediff

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type runner struct {
	code int
	args []string
}

func (r *runner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r.args = append([]string{name}, args...)
	command := exec.CommandContext(ctx, "sh", "-c", "exit "+strconv.Itoa(r.code))
	return []byte("diff with topsecret"), command.Run()
}
func (r *runner) RunWithStdin(ctx context.Context, _ io.Reader, name string, args ...string) ([]byte, error) {
	return r.Run(ctx, name, args...)
}
func (r *runner) LookPath(string) error { return nil }
func TestLiveDiffExitCodesAndRedaction(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifest, []byte("apiVersion: v1\nkind: Secret\nmetadata:\n  name: test\nstringData:\n  password: topsecret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{0, 1, 2} {
		r := &runner{code: code}
		out, err := Run(context.Background(), r, manifest, "test-kubeconfig", "kustomize-controller")
		if (err != nil) != (code == 2) {
			t.Fatalf("exit %d: %v", code, err)
		}
		if strings.Contains(string(out), "topsecret") {
			t.Fatal("secret leaked")
		}
		if !strings.Contains(strings.Join(r.args, " "), " diff --server-side ") {
			t.Fatalf("unexpected operation %v", r.args)
		}
	}
}
func TestEmptyLiveDiffFails(t *testing.T) {
	file := filepath.Join(t.TempDir(), "empty.yaml")
	os.WriteFile(file, nil, 0600)
	if _, err := Run(context.Background(), &runner{}, file, "test-kubeconfig", "kustomize-controller"); err == nil {
		t.Fatal("accepted deletion-only empty input")
	}
}
