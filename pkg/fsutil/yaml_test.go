package fsutil

import (
	"testing"
)

func TestSplitYAMLDocuments(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{name: "single", in: "a: 1\n", want: 1},
		{name: "two docs", in: "a: 1\n---\nb: 2\n", want: 2},
		{name: "leading separator", in: "---\na: 1\n", want: 1},
		{name: "trailing separator", in: "a: 1\n---\n", want: 1},
		{name: "empty docs dropped", in: "a: 1\n---\n---\nb: 2\n", want: 2},
		// The naive strings.Split(data, "\n---") mis-handled these:
		{name: "four dashes is not a separator", in: "a: 1\n----\nb: 2\n", want: 1},
		{name: "separator with trailing comment is content", in: "--- # header\na: 1\n", want: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitYAMLDocuments([]byte(tc.in))
			if len(got) != tc.want {
				t.Errorf("got %d documents, want %d: %q", len(got), tc.want, got)
			}
		})
	}
}
