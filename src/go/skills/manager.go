// Package skills — SkillManager: mount, unmount, shutdown of skills on an
// agent. Port of Python has_skills.SkillManager (src/python/has_skills.py:153).
//
// # Design
//
// The manager talks to the agent through a narrow AgentAPI interface
// declared in this package (not the agent package) to avoid an import
// cycle. *agent.Agent satisfies it.
//
// # Divergences from Python
//
//   - Setup/Teardown are synchronous (return error).
//   - Dependencies() returns instances, not classes (see skill.go).
//   - Middleware and hook removal on Unmount is a no-op, same as Python:
//     middleware slices and hook slices are append-only in both ports.
//     Tools and shell commands are removed because their registries do
//     support Unregister/UnregisterCommand.
//   - SkillPromptMiddleware is installed once by the builder when the
//     first skill is mounted (see agent.Builder.Build); the manager
//     itself does not rebuild the middleware slot on every mount,
//     matching Python's bare "if no active skills → remove" special
//     case only indirectly via the builder's install-once semantics.
//     We still register fresh middleware via PromptMiddleware that reads
//     the live skill list on every Pre pass — so adds after Build are
//     reflected without a rebuild step.
package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"

	"agent-harness/go/hooks"
	"agent-harness/go/middleware"
	"agent-harness/go/shell"
	"agent-harness/go/tools"
)

// loaderToolName is the name of the tool registered on the agent while at
// least one progressive skill is pending activation.
const loaderToolName = "load_skill"

// AgentAPI is the narrow surface SkillManager needs from its owning agent.
// Declared in this package (not agent) to keep the dependency arrow
// pointing skills → agent satisfiers, avoiding an import cycle.
//
// *agent.Agent satisfies this interface at compile time via its embedded
// subsystems and a handful of forwarders.
type AgentAPI interface {
	// Tools
	Register(tools.Def) *tools.Registry
	Unregister(name string) bool

	// Middleware
	Use(middleware.Middleware) *middleware.Chain

	// Hooks
	On(hooks.Event, hooks.Handler) *hooks.Hub
	EmitHook(ctx context.Context, event hooks.Event, args ...any)

	// Shell — HasShell gates RegisterCommand / UnregisterCommand usage
	// because *Agent's embedded shell.Host may be nil.
	HasShell() bool
	RegisterCommand(name string, handler shell.CmdHandler)
	UnregisterCommand(name string)
}

// Manager mounts skills onto an agent. Contributions (tools, middleware,
// hooks, commands) are plumbed through the agent's subsystems at mount
// time and — where possible — removed on unmount.
type Manager struct {
	mu       sync.RWMutex
	agent    AgentAPI
	mounted  map[string]*mountedSkill
	ordering []string // mount order, for teardown in reverse

	// Progressive skills registered but not yet activated.
	pending      map[string]*pendingSkill
	pendingOrder []string // registration order, for stable prompt rendering
	loaderActive bool     // whether the load_skill tool is currently registered
}

// pendingSkill holds a progressive skill awaiting activation along with the
// per-mount config captured at register time.
type pendingSkill struct {
	skill  Skill
	config map[string]any
}

// mountedSkill tracks the skill and contribution bookkeeping needed for a
// clean Unmount. Middleware and hook references are kept for parity with
// Python, even though the current Go hooks.Hub and middleware.Chain do
// not support surgical removal (same limitation as Python — see package
// doc above).
type mountedSkill struct {
	skill   Skill
	config  map[string]any
	sctx    *SkillContext
	tools   []string
	mws     []middleware.Middleware
	hooks   []hookBinding
	cmds    []string
}

type hookBinding struct {
	event   hooks.Event
	handler hooks.Handler
}

// NewManager creates a manager bound to an agent. Called from the Agent
// constructor after other subsystems are ready.
func NewManager(agent AgentAPI) *Manager {
	return &Manager{
		agent:    agent,
		mounted:  make(map[string]*mountedSkill),
		ordering: nil,
		pending:  make(map[string]*pendingSkill),
	}
}

