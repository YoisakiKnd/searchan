package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
)

type schemaField struct {
	name  string
	shape *schemaShape
}

type schemaShape struct {
	kind     string
	nullable bool
	fields   []schemaField
	item     *schemaShape
}

type outputSchema struct {
	data     json.RawMessage
	shape    *schemaShape
	required string
}

// Production has a small fixed set of output types. Bound retention even when
// library callers construct arbitrarily many dynamic reflect.StructOf types.
var outputSchemas = struct {
	sync.Mutex
	entries map[reflect.Type]*outputSchema
}{entries: make(map[reflect.Type]*outputSchema)}

var jsonDecoderType = reflect.TypeFor[json.Unmarshaler]()

func schemaForOutput(out any) (*outputSchema, error) {
	v := reflect.ValueOf(out)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return nil, errors.New("structured output requires a nonnil pointer to a typed object")
	}
	t := v.Type().Elem()
	outputSchemas.Lock()
	cached := outputSchemas.entries[t]
	outputSchemas.Unlock()
	if cached != nil {
		return cached, nil
	}
	nodes := 0
	shape, err := buildSchema(t, 0, &nodes, map[reflect.Type]bool{})
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(shape.wire())
	if err != nil || len(data) > 16<<10 {
		return nil, errors.New("structured output schema exceeds 16 KiB")
	}
	required := make([]string, 0, len(shape.fields))
	for _, f := range shape.fields {
		required = append(required, f.name)
	}
	keys, _ := json.Marshal(required)
	schema := &outputSchema{data: data, shape: shape, required: string(keys)}
	outputSchemas.Lock()
	defer outputSchemas.Unlock()
	if existing := outputSchemas.entries[t]; existing != nil {
		return existing, nil
	}
	if len(outputSchemas.entries) < 16 {
		outputSchemas.entries[t] = schema
	}
	return schema, nil
}

func buildSchema(t reflect.Type, depth int, nodes *int, path map[reflect.Type]bool) (*schemaShape, error) {
	*nodes++
	if depth > 8 || *nodes > 128 || path[t] {
		return nil, errors.New("structured output schema exceeds depth, node or recursion bounds")
	}
	if t.Implements(jsonDecoderType) || reflect.PointerTo(t).Implements(jsonDecoderType) {
		return nil, errors.New("structured output does not support custom JSON decoding")
	}
	path[t] = true
	defer delete(path, t)
	s := &schemaShape{}
	switch t.Kind() {
	case reflect.Pointer:
		child, err := buildSchema(t.Elem(), depth+1, nodes, path)
		if err != nil {
			return nil, err
		}
		child.nullable = true
		return child, nil
	case reflect.Struct:
		s.kind = "object"
		seen := map[string]bool{}
		for i := range t.NumField() {
			f := t.Field(i)
			if f.Tag.Get("json") == "-" {
				continue
			}
			if f.Anonymous {
				return nil, errors.New("structured output does not support embedded fields")
			}
			if f.PkgPath != "" {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")
			if len(tag) > 1 && containsSchemaOption(tag[1:], "string") {
				return nil, errors.New("structured output does not support string-encoded scalars")
			}
			name := tag[0]
			if name == "" {
				name = f.Name
			}
			if len(name) > 128 || seen[name] {
				return nil, errors.New("structured output has duplicate or oversized field names")
			}
			seen[name] = true
			child, err := buildSchema(f.Type, depth+1, nodes, path)
			if err != nil {
				return nil, err
			}
			s.fields = append(s.fields, schemaField{name, child})
		}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return nil, errors.New("structured output does not support binary slices")
		}
		s.kind = "array"
		var err error
		s.item, err = buildSchema(t.Elem(), depth+1, nodes, path)
		if err != nil {
			return nil, err
		}
	case reflect.String:
		s.kind = "string"
	case reflect.Bool:
		s.kind = "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s.kind = "integer"
	case reflect.Float32, reflect.Float64:
		s.kind = "number"
	default:
		return nil, errors.New("structured output contains an unsupported Go type")
	}
	return s, nil
}

func containsSchemaOption(options []string, wanted string) bool {
	for _, option := range options {
		if option == wanted {
			return true
		}
	}
	return false
}

func (s *schemaShape) wire() map[string]any {
	value := map[string]any{"type": s.kind}
	if s.kind == "object" {
		properties := map[string]any{}
		required := make([]string, 0, len(s.fields))
		for _, field := range s.fields {
			properties[field.name] = field.shape.wire()
			required = append(required, field.name)
		}
		value["properties"], value["required"], value["additionalProperties"] = properties, required, false
	} else if s.kind == "array" {
		value["items"] = s.item.wire()
	}
	if s.nullable {
		return map[string]any{"anyOf": []any{value, map[string]string{"type": "null"}}}
	}
	return value
}

// The typed decoder checks scalar types. Also enforce required fields, null
// rules and unknown-field rejection locally, since compatible gateways may
// accept and silently ignore the native schema parameter.
func (s *schemaShape) matches(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return false
	}
	if bytes.Equal(raw, []byte("null")) {
		return s.nullable
	}
	switch s.kind {
	case "object":
		var fields map[string]json.RawMessage
		if raw[0] != '{' || json.Unmarshal(raw, &fields) != nil || len(fields) != len(s.fields) {
			return false
		}
		for _, f := range s.fields {
			if !f.shape.matches(fields[f.name]) {
				return false
			}
		}
	case "array":
		var items []json.RawMessage
		if raw[0] != '[' || json.Unmarshal(raw, &items) != nil {
			return false
		}
		for _, item := range items {
			if !s.item.matches(item) {
				return false
			}
		}
	case "string":
		return raw[0] == '"'
	case "boolean":
		return bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false"))
	default:
		return raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9'
	}
	return true
}

// Only run the diagnostic walk after a mismatch. Paths refer to expected Go
// fields; unknown model-supplied keys and values never enter diagnostics.
func (s *schemaShape) issue(raw json.RawMessage, path string) (string, string) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "missing_field", path
	}
	if bytes.Equal(raw, []byte("null")) {
		return "null_not_allowed", path
	}
	switch s.kind {
	case "object":
		var fields map[string]json.RawMessage
		if raw[0] != '{' || json.Unmarshal(raw, &fields) != nil {
			return "wrong_type", path
		}
		for _, f := range s.fields {
			if !f.shape.matches(fields[f.name]) {
				next := f.name
				if path != "" {
					next = path + "." + next
				}
				return f.shape.issue(fields[f.name], next)
			}
		}
		return "unknown_field", path
	case "array":
		var items []json.RawMessage
		if raw[0] != '[' || json.Unmarshal(raw, &items) != nil {
			return "wrong_type", path
		}
		for _, item := range items {
			if !s.item.matches(item) {
				return s.item.issue(item, path)
			}
		}
	}
	return "shape_mismatch", path
}

func applyOutputSchema(api string, payload map[string]any, schema json.RawMessage) {
	switch api {
	case "chat_completions":
		payload["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "searchan_output", "strict": true, "schema": schema}}
	case "responses":
		payload["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "searchan_output", "strict": true, "schema": schema}}
	case "anthropic":
		config := map[string]any{"format": map[string]any{"type": "json_schema", "schema": schema}}
		if existing, ok := payload["output_config"].(map[string]string); ok {
			for k, v := range existing {
				config[k] = v
			}
		}
		payload["output_config"] = config
	}
}
