package cli

import (
	"fmt"
	"os"

	"github.com/alexandervidyaev/flux-tools/internal/kube"
	"github.com/alexandervidyaev/flux-tools/pkg/exec"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
	"github.com/spf13/cobra"
)

func NewKubeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "kube", Short: "Obtain explicit cluster access through Teleport"}
	var clusters []string
	var clusterList, proxy, token, outDir, configPath string
	login := &cobra.Command{
		Use: "login", Short: "Obtain a kubeconfig per cluster using tbot's GitLab join method",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all := append(append([]string{}, clusters...), kube.ParseClusters(clusterList)...)
			if len(all) == 0 {
				return fmt.Errorf("pass --cluster or --clusters explicitly")
			}
			return kube.Login(commandContext(cmd), &exec.RealRunner{}, output.New(false), kube.LoginOptions{
				Clusters: all, Proxy: proxy, Token: token, OutDir: outDir, ConfigPath: configPath,
			})
		},
	}
	login.Flags().StringArrayVar(&clusters, "cluster", nil, "Teleport Kubernetes cluster (repeatable)")
	login.Flags().StringVar(&clusterList, "clusters", "", "Whitespace-separated Teleport cluster names")
	login.Flags().StringVar(&proxy, "proxy", os.Getenv("TELEPORT_PROXY"), "Teleport proxy address")
	login.Flags().StringVar(&token, "token", os.Getenv("TBOT_TOKEN"), "Teleport GitLab join token name")
	login.Flags().StringVar(&outDir, "out-dir", kube.DefaultOutDir, "Directory for kubeconfigs")
	login.Flags().StringVar(&configPath, "config-path", kube.DefaultConfigPath, "Generated tbot config path")
	cmd.AddCommand(login)
	return cmd
}