// Mount installs s (and its transitive dependencies) onto the agent.
// Dependencies are resolved via DFS with a seen-set; cycles return an
// error. A skill with the same name as an already-mounted skill is a
// no-op (matches Python).
//
// config is applied only to s — dependencies mount with nil config.
// Matches Python has_skills.mount.
func (m *Manager) Mount(ctx context.Context, s Skill, config map[string]any) error {
	if s == nil {
		return errors.New("skills.Manager.Mount: skill is nil")
	}

	// Progressive skills are registered (deferred) — only their discovery
	// metadata is wired up and the load_skill tool is ensured. Their
	// dependencies are resolved at activation time, not here.
	if isProgressive(s) {
		name := AutoName(s)
		m.mu.RLock()
		_, mounted := m.mounted[name]
		_, pend := m.pending[name]
		m.mu.RUnlock()
		if mounted || pend {
			return nil
		}
		return m.registerDeferred(s, name, config)
	}

	// Resolve dependency order with cycle detection. Order is dep-first
	// so each skill mounts after its own dependencies.
	ordered, err := m.resolveDeps(s)
	if err != nil {
		return err
	}

	for _, sk := range ordered {
		name := AutoName(sk)

		m.mu.RLock()
		_, already := m.mounted[name]
		_, pend := m.pending[name]
		m.mu.RUnlock()
		if already || pend {
			continue
		}

		// Only the originally-requested skill receives the caller's
		// config; dependencies get nil (matches Python mount()).
		var cfg map[string]any
		if sk == s {
			cfg = config
		}
		if isProgressive(sk) {
			if err := m.registerDeferred(sk, name, cfg); err != nil {
				return err
			}
			continue
		}
		if err := m.mountSingle(ctx, sk, name, cfg); err != nil {
			return err
		}
	}
	return nil
}

// mountSingle performs the lifecycle + contribution wiring for one skill.
// Errors from Setup abort the mount and do not record the skill as
// mounted.
func (m *Manager) mountSingle(ctx context.Context, s Skill, name string, cfg map[string]any) error {
	sctx := &SkillContext{
		Skill:  s,
		Agent:  m.agent,
		Config: cfg,
	}

	if sp, ok := s.(Setuppable); ok {
		if err := sp.Setup(ctx, sctx); err != nil {
			return fmt.Errorf("skills.Manager: setup %q: %w", name, err)
		}
	}

	ms := &mountedSkill{
		skill:  s,
		config: cfg,
		sctx:   sctx,
	}

	// Tools.
	if tc, ok := s.(ToolsContributor); ok {
		for _, td := range tc.Tools() {
			m.agent.Register(td)
			ms.tools = append(ms.tools, td.Name)
		}
	}

	// Middleware.
	if mc, ok := s.(MiddlewareContributor); ok {
		for _, mw := range mc.Middleware() {
			m.agent.Use(mw)
			ms.mws = append(ms.mws, mw)
		}
	}

	// Hooks.
	if hc, ok := s.(HooksContributor); ok {
		for event, handlers := range hc.Hooks() {
			for _, h := range handlers {
				m.agent.On(event, h)
				ms.hooks = append(ms.hooks, hookBinding{event: event, handler: h})
			}
		}
	}

	// Shell commands — only if the agent has a shell attached.
	if cc, ok := s.(CommandsContributor); ok && m.agent.HasShell() {
		for cname, handler := range cc.Commands() {
			m.agent.RegisterCommand(cname, handler)
			ms.cmds = append(ms.cmds, cname)
		}
	}

	m.mu.Lock()
	m.mounted[name] = ms
	m.ordering = append(m.ordering, name)
	m.mu.Unlock()

	m.agent.EmitHook(ctx, hooks.SkillSetup, name)
	m.agent.EmitHook(ctx, hooks.SkillMount, name)
	return nil
}

// ---------------------------------------------------------------------------
// Progressive (two-phase) loading
// ---------------------------------------------------------------------------

// registerDeferred records a progressive skill without wiring any
// contributions, and ensures the load_skill loader tool is registered.
// A progressive skill must carry a non-empty Description — it is all the
// model sees before activation.
func (m *Manager) registerDeferred(s Skill, name string, config map[string]any) error {
	if s.Description() == "" {
		return fmt.Errorf("skills.Manager: progressive skill %q must have a non-empty description", name)
	}
	m.mu.Lock()
	m.pending[name] = &pendingSkill{skill: s, config: config}
	m.pendingOrder = append(m.pendingOrder, name)
	m.mu.Unlock()

	m.ensureLoaderTool()
	return nil
}

