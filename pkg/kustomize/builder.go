// Package kustomize renders directories the way kustomize-controller does,
// generating a kustomization for a directory that has none, and filters and
// substitutes variables in the objects that come back.
package kustomize

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	fluxexec "github.com/alexandervidyaev/flux-tools/pkg/exec"
	"github.com/alexandervidyaev/flux-tools/pkg/fsutil"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Builder handles kustomize build operations
type Builder struct {
	kustomizeBin string
	runner       fluxexec.CommandRunner
	ignore       *SourceIgnore
}

// NewBuilder creates a new Kustomize builder
func NewBuilder() *Builder {
	return &Builder{
		kustomizeBin: "kustomize",
		runner:       &fluxexec.RealRunner{},
	}
}

// SetSourceIgnore applies a .sourceignore matcher to generated kustomizations
// (see GenerateKustomization). Directories with their own kustomization.yaml
// are unaffected: kustomize reads what they list.
func (b *Builder) SetSourceIgnore(ignore *SourceIgnore) {
	b.ignore = ignore
}

// Build executes kustomize build on the given path
func (b *Builder) Build(ctx context.Context, path string) ([]byte, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	output, err := b.runner.Run(ctx, b.kustomizeBin, "build", "--load-restrictor=LoadRestrictionsNone", absPath)
	if err != nil {
		return nil, fmt.Errorf("kustomize build failed: %w", err)
	}

	return output, nil
}

// BuildDir builds a directory the way kustomize-controller does: with its own
// kustomization file when it has one, otherwise from a generated one listing
// every manifest under it (see GenerateKustomization).
func (b *Builder) BuildDir(ctx context.Context, dir string) ([]byte, error) {
	if fsutil.HasKustomizationFile(dir) {
		return b.Build(ctx, dir)
	}

	genDir, cleanup, err := GenerateKustomization(dir, b.ignore)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	output, err := b.Build(ctx, genDir)
	if err != nil {
		return nil, fmt.Errorf("%s has no kustomization.yaml and building a generated one failed: %w", dir, err)
	}
	return output, nil
}

// BuildDirAndParse is BuildDir followed by ParseKustomizeOutput.
func (b *Builder) BuildDirAndParse(ctx context.Context, dir string) ([]*unstructured.Unstructured, error) {
	data, err := b.BuildDir(ctx, dir)
	if err != nil {
		return nil, err
	}
	return ParseKustomizeOutput(data)
}

// ParseKustomizeOutput parses kustomize output into Kubernetes objects
func ParseKustomizeOutput(data []byte) ([]*unstructured.Unstructured, error) {
	var objects []*unstructured.Unstructured

	decoder := yaml.NewDecoder(bytes.NewReader(data))

	docIndex := 0
	for {
		var doc map[string]interface{}
		err := decoder.Decode(&doc)

		if err == io.EOF {
			break
		}

		if err != nil {
			// A type error means the document was read but is not a mapping
			// (a bare scalar, say), so skipping it is safe. Any other error is
			// a syntax error, after which the decoder stays put: continuing
			// would spin on the same document forever.
			var typeErr *yaml.TypeError
			if !errors.As(err, &typeErr) {
				return nil, fmt.Errorf("failed to parse YAML document %d: %w", docIndex+1, err)
			}
			docIndex++
			continue
		}

		if len(doc) == 0 {
			docIndex++
			continue
		}

		obj := &unstructured.Unstructured{Object: doc}

		if obj.GetAPIVersion() == "" || obj.GetKind() == "" {
			docIndex++
			continue
		}

		objects = append(objects, obj)
		docIndex++
	}

	return objects, nil
}

// CheckKustomizeInstalled checks if kustomize is installed and available
func (b *Builder) CheckKustomizeInstalled(ctx context.Context) error {
	_, err := b.runner.Run(ctx, b.kustomizeBin, "version")
	if err != nil {
		return fmt.Errorf("kustomize not found or not executable: %w\nPlease install kustomize: https://kubectl.docs.kubernetes.io/installation/kustomize/", err)
	}
	return nil
}
