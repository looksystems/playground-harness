package shell

import (
	"fmt"
	"sync"
)

// FactoryFunc constructs a Driver from a map of options.
// This mirrors Python's **kwargs pattern: each factory function reads
// whatever keys it needs from opts.
type FactoryFunc func(opts map[string]any) (Driver, error)

// DriverFactory registers named factory functions and creates Driver instances
// by name (e.g. "builtin", "bashkit", "openshell").
type DriverFactory struct {
	mu       sync.RWMutex
	registry map[string]FactoryFunc
	def      string // default driver name
}

// NewFactory returns an empty DriverFactory with no default set.
func NewFactory() *DriverFactory {
	return &DriverFactory{
		registry: make(map[string]FactoryFunc),
	}
}

// Register associates name with fn. Overwrites any existing registration.
// Returns the factory for chaining.
func (f *DriverFactory) Register(name string, fn FactoryFunc) *DriverFactory {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registry[name] = fn
	return f
}

// SetDefault configures which name is used when Create is called with an
// empty string. Returns the factory for chaining.
func (f *DriverFactory) SetDefault(name string) *DriverFactory {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.def = name
	return f
}

// Create instantiates a Driver by name. If name is "" the configured default
// is used. Returns an error if the name is unknown.
func (f *DriverFactory) Create(name string, opts map[string]any) (Driver, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	resolved := name
	if resolved == "" {
		resolved = f.def
	}
	if resolved == "" {
		return nil, fmt.Errorf("shell: no driver name provided and no default is set")
	}

	fn, ok := f.registry[resolved]
	if !ok {
		return nil, fmt.Errorf("shell: driver %q is not registered", resolved)
	}
	return fn(opts)
}

// DefaultFactory is a package-level DriverFactory for convenience.
// Callers that need isolation (e.g. tests) should use NewFactory instead.
var DefaultFactory = NewFactory()
