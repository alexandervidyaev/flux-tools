package diff

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// unified_test.go locks the native unified diff generator to the behavior of
// the real `git diff --no-index -U<n> --no-color` that compareFiles used to
// spawn. The hard criterion: after CleanDiffLines the outputs must match
// byte for byte (context/-/+ lines, hunk grouping, "\ No newline" markers).
// As a stricter cross-check the @@ hunk boundaries are compared too, with
// git's funcname decoration trimmed.

// gitBaseline runs the reference git diff. User/system git config is
// disabled so the oracle behaves like the pristine git in the CI image
// (default Myers algorithm with the indent heuristic).
func gitBaseline(t *testing.T, u int, aPath, bPath string) string {
	t.Helper()
	cmd := exec.Command("git", "diff", "--no-index", fmt.Sprintf("-U%d", u), "--no-color", aPath, bPath)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.Output()
	if err != nil {
		// Exit code 1 just means the files differ.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			t.Fatalf("git diff failed: %v", err)
		}
	}
	return string(out)
}

// normalizeHunks extracts the hunk stream from a raw diff: everything from
// the first @@ line on, with @@ headers cut after the second "@@" (git
// appends a funcname there, the native generator does not).
func normalizeHunks(raw string) string {
	lines := strings.Split(raw, "\n")
	var out []string
	started := false
	for _, line := range lines {
		if strings.HasPrefix(line, "@@ ") {
			started = true
			if idx := strings.Index(line[2:], "@@"); idx >= 0 {
				line = line[:2+idx+2]
			}
		}
		if started {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func firstMismatch(a, b []string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// assertGitParity writes the two contents to files, diffs them with real git
// and with the native generator, and compares the results.
func assertGitParity(t *testing.T, name string, current, incoming string, u int) {
	t.Helper()
	dir := t.TempDir()
	aPath := filepath.Join(dir, "current.yaml")
	bPath := filepath.Join(dir, "incoming.yaml")
	if err := os.WriteFile(aPath, []byte(current), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte(incoming), 0o644); err != nil {
		t.Fatal(err)
	}

	gitRaw := gitBaseline(t, u, aPath, bPath)
	nativeRaw := unifiedDiff([]byte(current), []byte(incoming), u)

	// The hard criterion: identical content lines after CleanDiffLines.
	gitClean := CleanDiffLines(gitRaw)
	nativeClean := CleanDiffLines(nativeRaw)
	if strings.Join(gitClean, "\n") != strings.Join(nativeClean, "\n") {
		i := firstMismatch(gitClean, nativeClean)
		t.Errorf("%s (-U%d): cleaned diff diverges from git at line %d\ngit:    %q\nnative: %q\n--- git cleaned ---\n%s\n--- native cleaned ---\n%s",
			name, u, i,
			lineAt(gitClean, i), lineAt(nativeClean, i),
			strings.Join(gitClean, "\n"), strings.Join(nativeClean, "\n"))
		return
	}

	// Stricter cross-check: same hunk boundaries and @@ numbering.
	if g, n := normalizeHunks(gitRaw), normalizeHunks(nativeRaw); g != n {
		t.Errorf("%s (-U%d): hunk stream diverges from git\n--- git ---\n%s\n--- native ---\n%s", name, u, g, n)
	}
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<missing>"
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH: golden comparison needs the reference binary")
	}
}

func TestUnifiedDiffGoldenAgainstGit(t *testing.T) {
	requireGit(t)

	base := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: app\n  namespace: demo\n  labels:\n    app: web\ndata:\n  key1: value1\n  key2: value2\n  key3: value3\n  key4: value4\n"

	manyLines := func(prefix string, n int) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, "%s%d: v\n", prefix, i)
		}
		return b.String()
	}

	cases := []struct {
		name              string
		current, incoming string
		ctx               []int
	}{
		{
			name:     "change at start",
			current:  base,
			incoming: strings.Replace(base, "apiVersion: v1", "apiVersion: v2", 1),
		},
		{
			name:     "change in middle",
			current:  base,
			incoming: strings.Replace(base, "  key2: value2", "  key2: changed", 1),
		},
		{
			name:     "change at end",
			current:  base,
			incoming: strings.Replace(base, "  key4: value4", "  key4: changed", 1),
		},
		{
			name:     "insert lines",
			current:  base,
			incoming: strings.Replace(base, "data:\n", "data:\n  key0: inserted\n  key00: inserted\n", 1),
		},
		{
			name:     "delete lines",
			current:  base,
			incoming: strings.Replace(base, "  key2: value2\n  key3: value3\n", "", 1),
		},
		{
			name:     "hunks merge at gap 2U (U=3)",
			current:  "a: 1\n" + manyLines("gap", 6) + "b: 1\n",
			incoming: "a: 2\n" + manyLines("gap", 6) + "b: 2\n",
			ctx:      []int{3},
		},
		{
			name:     "hunks split at gap 2U+1 (U=3)",
			current:  "a: 1\n" + manyLines("gap", 7) + "b: 1\n",
			incoming: "a: 2\n" + manyLines("gap", 7) + "b: 2\n",
			ctx:      []int{3},
		},
		{
			name:     "hunks merge at gap 2U (U=10)",
			current:  "a: 1\n" + manyLines("gap", 20) + "b: 1\n",
			incoming: "a: 2\n" + manyLines("gap", 20) + "b: 2\n",
			ctx:      []int{10},
		},
		{
			name:     "hunks split at gap 2U+1 (U=10)",
			current:  "a: 1\n" + manyLines("gap", 21) + "b: 1\n",
			incoming: "a: 2\n" + manyLines("gap", 21) + "b: 2\n",
			ctx:      []int{10},
		},
		{
			name:     "no trailing newline on both sides",
			current:  "a: 1\nb: 1\nc: old",
			incoming: "a: 1\nb: 1\nc: new",
		},
		{
			name:     "trailing newline removed",
			current:  "a: 1\nb: 1\nc: 1\n",
			incoming: "a: 1\nb: 1\nc: 1",
		},
		{
			name:     "trailing newline added",
			current:  "a: 1\nb: 1\nc: 1",
			incoming: "a: 1\nb: 1\nc: 1\n",
		},
		{
			name:     "no newline in unchanged context",
			current:  "a: old\nb: 1\nc: 1",
			incoming: "a: new\nb: 1\nc: 1",
		},
		{
			name:    "repeated block insertion (slider ambiguity)",
			current: "list:\n" + strings.Repeat("  - name: item\n    value: x\n", 4) + "tail: 1\n",
			incoming: "list:\n" + strings.Repeat("  - name: item\n    value: x\n", 6) +
				"tail: 1\n",
		},
		{
			name:     "blank lines around change",
			current:  "a: 1\n\nb: 1\n\nc: 1\n\nd: 1\n",
			incoming: "a: 1\n\nb: 2\n\nb2: 2\n\nc: 1\n\nd: 1\n",
		},
		{
			name: "heavily repeated lines (record cleanup path)",
			current: strings.Repeat("- item\n", 20) + "unique-a: 1\n" +
				strings.Repeat("- item\n", 20) + "unique-b: 1\n" +
				strings.Repeat("- item\n", 20),
			incoming: strings.Repeat("- item\n", 20) + "unique-a: 2\n" +
				strings.Repeat("- item\n", 22) + "unique-c: 1\n" +
				strings.Repeat("- item\n", 18),
		},
		{
			name:     "empty current side",
			current:  "",
			incoming: "a: 1\nb: 1\n",
		},
		{
			name:     "empty incoming side",
			current:  "a: 1\nb: 1\n",
			incoming: "",
		},
		{
			name:     "crlf content",
			current:  "a: 1\r\nb: old\r\nc: 1\r\n",
			incoming: "a: 1\r\nb: new\r\nc: 1\r\n",
		},
		{
			name:     "identical files produce no diff",
			current:  base,
			incoming: base,
		},
		{
			name:     "whitespace only lines",
			current:  "a: 1\n   \n\t\nb: old\n   \nc: 1\n",
			incoming: "a: 1\n   \n\t\nb: new\n   \nc: 1\n",
		},
	}

	for _, tc := range cases {
		ctxs := tc.ctx
		if ctxs == nil {
			ctxs = []int{0, 1, 3, 10}
		}
		for _, u := range ctxs {
			t.Run(fmt.Sprintf("%s U=%d", tc.name, u), func(t *testing.T) {
				assertGitParity(t, tc.name, tc.current, tc.incoming, u)
			})
		}
	}
}

