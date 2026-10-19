package mcpclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/google/jsonschema-go/jsonschema"
)

// boundedJSON 拒绝重复键和过深结构，避免不同解析器对同一参数产生不同理解。
func boundedJSON(raw []byte, limit int) (any, error) {
	if len(raw) > limit {
		return nil, errLimit
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	nodes := 0
	var walk func(int) error
	walk = func(depth int) error {
		nodes++
		if depth > 16 || nodes > 2048 {
			return errLimit
		}
		t, e := d.Token()
		if e != nil {
			return e
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return e
					}
					s, ok := k.(string)
					if !ok || seen[s] {
						return errors.New("duplicate_key")
					}
					seen[s] = true
					if e = walk(depth + 1); e != nil {
						return e
					}
				}
			case '[':
				for d.More() {
					if e = walk(depth + 1); e != nil {
						return e
					}
				}
			default:
				return errors.New("invalid_json")
			}
			_, e = d.Token()
			return e
		}
		return nil
	}
	if e := walk(0); e != nil {
		return nil, e
	}
	if _, e := d.Token(); e != io.EOF {
		return nil, errors.New("invalid_json")
	}
	var v any
	e := json.Unmarshal(raw, &v)
	return v, e
}
func compileSchema(raw []byte) (*jsonschema.Resolved, error) {
	v, e := boundedJSON(raw, 16<<10)
	if e != nil {
		return nil, e
	}
	root, ok := v.(map[string]any)
	if !ok || root["type"] != "object" {
		return nil, errors.New("schema_object_required")
	}
	var inspect func(any) bool
	inspect = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if k == "$ref" || k == "$dynamicRef" || k == "$recursiveRef" {
					return false
				}
				if !inspect(val) {
					return false
				}
			}
		case []any:
			for _, val := range x {
				if !inspect(val) {
					return false
				}
			}
		}
		return true
	}
	if !inspect(v) {
		return nil, errors.New("schema_reference_unsupported")
	}
	var s jsonschema.Schema
	if e = json.Unmarshal(raw, &s); e != nil {
		return nil, errors.New("invalid_schema")
	}
	return s.Resolve(nil)
}
