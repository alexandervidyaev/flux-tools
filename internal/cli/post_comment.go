package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/alexandervidyaev/flux-tools/internal/gitlab"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

func NewPostCommentCmd() *cobra.Command {
	var (
		projectID         string
		mrIID             string
		gitlabURL         string
		token             string
		skipDelete        bool
		skipSummary       bool
		environment       string
		workers           int
		timeout           int
		rateLimitMs       int
		deleteRateLimitMs int
	)

	cmd := &cobra.Command{
		Use:   "post-comment <diffs-dir>",
		Short: "Post per-cluster diff files as comments to GitLab MR",
		Long: `Post per-cluster diff files as separate comments to GitLab merge request.

This command automatically:
1. Deletes old flux-tools comments from the MR (to avoid clutter)
2. Posts index.md as summary comment (unless --skip-summary)
3. Posts each cluster diff as separate comment
4. Skips clusters with no changes
5. Replaces a diff too large for a comment with a compact one plus an artifact link

The command reads configuration from environment variables by default:
- GITLAB_TOKEN or CI_JOB_TOKEN - GitLab API token
- CI_PROJECT_ID - GitLab project ID
- CI_MERGE_REQUEST_IID - Merge request number
- CI_API_V4_URL - GitLab API URL (default: https://gitlab.com/api/v4)

Flags can be used to override environment variables.

Examples:
  # In GitLab CI (uses environment variables automatically)
  flux-tools post-comment ./manifests-diffs

  # Override specific values
  flux-tools post-comment ./manifests-diffs --mr-iid 123

  # Local testing
  export CI_PROJECT_ID="12345"
  export CI_MERGE_REQUEST_IID="42"
  export GITLAB_TOKEN="glpat-xxxxxxxxxxxx"
  flux-tools post-comment ./manifests-diffs

  # Don't delete old comments
  flux-tools post-comment ./manifests-diffs --skip-delete

  # Don't post the summary comment (per-cluster comments only)
  flux-tools post-comment ./manifests-diffs --skip-summary
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPostComment(commandContext(cmd), postCommentParams{
				DiffsDir:          args[0],
				ProjectID:         projectID,
				MrIID:             mrIID,
				GitLabURL:         gitlabURL,
				Token:             token,
				Environment:       environment,
				Workers:           workers,
				Timeout:           timeout,
				RateLimitMs:       rateLimitMs,
				DeleteRateLimitMs: deleteRateLimitMs,
				SkipDelete:        skipDelete,
				SkipSummary:       skipSummary,
				ProjectPath:       os.Getenv("CI_PROJECT_PATH"),
				JobID:             os.Getenv("CI_JOB_ID"),
			})
		},
	}

	// Flags with environment variable fallbacks
	cmd.Flags().StringVar(&projectID, "project-id", os.Getenv("CI_PROJECT_ID"),
		"GitLab project ID (default: $CI_PROJECT_ID)")
	cmd.Flags().StringVar(&mrIID, "mr-iid", os.Getenv("CI_MERGE_REQUEST_IID"),
		"Merge request IID (default: $CI_MERGE_REQUEST_IID)")
	cmd.Flags().StringVar(&gitlabURL, "gitlab-url", getGitLabURL(),
		"GitLab API URL (default: $CI_API_V4_URL or https://gitlab.com/api/v4)")
	cmd.Flags().StringVar(&token, "token", getGitLabToken(),
		"GitLab API token (default: $GITLAB_TOKEN or $CI_JOB_TOKEN)")
	cmd.Flags().StringVar(&environment, "environment", getEnvironment(),
		"Environment name (default: $CI_ENVIRONMENT_NAME or $ENVIRONMENT)")
	cmd.Flags().IntVar(&workers, "workers", 3,
		"Number of parallel workers for posting comments (default: 3)")
	cmd.Flags().IntVar(&timeout, "timeout", getTimeout(),
		"HTTP client timeout in seconds (default: $GITLAB_TIMEOUT or 30)")
	cmd.Flags().IntVar(&rateLimitMs, "rate-limit", getRateLimit(),
		"Rate limiter delay for posting in milliseconds, 0 = disabled (default: $GITLAB_RATE_LIMIT_MS or 500)")
	cmd.Flags().IntVar(&deleteRateLimitMs, "delete-rate-limit", getDeleteRateLimit(),
		"Rate limiter delay for deleting in milliseconds, 0 = disabled (default: $GITLAB_DELETE_RATE_LIMIT_MS or 200)")
	cmd.Flags().BoolVar(&skipDelete, "skip-delete", false,
		"Don't delete old flux-tools comments before posting new ones")
	cmd.Flags().BoolVar(&skipSummary, "skip-summary", getSkipSummary(),
		"Don't post the index.md summary comment (default: $FLUX_TOOLS_SKIP_SUMMARY)")

	return cmd
}

type postCommentParams struct {
	DiffsDir          string
	ProjectID         string
	MrIID             string
	GitLabURL         string
	Token             string
	Environment       string
	Workers           int
	Timeout           int
	RateLimitMs       int
	DeleteRateLimitMs int
	SkipDelete        bool
	SkipSummary       bool
	ProjectPath       string
	JobID             string
}

func runPostComment(ctx context.Context, p postCommentParams) error {
	pr := output.New(false)

	// Show configuration
	pr.Info("==================================================\n")
	pr.Info("📤 Posting diffs to GitLab MR\n")
	pr.Info("==================================================\n")
	pr.Info("Diffs directory:     %s\n", p.DiffsDir)
	pr.Info("GitLab URL:          %s\n", p.GitLabURL)
	pr.Info("Project ID:          %s\n", p.ProjectID)
	pr.Info("MR IID:              %s\n", p.MrIID)
	pr.Info("Environment:         %s\n", p.Environment)
	pr.Info("Workers:             %d\n", p.Workers)

	// Timeout
	if p.Timeout < 0 {
		pr.Info("Timeout:             30s (default)\n")
	} else {
		pr.Info("Timeout:             %ds\n", p.Timeout)
	}

	// Rate limit for posting
	if p.RateLimitMs < 0 {
		pr.Info("Rate limit:          500ms (default)\n")
	} else if p.RateLimitMs == 0 {
		pr.Info("Rate limit:          disabled\n")
	} else {
		pr.Info("Rate limit:          %dms\n", p.RateLimitMs)
	}

	// Rate limit for deleting
	if p.DeleteRateLimitMs < 0 {
		pr.Info("Delete rate limit:   200ms (default)\n")
	} else if p.DeleteRateLimitMs == 0 {
		pr.Info("Delete rate limit:   disabled\n")
	} else {
		pr.Info("Delete rate limit:   %dms\n", p.DeleteRateLimitMs)
	}

	pr.Info("Skip delete:         %v\n", p.SkipDelete)
	pr.Info("Skip summary:        %v\n", p.SkipSummary)
	if p.ProjectPath != "" && p.JobID != "" {
		pr.Info("Artifacts link:      enabled (job %s)\n", p.JobID)
	}
	pr.Info("\n")

	// Prepare options
	opts := gitlab.PostCommentOptions{
		DiffsDir:          p.DiffsDir,
		ProjectID:         p.ProjectID,
		MergeRequestIID:   p.MrIID,
		GitLabURL:         p.GitLabURL,
		Token:             p.Token,
		Environment:       p.Environment,
		Workers:           p.Workers,
		Timeout:           p.Timeout,
		RateLimitMs:       p.RateLimitMs,
		DeleteRateLimitMs: p.DeleteRateLimitMs,
		SkipDelete:        p.SkipDelete,
		SkipSummary:       p.SkipSummary,
		ProjectPath:       p.ProjectPath,
		JobID:             p.JobID,
	}

	// Post diffs
	result, err := gitlab.PostDiffsToMR(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed to post comments: %w", err)
	}

	// Print summary
	pr.Info("\n")
	pr.Info("==================================================\n")
	pr.Info("✅ Done!\n")
	pr.Info("==================================================\n")

	if result.Deleted > 0 {
		pr.Info("Deleted:  %d old comments\n", result.Deleted)
	}

	pr.Info("Posted:   %d comments\n", result.Posted)

	if result.Skipped > 0 {
		pr.Info("Skipped:  %d comments (no changes)\n", result.Skipped)
	}

	if result.TooLarge > 0 {
		pr.Info("Large:    %d comments (placeholders posted)\n", result.TooLarge)
	}

	if result.Failed > 0 {
		pr.Info("Failed:   %d comments\n", result.Failed)
		pr.Info("\n")
		pr.Info("⚠️  Some comments failed to post:\n")
		for _, err := range result.Errors {
			pr.Info("  - %v\n", err)
		}
		return fmt.Errorf("some comments failed to post")
	}

	pr.Info("\n")
	pr.Info("🎉 All diffs posted to MR !%s\n", p.MrIID)

	return nil
}

// getGitLabURL returns GitLab API URL from environment
func getGitLabURL() string {
	if url := os.Getenv("CI_API_V4_URL"); url != "" {
		return url
	}
	return "https://gitlab.com/api/v4"
}

// getGitLabToken returns GitLab token from environment
// Tries GITLAB_TOKEN first, then CI_JOB_TOKEN
func getGitLabToken() string {
	if token := os.Getenv("GITLAB_TOKEN"); token != "" {
		return token
	}
	return os.Getenv("CI_JOB_TOKEN")
}

// getEnvironment returns environment name from environment variables
// Tries CI_ENVIRONMENT_NAME first (GitLab CI standard), then ENVIRONMENT (custom)
func getEnvironment() string {
	if env := os.Getenv("CI_ENVIRONMENT_NAME"); env != "" {
		return env
	}
	return os.Getenv("ENVIRONMENT")
}

// getSkipSummary returns whether the summary comment should be skipped, from
// FLUX_TOOLS_SKIP_SUMMARY. Unset or unparseable means "post the summary" (the
// historical behavior); an unparseable value warns instead of silently
// flipping the default.
func getSkipSummary() bool {
	raw := os.Getenv("FLUX_TOOLS_SKIP_SUMMARY")
	if raw == "" {
		return false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		output.New(false).Warn("invalid FLUX_TOOLS_SKIP_SUMMARY=%q, expected boolean; using default\n", raw)
		return false
	}
	return v
}

// parseEnvInt parses an integer environment variable. Returns fallback (with
// a warning) when the value is set but not a valid integer — previously
// fmt.Sscanf silently yielded 0 on garbage, which e.g. disabled rate limiting
// on a typo.
func parseEnvInt(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		output.New(false).Warn("invalid %s=%q, expected integer; using default\n", name, raw)
		return fallback
	}
	return v
}

// getTimeout returns timeout in seconds from environment variable
// Returns -1 if not set (will use default 30s), or the value if set
func getTimeout() int {
	t := parseEnvInt("GITLAB_TIMEOUT", -1)
	if t == 0 || t < -1 {
		return 30 // Invalid value, use default
	}
	return t
}

// getRateLimit returns rate limit in milliseconds from environment variable
// Returns -1 if not set (will use default), 0 if explicitly set to 0 (disabled), or the value
func getRateLimit() int {
	return parseEnvInt("GITLAB_RATE_LIMIT_MS", -1) // -1 = not set (default), 0 = disabled, > 0 = explicit
}

// getDeleteRateLimit returns delete rate limit in milliseconds from environment variable
// Returns -1 if not set (will use default), 0 if explicitly set to 0 (disabled), or the value
func getDeleteRateLimit() int {
	return parseEnvInt("GITLAB_DELETE_RATE_LIMIT_MS", -1) // -1 = not set (default), 0 = disabled, > 0 = explicit
}
