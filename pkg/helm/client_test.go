package helm

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

// stubRunner is a no-op CommandRunner so tests never shell out to real helm.
type stubRunner struct{}

func (stubRunner) Run(context.Context, string, ...string) ([]byte, error) { return nil, nil }
func (stubRunner) RunWithStdin(context.Context, io.Reader, string, ...string) ([]byte, error) {
	return nil, nil
}
func (stubRunner) LookPath(string) error { return nil }

func newRepo(name string) *types.HelmRepository {
	r := &types.HelmRepository{Spec: types.HelmRepositorySpec{URL: "https://example.com/" + name}}
	r.Name = name
	r.Namespace = "flux-system"
	return r
}

// TestClientConcurrentRepositoryAccess exercises the repository maps from many
// goroutines. Its purpose is to run under `go test -race`: before 1.5,
// GetRepository and EnsureRepositoryAdded touched the maps without holding mu,
// which the race detector flags. AddRepository must not be called while holding
// mu (it locks internally), which this also indirectly checks (a re-lock would
// deadlock).
func TestClientConcurrentRepositoryAccess(t *testing.T) {
	c, err := NewClient(t.TempDir(), 60)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.runner = stubRunner{}

	repos := []*types.HelmRepository{newRepo("a"), newRepo("b"), newRepo("c")}

	var wg sync.WaitGroup
	for i := range 60 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := repos[i%len(repos)]
			// All three touch the shared maps; interleaving them across
			// goroutines is what the race detector inspects.
			_, _ = c.EnsureRepositoryAdded(context.Background(), r)
			_, _ = c.GetRepository(r.GetKey())
			c.RegisterRepository(r)
		}(i)
	}
	wg.Wait()

	// After the dust settles every repo should be registered and marked added.
	for _, r := range repos {
		if _, ok := c.GetRepository(r.GetKey()); !ok {
			t.Errorf("repository %s not registered", r.GetKey())
		}
	}
}
