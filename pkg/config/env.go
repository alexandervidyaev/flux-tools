// Package config reads the tool's settings from the environment.
package config

import (
	"os"
	"path/filepath"
	"strconv"
)

// Config holds application-wide configuration loaded from environment.
type Config struct {
	CacheDir    string
	HelmTimeout int
}

// LoadDefaults loads configuration from environment variables with sensible defaults.
func LoadDefaults() Config {
	return Config{
		CacheDir:    getCacheDir(),
		HelmTimeout: getHelmTimeout(),
	}
}

func getCacheDir() string {
	if dir := os.Getenv("FLUX_TOOLS_CACHE_DIR"); dir != "" {
		return dir
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/flux-tools"
	}
	return filepath.Join(homeDir, ".flux-tools", "cache")
}

func getHelmTimeout() int {
	if timeout := os.Getenv("FLUX_TOOLS_HELM_TIMEOUT"); timeout != "" {
		if t, err := strconv.Atoi(timeout); err == nil && t > 0 {
			return t
		}
	}
	return 300
}
