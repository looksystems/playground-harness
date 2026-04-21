package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"strings"

	"agent-harness/go/internal/util"
)

// Def is a registered tool definition.
type Def struct {
	Name        string
	Description string
	// Parameters is the OpenAI JSON-schema parameters object.
	// Example: {"type": "object", "properties": {"a": {"type": "integer"}}, "required": ["a"]}
	Parameters map[string]any
	// Execute runs the tool. args is the raw JSON arguments from the LLM.
	// Implementations should Unmarshal into a typed struct and return any
	// JSON-serializable result. Tool errors are returned as Go errors.
	Execute func(ctx context.Context, args []byte) (any, error)
}

// Option configures a tool built with Tool().
type Option func(*Def)

// Description sets the tool description.
func Description(s string) Option {
	return func(d *Def) { d.Description = s }
}

// Name overrides the tool name (default: snake_case of the function name).
func Name(s string) Option {
	return func(d *Def) { d.Name = s }
}

// ctxType is the reflect.Type for context.Context.
var ctxType = reflect.TypeOf((*context.Context)(nil)).Elem()

// errType is the reflect.Type for error.
var errType = reflect.TypeOf((*error)(nil)).Elem()

// Tool wraps a function of shape `func(ctx context.Context, args ArgsStruct) (Result, error)`
// into a Def. It uses reflection to derive the schema from ArgsStruct, unmarshal
// incoming JSON args, and invoke the fn.
//
// Programmer errors (wrong function shape, unsupported types) panic at
// construction time — mirroring how Python raises TypeError on decorator misuse.
//
// If the function is anonymous or its name cannot be derived, Name(...) is
// required; Tool panics if it is missing.
func Tool(fn any, opts ...Option) Def {
	if fn == nil {
		panic("tools.Tool: fn must not be nil")
	}
	fnVal := reflect.ValueOf(fn)
	fnType := fnVal.Type()

	if fnType.Kind() != reflect.Func {
		panic(fmt.Sprintf("tools.Tool: fn must be a function, got %s", fnType.Kind()))
	}

	// Validate signature: func(context.Context, ArgsStruct) (Result, error)
	if fnType.NumIn() != 2 {
		panic(fmt.Sprintf("tools.Tool: fn must have exactly 2 parameters (ctx, args), got %d", fnType.NumIn()))
	}
	if !fnType.In(0).Implements(ctxType) {
		panic(fmt.Sprintf("tools.Tool: fn first parameter must implement context.Context, got %s", fnType.In(0)))
	}
	argsType := fnType.In(1)
	if argsType.Kind() != reflect.Struct {
		panic(fmt.Sprintf("tools.Tool: fn second parameter must be a struct, got %s", argsType.Kind()))
	}

	if fnType.NumOut() != 2 {
		panic(fmt.Sprintf("tools.Tool: fn must return exactly 2 values (result, error), got %d", fnType.NumOut()))
	}
	if !fnType.Out(1).Implements(errType) {
		panic(fmt.Sprintf("tools.Tool: fn second return value must implement error, got %s", fnType.Out(1)))
	}

	// Derive default name from runtime function name
	defaultName := deriveFuncName(fnVal)

	d := Def{
		Name:       defaultName,
		Parameters: Schema(argsType),
	}

	// Apply options
	for _, opt := range opts {
		opt(&d)
	}

	// If name is still empty (anonymous fn with no Name option), panic
	if d.Name == "" {
		panic("tools.Tool: anonymous function requires tools.Name(...) option")
	}

	// Build Execute closure
	d.Execute = func(ctx context.Context, args []byte) (any, error) {
		argVal := reflect.New(argsType)
		if err := json.Unmarshal(args, argVal.Interface()); err != nil {
			return nil, fmt.Errorf("tools: unmarshal args for %q: %w", d.Name, err)
		}
		results := fnVal.Call([]reflect.Value{
			reflect.ValueOf(ctx),
			argVal.Elem(),
		})
		var execErr error
		if !results[1].IsNil() {
			execErr = results[1].Interface().(error)
		}
		return results[0].Interface(), execErr
	}

	return d
}

// deriveFuncName extracts a snake_case name from the runtime function name.
// Returns "" for anonymous functions.
func deriveFuncName(fnVal reflect.Value) string {
	pc := fnVal.Pointer()
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return ""
	}
	fullName := fn.Name()
	// fullName looks like "pkg/path.FuncName" or "pkg/path.FuncName.func1" for lambdas
	// Extract the last component after the final "."
	parts := strings.Split(fullName, ".")
	last := parts[len(parts)-1]

	// Anonymous functions have names like "func1", "func2", etc.
	// We detect them by checking if the name matches the func\d+ pattern from Go's runtime.
	if isAnonymous(last) {
		return ""
	}

	return util.SnakeCase(last)
}

// isAnonymous returns true if the function name segment looks like a Go anonymous
// function identifier. Go's runtime uses names like:
//   - "func1", "func2"   — top-level anonymous closure
//   - "1", "2"           — nested closure suffix after a dot in the full name
func isAnonymous(name string) bool {
	if name == "" {
		return true
	}
	// Pure numeric suffix: nested closure like "...func2.1"
	allDigits := true
	for _, r := range name {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return true
	}
	// "funcN" pattern
	if !strings.HasPrefix(name, "func") {
		return false
	}
	rest := name[len("func"):]
	if len(rest) == 0 {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