// activate runs the normal mount path for a pending progressive skill (and
// its dependencies), returning the activated skill's instructions so the body
// lands in context for the current turn. Idempotent: activating an
// already-loaded skill simply returns its instructions. Once no progressive
// skill remains pending, the loader tool is unregistered.
func (m *Manager) activate(ctx context.Context, name string) (string, error) {
	m.mu.RLock()
	if ms, ok := m.mounted[name]; ok {
		m.mu.RUnlock()
		return ms.skill.Instructions(), nil
	}
	ps, ok := m.pending[name]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("skills.Manager: no pending progressive skill named %q", name)
	}

	skill := ps.skill
	ordered, err := m.resolveDeps(skill)
	if err != nil {
		return "", err
	}

	for _, sk := range ordered {
		dname := AutoName(sk)

		m.mu.RLock()
		_, already := m.mounted[dname]
		dps, isPending := m.pending[dname]
		m.mu.RUnlock()
		if already {
			continue
		}

		var cfg map[string]any
		if sk == skill {
			cfg = ps.config
		} else if isPending {
			cfg = dps.config
		}

		if err := m.mountSingle(ctx, sk, dname, cfg); err != nil {
			return "", err
		}
		m.removePending(dname)
	}

	m.maybeUnregisterLoader()
	return skill.Instructions(), nil
}

// removePending drops name from the pending map and ordering slice.
func (m *Manager) removePending(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pending[name]; !ok {
		return
	}
	delete(m.pending, name)
	for i, n := range m.pendingOrder {
		if n == name {
			m.pendingOrder = append(m.pendingOrder[:i], m.pendingOrder[i+1:]...)
			break
		}
	}
}

// buildLoaderTool returns the load_skill tool definition. Its Execute decodes
// {"name": "..."} and activates that progressive skill, returning its body.
func (m *Manager) buildLoaderTool() tools.Def {
	return tools.Def{
		Name: loaderToolName,
		Description: "Load (activate) a progressive skill by name. Activating a skill " +
			"registers its tools and surfaces its full instructions. Pass the " +
			"skill name exactly as shown in the Available Skills list.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Name of the progressive skill to activate.",
				},
			},
			"required": []string{"name"},
		},
		Execute: func(ctx context.Context, args []byte) (any, error) {
			var p struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, fmt.Errorf("skills.load_skill: decode args: %w", err)
			}
			return m.activate(ctx, p.Name)
		},
	}
}

// ensureLoaderTool registers the load_skill tool once, idempotently.
func (m *Manager) ensureLoaderTool() {
	m.mu.Lock()
	if m.loaderActive {
		m.mu.Unlock()
		return
	}
	m.loaderActive = true
	m.mu.Unlock()
	m.agent.Register(m.buildLoaderTool())
}

// maybeUnregisterLoader removes the load_skill tool once nothing is pending.
func (m *Manager) maybeUnregisterLoader() {
	m.mu.Lock()
	if len(m.pending) > 0 {
		m.mu.Unlock()
		return
	}
	was := m.loaderActive
	m.loaderActive = false
	m.mu.Unlock()
	if was {
		m.agent.Unregister(loaderToolName)
	}
}

// Pending returns the pending (registered but not activated) progressive
// skills in registration order. Used by PromptMiddleware to render discovery
// metadata.
func (m *Manager) Pending() []Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Skill, 0, len(m.pendingOrder))
	for _, name := range m.pendingOrder {
		if ps, ok := m.pending[name]; ok {
			out = append(out, ps.skill)
		}
	}
	return out
}

// Mounted returns the names of currently mounted skills in mount order.
func (m *Manager) Mounted() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, len(m.ordering))
	copy(out, m.ordering)
	return out
}

// Active returns the currently mounted skills in mount order. Used by
// PromptMiddleware which needs instruction strings.
func (m *Manager) Active() []Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Skill, 0, len(m.ordering))
	for _, name := range m.ordering {
		if ms, ok := m.mounted[name]; ok {
			out = append(out, ms.skill)
		}
	}
	return out
}

