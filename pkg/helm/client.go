package helm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	fluxexec "github.com/alexandervidyaev/flux-tools/pkg/exec"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

// Client handles Helm operations
type Client struct {
	mu       sync.Mutex // Protects repositories, addedRepositories, gitRepositories, externalArtifacts from concurrent access
	helmBin  string
	cacheDir string
	rootPath string // repository root, used to resolve local charts from GitRepository sources
	// selfSource is the namespace/name of the GitRepository that is this
	// checkout (the flux-system sync source). Empty when unknown.
	selfSource string
	// undeclaredSourcesLocal treats a GitRepository that no manifest declares
	// as this checkout: with flux-operator the sync GitRepository lives only
	// in the cluster, so nothing in git names it.
	undeclaredSourcesLocal bool
	timeout                int
	repositories           map[string]*types.HelmRepository
	addedRepositories      map[string]bool
	gitRepositories        map[string]*types.GitRepository // indexed by namespace/name
	// externalArtifacts maps namespace/name of an ExternalArtifact to the
	// chart directory inside its source, derived from ArtifactGenerators
	externalArtifacts map[string]externalArtifactSource
	runner            fluxexec.CommandRunner
	printer           *output.Printer

	// Persistent helm template cache (I-3), see templatecache.go. The enabled
	// flag and hit/miss counters are guarded by mu; tool versions are fetched
	// at most once per client via sync.Once.
	templateCacheEnabled bool
	templateCacheHits    int
	templateCacheMisses  int
	helmVersionOnce      sync.Once
	helmVersionVal       string
	helmVersionErr       error
	kustomizeVersionOnce sync.Once
	kustomizeVersionVal  string
	kustomizeVersionErr  error
}

// SetRootPath sets the repository root used to resolve local chart paths
// referenced by HelmReleases whose source is a GitRepository pointing at the
// current repo (i.e. spec.chart is a path inside the same repo).
func (c *Client) SetRootPath(rootPath string) {
	c.rootPath = rootPath
}

// SetSelfSource names the GitRepository (namespace/name) that is this
// checkout. Kustomizations and HelmReleases sourced from it resolve against
// rootPath; every other GitRepository is cloned. Empty means unknown, in which
// case a GitRepository no manifest declares is taken to be this checkout.
func (c *Client) SetSelfSource(key string) {
	c.selfSource = key
	c.undeclaredSourcesLocal = key == ""
}

// ResolveGitSourcePath returns the directory a GitRepository (namespace/name)
// is checked out at: rootPath for this repository, a clone otherwise.
//
// The rule is strict on purpose: a Kustomization that points at another
// repository is never rendered from this checkout, even when the same relative
// path exists here. The remote is cloned, and a clone that cannot be made is
// an error rather than a silent substitution.
func (c *Client) ResolveGitSourcePath(ctx context.Context, key string) (string, error) {
	if c.rootPath == "" {
		return "", fmt.Errorf("repository root is not set")
	}
	if key == c.selfSource && key != "" {
		return c.rootPath, nil
	}

	repo, declared := c.GetGitRepository(key)
	if !declared {
		if c.undeclaredSourcesLocal {
			return c.rootPath, nil
		}
		return "", fmt.Errorf("GitRepository %s is neither the flux-system sync source (%s) nor declared in the manifests", key, c.selfSource)
	}
	return c.ResolveGitRepositoryPath(ctx, repo)
}

// NewClient creates a new Helm client
func NewClient(cacheDir string, timeout int) (*Client, error) {
	// Create cache directory if it doesn't exist
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Set default timeout if not provided
	if timeout <= 0 {
		timeout = 300 // 5 minutes default
	}

	helmEnv := []string{
		fmt.Sprintf("HELM_CACHE_HOME=%s", filepath.Join(cacheDir, "cache")),
		fmt.Sprintf("HELM_CONFIG_HOME=%s", filepath.Join(cacheDir, "config")),
		fmt.Sprintf("HELM_DATA_HOME=%s", filepath.Join(cacheDir, "data")),
	}

	return &Client{
		helmBin:           "helm",
		cacheDir:          cacheDir,
		timeout:           timeout,
		repositories:      make(map[string]*types.HelmRepository),
		addedRepositories: make(map[string]bool),
		gitRepositories:   make(map[string]*types.GitRepository),
		externalArtifacts: make(map[string]externalArtifactSource),
		runner:            &fluxexec.RealRunner{Env: helmEnv},
		printer:           output.New(false),
	}, nil
}

// CheckHelmInstalled checks if helm is installed and available
func (c *Client) CheckHelmInstalled(ctx context.Context) error {
	_, err := c.runner.Run(ctx, c.helmBin, "version", "--short")
	if err != nil {
		return fmt.Errorf("helm not found or not executable: %w\nPlease install helm: https://helm.sh/docs/intro/install/", err)
	}
	return nil
}

