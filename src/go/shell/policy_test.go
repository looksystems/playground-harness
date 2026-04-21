package shell_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"agent-harness/go/shell"
)

// ---------------------------------------------------------------------------
// SecurityPolicy zero-value usability
// ---------------------------------------------------------------------------

func TestSecurityPolicy_ZeroValue(t *testing.T) {
	var p shell.SecurityPolicy
	assert.Nil(t, p.FilesystemAllow)
	assert.Nil(t, p.NetworkRules)
	assert.False(t, p.Inference.RoutingEnabled)
	assert.Equal(t, "", p.Inference.Provider)
}

func TestSecurityPolicy_Literal(t *testing.T) {
	p := shell.SecurityPolicy{
		FilesystemAllow: []string{"/home/sandbox", "/tmp"},
		NetworkRules: []shell.NetworkRule{
			{CIDR: "10.0.0.0/8", Action: shell.AllowAction},
			{CIDR: "0.0.0.0/0", Action: shell.DenyAction},
		},
		Inference: shell.InferencePolicy{
			RoutingEnabled: true,
			Provider:       "anthropic",
		},
	}

	assert.Len(t, p.FilesystemAllow, 2)
	assert.Equal(t, "/home/sandbox", p.FilesystemAllow[0])

	assert.Len(t, p.NetworkRules, 2)
	assert.Equal(t, "10.0.0.0/8", p.NetworkRules[0].CIDR)
	assert.Equal(t, shell.AllowAction, p.NetworkRules[0].Action)
	assert.Equal(t, shell.DenyAction, p.NetworkRules[1].Action)

	assert.True(t, p.Inference.RoutingEnabled)
	assert.Equal(t, "anthropic", p.Inference.Provider)
}

// ---------------------------------------------------------------------------
// NetworkRule zero-value usability
// ---------------------------------------------------------------------------

func TestNetworkRule_ZeroValue(t *testing.T) {
	var r shell.NetworkRule
	assert.Equal(t, "", r.CIDR)
	assert.Equal(t, shell.AllowAction, r.Action) // zero value is AllowAction
}

// ---------------------------------------------------------------------------
// Action constants
// ---------------------------------------------------------------------------

func TestAction_DistinctValues(t *testing.T) {
	assert.NotEqual(t, shell.AllowAction, shell.DenyAction)
}

func TestAction_AllowIsZero(t *testing.T) {
	// Matches protobuf NetworkPolicy.Action ALLOW=0.
	assert.Equal(t, shell.Action(0), shell.AllowAction)
}

func TestAction_DenyIsOne(t *testing.T) {
	// Matches protobuf NetworkPolicy.Action DENY=1.
	assert.Equal(t, shell.Action(1), shell.DenyAction)
}

// ---------------------------------------------------------------------------
// InferencePolicy zero-value usability
// ---------------------------------------------------------------------------

func TestInferencePolicy_ZeroValue(t *testing.T) {
	var ip shell.InferencePolicy
	assert.False(t, ip.RoutingEnabled)
	assert.Equal(t, "", ip.Provider)
}

func TestInferencePolicy_AnyProvider(t *testing.T) {
	ip := shell.InferencePolicy{RoutingEnabled: true, Provider: ""}
	assert.True(t, ip.RoutingEnabled)
	// Empty string means "any provider" per the spec.
	assert.Equal(t, "", ip.Provider)
}
