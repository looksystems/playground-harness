package tools

import (
	"fmt"
	"reflect"
	"strings"
)

// Schema generates an OpenAI-compatible JSON-schema object from a Go struct
// type. It uses struct field tags to drive JSON names and descriptions:
//
//   - `json:"name"` — field name in JSON (default: lowercase Go name)
//   - `json:",omitempty"` — field is optional (not added to "required")
//   - `desc:"..."` — field description
//
// Supported kinds: string, bool, int/int32/int64, float32/float64, slice of
// supported, nested struct, map[string]any. Unsupported kinds (func, chan,
// interface) panic with a descriptive message.
//
// Cyclic types (a struct that reaches itself through a field, directly or
// transitively) are handled by returning a generic {"type":"object"} schema at
// the point of recursion, so the generator terminates rather than overflowing
// the stack.
func Schema(t reflect.Type) map[string]any {
	return schemaInternal(t, map[reflect.Type]bool{})
}

// schemaInternal is the recursive form that carries the "already on the stack"
// set used for cycle detection.
func schemaInternal(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("tools.Schema: expected struct type, got %s", t.Kind()))
	}

	// Cycle guard: if we are already producing a schema for this type higher
	// up the stack, break the recursion here by emitting a generic object.
	if seen[t] {
		return map[string]any{"type": "object"}
	}
	seen[t] = true
	defer delete(seen, t)

	props := make(map[string]any)
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Skip unexported fields
		if !field.IsExported() {
			continue
		}

		// Determine JSON name and whether optional
		jsonName := strings.ToLower(field.Name)
		optional := false
		if tag, ok := field.Tag.Lookup("json"); ok {
			parts := strings.Split(tag, ",")
			if parts[0] != "" && parts[0] != "-" {
				jsonName = parts[0]
			} else if parts[0] == "-" {
				continue
			}
			for _, part := range parts[1:] {
				if part == "omitempty" {
					optional = true
				}
			}
		}

		prop := kindToSchema(field.Type, seen)

		// Attach description if present
		if desc, ok := field.Tag.Lookup("desc"); ok && desc != "" {
			prop["description"] = desc
		}

		props[jsonName] = prop
		if !optional {
			required = append(required, jsonName)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// kindToSchema converts a reflect.Type to a JSON-schema property object. The
// seen set is forwarded so nested structs (including those reached through
// slices and pointers) participate in cycle detection.
func kindToSchema(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	// Dereference pointer
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}

	case reflect.Bool:
		return map[string]any{"type": "boolean"}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}

	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}

	case reflect.Slice:
		items := kindToSchema(t.Elem(), seen)
		return map[string]any{
			"type":  "array",
			"items": items,
		}

	case reflect.Map:
		// Treat map[string]any (and similar) as a generic object
		return map[string]any{"type": "object"}

	case reflect.Struct:
		return schemaInternal(t, seen)

	default:
		panic(fmt.Sprintf("tools.Schema: unsupported field kind %s", t.Kind()))
	}
}
