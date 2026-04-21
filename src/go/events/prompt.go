package events

import (
	"fmt"
	"sort"
	"strings"
)

// BuildPrompt generates the system-prompt section describing how the LLM
// should emit events. Returns empty string if types is empty.
//
// Example output for a buffered `user_response` event type with schema
// {"data":{"message":"string"}}, description "Send a message to the user",
// and instructions "Always use this for replies":
//
//	# Event Emission
//
//	You can emit structured events inline in your response using the
//	following format:
//
//	## Event: user_response
//	Description: Send a message to the user
//	Format:
//	```
//	---event
//	type: user_response
//	data:
//	  message: <string>
//	---
//	```
//	Always use this for replies.
//
// Matches the Python output shape exactly (spacing, delimiters, headings).
func BuildPrompt(types []EventType) string {
	if len(types) == 0 {
		return ""
	}

	var sections []string
	sections = append(sections, "# Event Emission")
	sections = append(sections, "")
	sections = append(sections, "You can emit structured events inline in your response using the following format:")
	sections = append(sections, "")

	for _, et := range types {
		sections = append(sections, fmt.Sprintf("## Event: %s", et.Name))
		sections = append(sections, fmt.Sprintf("Description: %s", et.Description))
		sections = append(sections, "Format:")
		sections = append(sections, "```")
		sections = append(sections, "---event")
		sections = append(sections, fmt.Sprintf("type: %s", et.Name))
		// Sort top-level schema keys for deterministic output.
		topKeys := make([]string, 0, len(et.Schema))
		for k := range et.Schema {
			topKeys = append(topKeys, k)
		}
		sort.Strings(topKeys)
		for _, key := range topKeys {
			val := et.Schema[key]
			switch v := val.(type) {
			case map[string]any:
				sections = append(sections, fmt.Sprintf("%s:", key))
				// Sort nested keys for deterministic output.
				subKeys := make([]string, 0, len(v))
				for k := range v {
					subKeys = append(subKeys, k)
				}
				sort.Strings(subKeys)
				for _, k := range subKeys {
					sections = append(sections, fmt.Sprintf("  %s: <%v>", k, v[k]))
				}
			default:
				sections = append(sections, fmt.Sprintf("%s: <%v>", key, val))
			}
		}
		sections = append(sections, "---")
		sections = append(sections, "```")
		if et.Instructions != "" {
			sections = append(sections, et.Instructions)
		}
		sections = append(sections, "")
	}

	return strings.Join(sections, "\n")
}
