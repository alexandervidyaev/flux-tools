package cli

import (
	"os"
	"path/filepath"

	"github.com/alexandervidyaev/flux-tools/internal/livediff"
	"github.com/alexandervidyaev/flux-tools/pkg/exec"
	"github.com/spf13/cobra"
)

func NewLiveDiffCmd() *cobra.Command {
	var kubeconfig, output, manager string
	cmd := &cobra.Command{Use: "diff-live <manifests.yaml>", Short: "Preview server-side changes against a cluster", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		out, err := livediff.Run(commandContext(cmd), &exec.RealRunner{}, args[0], kubeconfig, manager)
		if output != "" {
			if e := os.MkdirAll(filepath.Dir(output), 0700); e != nil {
				return e
			}
			if e := os.WriteFile(output, out, 0600); e != nil {
				return e
			}
		}
		if _, e := cmd.OutOrStdout().Write(out); e != nil {
			return e
		}
		return err
	}}
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", os.Getenv("KUBECONFIG"), "Kubeconfig from tbot")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Save diff report")
	cmd.Flags().StringVar(&manager, "field-manager", "kustomize-controller", "Server-side apply field manager")
	return cmd
}
