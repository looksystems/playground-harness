package tools

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// ErrNotFound is returned by Execute when the requested tool name is not
// registered in the Registry.
var ErrNotFound = errors.New("tool not found")

// Registry holds a thread-safe collection of tool definitions.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Def
}

// New creates and returns an empty Registry.
func New() *Registry {
	return &Registry{
		tools: make(map[string]Def),
	}
}

// Register adds d to the registry (or replaces an existing entry with the
// same name). It returns the registry for method chaining.
func (r *Registry) Register(d Def) *Registry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[d.Name] = d
	return r
}

// Unregister removes the tool with the given name. It returns true if the
// tool was present and has been removed, false if it was not registered.
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, exists := r.tools[name]
	if exists {
		delete(r.tools, name)
	}
	return exists
}

// Get looks up a tool by name.
func (r *Registry) Get(name string) (Def, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.tools[name]
	return d, ok
}

// List returns all registered tools sorted by name.
func (r *Registry) List() []Def {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]Def, 0, len(r.tools))
	for _, d := range r.tools {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}

// Schemas returns the OpenAI `tools` array representation of all registered
// tools, sorted by name. Each element has the shape:
//
//	{"type": "function", "function": {"name": ..., "description": ..., "parameters": ...}}
func (r *Registry) Schemas() []map[string]any {
	list := r.List()
	out := make([]map[string]any, len(list))
	for i, d := range list {
		out[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        d.Name,
				"description": d.Description,
				"parameters":  d.Parameters,
			},
		}
	}
	return out
}

// Execute runs the tool with the given name, passing args as the raw JSON
// argument payload. It returns ErrNotFound if no tool with that name is
// registered.
func (r *Registry) Execute(ctx context.Context, name string, args []byte) (any, error) {
	d, ok := r.Get(name)
	if !ok {
		return nil, ErrNotFound
	}
	return d.Execute(ctx, args)
}
