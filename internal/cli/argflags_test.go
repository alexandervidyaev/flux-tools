package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func eqStrings(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true // treat nil and empty as equal
	}
	return reflect.DeepEqual(a, b)
}

func TestExtractOwnFlags(t *testing.T) {
	both := ownFlagsSpec{concurrency: true, outputDir: true}
	withCQ := ownFlagsSpec{concurrency: true, codequalityReport: true}

	tests := []struct {
		name     string
		args     []string
		spec     ownFlagsSpec
		wantConc int
		wantSet  bool
		wantOut  string
		wantCQ   string
		wantVerb bool
		wantRest []string
		wantErr  bool
	}{
		{name: "j before path", args: []string{"-j", "5", "./p"}, spec: both, wantConc: 5, wantSet: true, wantRest: []string{"./p"}},
		{name: "j after path", args: []string{"./p", "-j", "5"}, spec: both, wantConc: 5, wantSet: true, wantRest: []string{"./p"}},
		{name: "long concurrency", args: []string{"--concurrency", "8"}, spec: both, wantConc: 8, wantSet: true},
		{name: "o before path", args: []string{"-o", "out", "./p"}, spec: both, wantOut: "out", wantRest: []string{"./p"}},
		{name: "o after path", args: []string{"./p", "--output-dir", "out"}, spec: both, wantOut: "out", wantRest: []string{"./p"}},
		{name: "verbose", args: []string{"-v", "./p"}, spec: both, wantVerb: true, wantRest: []string{"./p"}},
		{name: "passthrough preserved", args: []string{"--quiet", "./p", "-o", "out"}, spec: both, wantOut: "out", wantRest: []string{"--quiet", "./p"}},
		{name: "output value with dash consumed", args: []string{"-o", "-weird", "./p"}, spec: both, wantOut: "-weird", wantRest: []string{"./p"}},
		{name: "bad int errors", args: []string{"-j", "abc"}, spec: both, wantErr: true},
		{name: "missing concurrency value errors", args: []string{"-j"}, spec: both, wantErr: true},
		{name: "missing output value errors", args: []string{"-o"}, spec: both, wantErr: true},
		{
			name: "flag not in spec passes through",
			args: []string{"-j", "5"}, spec: ownFlagsSpec{outputDir: true},
			wantSet: false, wantRest: []string{"-j", "5"},
		},
		{name: "codequality report before path", args: []string{"--codequality-report", "gl.json", "./p"}, spec: withCQ, wantCQ: "gl.json", wantRest: []string{"./p"}},
		{name: "codequality report after path", args: []string{"./p", "--codequality-report", "gl.json"}, spec: withCQ, wantCQ: "gl.json", wantRest: []string{"./p"}},
		{name: "codequality report single dash", args: []string{"-codequality-report", "gl.json", "./p"}, spec: withCQ, wantCQ: "gl.json", wantRest: []string{"./p"}},
		{name: "missing codequality value errors", args: []string{"./p", "--codequality-report"}, spec: withCQ, wantErr: true},
		{
			name: "codequality flag not in spec passes through",
			args: []string{"--codequality-report", "gl.json"}, spec: both,
			wantRest: []string{"--codequality-report", "gl.json"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := extractOwnFlags(tc.args, tc.spec)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got none (result %+v)", f)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if f.concurrency != tc.wantConc || f.concurrencySet != tc.wantSet {
				t.Errorf("concurrency=%d set=%v, want %d/%v", f.concurrency, f.concurrencySet, tc.wantConc, tc.wantSet)
			}
			if f.outputDir != tc.wantOut {
				t.Errorf("outputDir=%q, want %q", f.outputDir, tc.wantOut)
			}
			if f.codequalityReport != tc.wantCQ {
				t.Errorf("codequalityReport=%q, want %q", f.codequalityReport, tc.wantCQ)
			}
			if f.verbose != tc.wantVerb {
				t.Errorf("verbose=%v, want %v", f.verbose, tc.wantVerb)
			}
			if !eqStrings(f.rest, tc.wantRest) {
				t.Errorf("rest=%v, want %v", f.rest, tc.wantRest)
			}
		})
	}
}

