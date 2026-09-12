package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

func TestEmptyResultCreatesTheOutputDirectory(t *testing.T) {
	var buf bytes.Buffer
	out := filepath.Join(t.TempDir(), "nested", "current")

	if err := emptyResult(output.NewWithWriter(&buf, false), out, "/absent"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The next stage of the pipeline has to find an empty tree, not a missing
	// one — that is the whole point of the flag.
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", out)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("output directory is not empty: %v", entries)
	}

	// The reason must be visible in the log; a silently empty diff is exactly
	// the failure this flag risks introducing.
	if !strings.Contains(buf.String(), "--allow-missing-path") {
		t.Errorf("the log does not say why the result is empty: %q", buf.String())
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("message does not end in a newline: %q", buf.String())
	}
}

func TestEmptyResultNeedsAnOutputDirectory(t *testing.T) {
	err := emptyResult(output.NewWithWriter(&bytes.Buffer{}, false), "", "/absent")
	if err == nil {
		t.Fatal("expected an error without an output directory")
	}
	if !strings.Contains(err.Error(), "-o") {
		t.Errorf("the error does not name the missing flag: %v", err)
	}
}

// The flag has to be recognised by the hand-rolled parser the passthrough
// commands use, and must not be forwarded to the wrapped tool: yq does not
// know it.
func TestAllowMissingPathIsExtractedNotPassedThrough(t *testing.T) {
	t.Run("yq", func(t *testing.T) {
		_, filter, path, outDir, _, allow, err := parseYqArgs(
			[]string{"eval-all", "select(.kind != \"HelmRelease\")", "./in", "-o", "./out", "--allow-missing-path"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !allow {
			t.Error("--allow-missing-path was not recognised")
		}
		if filter != `select(.kind != "HelmRelease")` || path != "./in" || outDir != "./out" {
			t.Errorf("positionals broke: filter=%q path=%q out=%q", filter, path, outDir)
		}
	})

}

// Without the flag the parsers must be unchanged: a stray --allow-missing-path
// is not silently swallowed for commands that do not offer it.
func TestAllowMissingPathDefaultsOff(t *testing.T) {
	_, _, _, _, _, allow, err := parseYqArgs([]string{"eval-all", ".", "./in", "-o", "./out"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allow {
		t.Error("the flag defaulted to on")
	}
}
