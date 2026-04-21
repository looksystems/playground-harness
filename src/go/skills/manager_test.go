package skills_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/hooks"
	"agent-harness/go/middleware"
	"agent-harness/go/shell"
	"agent-harness/go/skills"
	"agent-harness/go/tools"
)

// ---------------------------------------------------------------------------
// fakeAgent — records every AgentAPI call so tests can assert wiring.
// ---------------------------------------------------------------------------

type fakeAgent struct {
	mu           sync.Mutex
	tools        map[string]tools.Def
	toolOrder    []string
	unregistered []string
	mws          []middleware.Middleware
	hooks        map[hooks.Event][]hooks.Handler
	emitted      []emitRecord
	hasShell     bool
	cmds         map[string]shell.CmdHandler
	cmdOrder     []string
	cmdDelete    []string

	// Embedded registries/hub so we can return realistic values.
	reg  *tools.Registry
	hub  *hooks.Hub
	chn  *middleware.Chain
}

type emitRecord struct {
	event hooks.Event
	args  []any
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{
		tools: make(map[string]tools.Def),
		hooks: make(map[hooks.Event][]hooks.Handler),
		cmds:  make(map[string]shell.CmdHandler),
		reg:   tools.New(),
		hub:   hooks.NewHub(),
		chn:   middleware.NewChain(),
	}
}

func (f *fakeAgent) Register(d tools.Def) *tools.Registry {
	f.mu.Lock()
	f.tools[d.Name] = d
	f.toolOrder = append(f.toolOrder, d.Name)
	f.mu.Unlock()
	return f.reg.Register(d)
}

func (f *fakeAgent) Unregister(name string) bool {
	f.mu.Lock()
	_, ok := f.tools[name]
	delete(f.tools, name)
	f.unregistered = append(f.unregistered, name)
	f.mu.Unlock()
	f.reg.Unregister(name)
	return ok
}

func (f *fakeAgent) Use(mw middleware.Middleware) *middleware.Chain {
	f.mu.Lock()
	f.mws = append(f.mws, mw)
	f.mu.Unlock()
	return f.chn.Use(mw)
}

func (f *fakeAgent) On(e hooks.Event, h hooks.Handler) *hooks.Hub {
	f.mu.Lock()
	f.hooks[e] = append(f.hooks[e], h)
	f.mu.Unlock()
	return f.hub.On(e, h)
}

func (f *fakeAgent) EmitHook(_ context.Context, e hooks.Event, args ...any) {
	f.mu.Lock()
	f.emitted = append(f.emitted, emitRecord{event: e, args: append([]any(nil), args...)})
	f.mu.Unlock()
}

func (f *fakeAgent) HasShell() bool { return f.hasShell }

func (f *fakeAgent) RegisterCommand(name string, h shell.CmdHandler) {
	f.mu.Lock()
	f.cmds[name] = h
	f.cmdOrder = append(f.cmdOrder, name)
	f.mu.Unlock()
}

func (f *fakeAgent) UnregisterCommand(name string) {
	f.mu.Lock()
	delete(f.cmds, name)
	f.cmdDelete = append(f.cmdDelete, name)
	f.mu.Unlock()
}

func (f *fakeAgent) emittedEvents() []hooks.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]hooks.Event, len(f.emitted))
	for i, r := range f.emitted {
		out[i] = r.event
	}
	return out
}

// Compile-time assertion that fakeAgent satisfies AgentAPI.
var _ skills.AgentAPI = (*fakeAgent)(nil)

// ---------------------------------------------------------------------------
// Test skills
// ---------------------------------------------------------------------------

// bareSkill has no capabilities — tests the trivial mount path.
type bareSkill struct{ skills.Base }

// named: override Name.
type namedSkill struct{ skills.Base }

func (namedSkill) Name() string { return "explicit" }

// toolsSkill contributes one tool.
type toolsSkill struct{ skills.Base }

func (toolsSkill) Tools() []tools.Def {
	return []tools.Def{{
		Name:        "tools_skill_do",
		Description: "noop",
		Parameters:  map[string]any{"type": "object"},
		Execute:     func(_ context.Context, _ []byte) (any, error) { return "ok", nil },
	}}
}

// mwSkill contributes one middleware.
type mwSkill struct{ skills.Base }

type markerMW struct{ tag string }

func (markerMW) Pre(_ context.Context, m []middleware.Message, _ any) ([]middleware.Message, error) {
	return m, nil
}
func (markerMW) Post(_ context.Context, m middleware.Message, _ any) (middleware.Message, error) {
	return m, nil
}

func (mwSkill) Middleware() []middleware.Middleware {
	return []middleware.Middleware{markerMW{tag: "mw"}}
}

// hooksSkill contributes a hook handler.
type hooksSkill struct{ skills.Base }

func (hooksSkill) Hooks() map[hooks.Event][]hooks.Handler {
	return map[hooks.Event][]hooks.Handler{
		hooks.RunStart: {func(context.Context, ...any) {}},
	}
}

// cmdSkill contributes a shell command.
type cmdSkill struct{ skills.Base }

