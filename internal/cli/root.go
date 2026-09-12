package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

// version is set by goreleaser and the Dockerfile through -ldflags. A binary
// built by `go install module@version` has no ldflags, but the Go toolchain
// records the module version in the binary; that is used when nothing was
// injected.
var version = "dev"

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return version
	}
	return info.Main.Version
}

func NewRootCmd() *cobra.Command {
	var timeoutSec int

	rootCmd := &cobra.Command{
		Use:   "flux-tools",
		Short: "Flux GitOps Validator and CI Runner",
		Long: `flux-tools is a CLI that builds, validates and diffs Kubernetes manifests from a
Flux GitOps repository, without a cluster.

A cluster is a directory carrying a marker: flux-system/ always, plus whatever
--cluster-marker names. Every cluster below a path is processed, in parallel; a
path with no marker below it is taken as one entry as given, which is how
flux-operator layouts are built.

The repository root that spec.path resolves against is --flux-workdir, "." by
default. It is stated rather than inferred, because the root decides what every
spec.path points at.`,
		Version:      resolveVersion(),
		SilenceUsage: true,
		// Cobra otherwise prints "Error: ..." itself and Execute() prints it a
		// second time via p.Info below. Silence cobra's copy, keep ours.
		SilenceErrors: true,
		// Wrap the command context with the global --timeout. Done here (after
		// flag parsing, before RunE) so every subcommand inherits it via
		// cmd.Context().
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if timeoutSec > 0 {
				ctx, cancel := context.WithTimeout(cmd.Context(), time.Duration(timeoutSec)*time.Second)
				cmd.SetContext(ctx)
				// The context must outlive PersistentPreRun — it is used by
				// RunE. cancel releases the timer once the command finishes.
				cobra.OnFinalize(cancel)
			}
		},
	}

	rootCmd.PersistentFlags().IntVar(&timeoutSec, "timeout", 0, "Global timeout for the whole run in seconds (0 = no timeout)")

	rootCmd.AddCommand(NewTestCmd())
	rootCmd.AddCommand(NewBuildCmd())
	rootCmd.AddCommand(NewHelmPullCmd())
	rootCmd.AddCommand(NewKubeconformCmd())
	rootCmd.AddCommand(NewYqCmd())
	rootCmd.AddCommand(NewDiffCmd())
	rootCmd.AddCommand(NewPostCommentCmd())

	return rootCmd
}

func Execute() {
	p := output.New(false)

	// Root context: cancelled on Ctrl+C / SIGTERM so every exec'd subprocess
	// (kustomize, helm, git, ...) is cancelled cascadingly instead of the
	// process being killed with children left running.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rootCmd := NewRootCmd()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			p.Info("Error: interrupted\n")
		case errors.Is(err, context.DeadlineExceeded):
			p.Info("Error: global timeout exceeded\n")
		default:
			p.Info("Error: %v\n", err)
		}
		os.Exit(1)
	}
}

// commandContext returns the command's context, falling back to Background
// for tests that construct commands without ExecuteContext.
func commandContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}
