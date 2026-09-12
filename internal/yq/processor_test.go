package yq

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	fluxexec "github.com/alexandervidyaev/flux-tools/pkg/exec"
)

// mockRunner records every invocation so tests can pin the command line this
// package builds, and can fail either LookPath or Run on demand.
type mockRunner struct {
	mu          sync.Mutex
	calls       [][]string
	lookPathErr error
	output      []byte
	runErr      error
}

func (m *mockRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	m.mu.Lock()
	m.calls = append(m.calls, append([]string{name}, args...))
	m.mu.Unlock()
	return m.output, m.runErr
}

func (m *mockRunner) RunWithStdin(ctx context.Context, _ io.Reader, name string, args ...string) ([]byte, error) {
	return m.Run(ctx, name, args...)
}

func (m *mockRunner) LookPath(string) error { return m.lookPathErr }

// stubRunner swaps the package-level runner for one test. Tests using it must
// not run in parallel.
func stubRunner(t *testing.T, m *mockRunner) {
	t.Helper()
	orig := runner
	runner = m
	t.Cleanup(func() { runner = orig })
}

func TestProcessFileWritesYqOutput(t *testing.T) {
	m := &mockRunner{output: []byte("kind: ConfigMap\n")}
	stubRunner(t, m)

	outputDir := filepath.Join(t.TempDir(), "out")
	input := filepath.Join(t.TempDir(), "cluster-a.yaml")
	if err := os.WriteFile(input, []byte("kind: ConfigMap\nstatus: {}\n"), 0644); err != nil {
		t.Fatalf("seed input: %v", err)
	}

	res := ProcessFile(context.Background(), input, "del(.status)", outputDir)
	if !res.Success {
		t.Fatalf("ProcessFile failed: %v", res.Error)
	}
	if res.InputPath != input {
		t.Errorf("InputPath = %q, want %q", res.InputPath, input)
	}

	// The output keeps the input's base name inside outputDir.
	wantOut := filepath.Join(outputDir, "cluster-a.yaml")
	if res.OutputPath != wantOut {
		t.Errorf("OutputPath = %q, want %q", res.OutputPath, wantOut)
	}
	data, err := os.ReadFile(wantOut)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "kind: ConfigMap\n" {
		t.Errorf("output = %q, want yq's stdout verbatim", data)
	}

	// The command line is a contract: consumers pin it in their CI.
	want := []string{"yq", "eval-all", "del(.status)", input}
	if len(m.calls) != 1 || !slices.Equal(m.calls[0], want) {
		t.Errorf("calls = %v, want exactly one %v", m.calls, want)
	}
}

func TestProcessFileFailures(t *testing.T) {
	boom := errors.New("exit status 1")

	tests := []struct {
		name    string
		runner  *mockRunner
		badDir  bool
		wantErr string
		wantRun bool
	}{
		{
			name:    "yq missing from PATH",
			runner:  &mockRunner{lookPathErr: errors.New("not found")},
			wantErr: "yq binary not found in PATH",
		},
		{
			name:    "yq exits non-zero",
			runner:  &mockRunner{runErr: boom},
			wantErr: "yq command failed",
			wantRun: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubRunner(t, tt.runner)

			input := filepath.Join(t.TempDir(), "in.yaml")
			if err := os.WriteFile(input, []byte("kind: X\n"), 0644); err != nil {
				t.Fatalf("seed input: %v", err)
			}

			res := ProcessFile(context.Background(), input, ".", filepath.Join(t.TempDir(), "out"))
			if res.Success {
				t.Fatal("want a failed result")
			}
			if !strings.Contains(res.Error.Error(), tt.wantErr) {
				t.Errorf("Error = %q, want it to contain %q", res.Error, tt.wantErr)
			}
			// A missing binary must be detected before anything is executed.
			if got := len(tt.runner.calls) > 0; got != tt.wantRun {
				t.Errorf("runner invoked = %v, want %v", got, tt.wantRun)
			}
		})
	}
}

func TestProcessFileUnwritableOutputDir(t *testing.T) {
	stubRunner(t, &mockRunner{})

	// A regular file where the output directory should go: MkdirAll fails and
	// the failure is reported, not panicked on.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0644); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}

	res := ProcessFile(context.Background(), "in.yaml", ".", filepath.Join(blocker, "out"))
	if res.Success {
		t.Fatal("want a failed result")
	}
	if !strings.Contains(res.Error.Error(), "failed to create output directory") {
		t.Errorf("Error = %q", res.Error)
	}
}

func TestProcessFilesParallelProcessesEveryFile(t *testing.T) {
	m := &mockRunner{output: []byte("ok\n")}
	stubRunner(t, m)

	dir := t.TempDir()
	var files []string
	for _, name := range []string{"a.yaml", "b.yaml", "c.yaml"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("kind: X\n"), 0644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		files = append(files, p)
	}

	outputDir := filepath.Join(t.TempDir(), "out")
	results := ProcessFilesParallel(context.Background(), files, ".", outputDir, 2)

	if len(results) != len(files) {
		t.Fatalf("got %d results, want %d", len(results), len(files))
	}
	for _, res := range results {
		if !res.Success {
			t.Errorf("%s: %v", res.InputPath, res.Error)
		}
	}
	if len(m.calls) != len(files) {
		t.Errorf("yq ran %d times, want %d", len(m.calls), len(files))
	}
}

// yq is the CI step that strips noisy fields, so it must never re-read its own
// output: files carrying ".clean." in the name are skipped.
func TestFindManifestsSkipsCleanFilesAndRecurses(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, p := range []string{
		filepath.Join(dir, "a.yaml"),
		filepath.Join(dir, "a.clean.yaml"),
		filepath.Join(dir, "notes.txt"),
		filepath.Join(nested, "b.yml"),
	} {
		if err := os.WriteFile(p, []byte("kind: X\n"), 0644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	found, err := FindManifests(dir)
	if err != nil {
		t.Fatalf("FindManifests: %v", err)
	}

	var names []string
	for _, f := range found {
		names = append(names, filepath.Base(f))
	}
	slices.Sort(names)

	want := []string{"a.yaml", "b.yml"}
	if !slices.Equal(names, want) {
		t.Errorf("found %v, want %v", names, want)
	}
}

var _ fluxexec.CommandRunner = (*mockRunner)(nil)
