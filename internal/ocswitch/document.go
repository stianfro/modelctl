package ocswitch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tailscale/hujson"
)

type document struct {
	snapshot
	tree hujson.Value
}

func loadDocument(path string) (*document, error) {
	s, err := readFile(path)
	if err != nil {
		return nil, err
	}
	// hujson retains slices of its input. Keep the concurrency snapshot immutable.
	tree, err := hujson.Parse(bytes.Clone(s.data))
	if err != nil {
		// Parser errors can include source text containing API keys.
		return nil, fmt.Errorf("invalid JSON or JSONC in %s; file was not changed", path)
	}
	if _, ok := tree.Value.(*hujson.Object); !ok || duplicateKeys(tree) {
		return nil, fmt.Errorf("expected a JSON object with unique keys in %s", path)
	}
	return &document{snapshot: s, tree: tree}, nil
}

func duplicateKeys(v hujson.Value) bool {
	switch x := v.Value.(type) {
	case *hujson.Object:
		seen := map[string]bool{}
		for _, m := range x.Members {
			key := m.Name.Value.(hujson.Literal).String()
			if seen[key] || duplicateKeys(m.Value) {
				return true
			}
			seen[key] = true
		}
	case *hujson.Array:
		for _, item := range x.Elements {
			if duplicateKeys(item) {
				return true
			}
		}
	}
	return false
}

func pointer(path []string) string {
	var b strings.Builder
	for _, part := range path {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1"))
	}
	return b.String()
}

func (d *document) get(path ...string) *hujson.Value {
	return d.tree.Find(pointer(path))
}

func (d *document) object(path ...string) error {
	if v := d.get(path...); v != nil {
		if _, ok := v.Value.(*hujson.Object); !ok {
			return fmt.Errorf("expected an object at %s in %s", pointer(path), d.path)
		}
	}
	return nil
}

func (d *document) string(path ...string) (string, error) {
	v := d.get(path...)
	if v == nil {
		return "", nil
	}
	clean := v.Clone()
	clean.Standardize()
	if literal, ok := clean.Value.(hujson.Literal); !ok || literal.Kind() != '"' {
		return "", fmt.Errorf("expected a string at %s in %s", pointer(path), d.path)
	}
	var result string
	if err := json.Unmarshal(clean.Pack(), &result); err != nil {
		return "", fmt.Errorf("expected a string at %s in %s", pointer(path), d.path)
	}
	return result, nil
}

// Set only the requested leaf, so other fields and comments stay in the document.
func (d *document) set(path []string, value any) error {
	for i := 1; i < len(path); i++ {
		if err := d.object(path[:i]...); err != nil {
			return err
		}
		if d.get(path[:i]...) == nil {
			if err := d.patch(path[:i], map[string]any{}); err != nil {
				return err
			}
		}
	}
	return d.patch(path, value)
}

func (d *document) patch(path []string, value any) error {
	p, err := json.Marshal([]map[string]any{{"op": "add", "path": pointer(path), "value": value}})
	if err != nil {
		return fmt.Errorf("cannot encode config update")
	}
	if err := d.tree.Patch(p); err != nil {
		return fmt.Errorf("cannot update config field %s", pointer(path))
	}
	return nil
}

func (d *document) save(secret bool) (bool, error) {
	// Do not reformat or rewrite documents when an operation did not change them.
	if string(d.tree.Pack()) == string(d.data) {
		return d.snapshot.write(d.data, secret)
	}
	d.tree.Format()
	data := d.tree.Pack()
	if _, err := hujson.Parse(data); err != nil {
		return false, fmt.Errorf("invalid generated config; file was not changed")
	}
	return d.snapshot.write(data, secret)
}
