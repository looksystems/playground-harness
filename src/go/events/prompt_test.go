package events

import (
	"strings"
	"testing"
)

// TestBuildPromptEmpty verifies that an empty type list produces an empty string.
func TestBuildPromptEmpty(t *testing.T) {
	got := BuildPrompt(nil)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
	got = BuildPrompt([]EventType{})
	if got != "" {
		t.Errorf("expected empty string for empty slice, got %q", got)
	}
}

// TestBuildPromptSinglePrimitive tests one buffered event with primitive schema fields.
func TestBuildPromptSinglePrimitive(t *testing.T) {
	et := EventType{
		Name:        "user_response",
		Description: "Send a message to the user",
		Schema: map[string]any{
			"message": "string",
		},
		Instructions: "Always use this for replies.",
	}
	got := BuildPrompt([]EventType{et})

	want := "# Event Emission\n\nYou can emit structured events inline in your response using the following format:\n\n## Event: user_response\nDescription: Send a message to the user\nFormat:\n```\n---event\ntype: user_response\nmessage: <string>\n---\n```\nAlways use this for replies.\n"

	if got != want {
		t.Errorf("output mismatch.\nGot:\n%s\nWant:\n%s", got, want)
	}
}

// TestBuildPromptNestedSchema tests one event with a nested sub-object schema.
func TestBuildPromptNestedSchema(t *testing.T) {
	et := EventType{
		Name:        "tool_call",
		Description: "Call a tool",
		Schema: map[string]any{
			"data": map[string]any{
				"message": "string",
			},
		},
		Instructions: "Use when calling tools.",
	}
	got := BuildPrompt([]EventType{et})

	want := "# Event Emission\n\nYou can emit structured events inline in your response using the following format:\n\n## Event: tool_call\nDescription: Call a tool\nFormat:\n```\n---event\ntype: tool_call\ndata:\n  message: <string>\n---\n```\nUse when calling tools.\n"

	if got != want {
		t.Errorf("output mismatch.\nGot:\n%s\nWant:\n%s", got, want)
	}
}

// TestBuildPromptNoInstructions tests that the instructions section is omitted when empty.
func TestBuildPromptNoInstructions(t *testing.T) {
	et := EventType{
		Name:        "log_event",
		Description: "Log a message",
		Schema: map[string]any{
			"level": "string",
		},
		Instructions: "", // no instructions
	}
	got := BuildPrompt([]EventType{et})

	// Instructions line should not appear.
	if strings.Contains(got, "\nlog_event\n") {
		// sanity check — this should not trigger
		t.Error("unexpected content")
	}
	// The format block should close cleanly without an instructions line.
	want := "# Event Emission\n\nYou can emit structured events inline in your response using the following format:\n\n## Event: log_event\nDescription: Log a message\nFormat:\n```\n---event\ntype: log_event\nlevel: <string>\n---\n```\n"

	if got != want {
		t.Errorf("output mismatch (no instructions).\nGot:\n%s\nWant:\n%s", got, want)
	}
}

// TestBuildPromptStreamingEvent tests that streaming events produce the same
// prompt format as buffered events (Python does not alter format for streaming).
func TestBuildPromptStreamingEvent(t *testing.T) {
	et := EventType{
		Name:        "stream_chunk",
		Description: "Stream text to the user",
		Schema: map[string]any{
			"content": "string",
		},
		Instructions: "Stream content here.",
		Streaming: StreamConfig{
			Mode:         "streaming",
			StreamFields: []string{"content"},
		},
	}
	got := BuildPrompt([]EventType{et})

	// Streaming config does not alter the prompt format — same output shape.
	want := "# Event Emission\n\nYou can emit structured events inline in your response using the following format:\n\n## Event: stream_chunk\nDescription: Stream text to the user\nFormat:\n```\n---event\ntype: stream_chunk\ncontent: <string>\n---\n```\nStream content here.\n"

	if got != want {
		t.Errorf("streaming event output mismatch.\nGot:\n%s\nWant:\n%s", got, want)
	}
}

// TestBuildPromptMultipleEvents tests multiple event types concatenated correctly.
func TestBuildPromptMultipleEvents(t *testing.T) {
	types := []EventType{
		{
			Name:        "ask_user",
			Description: "Ask the user a question",
			Schema: map[string]any{
				"question": "string",
			},
			Instructions: "Use for questions.",
		},
		{
			Name:        "end_turn",
			Description: "Signal end of turn",
			Schema:      map[string]any{},
			Instructions: "",
		},
	}
	got := BuildPrompt(types)

	// Header must appear exactly once.
	headerCount := strings.Count(got, "# Event Emission")
	if headerCount != 1 {
		t.Errorf("expected 1 occurrence of '# Event Emission', got %d", headerCount)
	}

	// Both event sections must appear.
	if !strings.Contains(got, "## Event: ask_user") {
		t.Error("missing '## Event: ask_user'")
	}
	if !strings.Contains(got, "## Event: end_turn") {
		t.Error("missing '## Event: end_turn'")
	}

	// ask_user section must appear before end_turn section.
	askIdx := strings.Index(got, "## Event: ask_user")
	endIdx := strings.Index(got, "## Event: end_turn")
	if askIdx >= endIdx {
		t.Error("ask_user section should come before end_turn section")
	}

	// There should be a blank line between the two event sections.
	// ask_user has instructions, so separator is: instructions\n\n## Event: end_turn
	if !strings.Contains(got, "Use for questions.\n\n## Event: end_turn") {
		t.Errorf("expected blank line between event sections; got:\n%s", got)
	}
}

// TestBuildPromptGolden is an exact golden-string test for the full output.
func TestBuildPromptGolden(t *testing.T) {
	types := []EventType{
		{
			Name:        "user_response",
			Description: "Send a message to the user",
			Schema: map[string]any{
				"message": "string",
			},
			Instructions: "Always use this for replies.",
		},
		{
			Name:        "tool_call",
			Description: "Invoke a tool",
			Schema: map[string]any{
				"params": map[string]any{
					"name": "string",
				},
			},
			Instructions: "",
		},
	}

	want := `# Event Emission

You can emit structured events inline in your response using the following format:

## Event: user_response
Description: Send a message to the user
Format:
` + "```" + `
---event
type: user_response
message: <string>
---
` + "```" + `
Always use this for replies.

## Event: tool_call
Description: Invoke a tool
Format:
` + "```" + `
---event
type: tool_call
params:
  name: <string>
---
` + "```" + `
`

	got := BuildPrompt(types)
	if got != want {
		t.Errorf("golden mismatch.\nGot:\n%q\nWant:\n%q", got, want)
	}
}

// TestBuildPromptNoSchema tests an event with no schema fields.
func TestBuildPromptNoSchema(t *testing.T) {
	et := EventType{
		Name:        "ping",
		Description: "A ping event",
		Schema:      map[string]any{},
		Instructions: "",
	}
	got := BuildPrompt([]EventType{et})

	want := "# Event Emission\n\nYou can emit structured events inline in your response using the following format:\n\n## Event: ping\nDescription: A ping event\nFormat:\n```\n---event\ntype: ping\n---\n```\n"

	if got != want {
		t.Errorf("no-schema output mismatch.\nGot:\n%s\nWant:\n%s", got, want)
	}
}
