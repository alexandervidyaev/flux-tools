package helm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

// Package-level clone dedup — shared across all Client instances.
// Each worker in parallel build gets its own Client, but they must
// coordinate git clones to avoid racing on the same (URL, ref).
var (
	globalCloneMu       sync.Mutex
	globalClonedRepos   = make(map[string]string)
	globalCloneInFlight = make(map[string]chan struct{})
)

// ResolveGitRepositoryPath returns the local filesystem path that holds the
// content of the given GitRepository. If the GitRepository points at the same
// repository the build is running in (detected via matching git remote URL),
// the client's rootPath is returned and no clone is performed. Otherwise the
// repository is shallow-cloned into the cache directory at the requested ref
// and the clone path is returned. Subsequent calls for the same (URL, ref)
// pair are served from cache.
func (c *Client) ResolveGitRepositoryPath(ctx context.Context, repo *types.GitRepository) (string, error) {
	if repo == nil {
		return "", fmt.Errorf("nil GitRepository")
	}

	if c.isCurrentRepository(repo) {
		if c.rootPath == "" {
			return "", fmt.Errorf("GitRepository %s/%s points at the current repository, but rootPath is not set", repo.Namespace, repo.Name)
		}
		return c.rootPath, nil
	}

	if repo.Spec.URL == "" {
		return "", fmt.Errorf("GitRepository %s/%s has empty spec.url", repo.Namespace, repo.Name)
	}

	ref := gitRef(repo)
	cacheKey := gitCacheKey(repo.Spec.URL, ref)

	// Use package-level singleflight: parallel workers each have their own
	// Client, but must coordinate clones to avoid racing on the same (URL, ref).
	globalCloneMu.Lock()
	if cached, ok := globalClonedRepos[cacheKey]; ok {
		globalCloneMu.Unlock()
		return cached, nil
	}
	if ch, ok := globalCloneInFlight[cacheKey]; ok {
		globalCloneMu.Unlock()
		<-ch
		globalCloneMu.Lock()
		cached := globalClonedRepos[cacheKey]
		globalCloneMu.Unlock()
		if cached == "" {
			return "", fmt.Errorf("concurrent clone of %s @ %s failed", repo.Spec.URL, ref)
		}
		return cached, nil
	}
	ch := make(chan struct{})
	globalCloneInFlight[cacheKey] = ch
	globalCloneMu.Unlock()

	defer func() {
		globalCloneMu.Lock()
		delete(globalCloneInFlight, cacheKey)
		close(ch)
		globalCloneMu.Unlock()
	}()

	clonePath := filepath.Join(c.cacheDir, "git", cacheKey)

	// If the directory already exists from a previous run with the same ref,
	// reuse it (cheap for pinned tags / commits).
	if info, err := os.Stat(clonePath); err == nil && info.IsDir() {
		if _, err := os.Stat(filepath.Join(clonePath, ".git")); err == nil {
			c.cacheClone(cacheKey, clonePath)
			return clonePath, nil
		}
		// Stale directory without .git — wipe and re-clone.
		_ = os.RemoveAll(clonePath)
	}

	if err := os.MkdirAll(filepath.Dir(clonePath), 0o755); err != nil {
		return "", fmt.Errorf("failed to create git cache dir: %w", err)
	}

	cloneURL, err := withCIToken(repo.Spec.URL)
	if err != nil {
		return "", fmt.Errorf("failed to prepare clone URL for %s: %w", repo.Spec.URL, err)
	}

	args := []string{"clone", "--depth=1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, cloneURL, clonePath)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.timeout)*time.Second)
	defer cancel()

	c.printer.Verbose("  Cloning GitRepository %s/%s (%s @ %s) → %s\n",
		repo.Namespace, repo.Name, repo.Spec.URL, refOrHEAD(ref), clonePath)

	if _, err := c.runner.Run(ctx, "git", args...); err != nil {
		// Some refs (e.g. SemVer expressions, named refs, raw commits) cannot
		// be combined with --branch. Retry with full clone + checkout.
		if ref != "" {
			if alt, altErr := c.fullCloneAndCheckout(ctx, cloneURL, clonePath, ref, repo); altErr == nil {
				c.cacheClone(cacheKey, alt)
				return alt, nil
			}
		}
		return "", fmt.Errorf("failed to clone GitRepository %s/%s (%s): %w", repo.Namespace, repo.Name, repo.Spec.URL, err)
	}

	c.cacheClone(cacheKey, clonePath)
	return clonePath, nil
}

// isCurrentRepository returns true if the GitRepository points at the same
// repository the build is being run from, matched by remote.origin.url of the
// rootPath when a .git is available there. Callers that know the referenced
// path exists under rootPath do not need this and never clone.
func (c *Client) isCurrentRepository(repo *types.GitRepository) bool {
	if c.rootPath == "" || repo.Spec.URL == "" {
		return false
	}
	originURL, err := c.gitRemoteURL(c.rootPath)
	if err != nil {
		return false
	}
	return normalizeGitURL(originURL) == normalizeGitURL(repo.Spec.URL)
}

// gitRemoteURL returns the URL of remote.origin for the given working tree.
func (c *Client) gitRemoteURL(workTree string) (string, error) {
	cmd := exec.Command("git", "-C", workTree, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Client) cacheClone(key, path string) {
	globalCloneMu.Lock()
	globalClonedRepos[key] = path
	globalCloneMu.Unlock()
}

// fullCloneAndCheckout falls back to a non-shallow clone followed by a
// checkout of the requested ref. Used when --branch <ref> fails (e.g. for
// commit SHAs, semver expressions or names that aren't direct branch/tag
// names).
func (c *Client) fullCloneAndCheckout(ctx context.Context, cloneURL, clonePath, ref string, repo *types.GitRepository) (string, error) {
	_ = os.RemoveAll(clonePath)
	if _, err := c.runner.Run(ctx, "git", "clone", cloneURL, clonePath); err != nil {
		return "", err
	}
	if _, err := c.runner.Run(ctx, "git", "-C", clonePath, "checkout", ref); err != nil {
		return "", err
	}
	c.printer.Verbose("  Checked out GitRepository %s/%s @ %s (full clone fallback)\n",
		repo.Namespace, repo.Name, ref)
	return clonePath, nil
}

func gitRef(repo *types.GitRepository) string {
	if repo.Spec.Reference == nil {
		return ""
	}
	r := repo.Spec.Reference
	switch {
	case r.Commit != "":
		return r.Commit
	case r.Name != "":
		return r.Name
	case r.SemVer != "":
		return r.SemVer
	case r.Tag != "":
		return r.Tag
	case r.Branch != "":
		return r.Branch
	}
	return ""
}

func refOrHEAD(ref string) string {
	if ref == "" {
		return "HEAD"
	}
	return ref
}

func gitCacheKey(rawURL, ref string) string {
	sum := sha256.Sum256([]byte(rawURL))
	urlHash := hex.EncodeToString(sum[:])[:12]

	safeRef := ref
	if safeRef == "" {
		safeRef = "HEAD"
	}
	safeRef = strings.NewReplacer("/", "-", " ", "-").Replace(safeRef)
	if len(safeRef) > 64 {
		safeRef = safeRef[:64]
	}

	host := ""
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		host = strings.NewReplacer(":", "-").Replace(u.Host) + "_"
	}

	return fmt.Sprintf("%s%s_%s", host, urlHash, safeRef)
}

func withCIToken(rawURL string) (string, error) {
	token := os.Getenv("CI_JOB_TOKEN")
	if token == "" {
		return rawURL, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, nil
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return rawURL, nil
	}
	if u.User != nil {
		return rawURL, nil
	}
	u.User = url.UserPassword("gitlab-ci-token", token)
	return u.String(), nil
}

func normalizeGitURL(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	if !strings.Contains(s, "://") && strings.Contains(s, "@") && strings.Contains(s, ":") {
		at := strings.Index(s, "@")
		colon := strings.Index(s[at:], ":")
		if at >= 0 && colon > 0 {
			host := s[at+1 : at+colon]
			path := s[at+colon+1:]
			s = host + "/" + path
		}
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		s = strings.TrimPrefix(u.Host, "www.") + strings.TrimSuffix(u.Path, "/")
	}
	return strings.ToLower(s)
}
