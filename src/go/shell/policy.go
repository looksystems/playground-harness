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
// OpenShell fills this out via the grpc driver; the builtin driver may
// ignore it entirely. The zero value is a permissive policy (no explicit
// allows or denies, inference routing disabled).
type SecurityPolicy struct {
	// FilesystemAllow is an allowlist of writable path prefixes. An empty
	// slice means no file-system restrictions are applied by this policy.
	FilesystemAllow []string

	// NetworkRules is an ordered list of CIDR-based allow/deny rules.
	NetworkRules []NetworkRule

	// Inference configures model-inference routing.
	Inference InferencePolicy
}