// Get returns the mounted skill by name, or nil if not mounted.
func (m *Manager) Get(name string) Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if ms, ok := m.mounted[name]; ok {
		return ms.skill
	}
	return nil
}

// Unmount removes a skill (and only that skill — dependents are not
// checked in Python either). Calls Teardown if implemented. Returns nil
// if the skill wasn't mounted.
//
// Contribution cleanup is best-effort: tools and shell commands are
// removed via Unregister/UnregisterCommand; middleware and hook
// handlers stay registered because neither registry supports surgical
// removal in the Go port or in Python.
func (m *Manager) Unmount(ctx context.Context, name string) error {
	m.mu.Lock()
	ms, ok := m.mounted[name]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.mounted, name)
	// Drop from ordering slice.
	for i, n := range m.ordering {
		if n == name {
			m.ordering = append(m.ordering[:i], m.ordering[i+1:]...)
			break
		}
	}
	m.mu.Unlock()

	// Emit SkillTeardown before running the skill's Teardown — matches
	// Python's HasSkills.unmount which emits SKILL_TEARDOWN before the
	// manager.unmount call.
	m.agent.EmitHook(ctx, hooks.SkillTeardown, name)

	if td, ok := ms.skill.(Teardown); ok {
		if err := td.Teardown(ctx, ms.sctx); err != nil {
			// Log but do not block contribution cleanup, matching
			// Python's best-effort shutdown semantics.
			log.Printf("skills.Manager: teardown %q: %v", name, err)
		}
	}

	for _, toolName := range ms.tools {
		m.agent.Unregister(toolName)
	}
	if m.agent.HasShell() {
		for _, cmd := range ms.cmds {
			m.agent.UnregisterCommand(cmd)
		}
	}
	// middleware/hooks: no-op, documented limitation.

	m.agent.EmitHook(ctx, hooks.SkillUnmount, name)
	return nil
}

// Shutdown tears down all mounted skills in reverse mount order. Errors
// are logged; Shutdown does not short-circuit. Matches Python's
// SkillManager.shutdown.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.RLock()
	// Copy names so we can release the lock before calling Unmount (which
	// re-acquires it).
	names := make([]string, len(m.ordering))
	copy(names, m.ordering)
	m.mu.RUnlock()

	for i := len(names) - 1; i >= 0; i-- {
		if err := m.Unmount(ctx, names[i]); err != nil {
			log.Printf("skills.Manager: shutdown unmount %q: %v", names[i], err)
		}
	}

	// Drop any still-pending progressive skills and remove the loader tool.
	m.mu.Lock()
	m.pending = make(map[string]*pendingSkill)
	m.pendingOrder = nil
	m.mu.Unlock()
	m.maybeUnregisterLoader()
	return nil
}

// ---------------------------------------------------------------------------
// Dependency resolution
// ---------------------------------------------------------------------------

// resolveDeps returns s and its transitive dependencies in dep-first
// order. Cycles are detected by tracking "in progress" names during the
// DFS traversal and return a descriptive error.
//
// Diamond deps (A→B, A→C, B→D, C→D) are handled: D is visited once and
// appears once in the result because "seen" tracks completed visits.
func (m *Manager) resolveDeps(s Skill) ([]Skill, error) {
	var (
		seen    = map[string]bool{} // fully resolved
		stack   = map[string]bool{} // currently in the DFS stack
		ordered []Skill
	)

	var visit func(sk Skill, path []string) error
	visit = func(sk Skill, path []string) error {
		name := AutoName(sk)
		if seen[name] {
			return nil
		}
		if stack[name] {
			return fmt.Errorf("skills.Manager: dependency cycle detected: %v → %s", path, name)
		}
		stack[name] = true
		path = append(path, name)

		if dep, ok := sk.(Dependencies); ok {
			for _, d := range dep.Dependencies() {
				if d == nil {
					return fmt.Errorf("skills.Manager: skill %q returned nil dependency", name)
				}
				if err := visit(d, path); err != nil {
					return err
				}
			}
		}

		delete(stack, name)
		seen[name] = true
		ordered = append(ordered, sk)
		return nil
	}

	if err := visit(s, nil); err != nil {
		return nil, err
	}
	return ordered, nil
}
