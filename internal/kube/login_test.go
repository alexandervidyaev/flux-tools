package kube

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

// mockRunner stands in for tbot. onRun materialises the kubeconfigs a real tbot
// would write, so the post-run check can be exercised both ways.
type mockRunner struct {
	calls []string
	onRun func(args []string) error
}

func (m *mockRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	m.calls = append(m.calls, strings.Join(append([]string{name}, args...), " "))
	if m.onRun != nil {
		return nil, m.onRun(args)
	}
	return nil, nil
}

func (m *mockRunner) RunWithStdin(ctx context.Context, _ io.Reader, name string, args ...string) ([]byte, error) {
	return m.Run(ctx, name, args...)
}

func (m *mockRunner) LookPath(string) error { return nil }

// writesKubeconfigs simulates a successful tbot: a kubeconfig per cluster.
func writesKubeconfigs(outDir string, clusters []string) func([]string) error {
	return func([]string) error {
		for _, c := range clusters {
			path := KubeconfigPath(outDir, c)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
				return err
			}
		}
		return nil
	}
}

func discard() *output.Printer { return output.NewWithWriter(io.Discard, false) }

func TestLoginWritesOneConfigForAllClusters(t *testing.T) {
	dir := t.TempDir()
	clusters := []string{"kube-a", "kube-b", "kube-c"}
	opts := LoginOptions{
		Clusters:   clusters,
		Proxy:      "teleport.example.invalid:443",
		Token:      "flux-validator-bot-token",
		OutDir:     filepath.Join(dir, "ti"),
		ConfigPath: filepath.Join(dir, "tbot.yaml"),
	}
	runner := &mockRunner{onRun: writesKubeconfigs(opts.OutDir, clusters)}

	if err := Login(context.Background(), runner, discard(), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// One join for every cluster, not one per cluster.
	if len(runner.calls) != 1 {
		t.Errorf("tbot ran %d times, want once: %v", len(runner.calls), runner.calls)
	}
	if runner.calls[0] != "tbot start -c "+opts.ConfigPath {
		t.Errorf("unexpected invocation: %q", runner.calls[0])
	}

	data, err := os.ReadFile(opts.ConfigPath)
	if err != nil {
		t.Fatalf("config was not written: %v", err)
	}
	var cfg tbotConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("generated config is not valid YAML: %v\n%s", err, data)
	}

	if cfg.Version != "v2" || !cfg.Oneshot || cfg.Storage.Type != "memory" {
		t.Errorf("unexpected config shape: %+v", cfg)
	}
	if cfg.Onboarding.JoinMethod != "gitlab" || cfg.Onboarding.Token != opts.Token {
		t.Errorf("unexpected onboarding: %+v", cfg.Onboarding)
	}
	if len(cfg.Outputs) != len(clusters) {
		t.Fatalf("outputs = %d, want %d", len(cfg.Outputs), len(clusters))
	}
	for i, c := range clusters {
		got := cfg.Outputs[i]
		if got.Type != "kubernetes" || got.Cluster != c {
			t.Errorf("output %d = %+v, want cluster %s", i, got, c)
		}
		if got.Destination.Path != filepath.Join(opts.OutDir, c) {
			t.Errorf("output %d path = %q", i, got.Destination.Path)
		}
	}
}

// The heredoc version interpolated the token straight into YAML. A token with a
// colon, or one starting with * or &, produced a config that either failed to
// parse or parsed into something else entirely.
func TestLoginQuotesAwkwardTokens(t *testing.T) {
	for _, token := range []string{"has: colon", "*anchor-like", "&amp-like", "#hash", "123", "true"} {
		t.Run(token, func(t *testing.T) {
			dir := t.TempDir()
			opts := LoginOptions{
				Clusters:   []string{"kube-a"},
				Proxy:      "p:443",
				Token:      token,
				OutDir:     filepath.Join(dir, "ti"),
				ConfigPath: filepath.Join(dir, "tbot.yaml"),
			}
			runner := &mockRunner{onRun: writesKubeconfigs(opts.OutDir, opts.Clusters)}

			if err := Login(context.Background(), runner, discard(), opts); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			data, err := os.ReadFile(opts.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			var cfg tbotConfig
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				t.Fatalf("config broke on token %q: %v\n%s", token, err, data)
			}
			if cfg.Onboarding.Token != token {
				t.Errorf("token round-tripped as %q, want %q\n%s", cfg.Onboarding.Token, token, data)
			}
		})
	}
}

// tbot exiting zero without writing a kubeconfig has to fail here, not later as
// an opaque kubectl error against an empty config.
func TestLoginFailsWhenNoKubeconfigAppears(t *testing.T) {
	dir := t.TempDir()
	opts := LoginOptions{
		Clusters:   []string{"kube-a"},
		Proxy:      "p:443",
		Token:      "t",
		OutDir:     filepath.Join(dir, "ti"),
		ConfigPath: filepath.Join(dir, "tbot.yaml"),
	}
	runner := &mockRunner{} // exits zero, writes nothing

	err := Login(context.Background(), runner, discard(), opts)
	if err == nil {
		t.Fatal("expected an error when no kubeconfig was produced")
	}
	if !strings.Contains(err.Error(), "kube-a") {
		t.Errorf("the error does not name the cluster: %v", err)
	}
}

func TestLoginPropagatesTbotFailure(t *testing.T) {
	dir := t.TempDir()
	runner := &mockRunner{onRun: func([]string) error { return fmt.Errorf("join denied") }}

	err := Login(context.Background(), runner, discard(), LoginOptions{
		Clusters:   []string{"kube-a"},
		Proxy:      "p:443",
		Token:      "t",
		OutDir:     filepath.Join(dir, "ti"),
		ConfigPath: filepath.Join(dir, "tbot.yaml"),
	})
	if err == nil || !strings.Contains(err.Error(), "join denied") {
		t.Errorf("err = %v, want it to carry the tbot failure", err)
	}
}

func TestLoginValidatesInput(t *testing.T) {
	base := LoginOptions{Clusters: []string{"a"}, Proxy: "p:443", Token: "t", OutDir: t.TempDir()}

	tests := map[string]LoginOptions{
		"no clusters": {Proxy: base.Proxy, Token: base.Token},
		"no proxy":    {Clusters: base.Clusters, Token: base.Token},
		"no token":    {Clusters: base.Clusters, Proxy: base.Proxy},
	}
	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			if err := Login(context.Background(), &mockRunner{}, discard(), opts); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestParseClusters(t *testing.T) {
	tests := map[string][]string{
		"a b c":     {"a", "b", "c"},
		"  a   b  ": {"a", "b"},
		"single":    {"single"},
		"":          nil,
		"   ":       nil,
		"a\tb\nc":   {"a", "b", "c"},
	}
	for in, want := range tests {
		got := ParseClusters(in)
		if len(got) != len(want) {
			t.Errorf("ParseClusters(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("ParseClusters(%q) = %v, want %v", in, got, want)
				break
			}
		}
	}
}
