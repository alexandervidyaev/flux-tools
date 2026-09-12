// Package test provides test discovery and execution for Flux Kustomization and HelmRelease resources.
package test

import (
	"context"
	"io"
	"time"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

// TestType represents the type of test
type TestType string

const (
	KustomizationTest TestType = "Kustomization"
	HelmReleaseTest   TestType = "HelmRelease"
)

// TestStatus represents the status of a test
type TestStatus string

const (
	TestPassed  TestStatus = "PASSED"
	TestFailed  TestStatus = "FAILED"
	TestSkipped TestStatus = "SKIPPED"
)

// TestOptions contains options for test execution
type TestOptions struct {
	Path             string
	RootPath         string // repository root; empty derives it from flux-system
	EnableHelm       bool
	Verbose          bool
	Strict           bool // Strict mode - fail on missing dependencies
	SkipCRDs         bool
	SkipSecrets      bool
	SkipFluxSystem   bool
	SkipFailedCharts bool // Skip charts that fail to template (by default, template errors are fatal)
	SkipOCI          bool // Skip Kustomizations with OCIRepository sourceRef instead of failing
	NoTemplateCache  bool // Disable the persistent helm template cache (--no-template-cache)
	Sequential       bool // Force sequential execution across clusters (escape hatch for parallel Helm testing)
	CacheDir         string
	HelmTimeout      int
	Output           io.Writer // Output writer (defaults to os.Stderr if nil)
}

// TestResult represents the result of a single test
type TestResult struct {
	Name     string
	Type     TestType
	Status   TestStatus
	Duration time.Duration
	Error    error
	Path     string
	Objects  int // Number of objects generated
}

// TestResults aggregates all test results
type TestResults struct {
	Total    int
	Passed   int
	Failed   int
	Skipped  int
	Duration time.Duration
	Tests    []TestResult
}

// Test interface for all test types
type Test interface {
	Name() string
	Type() TestType
	Path() string
	Run(ctx context.Context) TestResult
}

// BuildPathResolver returns the local filesystem path that should be fed to
// `kustomize build` for the given Kustomization. It is responsible for
// cloning external GitRepository sources when needed. When nil, callers fall
// back to joining spec.path against the configured RootPath.
type BuildPathResolver func(ctx context.Context, ks *types.Kustomization) (string, error)

// KustomizationTestCase validates kustomize build
type KustomizationTestCase struct {
	TestName          string
	Kustomization     *types.Kustomization
	RootPath          string // Repository root path
	Options           TestOptions
	BuildPathResolver BuildPathResolver // when set, takes precedence over RootPath
}

func (t *KustomizationTestCase) Name() string {
	return t.TestName
}

func (t *KustomizationTestCase) Type() TestType {
	return KustomizationTest
}

func (t *KustomizationTestCase) Path() string {
	return t.Kustomization.Spec.Path
}

// HelmReleaseTestCase validates helm template
type HelmReleaseTestCase struct {
	TestName         string
	HelmRelease      *types.HelmRelease
	HelmRepositories map[string]*types.HelmRepository
	// ArtifactGenerators resolve ExternalArtifacts referenced via spec.chartRef
	ArtifactGenerators map[string]*types.ArtifactGenerator
	// GitRepositories declared in the manifests; a chart from one of them is
	// cloned unless the GitRepository is SelfSource
	GitRepositories map[string]*types.GitRepository
	SelfSource      string // namespace/name of the GitRepository that is this checkout, empty when unknown
	RootPath        string // repository root, needed to resolve local charts from GitRepository sources
	Options         TestOptions
}

func (t *HelmReleaseTestCase) Name() string {
	return t.TestName
}

func (t *HelmReleaseTestCase) Type() TestType {
	return HelmReleaseTest
}

func (t *HelmReleaseTestCase) Path() string {
	return t.HelmRelease.Namespace + "/" + t.HelmRelease.Name
}
