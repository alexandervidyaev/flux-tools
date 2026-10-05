// Package livediff previews server-side manifest changes without applying them.
package livediff

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	fluxexec "github.com/alexandervidyaev/flux-tools/pkg/exec"
	"github.com/alexandervidyaev/flux-tools/pkg/kustomize"
)

func Run(ctx context.Context, runner fluxexec.CommandRunner, manifests, kubeconfig, manager string) ([]byte, error) {
	if kubeconfig == "" {
		return nil, fmt.Errorf("explicit kubeconfig is required")
	}
	data, err := os.ReadFile(manifests)
	if err != nil {
		return nil, err
	}
	objects, err := kustomize.ParseKustomizeOutput(data)
	if err != nil {
		return nil, err
	}
	if len(objects) == 0 {
		return nil, fmt.Errorf("empty manifests: deletion-only changes require an inventory comparison")
	}
	var secrets []string
	for _, obj := range objects {
		if obj.GetKind() != "Secret" {
			continue
		}
		for _, field := range []string{"data", "stringData"} {
			values, _ := obj.Object[field].(map[string]interface{})
			for _, v := range values {
				value, ok := v.(string)
				if !ok || value == "" {
					continue
				}
				secrets = append(secrets, value)
				if field == "data" {
					if decoded, e := base64.StdEncoding.DecodeString(value); e == nil && len(decoded) > 0 {
						secrets = append(secrets, string(decoded))
					}
				}
			}
		}
	}
	out, err := runner.Run(ctx, "kubectl", "--kubeconfig", kubeconfig, "diff", "--server-side", "--field-manager", manager, "-f", manifests)
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	mask := func(value string) string {
		for _, secret := range secrets {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
		return value
	}
	out = []byte(mask(string(out)))
	if err == nil {
		return out, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return out, nil
	}
	return out, fmt.Errorf("live diff failed: %s", mask(err.Error()))
}