// RegisterRepository registers a repository without adding it to helm
// This allows the client to know about the repository for later use
func (c *Client) RegisterRepository(repo *types.HelmRepository) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.repositories[repo.GetKey()] = repo
}

// AddRepository adds a Helm repository
func (c *Client) AddRepository(ctx context.Context, repo *types.HelmRepository) error {
	// Store repository (with mutex protection)
	c.mu.Lock()
	c.repositories[repo.GetKey()] = repo
	isOCI := repo.IsOCI()
	c.mu.Unlock()

	// Skip adding if it's an OCI repository (they don't need to be added)
	if isOCI {
		return nil
	}

	// Build helm repo add command (no mutex needed - helm binary handles concurrency)
	args := []string{
		"repo",
		"add",
		repo.Name,
		repo.Spec.URL,
	}

	// Add force update flag
	args = append(args, "--force-update")

	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.timeout)*time.Second)
	defer cancel()

	_, err := c.runner.Run(ctx, c.helmBin, args...)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("helm repo add timed out after %d seconds for repository %s", c.timeout, repo.Name)
		}
		return fmt.Errorf("failed to add helm repository %s: %w", repo.Name, err)
	}

	return nil
}

// PullChart downloads a chart (as a .tgz) into {cacheDir}/charts using the
// client's configured runner (which already carries HELM_*_HOME env) and
// timeout. chartRef is a fully-formed reference — "oci://registry/chart" for
// OCI or "repo-name/chart" for HTTP repositories. This is the single place
// that knows how to invoke `helm pull`.
func (c *Client) PullChart(ctx context.Context, chartRef, version string) error {
	chartsDir := filepath.Join(c.cacheDir, "charts")
	if err := os.MkdirAll(chartsDir, 0755); err != nil {
		return fmt.Errorf("failed to create charts directory: %w", err)
	}

	args := []string{"pull", chartRef, "--destination", chartsDir}
	if version != "" {
		args = append(args, "--version", version)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.timeout)*time.Second)
	defer cancel()

	if _, err := c.runner.Run(ctx, c.helmBin, args...); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("helm pull timed out after %d seconds for %s", c.timeout, chartRef)
		}
		return fmt.Errorf("helm pull failed for %s: %w", chartRef, err)
	}

	return nil
}

// UpdateRepositories updates all non-OCI repositories
func (c *Client) UpdateRepositories(ctx context.Context) error {
	// Check if there are any non-OCI repositories (with mutex protection)
	c.mu.Lock()
	hasNonOCI := false
	for _, repo := range c.repositories {
		if !repo.IsOCI() {
			hasNonOCI = true
			break
		}
	}
	c.mu.Unlock()

	if !hasNonOCI {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.timeout)*time.Second)
	defer cancel()

	_, err := c.runner.Run(ctx, c.helmBin, "repo", "update")
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("helm repo update timed out after %d seconds", c.timeout)
		}
		return fmt.Errorf("failed to update helm repositories: %w", err)
	}

	return nil
}

// UpdateSpecificRepositories updates only the specified repositories in parallel
func (c *Client) UpdateSpecificRepositories(ctx context.Context, repoNames []string) error {
	if len(repoNames) == 0 {
		return nil
	}

	// Update repositories in parallel
	var wg sync.WaitGroup
	errChan := make(chan error, len(repoNames))

	for _, repoName := range repoNames {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()

			timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(c.timeout)*time.Second)
			defer cancel()

			_, err := c.runner.Run(timeoutCtx, c.helmBin, "repo", "update", name)
			if err != nil {
				if timeoutCtx.Err() == context.DeadlineExceeded {
					errChan <- fmt.Errorf("helm repo update timed out after %d seconds for repo: %s", c.timeout, name)
				} else {
					errChan <- fmt.Errorf("failed to update helm repository %s: %w", name, err)
				}
			}
		}(repoName)
	}

	// Wait for all updates to complete
	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	return nil
}

// GetRepository returns a repository by key
func (c *Client) GetRepository(key string) (*types.HelmRepository, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	repo, exists := c.repositories[key]
	return repo, exists
}

// RegisterGitRepository registers a GitRepository so charts referenced via
// HelmRelease.spec.chart.spec.sourceRef.kind == "GitRepository" can be
// resolved to a local filesystem path (either current repo root or a clone
// of an external repository).
func (c *Client) RegisterGitRepository(repo *types.GitRepository) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gitRepositories[repo.GetKey()] = repo
}