func (cmdSkill) Commands() map[string]shell.CmdHandler {
	return map[string]shell.CmdHandler{
		"hello": func(_ []string, _ string) shell.ExecResult {
			return shell.ExecResult{Stdout: "hi"}
		},
	}
}

// setupSkill tracks Setup/Teardown calls and optionally errors on Setup.
type setupSkill struct {
	skills.Base
	setupCalls    int
	teardownCalls int
	setupErr      error
	sctx          *skills.SkillContext
}

func (s *setupSkill) Setup(_ context.Context, sctx *skills.SkillContext) error {
	s.setupCalls++
	s.sctx = sctx
	return s.setupErr
}

func (s *setupSkill) Teardown(_ context.Context, _ *skills.SkillContext) error {
	s.teardownCalls++
	return nil
}

func (setupSkill) Name() string { return "setup_skill" }

// depA depends on depB; depB has no deps.
type depA struct{ skills.Base }

func (depA) Name() string                  { return "dep_a" }
func (depA) Dependencies() []skills.Skill { return []skills.Skill{&depB{}} }

type depB struct{ skills.Base }

func (depB) Name() string { return "dep_b" }

// cyclicA ↔ cyclicB
type cyclicA struct{ skills.Base }

func (cyclicA) Name() string                  { return "cyclic_a" }
func (cyclicA) Dependencies() []skills.Skill { return []skills.Skill{&cyclicB{}} }

type cyclicB struct{ skills.Base }

func (cyclicB) Name() string                  { return "cyclic_b" }
func (cyclicB) Dependencies() []skills.Skill { return []skills.Skill{&cyclicA{}} }

// ---------------------------------------------------------------------------
// Mount basics
// ---------------------------------------------------------------------------

func TestManager_MountBareSkill(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	err := m.Mount(context.Background(), &bareSkill{}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"bare"}, m.Mounted())
	assert.NotNil(t, m.Get("bare"))
	assert.Nil(t, m.Get("does_not_exist"))
}

func TestManager_MountExplicitName(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	err := m.Mount(context.Background(), namedSkill{}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"explicit"}, m.Mounted())
}

func TestManager_MountNilSkillErrors(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	err := m.Mount(context.Background(), nil, nil)
	require.Error(t, err)
}

func TestManager_MountIdempotent(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	require.NoError(t, m.Mount(context.Background(), &bareSkill{}, nil))
	// Second mount under the same name is a no-op.
	require.NoError(t, m.Mount(context.Background(), &bareSkill{}, nil))
	assert.Equal(t, []string{"bare"}, m.Mounted())
}

// ---------------------------------------------------------------------------
// Contribution wiring
// ---------------------------------------------------------------------------

func TestManager_Mount_RegistersTools(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	require.NoError(t, m.Mount(context.Background(), &toolsSkill{}, nil))
	_, ok := fa.tools["tools_skill_do"]
	assert.True(t, ok, "expected tool to be registered on fake agent")
}

func TestManager_Mount_AppendsMiddleware(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	require.NoError(t, m.Mount(context.Background(), &mwSkill{}, nil))
	require.Len(t, fa.mws, 1, "middleware should be registered")
}

func TestManager_Mount_WiresHooks(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	require.NoError(t, m.Mount(context.Background(), &hooksSkill{}, nil))
	require.Len(t, fa.hooks[hooks.RunStart], 1, "hook should be registered")
}

func TestManager_Mount_RegistersCommands_onlyWhenShell(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	// No shell: commands must be skipped.
	require.NoError(t, m.Mount(context.Background(), &cmdSkill{}, nil))
	assert.Empty(t, fa.cmds, "commands should be skipped when agent has no shell")

	// With shell present: the next skill's commands go through.
	fa2 := newFakeAgent()
	fa2.hasShell = true
	m2 := skills.NewManager(fa2)
	require.NoError(t, m2.Mount(context.Background(), &cmdSkill{}, nil))
	_, ok := fa2.cmds["hello"]
	assert.True(t, ok, "commands should be registered when shell is attached")
}

// ---------------------------------------------------------------------------
// Setup + context
// ---------------------------------------------------------------------------

func TestManager_Setup_CalledWithContext(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)
	s := &setupSkill{}

	cfg := map[string]any{"k": "v"}
	require.NoError(t, m.Mount(context.Background(), s, cfg))
	assert.Equal(t, 1, s.setupCalls)
	require.NotNil(t, s.sctx)
	assert.Equal(t, s, s.sctx.Skill)
	assert.Equal(t, cfg, s.sctx.Config)
	// Agent is passed opaquely — type-assert for equality.
	assert.Equal(t, any(fa), s.sctx.Agent)
}

func TestManager_Setup_ErrorAborts(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)
	boom := errors.New("setup failed")
	s := &setupSkill{setupErr: boom}

	err := m.Mount(context.Background(), s, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	// Should NOT be mounted.
	assert.Empty(t, m.Mounted())
	assert.Nil(t, m.Get("setup_skill"))
}

// ---------------------------------------------------------------------------
// Dependencies
// ---------------------------------------------------------------------------