// TestUnifiedDiffGoldenLargeShuffled exercises the libxdiff cost-cut and
// "good snake" heuristics in xdlSplit: two large files sharing long common
// runs separated by disjoint unique blocks, giving a big edit distance.
func TestUnifiedDiffGoldenLargeShuffled(t *testing.T) {
	requireGit(t)

	rng := rand.New(rand.NewSource(7))
	var a, b strings.Builder
	for block := range 40 {
		// Shared run: long enough to form snakes.
		for i := range 30 {
			fmt.Fprintf(&a, "shared-%d-%d: ok\n", block, i)
			fmt.Fprintf(&b, "shared-%d-%d: ok\n", block, i)
		}
		// Disjoint unique blocks on each side.
		for i := range 25 {
			fmt.Fprintf(&a, "only-a-%d-%d: %d\n", block, i, rng.Intn(1000))
		}
		for i := range 25 {
			fmt.Fprintf(&b, "only-b-%d-%d: %d\n", block, i, rng.Intn(1000))
		}
	}
	assertGitParity(t, "large shuffled", a.String(), b.String(), 3)
	assertGitParity(t, "large shuffled", a.String(), b.String(), 10)
}

// TestUnifiedDiffGoldenRandomized compares the native generator with git on
// deterministic pseudo-random YAML-like pairs across the context sizes used
// in practice (default 3, CI 10, plus 0 and 1 as edge cases).
func TestUnifiedDiffGoldenRandomized(t *testing.T) {
	requireGit(t)

	vocab := []string{
		"apiVersion: v1\n",
		"kind: ConfigMap\n",
		"metadata:\n",
		"  name: app\n",
		"  labels:\n",
		"    app: web\n",
		"    tier: backend\n",
		"spec:\n",
		"  replicas: 3\n",
		"  template:\n",
		"    spec:\n",
		"      containers:\n",
		"      - name: main\n",
		"        image: nginx\n",
		"\n",
		"---\n",
		"data:\n",
		"  key: value\n",
	}

	rng := rand.New(rand.NewSource(42))
	ctxs := []int{0, 1, 3, 10}

	for iter := range 200 {
		n := 1 + rng.Intn(120)
		current := make([]string, 0, n)
		for range n {
			current = append(current, vocab[rng.Intn(len(vocab))])
		}

		incoming := make([]string, 0, n+10)
		for _, line := range current {
			switch rng.Intn(10) {
			case 0: // drop
			case 1: // replace
				incoming = append(incoming, vocab[rng.Intn(len(vocab))])
			default: // keep
				incoming = append(incoming, line)
			}
			if rng.Intn(12) == 0 { // insert
				incoming = append(incoming, vocab[rng.Intn(len(vocab))])
			}
		}

		a := strings.Join(current, "")
		b := strings.Join(incoming, "")
		// Occasionally drop the trailing newline on either side.
		if rng.Intn(6) == 0 {
			a = strings.TrimSuffix(a, "\n")
		}
		if rng.Intn(6) == 0 {
			b = strings.TrimSuffix(b, "\n")
		}

		u := ctxs[rng.Intn(len(ctxs))]
		assertGitParity(t, fmt.Sprintf("randomized iter %d", iter), a, b, u)
		if t.Failed() {
			t.Fatalf("randomized golden mismatch at iter %d (seed 42)", iter)
		}
	}
}

// TestCleanDiffLinesKeepsNoNewlineMarker documents that the "\ No newline at
// end of file" marker survives CleanDiffLines and reaches reports, so the
// native generator must reproduce it exactly like git.
func TestCleanDiffLinesKeepsNoNewlineMarker(t *testing.T) {
	raw := "--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new\n\\ No newline at end of file\n"
	cleaned := CleanDiffLines(raw)
	want := []string{"-old", "+new", "\\ No newline at end of file"}
	if strings.Join(cleaned, "\n") != strings.Join(want, "\n") {
		t.Fatalf("CleanDiffLines = %q, want %q", cleaned, want)
	}
}