// GetGitRepository returns a GitRepository by namespace/name key.
func (c *Client) GetGitRepository(key string) (*types.GitRepository, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	repo, exists := c.gitRepositories[key]
	return repo, exists
}

// EnsureRepositoryAdded adds a repository if not already added
// Returns true if the repository was added, false if it was already added.
//
// The addedRepositories map is guarded by mu, but the network-bound
// AddRepository is intentionally called without holding mu (it does its own
// short-lived locking). Under concurrency two goroutines may both pass the
// check and both invoke AddRepository — that is harmless because `helm repo
// add --force-update` is idempotent.
func (c *Client) EnsureRepositoryAdded(ctx context.Context, repo *types.HelmRepository) (bool, error) {
	key := repo.GetKey()

	// Check if already added
	c.mu.Lock()
	added := c.addedRepositories[key]
	c.mu.Unlock()
	if added {
		return false, nil
	}

	// Add the repository (own locking; must not hold mu here)
	if err := c.AddRepository(ctx, repo); err != nil {
		return false, err
	}

	// Mark as added
	c.mu.Lock()
	c.addedRepositories[key] = true
	c.mu.Unlock()
	return true, nil
}

// LoginOCI performs OCI registry login using credentials from Secret
func (c *Client) LoginOCI(ctx context.Context, repo *types.HelmRepository, secretData map[string]string) error {
	if !repo.IsOCI() {
		return fmt.Errorf("repository %s is not OCI type", repo.Name)
	}

	// Extract registry host from OCI URL (oci://host/path -> host)
	url := repo.Spec.URL
	if !strings.HasPrefix(url, "oci://") {
		return fmt.Errorf("invalid OCI URL: %s", url)
	}

	registryHost := strings.TrimPrefix(url, "oci://")
	// Remove path part, keep only host
	if idx := strings.Index(registryHost, "/"); idx > 0 {
		registryHost = registryHost[:idx]
	}

	// Extract credentials from secret
	// Common keys: username/password, .dockerconfigjson
	var username, password string

	// Try standard username/password keys
	if user, ok := secretData["username"]; ok {
		username = user
	}
	if pass, ok := secretData["password"]; ok {
		password = pass
	}

	// If no username/password, try .dockerconfigjson
	// (kubernetes.io/dockerconfigjson secrets)
	if username == "" || password == "" {
		if dockerConfig, ok := secretData[".dockerconfigjson"]; ok {
			user, pass, err := credentialsFromDockerConfig(dockerConfig, registryHost)
			if err != nil {
				return fmt.Errorf("failed to parse .dockerconfigjson: %w", err)
			}
			username, password = user, pass
		}
	}

	if username == "" || password == "" {
		return fmt.Errorf("no valid credentials found in secret")
	}

	// Execute helm registry login
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.timeout)*time.Second)
	defer cancel()

	_, err := c.runner.Run(ctx, c.helmBin, "registry", "login", registryHost, "-u", username, "-p", password)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("helm registry login timed out after %d seconds for %s", c.timeout, repo.Spec.URL)
		}
		return fmt.Errorf("helm registry login failed: %w", err)
	}

	return nil
}

// dockerConfigJSON mirrors the payload of kubernetes.io/dockerconfigjson secrets:
// {"auths": {"<registry>": {"username": ..., "password": ..., "auth": base64(user:pass)}}}
type dockerConfigJSON struct {
	Auths map[string]dockerConfigAuth `json:"auths"`
}

type dockerConfigAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Auth     string `json:"auth"`
}

// credentialsFromDockerConfig extracts the username/password for registryHost
// from a .dockerconfigjson secret value. Secret `data` values are
// base64-encoded; values that came from `stringData` are accepted raw.
func credentialsFromDockerConfig(raw, registryHost string) (string, string, error) {
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		decoded = []byte(raw)
	}

	var cfg dockerConfigJSON
	if err := json.Unmarshal(decoded, &cfg); err != nil {
		return "", "", fmt.Errorf("invalid JSON: %w", err)
	}

	entry, ok := cfg.Auths[registryHost]
	if !ok {
		return "", "", fmt.Errorf("no auth entry for registry %q", registryHost)
	}

	if entry.Username != "" && entry.Password != "" {
		return entry.Username, entry.Password, nil
	}

	if entry.Auth != "" {
		pair, err := base64.StdEncoding.DecodeString(entry.Auth)
		if err != nil {
			return "", "", fmt.Errorf("invalid auth field for registry %q: %w", registryHost, err)
		}
		user, pass, ok := strings.Cut(string(pair), ":")
		if !ok {
			return "", "", fmt.Errorf("auth field for registry %q is not in user:password form", registryHost)
		}
		return user, pass, nil
	}

	return "", "", fmt.Errorf("no usable credentials for registry %q", registryHost)
}
