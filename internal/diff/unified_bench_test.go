package diff

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The benchmarks below compare the diff stage on a synthetic cluster-sized
// change: 100 modified file pairs of ~300 lines each. BenchmarkDiffStageGit
// reproduces the pre-I-4 implementation (one `git diff --no-index` process
// per pair) as the baseline for the native generator.

const benchPairs = 100

func writeBenchCorpus(tb testing.TB) (dir string) {
	tb.Helper()
	dir = tb.TempDir()

	var base strings.Builder
	base.WriteString("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\nspec:\n")
	for i := range 300 {
		fmt.Fprintf(&base, "  key%03d: value%03d\n", i, i)
	}

	for p := range benchPairs {
		current := base.String()
		incoming := strings.Replace(current, fmt.Sprintf("key%03d: value%03d", p, p),
			fmt.Sprintf("key%03d: changed%03d", p, p), 1)
		incoming = strings.Replace(incoming, "key250: value250", "key250: changed", 1)
		incoming += fmt.Sprintf("  added%03d: tail\n", p)

		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("current-%03d.yaml", p)), []byte(current), 0o644); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("incoming-%03d.yaml", p)), []byte(incoming), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return dir
}

func benchPair(dir string, p int) (string, string) {
	return filepath.Join(dir, fmt.Sprintf("current-%03d.yaml", p)),
		filepath.Join(dir, fmt.Sprintf("incoming-%03d.yaml", p))
}

func BenchmarkDiffStageNative(b *testing.B) {
	dir := writeBenchCorpus(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for p := range benchPairs {
			cur, inc := benchPair(dir, p)
			change, err := compareFiles("Namespace:x/Kind:y/Name:z.yaml", cur, inc, 10)
			if err != nil {
				b.Fatal(err)
			}
			if change == nil {
				b.Fatal("expected a change")
			}
		}
	}
}

func BenchmarkDiffStageGit(b *testing.B) {
	if _, err := exec.LookPath("git"); err != nil {
		b.Skip("git not in PATH")
	}
	dir := writeBenchCorpus(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for p := range benchPairs {
			cur, inc := benchPair(dir, p)
			// Pre-I-4 code path: one subprocess per modified pair.
			out, err := exec.Command("git", "diff", "--no-index", "-U10", "--no-color", cur, inc).Output()
			if err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
					b.Fatalf("git diff failed: %v", err)
				}
			}
			if len(out) == 0 {
				b.Fatal("expected a diff")
			}
		}
	}
}
