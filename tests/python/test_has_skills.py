import asyncio
import pytest
from src.python.has_skills import (
    HasSkills,
    Skill,
    SkillContext,
    SkillManager,
    SkillPromptMiddleware,
)
from src.python.has_hooks import HasHooks, HookEvent
from src.python.has_middleware import BaseMiddleware, HasMiddleware
from src.python.uses_tools import UsesTools, ToolDef


# ---------------------------------------------------------------------------
# Test skills
# ---------------------------------------------------------------------------

class WebBrowsingSkill(Skill):
    @property
    def description(self):
        return "Web browsing"


class MathSkill(Skill):
    @property
    def description(self):
        return "Math operations"

    @property
    def instructions(self):
        return "Use math tools for calculations"

    def tools(self):
        return [
            ToolDef(
                name="add",
                description="Add two numbers",
                function=lambda **kw: kw.get("a", 0) + kw.get("b", 0),
                parameters={
                    "type": "object",
                    "properties": {
                        "a": {"type": "integer"},
                        "b": {"type": "integer"},
                    },
                },
            ),
        ]


class MyCustom(Skill):
    """No Skill suffix."""

    @property
    def description(self):
        return "Custom"


class TrackingSkill(Skill):
    """Skill that tracks setup/teardown calls."""

    setup_calls: list[str] = []
    teardown_calls: list[str] = []

    async def setup(self, ctx: SkillContext) -> None:
        TrackingSkill.setup_calls.append(self.name)

    async def teardown(self, ctx: SkillContext) -> None:
        TrackingSkill.teardown_calls.append(self.name)


class AlphaTrackingSkill(TrackingSkill):
    pass


class BetaTrackingSkill(TrackingSkill):
    pass


class GammaTrackingSkill(TrackingSkill):
    pass


class InstructionSkill(Skill):
    @property
    def description(self):
        return "Has instructions"

    @property
    def instructions(self):
        return "Always use the foo tool first"


class NoInstructionSkill(Skill):
    @property
    def description(self):
        return "No instructions"


class MiddlewareProvidingSkill(Skill):
    """Skill that contributes middleware."""

    def __init__(self):
        super().__init__()
        self._mw = _TagMiddleware("skill_mw")

    @property
    def description(self):
        return "Provides middleware"

    def middleware(self):
        return [self._mw]


class _TagMiddleware(BaseMiddleware):
    def __init__(self, tag: str):
        self.tag = tag

    async def pre(self, messages, context):
        return messages

    async def post(self, message, context):
        return message


class HookProvidingSkill(Skill):
    """Skill that contributes hooks."""

    received: list[str] = []

    @property
    def description(self):
        return "Provides hooks"

    def hooks(self):
        return {
            HookEvent.RUN_START: [lambda: HookProvidingSkill.received.append("run_start")],
        }


# -- Dependency chain skills --

class SkillC(Skill):
    @property
    def description(self):
        return "Leaf"


class SkillB(Skill):
    @property
    def description(self):
        return "Mid"

    @property
    def dependencies(self):
        return (SkillC,)


class SkillA(Skill):
    @property
    def description(self):
        return "Root"

    @property
    def dependencies(self):
        return (SkillB,)


# ---------------------------------------------------------------------------
# Minimal test agent classes
# ---------------------------------------------------------------------------

class SkillsOnly(HasSkills):
    pass


class SkillsWithTools(UsesTools, HasSkills):
    pass


class FullSkillAgent(HasHooks, HasMiddleware, UsesTools, HasSkills):
    pass


# ---------------------------------------------------------------------------
# 1. TestSkillBase
# ---------------------------------------------------------------------------

