package helm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RepoMetadata stores metadata about repository updates
type RepoMetadata struct {
	Repositories map[string]*RepoInfo `json:"repositories"`
}

// RepoInfo stores information about a single repository
type RepoInfo struct {
	URL         string    `json:"url"`
	LastUpdated time.Time `json:"last_updated"`
	LastCheck   time.Time `json:"last_check"`
}

// RepoCache manages repository metadata cache.
//
// Mark* methods only mutate the in-memory state; call Flush once at the end
// of the operation to persist accumulated changes. Previously every mark
// rewrote the metadata file, which meant one disk write per skipped
// repository.
type RepoCache struct {
	mu               sync.Mutex // Protects metadata and dirty from concurrent access
	cacheDir         string
	metadata         *RepoMetadata
	ttl              time.Duration
	metadataFilename string // Custom metadata filename
	dirty            bool   // Unsaved in-memory changes pending Flush
}

// NewRepoCache creates a new repository cache
// customFilename can be empty to use default "repo-metadata.json"
// or provide a custom name like "repo-metadata-clusters-dev.json"
func NewRepoCache(cacheDir string, ttlMinutes int, customFilename string) (*RepoCache, error) {
	if ttlMinutes <= 0 {
		ttlMinutes = 60 // Default: 1 hour
	}

	if customFilename == "" {
		customFilename = "repo-metadata.json"
	}

	cache := &RepoCache{
		cacheDir:         cacheDir,
		ttl:              time.Duration(ttlMinutes) * time.Minute,
		metadataFilename: customFilename,
		metadata: &RepoMetadata{
			Repositories: make(map[string]*RepoInfo),
		},
	}

	// Try to load existing metadata
	if err := cache.load(); err != nil {
		// If file doesn't exist or corrupt, start fresh
		cache.metadata = &RepoMetadata{
			Repositories: make(map[string]*RepoInfo),
		}
	}

	return cache, nil
}

// ShouldUpdate checks if a repository should be updated
func (c *RepoCache) ShouldUpdate(repoName, repoURL string, force bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Always update if force flag is set
	if force {
		return true
	}

	// Check if we have metadata for this repo
	info, exists := c.metadata.Repositories[repoName]
	if !exists {
		// Never updated before
		return true
	}

	// Check if URL changed
	if info.URL != repoURL {
		// URL changed, need to update
		return true
	}

	// Check if TTL expired
	elapsed := time.Since(info.LastUpdated)
	if elapsed > c.ttl {
		// TTL expired, need to update
		return true
	}

	// Cache is still valid
	return false
}

// MarkUpdated marks a repository as updated (in memory; persisted by Flush)
func (c *RepoCache) MarkUpdated(repoName, repoURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	c.metadata.Repositories[repoName] = &RepoInfo{
		URL:         repoURL,
		LastUpdated: now,
		LastCheck:   now,
	}
	c.dirty = true
}

// MarkChecked marks a repository as checked without update (in memory;
// persisted by Flush)
func (c *RepoCache) MarkChecked(repoName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if info, exists := c.metadata.Repositories[repoName]; exists {
		info.LastCheck = time.Now()
		c.dirty = true
	}
}

// Flush writes accumulated metadata changes to disk. No-op when nothing
// changed since the last Flush.
func (c *RepoCache) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.dirty {
		return nil
	}
	if err := c.save(); err != nil {
		return err
	}
	c.dirty = false
	return nil
}

// GetInfo returns repository info
func (c *RepoCache) GetInfo(repoName string) (*RepoInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	info, exists := c.metadata.Repositories[repoName]
	return info, exists
}

// metadataPath returns the path to metadata file
func (c *RepoCache) metadataPath() string {
	return filepath.Join(c.cacheDir, c.metadataFilename)
}

// load loads metadata from disk
func (c *RepoCache) load() error {
	path := c.metadataPath()

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, c.metadata)
}

// save saves metadata to disk
func (c *RepoCache) save() error {
	// Ensure cache directory exists
	if err := os.MkdirAll(c.cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	path := c.metadataPath()

	data, err := json.MarshalIndent(c.metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// Stats returns cache statistics
func (c *RepoCache) Stats() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	total := len(c.metadata.Repositories)
	fresh := 0
	stale := 0

	now := time.Now()
	for _, info := range c.metadata.Repositories {
		if now.Sub(info.LastUpdated) < c.ttl {
			fresh++
		} else {
			stale++
		}
	}

	return fmt.Sprintf("Total repos: %d, Fresh: %d, Stale: %d (TTL: %s)",
		total, fresh, stale, c.ttl)
}
