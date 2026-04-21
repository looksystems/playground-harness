package shell

// Action is the verdict of a NetworkRule.
// Values match the protobuf NetworkPolicy.Action ordering: ALLOW=0, DENY=1.
type Action int

const (
	AllowAction Action = iota
	DenyAction
)

// NetworkRule describes a single network access rule.
type NetworkRule struct {
	CIDR   string
	Action Action
}

// InferencePolicy controls model-inference routing inside a sandbox.
type InferencePolicy struct {
	RoutingEnabled bool
	Provider       string // "openai" | "anthropic" | "" (any)
}

// SecurityPolicy is a lightweight driver-level security configuration.
// OpenShell fills this out via the grpc driver; the builtin driver
// honours AllowedCommands (see BuiltinShellDriver.WithSecurityPolicy).
// The zero value is a permissive policy (no explicit allows or denies,
// inference routing disabled, all commands allowed).
type SecurityPolicy struct {
	// AllowedCommands, when non-empty, restricts the builtin registry
	// to just these command names. Custom commands registered via
	// RegisterCommand are always allowed regardless of this field — it
	// filters the *default* registry only, matching Python's
	// allowed_commands semantics in src/python/shell.py.
	AllowedCommands []string

	// FilesystemAllow is an allowlist of writable path prefixes. An empty
	// slice means no file-system restrictions are applied by this policy.
	FilesystemAllow []string

	// NetworkRules is an ordered list of CIDR-based allow/deny rules.
	NetworkRules []NetworkRule

	// Inference configures model-inference routing.
	Inference InferencePolicy
}
