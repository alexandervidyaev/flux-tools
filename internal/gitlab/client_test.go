package gitlab

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// stubDoer is a mock httpDoer — possible only because HTTPClient is now an
// interface (3.7) instead of interface{} with a *http.Client type assertion.
// Responses are served from a queue (one per expected request); requests are
// recorded for assertions.
type stubDoer struct {
	resps []*http.Response
	err   error
	reqs  []*http.Request
	last  *http.Request
}

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	s.last = req
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	if len(s.resps) == 0 {
		return nil, fmt.Errorf("stubDoer: unexpected request %s %s", req.Method, req.URL)
	}
	resp := s.resps[0]
	s.resps = s.resps[1:]
	return resp, nil
}

func newResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// newPagedResp builds a 200 response carrying GitLab's X-Next-Page header.
func newPagedResp(body string, nextPage string) *http.Response {
	resp := newResp(http.StatusOK, body)
	if nextPage != "" {
		resp.Header.Set("X-Next-Page", nextPage)
	}
	return resp
}

func TestListMergeRequestNotesParsesAndSetsToken(t *testing.T) {
	doer := &stubDoer{resps: []*http.Response{newResp(http.StatusOK, `[{"id":1,"body":"hi"},{"id":2,"body":"bye"}]`)}}
	c := &Client{BaseURL: "https://gl/api/v4", Token: "tok", ProjectID: "42", HTTPClient: doer}

	notes, err := c.ListMergeRequestNotes("7")
	if err != nil {
		t.Fatalf("ListMergeRequestNotes: %v", err)
	}
	if len(notes) != 2 || notes[0].ID != 1 || notes[1].Body != "bye" {
		t.Errorf("unexpected notes: %+v", notes)
	}
	if got := doer.last.Header.Get("PRIVATE-TOKEN"); got != "tok" {
		t.Errorf("PRIVATE-TOKEN header = %q, want tok", got)
	}
}

func TestListMergeRequestNotesNon2xxIsError(t *testing.T) {
	doer := &stubDoer{resps: []*http.Response{newResp(http.StatusForbidden, `{"message":"403 Forbidden"}`)}}
	c := &Client{BaseURL: "https://gl/api/v4", Token: "tok", ProjectID: "42", HTTPClient: doer}

	if _, err := c.ListMergeRequestNotes("7"); err == nil {
		t.Fatal("expected error on 403 response")
	}
}

// TestListMergeRequestNotesFollowsPagination verifies that all pages are
// fetched by following X-Next-Page. Previously only the first ?per_page=100
// page was read, so notes past 100 were silently dropped.
func TestListMergeRequestNotesFollowsPagination(t *testing.T) {
	doer := &stubDoer{resps: []*http.Response{
		newPagedResp(`[{"id":1,"body":"page1"}]`, "2"),
		newPagedResp(`[{"id":2,"body":"page2"}]`, "3"),
		newPagedResp(`[{"id":3,"body":"page3"}]`, ""),
	}}
	c := &Client{BaseURL: "https://gl/api/v4", Token: "tok", ProjectID: "42", HTTPClient: doer}

	notes, err := c.ListMergeRequestNotes("7")
	if err != nil {
		t.Fatalf("ListMergeRequestNotes: %v", err)
	}
	if len(notes) != 3 {
		t.Fatalf("got %d notes, want 3 (all pages)", len(notes))
	}
	if len(doer.reqs) != 3 {
		t.Fatalf("made %d requests, want 3", len(doer.reqs))
	}
	for i, wantPage := range []string{"page=1", "page=2", "page=3"} {
		if got := doer.reqs[i].URL.RawQuery; !strings.Contains(got, wantPage) {
			t.Errorf("request %d query = %q, want it to contain %q", i, got, wantPage)
		}
	}
}

// TestDeleteFluxToolsCommentsMarkers verifies marker matching: only non-system
// notes starting with the marker of the requested environment are deleted.
func TestDeleteFluxToolsCommentsMarkers(t *testing.T) {
	devMarker := GetCommentMarker("dev")
	stableMarker := GetCommentMarker("stable")

	notesJSON := fmt.Sprintf(`[
		{"id":1,"body":%q,"system":false},
		{"id":2,"body":%q,"system":false},
		{"id":3,"body":%q,"system":true},
		{"id":4,"body":"human comment","system":false}
	]`, devMarker+"\n\ndiff dev", stableMarker+"\n\ndiff stable", devMarker+"\n\nsystem")

	doer := &stubDoer{resps: []*http.Response{
		newPagedResp(notesJSON, ""),
		newResp(http.StatusNoContent, ""), // DELETE note 1
	}}
	c := &Client{BaseURL: "https://gl/api/v4", Token: "tok", ProjectID: "42", HTTPClient: doer}

	deleted, err := c.DeleteFluxToolsComments("7", "dev", 0)
	if err != nil {
		t.Fatalf("DeleteFluxToolsComments: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (only non-system dev note)", deleted)
	}
	del := doer.reqs[len(doer.reqs)-1]
	if del.Method != "DELETE" || !strings.HasSuffix(del.URL.Path, "/notes/1") {
		t.Errorf("last request = %s %s, want DELETE .../notes/1", del.Method, del.URL.Path)
	}
}

// TestPostNotePrependsMarker verifies the environment marker is prepended so
// the comment can later be found and deleted by DeleteFluxToolsComments.
func TestPostNotePrependsMarker(t *testing.T) {
	doer := &stubDoer{resps: []*http.Response{newResp(http.StatusCreated, `{}`)}}
	c := &Client{BaseURL: "https://gl/api/v4", Token: "tok", ProjectID: "42", HTTPClient: doer}

	if err := c.PostNote("7", "hello", "dev"); err != nil {
		t.Fatalf("PostNote: %v", err)
	}
	raw, _ := io.ReadAll(doer.last.Body)
	var payload map[string]string
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if !strings.HasPrefix(payload["body"], GetCommentMarker("dev")) {
		t.Errorf("posted body does not start with environment marker: %q", payload["body"])
	}
}
