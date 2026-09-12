// Package cli wires the Cobra commands and turns their flags and positional
// paths into the options the validator, differ and posters take.
package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// ownFlagsSpec declares which flux-tools-owned flags a passthrough command
// recognizes. -v/--verbose is always recognized.
type ownFlagsSpec struct {
	concurrency       bool // -j / --concurrency <int>
	outputDir         bool // -o / --output-dir <string>
	codequalityReport bool // --codequality-report <path>
	allowMissingPath  bool // --allow-missing-path
}

// ownFlags is the result of stripping flux-tools-owned flags from a raw
// passthrough argument list.
type ownFlags struct {
	concurrency       int
	concurrencySet    bool // true if -j/--concurrency was present (lets callers keep their own default)
	outputDir         string
	codequalityReport string
	allowMissingPath  bool
	verbose           bool
	// rest is every argument not consumed as an own flag, order preserved.
	// It still contains the wrapped tool's own flags and the positional args;
	// each command interprets the positionals from here itself.
	rest []string
}

// extractOwnFlags pulls the shared flux-tools flags (-j/--concurrency,
// -o/--output-dir, --codequality-report, -v/--verbose) out of a raw argument
// list, returning them plus the leftover arguments in their original order.
//
// It replaces three near-identical hand-rolled parsers. Unlike the old code it
// validates the concurrency value via strconv.Atoi (returning an error) instead
// of fmt.Sscanf, which silently yielded 0 on garbage, and it recognizes owned
// flags anywhere in the list rather than only before the positional path.
func extractOwnFlags(args []string, spec ownFlagsSpec) (ownFlags, error) {
	var f ownFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case spec.concurrency && (arg == "-j" || arg == "--concurrency"):
			if i+1 >= len(args) {
				return f, fmt.Errorf("flag %s requires an integer value", arg)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return f, fmt.Errorf("invalid value for %s: %q is not an integer", arg, args[i])
			}
			f.concurrency = n
			f.concurrencySet = true
		case spec.allowMissingPath && arg == "--allow-missing-path":
			f.allowMissingPath = true
		case spec.outputDir && (arg == "-o" || arg == "--output-dir"):
			if i+1 >= len(args) {
				return f, fmt.Errorf("flag %s requires a value", arg)
			}
			i++
			f.outputDir = args[i]
		case spec.codequalityReport && (arg == "--codequality-report" || arg == "-codequality-report"):
			if i+1 >= len(args) {
				return f, fmt.Errorf("flag %s requires a file path value", arg)
			}
			i++
			f.codequalityReport = args[i]
		case arg == "-v" || arg == "--verbose":
			f.verbose = true
		default:
			f.rest = append(f.rest, arg)
		}
	}
	return f, nil
}

// lastPositional returns the last non-flag argument in args and the args with
// that element removed (order preserved). ok is false when there is no
// positional argument. Used by commands whose single positional (a path) may
// appear before or after the wrapped tool's flags.
func lastPositional(args []string) (positional string, rest []string, ok bool) {
	idx := -1
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			idx = i
		}
	}
	if idx == -1 {
		return "", args, false
	}
	rest = make([]string, 0, len(args)-1)
	rest = append(rest, args[:idx]...)
	rest = append(rest, args[idx+1:]...)
	return args[idx], rest, true
}

// isHelpRequest reports whether the only thing asked for is the command's own
// help. The passthrough commands disable cobra's flag parsing to forward
// everything to the wrapped tool, which means cobra never sees -h/--help and
// the command has to recognise it itself.
func isHelpRequest(args []string) bool {
	return len(args) == 1 && (args[0] == "-h" || args[0] == "--help")
}