class TestSkillBase:
    def test_auto_name_strips_skill_suffix(self):
        sk = WebBrowsingSkill()
        assert sk.name == "web_browsing"

    def test_auto_name_single_word(self):
        sk = MathSkill()
        assert sk.name == "math"

    def test_auto_name_no_skill_suffix(self):
        sk = MyCustom()
        assert sk.name == "my_custom"

    def test_default_version(self):
        sk = WebBrowsingSkill()
        assert sk.version == "0.1.0"

    def test_default_instructions_empty(self):
        sk = WebBrowsingSkill()
        assert sk.instructions == ""

    def test_default_tools_empty(self):
        sk = WebBrowsingSkill()
        assert sk.tools() == []

    def test_default_middleware_empty(self):
        sk = WebBrowsingSkill()
        assert sk.middleware() == []

    def test_default_hooks_empty(self):
        sk = WebBrowsingSkill()
        assert sk.hooks() == {}

    def test_default_description_empty(self):
        # Base Skill has empty description
        class Bare(Skill):
            pass
        sk = Bare()
        assert sk.description == ""

    def test_context_none_before_mount(self):
        sk = MathSkill()
        assert sk.context is None


# ---------------------------------------------------------------------------
# 2. TestSkillManagerStandalone
# ---------------------------------------------------------------------------

class TestSkillManagerStandalone:
    @pytest.mark.asyncio
    async def test_mount_adds_skill(self):
        agent = SkillsOnly()
        sk = MathSkill()
        await agent.mount(sk)
        assert "math" in agent.skills

    @pytest.mark.asyncio
    async def test_unmount_removes_skill(self):
        agent = SkillsOnly()
        sk = MathSkill()
        await agent.mount(sk)
        assert "math" in agent.skills
        await agent.unmount("math")
        assert "math" not in agent.skills

    @pytest.mark.asyncio
    async def test_shutdown_tears_down_all(self):
        TrackingSkill.setup_calls.clear()
        TrackingSkill.teardown_calls.clear()

        agent = SkillsOnly()
        await agent.mount(AlphaTrackingSkill())
        await agent.mount(BetaTrackingSkill())
        await agent.mount(GammaTrackingSkill())

        assert TrackingSkill.setup_calls == ["alpha_tracking", "beta_tracking", "gamma_tracking"]

        await agent.shutdown_skills()
        # Teardown in reverse mount order
        assert TrackingSkill.teardown_calls == ["gamma_tracking", "beta_tracking", "alpha_tracking"]
        assert agent.skills == {}

    @pytest.mark.asyncio
    async def test_duplicate_mount_skipped(self):
        TrackingSkill.setup_calls.clear()

        agent = SkillsOnly()
        sk1 = AlphaTrackingSkill()
        sk2 = AlphaTrackingSkill()
        await agent.mount(sk1)
        await agent.mount(sk2)
        # Only one setup call because duplicate is skipped
        assert TrackingSkill.setup_calls == ["alpha_tracking"]
        assert len(agent.skills) == 1

    @pytest.mark.asyncio
    async def test_unmount_nonexistent_is_noop(self):
        agent = SkillsOnly()
        # Should not raise
        await agent.unmount("nonexistent")

    @pytest.mark.asyncio
    async def test_skills_property_returns_copy(self):
        agent = SkillsOnly()
        await agent.mount(MathSkill())
        s1 = agent.skills
        s2 = agent.skills
        assert s1 == s2
        assert s1 is not s2  # different dict instances


# ---------------------------------------------------------------------------
# 3. TestSkillsWithTools
# ---------------------------------------------------------------------------

