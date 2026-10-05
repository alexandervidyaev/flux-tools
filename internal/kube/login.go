// Package kube obtains explicit Kubernetes credentials through Teleport tbot.
package kube

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/alexandervidyaev/flux-tools/pkg/exec"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

const (
	DefaultConfigPath = "/tmp/tbot.yaml"
	DefaultOutDir     = "/tmp/ti"
)

// LoginOptions describes one tbot run.
type LoginOptions struct {
	// Clusters to obtain access to. One tbot run covers all of them, so the join
	// happens once no matter how many clusters an environment has.
	Clusters []string
	Proxy    string // host:port
	Token    string // Teleport join token name (gitlab method)
	OutDir   string
	// ConfigPath is where the generated tbot config is written.
	ConfigPath string
}

// tbot config, version v2. Typed rather than a heredoc: a token containing YAML
// metacharacters (a colon, a leading * or &) silently produced a broken config in
// the shell version, and marshalling quotes it correctly.
type tbotConfig struct {
	Version    string       `yaml:"version"`
	Proxy      string       `yaml:"proxy_server"`
	Onboarding tbotOnboard  `yaml:"onboarding"`
	Oneshot    bool         `yaml:"oneshot"`
	Storage    tbotStorage  `yaml:"storage"`
	Outputs    []tbotOutput `yaml:"outputs"`
}

type tbotOnboard struct {
	JoinMethod string     `yaml:"join_method"`
	Token      string     `yaml:"token"`
	GitLab     tbotGitLab `yaml:"gitlab"`
}

type tbotGitLab struct {
	TokenEnvVarName string `yaml:"token_env_var_name"`
}

type tbotStorage struct {
	Type string `yaml:"type"`
}

type tbotOutput struct {
	Type        string          `yaml:"type"`
	Cluster     string          `yaml:"kubernetes_cluster"`
	Destination tbotDestination `yaml:"destination"`
}

type tbotDestination struct {
	Type string `yaml:"type"`
	Path string `yaml:"path"`
}

// KubeconfigPath is where tbot leaves the kubeconfig for one cluster. Callers
// export it as KUBECONFIG per cluster rather than switching contexts, so parallel
// work on several clusters cannot race on a shared current-context.
func KubeconfigPath(outDir, cluster string) string {
	return filepath.Join(outDir, cluster, "kubeconfig.yaml")
}

// Login runs tbot once for every cluster in opts and leaves a kubeconfig per
// cluster under opts.OutDir.
func Login(ctx context.Context, runner exec.CommandRunner, p *output.Printer, opts LoginOptions) error {
	if len(opts.Clusters) == 0 {
		return fmt.Errorf("no clusters specified")
	}
	if opts.Proxy == "" {
		return fmt.Errorf("teleport proxy is required")
	}
	if opts.Token == "" {
		return fmt.Errorf("teleport join token name is required")
	}
	if opts.OutDir == "" {
		opts.OutDir = DefaultOutDir
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = DefaultConfigPath
	}

	cfg := tbotConfig{
		Version:    "v2",
		Proxy:      opts.Proxy,
		Onboarding: tbotOnboard{JoinMethod: "gitlab", Token: opts.Token, GitLab: tbotGitLab{TokenEnvVarName: "TBOT_GITLAB_JWT"}},
		Oneshot:    true,
		Storage:    tbotStorage{Type: "memory"},
	}
	for _, c := range opts.Clusters {
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`).MatchString(c) {
			return fmt.Errorf("invalid cluster name %q", c)
		}
		if err := os.MkdirAll(filepath.Join(opts.OutDir, c), 0700); err != nil {
			return err
		}
		cfg.Outputs = append(cfg.Outputs, tbotOutput{
			Type:        "kubernetes",
			Cluster:     c,
			Destination: tbotDestination{Type: "directory", Path: filepath.Join(opts.OutDir, c)},
		})
	}

	if err := os.MkdirAll(filepath.Dir(opts.ConfigPath), 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal tbot config: %w", err)
	}
	// 0600: the config carries the join token.
	if err := os.WriteFile(opts.ConfigPath, data, 0o600); err != nil {
		return fmt.Errorf("write tbot config %s: %w", opts.ConfigPath, err)
	}

	p.Info("Obtaining cluster access through %s: %s\n", opts.Proxy, strings.Join(opts.Clusters, " "))

	if _, err := runner.Run(ctx, "tbot", "start", "-c", opts.ConfigPath); err != nil {
		return fmt.Errorf("tbot login failed: %w", err)
	}

	// A tbot that exits zero without writing a kubeconfig would surface much
	// later, as an opaque kubectl error against an empty config.
	for _, c := range opts.Clusters {
		path := KubeconfigPath(opts.OutDir, c)
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("tbot did not create kubeconfig for %s at %s", c, path)
		}
		if err := os.Chmod(path, 0600); err != nil {
			return err
		}
	}

	return nil
}