func TestManager_Dependencies_ResolvedFirst(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	require.NoError(t, m.Mount(context.Background(), &depA{}, nil))
	// dep_b must be mounted BEFORE dep_a.
	assert.Equal(t, []string{"dep_b", "dep_a"}, m.Mounted())
}

func TestManager_Dependencies_CycleReturnsError(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	err := m.Mount(context.Background(), &cyclicA{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

// diamondRoot → diamondLeft, diamondRight ; both → diamondLeaf
type diamondLeaf struct{ skills.Base }

func (diamondLeaf) Name() string { return "d_leaf" }

type diamondLeft struct{ skills.Base }

func (diamondLeft) Name() string                  { return "d_left" }
func (diamondLeft) Dependencies() []skills.Skill { return []skills.Skill{&diamondLeaf{}} }

type diamondRight struct{ skills.Base }

func (diamondRight) Name() string                  { return "d_right" }
func (diamondRight) Dependencies() []skills.Skill { return []skills.Skill{&diamondLeaf{}} }

type diamondRoot struct{ skills.Base }

func (diamondRoot) Name() string { return "d_root" }
func (diamondRoot) Dependencies() []skills.Skill {
	return []skills.Skill{&diamondLeft{}, &diamondRight{}}
}

func TestManager_Dependencies_DiamondResolvedOnce(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	require.NoError(t, m.Mount(context.Background(), &diamondRoot{}, nil))
	names := m.Mounted()
	// d_leaf must appear exactly once.
	count := 0
	for _, n := range names {
		if n == "d_leaf" {
			count++
		}
	}
	assert.Equal(t, 1, count, "diamond leaf should mount once: %v", names)
	// And must appear before d_left, d_right, d_root.
	idx := func(n string) int {
		for i, x := range names {
			if x == n {
				return i
			}
		}
		return -1
	}
	assert.Less(t, idx("d_leaf"), idx("d_left"))
	assert.Less(t, idx("d_leaf"), idx("d_right"))
	assert.Less(t, idx("d_left"), idx("d_root"))
	assert.Less(t, idx("d_right"), idx("d_root"))
}

// ---------------------------------------------------------------------------
// Unmount + Shutdown
// ---------------------------------------------------------------------------

func TestManager_Unmount_Nonexistent_NoError(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)
	require.NoError(t, m.Unmount(context.Background(), "nope"))
}

func TestManager_Unmount_CallsTeardown(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)
	s := &setupSkill{}

	require.NoError(t, m.Mount(context.Background(), s, nil))
	require.Equal(t, 0, s.teardownCalls)
	require.NoError(t, m.Unmount(context.Background(), "setup_skill"))
	assert.Equal(t, 1, s.teardownCalls)
	assert.Empty(t, m.Mounted())
}

func TestManager_Unmount_UnregistersTools(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	require.NoError(t, m.Mount(context.Background(), &toolsSkill{}, nil))
	require.Contains(t, fa.tools, "tools_skill_do")

	require.NoError(t, m.Unmount(context.Background(), "tools"))
	_, ok := fa.tools["tools_skill_do"]
	assert.False(t, ok, "tool should be removed on unmount")
	assert.Contains(t, fa.unregistered, "tools_skill_do")
}

func TestManager_Unmount_UnregistersShellCommands(t *testing.T) {
	fa := newFakeAgent()
	fa.hasShell = true
	m := skills.NewManager(fa)

	require.NoError(t, m.Mount(context.Background(), &cmdSkill{}, nil))
	require.Contains(t, fa.cmds, "hello")

	require.NoError(t, m.Unmount(context.Background(), "cmd"))
	_, ok := fa.cmds["hello"]
	assert.False(t, ok)
	assert.Contains(t, fa.cmdDelete, "hello")
}

func TestManager_Shutdown_ReverseOrder(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	s1 := &setupSkill{}
	require.NoError(t, m.Mount(context.Background(), s1, nil))

	require.NoError(t, m.Mount(context.Background(), &bareSkill{}, nil))

	require.Equal(t, []string{"setup_skill", "bare"}, m.Mounted())

	require.NoError(t, m.Shutdown(context.Background()))
	assert.Empty(t, m.Mounted())
	assert.Equal(t, 1, s1.teardownCalls, "teardown should fire for setup_skill")
}

// ---------------------------------------------------------------------------
// Hook emission
// ---------------------------------------------------------------------------

func TestManager_Mount_EmitsSetupAndMountHooks(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	require.NoError(t, m.Mount(context.Background(), &bareSkill{}, nil))
	events := fa.emittedEvents()
	assert.Contains(t, events, hooks.SkillSetup)
	assert.Contains(t, events, hooks.SkillMount)
}

func TestManager_Unmount_EmitsTeardownAndUnmount(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)

	require.NoError(t, m.Mount(context.Background(), &bareSkill{}, nil))
	fa.mu.Lock()
	fa.emitted = nil
	fa.mu.Unlock()

	require.NoError(t, m.Unmount(context.Background(), "bare"))
	events := fa.emittedEvents()
	assert.Contains(t, events, hooks.SkillTeardown)
	assert.Contains(t, events, hooks.SkillUnmount)
}
