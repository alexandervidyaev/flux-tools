// Command flux-tools validates, renders and diffs Kubernetes manifests from
// Flux CD GitOps repositories without a cluster.
package main

import "github.com/alexandervidyaev/flux-tools/internal/cli"

func main() {
	cli.Execute()
}
