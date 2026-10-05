package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexandervidyaev/flux-tools/internal/validator/build"
	"github.com/spf13/cobra"
)

func NewRenderCmd() *cobra.Command {
	var output, root, cache string
	var local, rewrites []string
	var enableHelm, strict bool
	cmd := &cobra.Command{Use: "render <path>", Short: "Render one GitOps artifact root", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		locals, err := parseAssignments(local)
		if err != nil {
			return err
		}
		mappings, err := parseAssignments(rewrites)
		if err != nil {
			return err
		}
		b, err := build.NewBuilder(commandContext(cmd), build.BuildOptions{Path: args[0], RootPath: root, NativeFlux: true, CacheDir: cache, EnableHelm: enableHelm, Strict: strict, LocalArtifacts: locals, OCIRewrite: mappings})
		if err != nil {
			return err
		}
		defer b.Close()
		objects, err := b.BuildAll(commandContext(cmd))
		if err != nil {
			return err
		}
		data, err := build.SerializeObjects(objects, "yaml")
		if err != nil {
			return err
		}
		if output == "" {
			_, err = cmd.OutOrStdout().Write(data)
			return err
		}
		if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
			return err
		}
		return os.WriteFile(output, data, 0600)
	}}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output manifest file (default stdout)")
	cmd.Flags().StringVar(&root, "flux-workdir", ".", fluxWorkdirUsage)
	cmd.Flags().StringVar(&root, "source-root", ".", "Alias for --flux-workdir")
	cmd.Flags().StringVar(&cache, "cache-dir", "", "Chart and source cache")
	cmd.Flags().StringArrayVar(&local, "local-artifact", nil, "OCI URL suffix=local directory (repeatable)")
	cmd.Flags().StringArrayVar(&rewrites, "oci-rewrite", nil, "OCI URL prefix=replacement (repeatable)")
	cmd.Flags().BoolVar(&enableHelm, "enable-helm", true, "Render HelmReleases")
	cmd.Flags().BoolVar(&strict, "strict", true, "Fail on unresolved dependencies and substitutions")
	return cmd
}
func parseAssignments(values []string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range values {
		k, v, ok := strings.Cut(pair, "=")
		if !ok || k == "" || v == "" {
			return nil, fmt.Errorf("expected key=value, got %q", pair)
		}
		out[k] = v
	}
	return out, nil
}