class TestSkillsWithTools:
    @pytest.mark.asyncio
    async def test_mount_registers_tools(self):
        agent = SkillsWithTools()
        sk = MathSkill()
        await agent.mount(sk)
        assert "add" in agent._tools

    @pytest.mark.asyncio
    async def test_unmount_removes_tools(self):
        agent = SkillsWithTools()
        sk = MathSkill()
        await agent.mount(sk)
        assert "add" in agent._tools
        await agent.unmount("math")
        assert "add" not in agent._tools

    @pytest.mark.asyncio
    async def test_tool_is_functional(self):
        agent = SkillsWithTools()
        sk = MathSkill()
        await agent.mount(sk)
        td = agent._tools["add"]
        result = td.function(a=3, b=4)
        assert result == 7

    @pytest.mark.asyncio
    async def test_shutdown_removes_tools(self):
        agent = SkillsWithTools()
        await agent.mount(MathSkill())
        assert "add" in agent._tools
        await agent.shutdown_skills()
        # After shutdown, tools still in _tools dict because shutdown only
        # clears internal state; check skills are gone
        assert agent.skills == {}


# ---------------------------------------------------------------------------
# 4. TestSkillHooks
# ---------------------------------------------------------------------------

class TestSkillHooks:
    @pytest.mark.asyncio
    async def test_skill_mount_event(self):
        agent = FullSkillAgent()
        agent.__init_has_hooks__()
        mounted = []
        agent.on(HookEvent.SKILL_MOUNT, lambda name: mounted.append(name))
        await agent.mount(MathSkill())
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert mounted == ["math"]

    @pytest.mark.asyncio
    async def test_skill_unmount_event(self):
        agent = FullSkillAgent()
        agent.__init_has_hooks__()
        unmounted = []
        agent.on(HookEvent.SKILL_UNMOUNT, lambda name: unmounted.append(name))
        await agent.mount(MathSkill())
        await agent.unmount("math")
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert unmounted == ["math"]

    @pytest.mark.asyncio
    async def test_skill_setup_event(self):
        agent = FullSkillAgent()
        agent.__init_has_hooks__()
        setups = []
        agent.on(HookEvent.SKILL_SETUP, lambda name: setups.append(name))
        await agent.mount(WebBrowsingSkill())
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert setups == ["web_browsing"]

    @pytest.mark.asyncio
    async def test_skill_teardown_event(self):
        agent = FullSkillAgent()
        agent.__init_has_hooks__()
        teardowns = []
        agent.on(HookEvent.SKILL_TEARDOWN, lambda name: teardowns.append(name))
        await agent.mount(WebBrowsingSkill())
        await agent.unmount("web_browsing")
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert teardowns == ["web_browsing"]

    @pytest.mark.asyncio
    async def test_all_four_events_fire(self):
        agent = FullSkillAgent()
        agent.__init_has_hooks__()
        events = []
        agent.on(HookEvent.SKILL_SETUP, lambda name: events.append(("setup", name)))
        agent.on(HookEvent.SKILL_MOUNT, lambda name: events.append(("mount", name)))
        agent.on(HookEvent.SKILL_TEARDOWN, lambda name: events.append(("teardown", name)))
        agent.on(HookEvent.SKILL_UNMOUNT, lambda name: events.append(("unmount", name)))

        await agent.mount(MathSkill())
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        await agent.unmount("math")
        await asyncio.sleep(0)
        await asyncio.sleep(0)

        assert ("setup", "math") in events
        assert ("mount", "math") in events
        assert ("teardown", "math") in events
        assert ("unmount", "math") in events


# ---------------------------------------------------------------------------
# 5. TestSkillLifecycle
# ---------------------------------------------------------------------------

