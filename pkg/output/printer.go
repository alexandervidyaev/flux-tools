// Package output provides a unified interface for writing structured output to stderr.
package output

import (
	"fmt"
	"io"
	"os"
)

// Printer handles structured output.
type Printer struct {
	w       io.Writer
	verbose bool
}

// New creates a Printer that writes to stderr.
func New(verbose bool) *Printer {
	return &Printer{w: os.Stderr, verbose: verbose}
}

// NewWithWriter creates a Printer that writes to w. Tests use it to assert on
// what a command actually reports, which for the diff pipeline is the product
// rather than a side effect.
func NewWithWriter(w io.Writer, verbose bool) *Printer {
	return &Printer{w: w, verbose: verbose}
}

// Info writes an informational message unconditionally.
func (p *Printer) Info(format string, args ...any) {
	fmt.Fprintf(p.w, format, args...)
}

// Verbose writes a message only when verbose mode is enabled.
func (p *Printer) Verbose(format string, args ...any) {
	if p.verbose {
		fmt.Fprintf(p.w, format, args...)
	}
}

// Warn writes a warning message unconditionally.
func (p *Printer) Warn(format string, args ...any) {
	fmt.Fprintf(p.w, "Warning: "+format, args...)
}

// IsVerbose returns whether verbose mode is enabled.
func (p *Printer) IsVerbose() bool {
	return p.verbose
}
