// Package gitlab provides a client for interacting with the GitLab merge request API.
package gitlab

import (
	"net/http"
	"time"
)

// httpDoer is the subset of *http.Client the GitLab client needs. Declaring it
// as an interface (instead of interface{} + a runtime type assertion) makes the
// client mockable in unit tests: *http.Client satisfies it, and so does any
// test double.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client represents a GitLab API client
type Client struct {
	BaseURL    string
	Token      string
	ProjectID  string
	HTTPClient httpDoer
}

// Note represents a GitLab note (comment)
type Note struct {
	ID        int       `json:"id"`
	Body      string    `json:"body"`
	Author    Author    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
	System    bool      `json:"system"`
}

// Author represents a GitLab user
type Author struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

// PostCommentOptions contains options for posting comments
type PostCommentOptions struct {
	DiffsDir          string
	ProjectID         string
	MergeRequestIID   string
	GitLabURL         string
	Token             string
	SkipDelete        bool   // Don't delete old comments
	SkipSummary       bool   // Don't post the index.md summary comment
	Environment       string // Environment name (e.g., "prod", "staging")
	Workers           int    // Number of parallel workers for posting (default: 3)
	ProjectPath       string // CI_PROJECT_PATH for artifacts link
	JobID             string // CI_JOB_ID for artifacts link
	Timeout           int    // HTTP client timeout in seconds (default: 30)
	RateLimitMs       int    // Rate limiter delay for posting in milliseconds (default: 500)
	DeleteRateLimitMs int    // Rate limiter delay for deleting in milliseconds (default: 200)
}

// PostResult represents the result of posting operation
type PostResult struct {
	Posted   int
	Skipped  int
	Failed   int
	Deleted  int
	TooLarge int
	Errors   []error
}

// Comment marker prefix to identify flux-tools generated comments
const CommentMarkerPrefix = "<!-- flux-tools-generated"

// GetCommentMarker returns environment-specific comment marker
// Returns: <!-- flux-tools-generated:environment -->
// If environment is empty, returns: <!-- flux-tools-generated -->
func GetCommentMarker(environment string) string {
	if environment == "" || environment == "N/A" {
		return CommentMarkerPrefix + " -->"
	}
	return CommentMarkerPrefix + ":" + environment + " -->"
}