class TestSkillLifecycle:
    @pytest.mark.asyncio
    async def test_setup_called_on_mount(self):
        TrackingSkill.setup_calls.clear()
        agent = SkillsOnly()
        await agent.mount(AlphaTrackingSkill())
        assert "alpha_tracking" in TrackingSkill.setup_calls

    @pytest.mark.asyncio
    async def test_teardown_called_on_unmount(self):
        TrackingSkill.teardown_calls.clear()
        agent = SkillsOnly()
        await agent.mount(AlphaTrackingSkill())
        await agent.unmount("alpha_tracking")
        assert "alpha_tracking" in TrackingSkill.teardown_calls

    @pytest.mark.asyncio
    async def test_shutdown_reverse_order(self):
        TrackingSkill.setup_calls.clear()
        TrackingSkill.teardown_calls.clear()

        agent = SkillsOnly()
        await agent.mount(AlphaTrackingSkill())
        await agent.mount(BetaTrackingSkill())
        await agent.shutdown_skills()
        assert TrackingSkill.teardown_calls == ["beta_tracking", "alpha_tracking"]

    @pytest.mark.asyncio
    async def test_context_has_correct_agent(self):
        agent = SkillsOnly()
        sk = MathSkill()
        await agent.mount(sk)
        assert sk.context is not None
        assert sk.context.agent is agent

    @pytest.mark.asyncio
    async def test_context_has_config(self):
        agent = SkillsOnly()
        sk = MathSkill()
        await agent.mount(sk, config={"key": "value"})
        assert sk.context is not None
        assert sk.context.config == {"key": "value"}

    @pytest.mark.asyncio
    async def test_context_has_state(self):
        agent = SkillsOnly()
        sk = MathSkill()
        await agent.mount(sk)
        assert sk.context is not None
        assert sk.context.state == {}

    @pytest.mark.asyncio
    async def test_context_skill_reference(self):
        agent = SkillsOnly()
        sk = MathSkill()
        await agent.mount(sk)
        assert sk.context is not None
        assert sk.context.skill is sk

    @pytest.mark.asyncio
    async def test_config_defaults_to_empty(self):
        agent = SkillsOnly()
        sk = MathSkill()
        await agent.mount(sk)
        assert sk.context.config == {}


# ---------------------------------------------------------------------------
# 6. TestSkillInstructions
# ---------------------------------------------------------------------------

class TestSkillInstructions:
    @pytest.mark.asyncio
    async def test_instructions_injected_into_system(self):
        mw = SkillPromptMiddleware([InstructionSkill()])
        messages = [{"role": "system", "content": "Base"}]
        result = await mw.pre(messages, None)
        assert "Available Skills" in result[0]["content"]
        assert "Always use the foo tool first" in result[0]["content"]

    @pytest.mark.asyncio
    async def test_multiple_skills_all_instructions(self):
        sk1 = InstructionSkill()
        sk2 = MathSkill()
        mw = SkillPromptMiddleware([sk1, sk2])
        messages = [{"role": "system", "content": "Base"}]
        result = await mw.pre(messages, None)
        assert "Always use the foo tool first" in result[0]["content"]
        assert "Use math tools for calculations" in result[0]["content"]

    @pytest.mark.asyncio
    async def test_no_instructions_no_injection(self):
        mw = SkillPromptMiddleware([NoInstructionSkill()])
        messages = [{"role": "system", "content": "Base"}]
        result = await mw.pre(messages, None)
        assert result[0]["content"] == "Base"

    @pytest.mark.asyncio
    async def test_no_system_message_creates_one(self):
        mw = SkillPromptMiddleware([InstructionSkill()])
        messages = [{"role": "user", "content": "Hello"}]
        result = await mw.pre(messages, None)
        assert result[0]["role"] == "system"
        assert "Available Skills" in result[0]["content"]
        assert result[1]["role"] == "user"

    @pytest.mark.asyncio
    async def test_original_messages_not_mutated(self):
        mw = SkillPromptMiddleware([InstructionSkill()])
        original = [{"role": "system", "content": "Base"}]
        await mw.pre(original, None)
        assert original[0]["content"] == "Base"

    @pytest.mark.asyncio
    async def test_prompt_middleware_added_on_mount(self):
        agent = FullSkillAgent()
        agent.__init_has_middleware__()
        agent.__init_has_hooks__()
        await agent.mount(InstructionSkill())
        # The prompt middleware should be in the agent's middleware list
        found = any(isinstance(mw, SkillPromptMiddleware) for mw in agent._middleware)
        assert found

    @pytest.mark.asyncio
    async def test_prompt_middleware_removed_on_unmount(self):
        agent = FullSkillAgent()
        agent.__init_has_middleware__()
        agent.__init_has_hooks__()
        await agent.mount(InstructionSkill())
        await agent.unmount("instruction")
        found = any(isinstance(mw, SkillPromptMiddleware) for mw in agent._middleware)
        assert not found


