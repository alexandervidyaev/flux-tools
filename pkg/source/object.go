// Package source resolves OCI artifacts and composes ArtifactGenerator outputs.
package source

import (
	"bytes"
	"encoding/json"
	"io"

	"gopkg.in/yaml.v3"
)

type Object = map[string]any
type Item struct{ Object Object }

func value(m Object, keys ...string) any {
	var v any = m
	for _, k := range keys {
		a, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = a[k]
	}
	return v
}
func text(m Object, keys ...string) string { v, _ := value(m, keys...).(string); return v }
func object(m Object, keys ...string) Object {
	v, _ := value(m, keys...).(map[string]any)
	if v == nil {
		return Object{}
	}
	return v
}
func array(m Object, keys ...string) []any { v, _ := value(m, keys...).([]any); return v }
func fallback(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
func namespace(m Object) string { return fallback(text(m, "metadata", "namespace"), "default") }
func name(m Object) string {
	return fallback(text(m, "metadata", "name"), text(m, "metadata", "generateName"))
}
func copyValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := Object{}
		for k, v := range x {
			out[k] = copyValue(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = copyValue(v)
		}
		return out
	default:
		return v
	}
}
func clone(m Object) Object {
	if m == nil {
		return nil
	}
	return copyValue(m).(map[string]any)
}
func merge(a, b Object) Object {
	out := clone(a)
	for k, v := range b {
		x, xo := out[k].(map[string]any)
		y, yo := v.(map[string]any)
		if xo && yo {
			out[k] = merge(x, y)
		} else {
			out[k] = v
		}
	}
	return out
}
func documents(b []byte) ([]Object, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var out []Object
	for {
		var doc Object
		err := dec.Decode(&doc)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		if doc == nil {
			continue
		}
		// Reject YAML values that cannot be sent to the Kubernetes JSON API.
		_, err = json.Marshal(doc)
		if err != nil {
			return out, err
		}
		out = append(out, doc)
	}
}
func find(in []Item, kind, ns, n string) Object {
	for _, it := range in {
		if text(it.Object, "kind") == kind && namespace(it.Object) == ns && name(it.Object) == n {
			return it.Object
		}
	}
	return nil
}
