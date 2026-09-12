package gitlab

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexandervidyaev/flux-tools/internal/diff"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

// TestPostClusterDiffNoChanges verifies the "no changes" path returns the
// errNoChanges sentinel (matchable via errors.Is) before any network call,
// replacing the previous fragile strings.Contains(err.Error(), "no changes").
func TestPostClusterDiffNoChanges(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "cluster-a-diff.md")
	if err := os.WriteFile(file, []byte("## ✅ No changes\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	outcome, err := postClusterDiff(&Client{}, PostCommentOptions{}, file, "cluster-a")
	if !errors.Is(err, errNoChanges) {
		t.Fatalf("err = %v, want errNoChanges", err)
	}
	if outcome.tooLarge {
		t.Errorf("outcome.tooLarge = true, want false for no-changes diff")
	}
}

// TestPostClusterDiffReadError checks that an unreadable file yields a real
// error that is NOT classified as errNoChanges (so the caller counts it as
// failed, not skipped).
func TestPostClusterDiffReadError(t *testing.T) {
	_, err := postClusterDiff(&Client{}, PostCommentOptions{}, filepath.Join(t.TempDir(), "missing-diff.md"), "cluster-x")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if errors.Is(err, errNoChanges) {
		t.Errorf("read error must not be classified as errNoChanges")
	}
}

// TestPostClusterDiffUsesJSONStats verifies that with a <cluster>-diff.json
// artifact next to the markdown, stats come from the structured data (the
// markdown here intentionally carries no parseable section headers).
func TestPostClusterDiffUsesJSONStats(t *testing.T) {
	dir := t.TempDir()
	mdPath := filepath.Join(dir, "cluster-a-diff.md")
	if err := os.WriteFile(mdPath, []byte("some human-oriented body without section headers\n"), 0o644); err != nil {
		t.Fatalf("write md: %v", err)
	}

	clusterDiff := &diff.ClusterDiff{
		ClusterName: "cluster-a",
		Changes: []*diff.FileChange{
			{Resource: diff.ResourceInfo{Kind: "deployment", Name: "a"}, Status: diff.StatusAdded, Diff: "x"},
			{Resource: diff.ResourceInfo{Kind: "deployment", Name: "b"}, Status: diff.StatusModified, Diff: "y"},
		},
	}
	data, err := diff.MarshalClusterDiff(clusterDiff)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cluster-a-diff.json"), data, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	doer := &stubDoer{resps: []*http.Response{newResp(http.StatusCreated, `{}`)}}
	client := &Client{BaseURL: "https://gl/api/v4", Token: "t", ProjectID: "42", HTTPClient: doer}

	if _, err := postClusterDiff(client, PostCommentOptions{MergeRequestIID: "7"}, mdPath, "cluster-a"); err != nil {
		t.Fatalf("postClusterDiff: %v", err)
	}

	body, _ := io.ReadAll(doer.last.Body)
	posted := string(body)
	if !strings.Contains(posted, "Added: 1") || !strings.Contains(posted, "Modified: 1") {
		t.Errorf("posted body lacks structured stats: %s", posted)
	}
}

// TestPostSummaryIndex verifies index.md is posted as a comment and that a
// missing index is not an error (older artifacts).
func TestPostSummaryIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte("# 🔄 Changes Summary\n"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	doer := &stubDoer{resps: []*http.Response{newResp(http.StatusCreated, `{}`)}}
	client := &Client{BaseURL: "https://gl/api/v4", Token: "t", ProjectID: "42", HTTPClient: doer}
	p := output.New(false)

	if err := postSummaryIndex(client, PostCommentOptions{DiffsDir: dir, MergeRequestIID: "7"}, p); err != nil {
		t.Fatalf("postSummaryIndex: %v", err)
	}
	if doer.last == nil {
		t.Fatal("no request was made")
	}
	body, _ := io.ReadAll(doer.last.Body)
	if !strings.Contains(string(body), "Changes Summary") {
		t.Errorf("posted body lacks index content: %s", body)
	}

	// Missing index.md — silently skipped
	doer2 := &stubDoer{resps: []*http.Response{newResp(http.StatusCreated, `{}`)}}
	client2 := &Client{BaseURL: "https://gl/api/v4", Token: "t", ProjectID: "42", HTTPClient: doer2}
	if err := postSummaryIndex(client2, PostCommentOptions{DiffsDir: t.TempDir(), MergeRequestIID: "7"}, p); err != nil {
		t.Fatalf("postSummaryIndex without index.md: %v", err)
	}
	if doer2.last != nil {
		t.Error("request was made despite missing index.md")
	}
}

// TestPostSummaryIndexSkipped verifies SkipSummary suppresses the summary
// comment even when index.md exists.
func TestPostSummaryIndexSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte("# 🔄 Changes Summary\n"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	doer := &stubDoer{resps: []*http.Response{newResp(http.StatusCreated, `{}`)}}
	client := &Client{BaseURL: "https://gl/api/v4", Token: "t", ProjectID: "42", HTTPClient: doer}

	opts := PostCommentOptions{DiffsDir: dir, MergeRequestIID: "7", SkipSummary: true}
	if err := postSummaryIndex(client, opts, output.New(false)); err != nil {
		t.Fatalf("postSummaryIndex: %v", err)
	}
	if doer.last != nil {
		t.Error("summary comment was posted despite SkipSummary")
	}
}

// TestPostClusterDiffRequiresJSON: after the markdown reverse-parser removal
// the structured artifact is mandatory — a diff without it fails loudly
// instead of posting wrong stats.
func TestPostClusterDiffRequiresJSON(t *testing.T) {
	dir := t.TempDir()
	mdPath := filepath.Join(dir, "cluster-a-diff.md")
	if err := os.WriteFile(mdPath, []byte("### ⚙️ Modified (1):\n"), 0o644); err != nil {
		t.Fatalf("write md: %v", err)
	}

	_, err := postClusterDiff(&Client{}, PostCommentOptions{MergeRequestIID: "7"}, mdPath, "cluster-a")
	if err == nil {
		t.Fatal("expected error when <cluster>-diff.json is missing")
	}
	if errors.Is(err, errNoChanges) {
		t.Error("missing JSON must not be classified as errNoChanges")
	}
}

// TestPostClusterDiffTooLargeUsesCompactFormat: oversized diffs are replaced
// with the compact format built from the structured JSON (lists for
// Added/Deleted, cleaned diff blocks for Modified) and flagged tooLarge.
func TestPostClusterDiffTooLargeUsesCompactFormat(t *testing.T) {
	dir := t.TempDir()
	mdPath := filepath.Join(dir, "cluster-a-diff.md")
	// Oversized human-facing markdown (> maxCommentSize)
	if err := os.WriteFile(mdPath, []byte(strings.Repeat("x", maxCommentSize+1)), 0o644); err != nil {
		t.Fatalf("write md: %v", err)
	}

	clusterDiff := &diff.ClusterDiff{
		ClusterName: "cluster-a",
		Changes: []*diff.FileChange{
			{Resource: diff.ResourceInfo{Namespace: "ns", Kind: "deployment", Name: "new-app"}, Status: diff.StatusAdded, Diff: "kind: Deployment\n"},
			{
				Resource: diff.ResourceInfo{Namespace: "ns", Kind: "configmap", Name: "cm"},
				Status:   diff.StatusModified,
				Diff:     "diff --git a/x b/x\nindex 1..2 100644\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n",
			},
		},
	}
	data, err := diff.MarshalClusterDiff(clusterDiff)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cluster-a-diff.json"), data, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	doer := &stubDoer{resps: []*http.Response{newResp(http.StatusCreated, `{}`)}}
	client := &Client{BaseURL: "https://gl/api/v4", Token: "t", ProjectID: "42", HTTPClient: doer}

	outcome, err := postClusterDiff(client, PostCommentOptions{MergeRequestIID: "7"}, mdPath, "cluster-a")
	if err != nil {
		t.Fatalf("postClusterDiff: %v", err)
	}
	if !outcome.tooLarge {
		t.Error("outcome.tooLarge = false, want true for oversized diff")
	}

	raw, _ := io.ReadAll(doer.last.Body)
	var payload map[string]string
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	body := payload["body"]
	if len(body) > maxCommentSize {
		t.Errorf("compact body is still oversized: %d bytes", len(body))
	}
	for _, want := range []string{"Diff too large", "ns/deployment/new-app", "-old", "+new"} {
		if !strings.Contains(body, want) {
			t.Errorf("compact body lacks %q", want)
		}
	}
	// Git headers must be cleaned by diff.CleanDiffLines
	for _, banned := range []string{"diff --git", "@@", "index 1..2"} {
		if strings.Contains(body, banned) {
			t.Errorf("compact body leaks git header %q", banned)
		}
	}
}