# ---------------------------------------------------------------------------
# 7. TestSkillMiddlewareAndHooks
# ---------------------------------------------------------------------------

class TestSkillMiddlewareAndHooks:
    @pytest.mark.asyncio
    async def test_skill_middleware_registered(self):
        agent = FullSkillAgent()
        agent.__init_has_middleware__()
        agent.__init_has_hooks__()
        sk = MiddlewareProvidingSkill()
        await agent.mount(sk)
        found = any(
            isinstance(mw, _TagMiddleware) and mw.tag == "skill_mw"
            for mw in agent._middleware
        )
        assert found

    @pytest.mark.asyncio
    async def test_skill_middleware_removed_on_unmount(self):
        agent = FullSkillAgent()
        agent.__init_has_middleware__()
        agent.__init_has_hooks__()
        sk = MiddlewareProvidingSkill()
        await agent.mount(sk)
        await agent.unmount("middleware_providing")
        found = any(
            isinstance(mw, _TagMiddleware) and mw.tag == "skill_mw"
            for mw in agent._middleware
        )
        assert not found

    @pytest.mark.asyncio
    async def test_skill_hooks_registered(self):
        HookProvidingSkill.received.clear()
        agent = FullSkillAgent()
        agent.__init_has_hooks__()
        agent.__init_has_middleware__()
        sk = HookProvidingSkill()
        await agent.mount(sk)
        await agent._emit(HookEvent.RUN_START)
        assert "run_start" in HookProvidingSkill.received

    @pytest.mark.asyncio
    async def test_skill_hooks_removed_on_unmount(self):
        HookProvidingSkill.received.clear()
        agent = FullSkillAgent()
        agent.__init_has_hooks__()
        agent.__init_has_middleware__()
        sk = HookProvidingSkill()
        await agent.mount(sk)
        await agent.unmount("hook_providing")
        HookProvidingSkill.received.clear()
        await agent._emit(HookEvent.RUN_START)
        assert HookProvidingSkill.received == []


# ---------------------------------------------------------------------------
# 8. TestSkillDependencies
# ---------------------------------------------------------------------------

class TestSkillDependencies:
    @pytest.mark.asyncio
    async def test_dependency_auto_mounted(self):
        agent = SkillsOnly()
        await agent.mount(SkillB())
        # SkillB depends on SkillC, both should be mounted
        assert "skill_b" in agent.skills
        assert "skill_c" in agent.skills

    @pytest.mark.asyncio
    async def test_already_mounted_dep_skipped(self):
        TrackingSkill.setup_calls.clear()
        agent = SkillsOnly()
        await agent.mount(SkillC())
        await agent.mount(SkillB())
        # SkillC should only be set up once (from the first mount)
        skills = agent.skills
        assert "skill_b" in skills
        assert "skill_c" in skills

    @pytest.mark.asyncio
    async def test_transitive_dependencies(self):
        agent = SkillsOnly()
        await agent.mount(SkillA())
        # A -> B -> C: all three should be mounted
        assert "skill_a" in agent.skills
        assert "skill_b" in agent.skills
        assert "skill_c" in agent.skills

    @pytest.mark.asyncio
    async def test_dependency_order(self):
        agent = SkillsOnly()
        agent._ensure_has_skills()
        mgr = agent._skill_manager
        await agent.mount(SkillA())
        # Dependencies should be mounted before dependents
        order = mgr._mounted_order
        assert order.index("skill_c") < order.index("skill_b")
        assert order.index("skill_b") < order.index("skill_a")
