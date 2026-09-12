package kustomize

import (
	"strings"
	"testing"
	"time"
)

// A syntax error used to spin forever: yaml.Decoder does not move past a
// document it could not lex, so the skip-and-continue loop re-read the same
// bytes without end. It must fail instead.
func TestParseKustomizeOutputRejectsBrokenYAML(t *testing.T) {
	done := make(chan struct{})
	var err error

	go func() {
		defer close(done)
		_, err = ParseKustomizeOutput([]byte("this: is: not: valid: yaml:\n\t- broken\n"))
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ParseKustomizeOutput did not return: the decoder loop is stuck again")
	}

	if err == nil {
		t.Fatal("want an error for a document that cannot be parsed")
	}
	if !strings.Contains(err.Error(), "document 1") {
		t.Errorf("err = %q, want it to name the document", err)
	}
}

// A document that parses but is not a mapping is still skipped, which is how
// kustomize's own trailing separators and stray scalars are tolerated.
func TestParseKustomizeOutputSkipsNonMappingDocuments(t *testing.T) {
	objects, err := ParseKustomizeOutput([]byte(`just-a-scalar
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: kept
---
`))
	if err != nil {
		t.Fatalf("ParseKustomizeOutput: %v", err)
	}
	if len(objects) != 1 || objects[0].GetName() != "kept" {
		t.Errorf("objects = %v, want only the ConfigMap", objects)
	}
}
