package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserOutputFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "no args", args: nil, want: ""},
		{name: "unrelated flags", args: []string{"-strict", "-summary", "./p"}, want: ""},
		{name: "single dash", args: []string{"-output", "json"}, want: "-output"},
		{name: "double dash", args: []string{"--output", "pretty"}, want: "--output"},
		{name: "single dash with equals", args: []string{"-output=json"}, want: "-output=json"},
		{name: "double dash with equals", args: []string{"--output=tap"}, want: "--output=tap"},
		{name: "prefix does not match", args: []string{"-output-dir", "x"}, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := userOutputFlag(tc.args); got != tc.want {
				t.Errorf("userOutputFlag(%v)=%q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func TestRunKubeconformCodequalityOutputConflict(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "m.yaml")
	if err := os.WriteFile(manifest, []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := runKubeconform(context.Background(), manifest, []string{"-output", "json"}, 0, false, filepath.Join(dir, "gl.json"))
	if err == nil {
		t.Fatal("expected error when combining --codequality-report with user -output")
	}
	if !strings.Contains(err.Error(), "--codequality-report is incompatible") || !strings.Contains(err.Error(), "-output") {
		t.Errorf("error message does not explain the incompatibility: %v", err)
	}
}
