package helm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeMetadata(t *testing.T, dir, filename string, repos map[string]*RepoInfo) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(&RepoMetadata{Repositories: repos})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), data, 0644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
}

// A cache that cannot be read must never break a run: an absent or corrupt
// metadata file degrades to an empty cache, which only costs a repo refresh.
func TestNewRepoCacheToleratesUnreadableMetadata(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{name: "no metadata file at all", setup: func(*testing.T, string) {}},
		{
			name: "corrupt metadata file",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "repo-metadata.json"), []byte("{not json"), 0644); err != nil {
					t.Fatalf("seed: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)

			cache, err := NewRepoCache(dir, 60, "")
			if err != nil {
				t.Fatalf("NewRepoCache: %v", err)
			}
			if _, ok := cache.GetInfo("anything"); ok {
				t.Error("a cache built from unreadable metadata must start empty")
			}
			// An empty cache asks for an update, which is the safe default.
			if !cache.ShouldUpdate("charts", "https://charts.example.com", false) {
				t.Error("an unknown repository must be updated")
			}
		})
	}
}

func TestRepoCacheShouldUpdate(t *testing.T) {
	const name, url = "charts", "https://charts.example.com"
	now := time.Now()

	tests := []struct {
		name  string
		repos map[string]*RepoInfo
		force bool
		want  bool
		why   string
	}{
		{
			name: "fresh entry is reused",
			repos: map[string]*RepoInfo{
				name: {URL: url, LastUpdated: now, LastCheck: now},
			},
			want: false,
			why:  "an entry inside the TTL needs no refresh",
		},
		{
			name: "force overrides a fresh entry",
			repos: map[string]*RepoInfo{
				name: {URL: url, LastUpdated: now, LastCheck: now},
			},
			force: true,
			want:  true,
			why:   "--force-repo-update must bypass the TTL",
		},
		{
			name:  "unknown repository",
			repos: map[string]*RepoInfo{},
			want:  true,
			why:   "nothing is known about it yet",
		},
		{
			name: "the URL moved",
			repos: map[string]*RepoInfo{
				name: {URL: "https://old.example.com", LastUpdated: now, LastCheck: now},
			},
			want: true,
			why:  "a repository pointed elsewhere must be re-added, however fresh",
		},
		{
			name: "the entry aged past the TTL",
			repos: map[string]*RepoInfo{
				name: {URL: url, LastUpdated: now.Add(-90 * time.Minute), LastCheck: now},
			},
			want: true,
			why:  "60 minutes of TTL have elapsed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMetadata(t, dir, "repo-metadata.json", tt.repos)

			cache, err := NewRepoCache(dir, 60, "")
			if err != nil {
				t.Fatalf("NewRepoCache: %v", err)
			}
			if got := cache.ShouldUpdate(name, url, tt.force); got != tt.want {
				t.Errorf("ShouldUpdate = %v, want %v: %s", got, tt.want, tt.why)
			}
		})
	}
}

// A non-positive TTL is not "never cache": it falls back to the documented
// one-hour default, so a bad flag cannot turn every run into a full refresh.
func TestNewRepoCacheDefaultsTTLAndFilename(t *testing.T) {
	dir := t.TempDir()
	writeMetadata(t, dir, "repo-metadata.json", map[string]*RepoInfo{
		"charts": {URL: "u", LastUpdated: time.Now().Add(-30 * time.Minute)},
	})

	cache, err := NewRepoCache(dir, 0, "")
	if err != nil {
		t.Fatalf("NewRepoCache: %v", err)
	}
	if cache.ShouldUpdate("charts", "u", false) {
		t.Error("30 minutes is inside the default 60-minute TTL")
	}
	if !strings.Contains(cache.Stats(), "TTL: 1h0m0s") {
		t.Errorf("Stats = %q, want the default TTL", cache.Stats())
	}
}

func TestNewRepoCacheCustomFilename(t *testing.T) {
	dir := t.TempDir()
	writeMetadata(t, dir, "other.json", map[string]*RepoInfo{
		"charts": {URL: "u", LastUpdated: time.Now()},
	})

	// The default name sees nothing; the custom one finds the entry.
	def, _ := NewRepoCache(dir, 60, "")
	if _, ok := def.GetInfo("charts"); ok {
		t.Error("the default filename must not read other.json")
	}

	custom, _ := NewRepoCache(dir, 60, "other.json")
	if _, ok := custom.GetInfo("charts"); !ok {
		t.Error("the custom filename was not honoured")
	}
}

func TestRepoCacheMarkAndFlushRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")

	cache, err := NewRepoCache(dir, 60, "")
	if err != nil {
		t.Fatalf("NewRepoCache: %v", err)
	}

	// Nothing changed yet: Flush must not even create the directory, so a
	// read-only run leaves no trace.
	if err := cache.Flush(); err != nil {
		t.Fatalf("Flush on a clean cache: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("a clean Flush must not touch the cache directory")
	}

	cache.MarkUpdated("charts", "https://charts.example.com")
	info, ok := cache.GetInfo("charts")
	if !ok {
		t.Fatal("MarkUpdated did not record the repository")
	}
	if info.URL != "https://charts.example.com" {
		t.Errorf("URL = %q", info.URL)
	}
	if info.LastUpdated.IsZero() || info.LastCheck.IsZero() {
		t.Error("MarkUpdated must stamp both timestamps")
	}

	if err := cache.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	reloaded, err := NewRepoCache(dir, 60, "")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	back, ok := reloaded.GetInfo("charts")
	if !ok {
		t.Fatal("the flushed entry did not survive a reload")
	}
	if back.URL != info.URL {
		t.Errorf("URL after reload = %q, want %q", back.URL, info.URL)
	}
	if reloaded.ShouldUpdate("charts", info.URL, false) {
		t.Error("a just-flushed entry must read as fresh")
	}
}

func TestRepoCacheMarkCheckedOnlyTouchesKnownRepos(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewRepoCache(dir, 60, "")
	if err != nil {
		t.Fatalf("NewRepoCache: %v", err)
	}

	// An unknown repository is not created by a check.
	cache.MarkChecked("ghost")
	if _, ok := cache.GetInfo("ghost"); ok {
		t.Error("MarkChecked must not invent an entry")
	}
	if err := cache.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "repo-metadata.json")); !os.IsNotExist(err) {
		t.Error("a no-op MarkChecked must leave the cache clean")
	}

	cache.MarkUpdated("charts", "u")
	before, _ := cache.GetInfo("charts")
	updatedAt := before.LastUpdated

	time.Sleep(2 * time.Millisecond)
	cache.MarkChecked("charts")

	after, _ := cache.GetInfo("charts")
	if !after.LastUpdated.Equal(updatedAt) {
		t.Error("MarkChecked must not move LastUpdated, only LastCheck")
	}
	if !after.LastCheck.After(updatedAt) {
		t.Error("MarkChecked must move LastCheck forward")
	}
}

func TestRepoCacheStatsCountsFreshAndStale(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeMetadata(t, dir, "repo-metadata.json", map[string]*RepoInfo{
		"fresh-a": {URL: "a", LastUpdated: now},
		"fresh-b": {URL: "b", LastUpdated: now.Add(-10 * time.Minute)},
		"stale":   {URL: "c", LastUpdated: now.Add(-2 * time.Hour)},
	})

	cache, err := NewRepoCache(dir, 60, "")
	if err != nil {
		t.Fatalf("NewRepoCache: %v", err)
	}

	got := cache.Stats()
	if want := "Total repos: 3, Fresh: 2, Stale: 1 (TTL: 1h0m0s)"; got != want {
		t.Errorf("Stats = %q, want %q", got, want)
	}
}

// The cache is shared by the workers that set repositories up, so every
// exported method has to be safe under -race.
func TestRepoCacheConcurrentAccess(t *testing.T) {
	cache, err := NewRepoCache(t.TempDir(), 60, "")
	if err != nil {
		t.Fatalf("NewRepoCache: %v", err)
	}

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := string(rune('a' + i))
			cache.MarkUpdated(name, "https://"+name+".example.com")
			cache.MarkChecked(name)
			cache.ShouldUpdate(name, "https://"+name+".example.com", false)
			cache.GetInfo(name)
			_ = cache.Stats()
		}()
	}
	wg.Wait()

	if err := cache.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}
