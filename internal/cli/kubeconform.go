package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alexandervidyaev/flux-tools/internal/kubeconform"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

func NewKubeconformCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kubeconform [path] [kubeconform-flags...]",
		Short: "Validate Kubernetes manifests using kubeconform",
		Long: `Validate Kubernetes manifests using kubeconform.

This command is a thin wrapper around kubeconform that provides a convenient
interface with the -j flag for concurrency control. All kubeconform flags are
passed through natively.

The -j flag maps to kubeconform's native -n flag (number of goroutines).
Kubeconform handles files and directories efficiently on its own.

The --codequality-report <path> flag writes a GitLab Code Quality JSON
report built from kubeconform's own JSON output (flux-tools adds
'-output json' itself, so it cannot be combined with a user-supplied
-output flag). Invalid resources are reported as severity "major",
processing errors as "critical". The report is written even when
validation fails; zero findings produce an empty [] report.

Examples:
  # Validate single file
  flux-tools kubeconform ./cluster-manifests/kube-dev.yaml -summary

  # Validate directory
  flux-tools kubeconform ./cluster-manifests/ -summary -ignore-missing-schemas

  # Validate with custom concurrency (10 goroutines)
  flux-tools kubeconform ./cluster-manifests/ -j 10 -summary

  # Write a GitLab Code Quality report (for artifacts:reports:codequality)
  flux-tools kubeconform ./cluster-manifests/ --codequality-report gl-codequality.json

  # All kubeconform flags work natively
  flux-tools kubeconform ./cluster-manifests/ \
    -kubernetes-version 1.28.0 \
    -schema-location default \
    -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
    -skip CustomResourceDefinition \
    -summary \
    -output pretty

Supported kubeconform flags (passed through):
  --strict                      Strict validation
  --ignore-missing-schemas      Skip resources with missing schemas
  --kubernetes-version VERSION  K8s version to validate against
  --schema-location URL         Override schema location
  --skip KINDS                  Comma-separated kinds to skip
  --reject KINDS                Comma-separated kinds to reject
  --output FORMAT               Output format (text, json, junit, tap, pretty)
  --summary                     Print summary at the end
  --verbose                     Print results for all resources
  --exit-on-error               Stop on first error
  And all other kubeconform flags...
`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// DisableFlagParsing hands every argument to RunE, cobra's own help
			// among them, so the command has to answer it itself.
			if isHelpRequest(args) {
				return cmd.Help()
			}

			// Manually parse our own flags
			concurrency, verbose, codequalityReport, path, kubeconformArgs, err := parseKubeconformArgs(args)
			if err != nil {
				return err
			}
			return runKubeconform(commandContext(cmd), path, kubeconformArgs, concurrency, verbose, codequalityReport)
		},
		// Disable flag parsing to pass all unknown flags through to kubeconform
		DisableFlagParsing: true,
	}

	return cmd
}

// parseKubeconformArgs extracts flux-tools-owned flags (-j, -v,
// --codequality-report) and the positional path, passing everything else
// through to kubeconform. The path may appear anywhere among the arguments;
// the last positional wins.
func parseKubeconformArgs(args []string) (concurrency int, verbose bool, codequalityReport string, path string, kubeconformArgs []string, err error) {
	f, err := extractOwnFlags(args, ownFlagsSpec{concurrency: true, codequalityReport: true})
	if err != nil {
		return 0, false, "", "", nil, err
	}

	path, kubeconformArgs, _ = lastPositional(f.rest)
	if kubeconformArgs == nil {
		kubeconformArgs = []string{}
	}
	return f.concurrency, f.verbose, f.codequalityReport, path, kubeconformArgs, nil
}

// userOutputFlag returns the first kubeconform -output flag found in args
// ("" when absent). --codequality-report needs '-output json' for itself, so
// a user-supplied output format cannot be honored at the same time.
func userOutputFlag(args []string) string {
	for _, a := range args {
		name := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
		if name == "output" || strings.HasPrefix(name, "output=") {
			return a
		}
	}
	return ""
}

func runKubeconform(ctx context.Context, path string, kubeconformArgs []string, concurrency int, verbose bool, codequalityReport string) error {
	p := output.New(verbose)

	// The Code Quality report is built from kubeconform's own JSON output,
	// so the output format is not free for the user to choose. Checked
	// before path validation: the argument parser may mistake a value of a
	// passthrough flag (as in "-output pretty") for the positional path,
	// and the incompatibility error is the clearer one.
	if codequalityReport != "" {
		if flag := userOutputFlag(kubeconformArgs); flag != "" {
			return fmt.Errorf("--codequality-report is incompatible with kubeconform %s: the report is parsed from '-output json', which flux-tools sets itself; remove %s", flag, flag)
		}
	}

	// Check if path is provided
	if path == "" {
		return fmt.Errorf("path argument is required (use --help for usage)")
	}

	// Check if path exists
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("path does not exist: %s", path)
	}

	// Inject concurrency flag if specified (maps to kubeconform's -n flag)
	finalArgs := kubeconformArgs
	if concurrency > 0 {
		// Add -n flag before other flags
		finalArgs = append([]string{"-n", fmt.Sprintf("%d", concurrency)}, kubeconformArgs...)
	}

	// Ask kubeconform for machine-readable output to build the report from.
	if codequalityReport != "" {
		finalArgs = append([]string{"-output", "json"}, finalArgs...)
	}

	p.Verbose("Validating: %s\n", path)
	if concurrency > 0 {
		p.Verbose("Concurrency: %d goroutines\n", concurrency)
	}
	p.Verbose("\n")

	// Direct passthrough to kubeconform - let it handle files/directories natively
	result := kubeconform.ValidateFile(ctx, path, finalArgs)

	if codequalityReport != "" {
		return reportCodeQuality(result, codequalityReport)
	}

	// Print output directly
	fmt.Print(result.Output)

	// Return error if validation failed
	if !result.Passed() {
		return fmt.Errorf("kubeconform validation failed (exit code %d)", result.ExitCode)
	}

	return nil
}

// reportCodeQuality parses kubeconform's JSON output, prints a short
// human-readable summary and writes the GitLab Code Quality report. The
// report is written even when validation failed, before the non-zero exit
// is propagated; zero findings produce a valid empty report.
func reportCodeQuality(result *kubeconform.ValidationResult, reportPath string) error {
	if result.Err != nil {
		// kubeconform did not run at all (binary missing, exec failure):
		// there is no JSON to parse and nothing to report.
		return result.Err
	}

	resources, err := kubeconform.ParseJSONOutput([]byte(result.Output))
	if err != nil {
		// kubeconform produced non-JSON output (e.g. a usage error);
		// surface it as-is before failing.
		fmt.Print(result.Output)
		return fmt.Errorf("cannot build Code Quality report: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	if err := kubeconform.WriteCodeQualityReport(reportPath, kubeconform.ToCodeQuality(resources, cwd)); err != nil {
		return err
	}

	invalidCount, errorCount := 0, 0
	for _, r := range resources {
		switch r.Status {
		case kubeconform.StatusInvalid:
			invalidCount++
			fmt.Printf("INVALID %s: %s/%s: %s\n", r.Filename, r.Kind, r.Name, r.Msg)
		case kubeconform.StatusError:
			errorCount++
			fmt.Printf("ERROR   %s: %s\n", r.Filename, r.Msg)
		}
	}
	fmt.Printf("kubeconform: %d invalid, %d errors, Code Quality report: %s\n", invalidCount, errorCount, reportPath)

	if !result.Passed() {
		return fmt.Errorf("kubeconform validation failed (exit code %d)", result.ExitCode)
	}
	return nil
}