func TestLastPositional(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantPos string
		wantOK  bool
		wantRst []string
	}{
		{name: "empty", args: nil, wantOK: false},
		{name: "flags only", args: []string{"-a", "-b"}, wantOK: false, wantRst: []string{"-a", "-b"}},
		{name: "single", args: []string{"a"}, wantPos: "a", wantOK: true, wantRst: nil},
		{name: "picks last", args: []string{"a", "-b", "c"}, wantPos: "c", wantOK: true, wantRst: []string{"a", "-b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pos, rest, ok := lastPositional(tc.args)
			if ok != tc.wantOK || pos != tc.wantPos {
				t.Errorf("pos=%q ok=%v, want %q/%v", pos, ok, tc.wantPos, tc.wantOK)
			}
			if tc.wantOK && !eqStrings(rest, tc.wantRst) {
				t.Errorf("rest=%v, want %v", rest, tc.wantRst)
			}
		})
	}
}

func TestParseKubeconformArgs(t *testing.T) {
	// -summary before path
	conc, verbose, cqReport, path, extra, err := parseKubeconformArgs([]string{"-summary", "./p"})
	if err != nil || path != "./p" || conc != 0 || verbose || cqReport != "" {
		t.Fatalf("got conc=%d verbose=%v cqReport=%q path=%q err=%v", conc, verbose, cqReport, path, err)
	}
	if !eqStrings(extra, []string{"-summary"}) {
		t.Errorf("extra=%v, want [-summary]", extra)
	}

	// -j after path is now consumed correctly (old parser mis-picked the path)
	conc, _, _, path, extra, err = parseKubeconformArgs([]string{"./p", "-j", "5"})
	if err != nil || path != "./p" || conc != 5 || len(extra) != 0 {
		t.Fatalf("got conc=%d path=%q extra=%v err=%v", conc, path, extra, err)
	}

	// --codequality-report is consumed, its value is not mistaken for the path
	_, _, cqReport, path, extra, err = parseKubeconformArgs([]string{"./p", "--codequality-report", "gl-codequality.json", "-strict"})
	if err != nil || cqReport != "gl-codequality.json" || path != "./p" {
		t.Fatalf("got cqReport=%q path=%q err=%v", cqReport, path, err)
	}
	if !eqStrings(extra, []string{"-strict"}) {
		t.Errorf("extra=%v, want [-strict]", extra)
	}

	// bad concurrency errors
	if _, _, _, _, _, err := parseKubeconformArgs([]string{"-j", "x", "./p"}); err == nil {
		t.Error("expected error on non-integer -j")
	}

	// missing --codequality-report value errors
	if _, _, _, _, _, err := parseKubeconformArgs([]string{"./p", "--codequality-report"}); err == nil {
		t.Error("expected error on missing --codequality-report value")
	}
}
func TestParseYqArgs(t *testing.T) {
	// eval-all with own flags interspersed
	conc, filter, path, out, _, _, err := parseYqArgs([]string{"-j", "10", "-o", "out", "eval-all", "sel(.x)", "./p"})
	if err != nil || conc != 10 || filter != "sel(.x)" || path != "./p" || out != "out" {
		t.Fatalf("got conc=%d filter=%q path=%q out=%q err=%v", conc, filter, path, out, err)
	}

	// standalone path (filter from env)
	_, filter, path, _, _, _, err = parseYqArgs([]string{"./only"})
	if err != nil || filter != "" || path != "./only" {
		t.Fatalf("got filter=%q path=%q err=%v", filter, path, err)
	}
}

// The passthrough commands disable cobra's flag parsing, so -h and --help
// reach RunE like any other argument. Every one of them has to answer it, or
// the command becomes undiscoverable: `yq --help` used to fail with "path
// argument is required (use --help for usage)".
func TestHelpIsAnsweredByPassthroughCommands(t *testing.T) {
	for _, name := range []string{"yq", "kubeconform"} {
		for _, flag := range []string{"-h", "--help"} {
			t.Run(name+" "+flag, func(t *testing.T) {
				root := NewRootCmd()
				var out bytes.Buffer
				root.SetOut(&out)
				root.SetErr(&out)
				root.SetArgs([]string{name, flag})

				if err := root.Execute(); err != nil {
					t.Fatalf("%s %s: %v", name, flag, err)
				}
				if !strings.Contains(out.String(), "Usage:") {
					t.Errorf("%s %s printed no usage:\n%s", name, flag, out.String())
				}
			})
		}
	}
}

func TestIsHelpRequest(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"--help"}, true},
		{[]string{"-h"}, true},
		{nil, false},
		{[]string{"./manifests"}, false},
		// A help flag meant for the wrapped tool is forwarded, not intercepted.
		{[]string{"./manifests", "--help"}, false},
	}
	for _, c := range cases {
		if got := isHelpRequest(c.args); got != c.want {
			t.Errorf("isHelpRequest(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}
