package skills

import (
	"context"
	"strings"

	"agent-harness/go/middleware"
)

// PromptMiddleware injects the mounted skills' instructions into the
// system prompt on every Pre pass. Post is a no-op.
//
// Port of Python SkillPromptMiddleware (has_skills.py:116-142).
//
// Unlike the Python version — which is constructed with a fixed list of
// skills and replaced on every mount/unmount — the Go version reads the
// live manager state on each Pre call. This means adds or removes after
// construction are reflected automatically without re-installing the
// middleware.
type PromptMiddleware struct {
	manager *Manager
}

// NewPromptMiddleware returns a middleware bound to m. It reads m's
// active skill list on every Pre invocation.
func NewPromptMiddleware(m *Manager) *PromptMiddleware {
	return &PromptMiddleware{manager: m}
}

// Pre appends a block listing each mounted skill's instructions to the
// system message (or prepends a new system message if none exists).
// If no skill has non-empty instructions, messages is returned
// unchanged.
//
// The wire format matches Python byte-for-byte:
//
//	<existing system content>\n\n---\n**Available Skills:**\n\n## <name>\n<instructions>\n\n## <name>\n<instructions>...
//
// When no system message is present the leading "\n\n" is stripped so
// the injected message does not begin with blank lines.
func (p *PromptMiddleware) Pre(
	_ context.Context,
	messages []middleware.Message,
	_ any,
) ([]middleware.Message, error) {
	if p.manager == nil {
		return messages, nil
	}
	active := p.manager.Active()
	sections := make([]string, 0, len(active))
	for _, sk := range active {
		instr := sk.Instructions()
		if instr == "" {
			continue
		}
		sections = append(sections, "## "+AutoName(sk)+"\n"+instr)
	}
	if len(sections) == 0 {
		return messages, nil
	}
	block := "\n\n---\n**Available Skills:**\n\n" + strings.Join(sections, "\n\n")

	out := make([]middleware.Message, len(messages))
	copy(out, messages)

	for i := range out {
		if out[i].Role == "system" {
			out[i].Content = out[i].Content + block
			return out, nil
		}
	}

	// No system message — prepend one carrying the (left-trimmed) block.
	sys := middleware.Message{Role: "system", Content: strings.TrimLeft(block, "\n")}
	return append([]middleware.Message{sys}, out...), nil
}

// Post is a no-op.
func (p *PromptMiddleware) Post(
	_ context.Context,
	msg middleware.Message,
	_ any,
) (middleware.Message, error) {
	return msg, nil
}
