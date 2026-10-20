package assist

// schemasubset_test.go, a small JSON Schema checker for the request shape tests (sub item 3).
//
// The module has no JSON Schema library and a test must not add a dependency, so this implements
// the subset the two schemas in testdata use: type (one or several), enum, const, properties,
// required, additionalProperties false, items, minItems, minLength, minimum, pattern, oneOf and a
// local $ref into $defs. Anything else in a schema is an error, so the schema cannot quietly use a
// keyword this checker does not enforce.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type schemaDoc struct {
	root map[string]any
}

var supportedKeywords = map[string]bool{
	"$schema": true, "$comment": true, "$defs": true, "$ref": true, "type": true, "enum": true, "const": true,
	"properties": true, "required": true, "additionalProperties": true, "items": true, "minItems": true,
	"minLength": true, "minimum": true, "pattern": true, "oneOf": true,
}

func loadSchema(t *testing.T, path string) *schemaDoc {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return &schemaDoc{root: root}
}

// Check returns every violation of the schema by the JSON document, or nil.
func (d *schemaDoc) Check(doc []byte) []string {
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return []string{"not JSON: " + err.Error()}
	}
	return d.validate(d.root, v, "$")
}

func (d *schemaDoc) validate(s map[string]any, v any, path string) []string {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, path+": "+fmt.Sprintf(format, a...)) }
	for k := range s {
		if !supportedKeywords[k] {
			add("schema keyword %q is not supported by the checker", k)
		}
	}
	if ref, ok := s["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		defs, _ := d.root["$defs"].(map[string]any)
		target, ok := defs[name].(map[string]any)
		if !ok {
			return append(errs, path+": unresolved $ref "+ref)
		}
		return append(errs, d.validate(target, v, path)...)
	}
	if t, ok := s["type"]; ok {
		var types []string
		switch tt := t.(type) {
		case string:
			types = []string{tt}
		case []any:
			for _, x := range tt {
				types = append(types, x.(string))
			}
		}
		matched := false
		for _, want := range types {
			if jsonTypeIs(v, want) {
				matched = true
			}
		}
		if !matched {
			add("want type %v, got %s", types, jsonTypeName(v))
			return errs
		}
	}
	if c, ok := s["const"]; ok && !reflect.DeepEqual(c, v) {
		add("want const %v, got %v", c, v)
	}
	if e, ok := s["enum"].([]any); ok {
		found := false
		for _, x := range e {
			if reflect.DeepEqual(x, v) {
				found = true
			}
		}
		if !found {
			add("%v is not one of %v", v, e)
		}
	}
	switch tv := v.(type) {
	case string:
		if n, ok := s["minLength"].(float64); ok && float64(len([]rune(tv))) < n {
			add("shorter than %v", n)
		}
		if p, ok := s["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(tv) {
			add("%q does not match %s", tv, p)
		}
	case float64:
		if n, ok := s["minimum"].(float64); ok && tv < n {
			add("%v is below %v", tv, n)
		}
	case []any:
		if n, ok := s["minItems"].(float64); ok && float64(len(tv)) < n {
			add("fewer than %v items", n)
		}
		if items, ok := s["items"].(map[string]any); ok {
			for i, x := range tv {
				errs = append(errs, d.validate(items, x, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	case map[string]any:
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if _, present := tv[r.(string)]; !present {
					add("missing required %q", r)
				}
			}
		}
		props, _ := s["properties"].(map[string]any)
		keys := make([]string, 0, len(tv))
		for k := range tv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if sub, ok := props[k].(map[string]any); ok {
				errs = append(errs, d.validate(sub, tv[k], path+"."+k)...)
			} else if ap, ok := s["additionalProperties"].(bool); ok && !ap {
				add("unexpected field %q", k)
			}
		}
	}
	if of, ok := s["oneOf"].([]any); ok {
		valid := 0
		for _, alt := range of {
			if len(d.validate(alt.(map[string]any), v, path)) == 0 {
				valid++
			}
		}
		if valid != 1 {
			add("matches %d of the %d oneOf alternatives, want exactly 1", valid, len(of))
		}
	}
	return errs
}

func jsonTypeIs(v any, want string) bool {
	switch want {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f)
	}
	return false
}

func jsonTypeName(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case nil:
		return "null"
	case float64:
		return "number"
	}
	return fmt.Sprintf("%T", v)
}

// -- the checker has teeth ------------------------------------------------------------------------

func TestSchemaSubset_RejectsWhatTheDocsRefuse(t *testing.T) {
	a := loadSchema(t, "testdata/anthropic_messages_request.schema.json")
	bad := map[string]string{
		"tool name with a dot":            `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"x"}]}],"tools":[{"name":"order.lookup","description":"d","input_schema":{"type":"object"}}]}`,
		"tool_result without tool_use_id": `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"tool_result","content":"x"}]}]}`,
		"unknown role":                    `{"model":"m","max_tokens":1,"messages":[{"role":"tool","content":[{"type":"text","text":"x"}]}]}`,
		"tool_use without id":             `{"model":"m","max_tokens":1,"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"a","input":{}}]}]}`,
		"unexpected field":                `{"model":"m","max_tokens":1,"temperature_typo":1,"messages":[{"role":"user","content":[{"type":"text","text":"x"}]}]}`,
		"no messages":                     `{"model":"m","max_tokens":1,"messages":[]}`,
	}
	for name, doc := range bad {
		if errs := a.Check([]byte(doc)); len(errs) == 0 {
			t.Errorf("%s: the schema accepted a request the API refuses", name)
		}
	}
	good := `{"model":"m","max_tokens":1,"system":"s","messages":[{"role":"user","content":[{"type":"text","text":"x"}]},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"order__lookup","input":{"a":"b"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"r"}]}],"tools":[{"name":"order__lookup","description":"d","input_schema":{"type":"object","properties":{},"required":[]}}]}`
	if errs := a.Check([]byte(good)); len(errs) != 0 {
		t.Errorf("the schema refused a valid request: %v", errs)
	}
}
