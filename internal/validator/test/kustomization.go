package test

import (
	"context"
	"fmt"
	"time"

	"github.com/alexandervidyaev/flux-tools/pkg/fsutil"
	"github.com/alexandervidyaev/flux-tools/pkg/kustomize"
)

// Run executes the kustomization test
func (t *KustomizationTestCase) Run(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{
		Name: t.TestName,
		Type: KustomizationTest,
		Path: t.Kustomization.Spec.Path,
	}

	if t.Options.SkipOCI && t.Kustomization.Spec.SourceRef.Kind == "OCIRepository" {
		result.Status = TestSkipped
		result.Duration = time.Since(start)
		return result
	}

	// Resolve build path. The runner injects a BuildPathResolver that knows
	// how to clone external GitRepository sources; for synthetic test cases
	// without a resolver we fall back to spec.path joined with RootPath.
	buildPath, err := t.resolveBuildPath(ctx)
	if err != nil {
		result.Status = TestFailed
		result.Error = fmt.Errorf("failed to resolve build path: %w", err)
		result.Duration = time.Since(start)
		return result
	}

	ignore, err := kustomize.LoadSourceIgnore(t.RootPath)
	if err != nil {
		result.Status = TestFailed
		result.Error = err
		result.Duration = time.Since(start)
		return result
	}
	builder := kustomize.NewBuilder()
	builder.SetSourceIgnore(ignore)
	objects, err := builder.BuildDirAndParse(ctx, buildPath)
	if err != nil {
		result.Status = TestFailed
		result.Error = fmt.Errorf("kustomize build failed: %w", err)
		result.Duration = time.Since(start)
		return result
	}

	result.Status = TestPassed
	result.Objects = len(objects)

	result.Duration = time.Since(start)
	return result
}

// resolveBuildPath delegates to BuildPathResolver when set, otherwise joins
// spec.path against the configured RootPath.
func (t *KustomizationTestCase) resolveBuildPath(ctx context.Context) (string, error) {
	if t.BuildPathResolver != nil {
		return t.BuildPathResolver(ctx, t.Kustomization)
	}
	return fsutil.ResolvePath(t.RootPath, t.Kustomization.Spec.Path), nil
}
