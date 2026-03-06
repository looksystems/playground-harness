"""
skills.py — Skill system for agent_harness.py

A Skill is a composable unit that bundles:
  - Tools (functions the LLM can call)
  - System prompt fragments (instructions injected into the prompt)
  - Middleware (pre/post message transforms)
  - Hooks (lifecycle event listeners)
  - Lifecycle methods (setup/teardown for resources)

Skills can declare dependencies on other skills, and the agent resolves
the full dependency graph when mounting.

Usage:
    from agent_harness import Agent, tool, HookEvent
    from skills import Skill, SkillContext

    class WebBrowsingSkill(Skill):
        name = "web_browsing"
        description = "Browse and extract content from web pages"
        instructions = "You can browse the web using the fetch_page tool."

        async def setup(self, ctx: SkillContext) -> None:
            # Acquire resources — called once when skill is mounted
            ctx.state["session"] = aiohttp.ClientSession()

        async def teardown(self, ctx: SkillContext) -> None:
            await ctx.state["session"].close()

        def tools(self) -> list:
            @tool(description="Fetch a web page")
            async def fetch_page(url: str) -> str:
                async with self.context.state["session"].get(url) as r:
                    return await r.text()
            return [fetch_page]

    agent = Agent(model="anthropic/claude-sonnet-4-20250514")
    await agent.mount(WebBrowsingSkill())
    result = await agent.run("Summarize https://example.com")
    await agent.shutdown()  # calls teardown on all skills
"""

from __future__ import annotations

import asyncio
import inspect
import logging
from abc import ABC
from dataclasses import dataclass, field
from typing import Any, Callable, Sequence

# Import from the harness (assumed to be in the same package / directory)
from agent_harness import (
    Agent,
    BaseMiddleware,
    HookEvent,
    RunContext,
    ToolDef,
    tool,
    _call_fn,
)

log = logging.getLogger("agent_harness.skills")

# ---------------------------------------------------------------------------
# Skill context — per-skill state bag
# ---------------------------------------------------------------------------

@dataclass
class SkillContext:
    """
    Per-skill state, created when a skill is mounted.
    Skills store connections, caches, config, etc. here.
    """
    skill: Skill
    agent: Agent
    config: dict[str, Any] = field(default_factory=dict)
    state: dict[str, Any] = field(default_factory=dict)


# ---------------------------------------------------------------------------
# Skill base class
# ---------------------------------------------------------------------------

class Skill(ABC):
    """
    Base class for agent skills.

    Subclass and override any combination of:
      - name / description / version   (metadata)
      - instructions                   (injected into system prompt)
      - dependencies                   (other skill classes required)
      - tools()                        (return list of @tool-decorated fns or ToolDefs)
      - middleware()                    (return list of Middleware instances)
      - hooks()                        (return dict of HookEvent -> callbacks)
      - setup(ctx) / teardown(ctx)     (async lifecycle)
    """

    # -- Metadata (override in subclasses) ----------------------------------

    name: str = ""
    description: str = ""
    version: str = "0.1.0"
    instructions: str = ""

    # Skill classes this skill depends on (resolved transitively)
    dependencies: Sequence[type[Skill]] = ()

    # Set by the agent when mounted
    context: SkillContext | None = None

    # -- Lifecycle ----------------------------------------------------------

    async def setup(self, ctx: SkillContext) -> None:
        """Called once when the skill is mounted. Acquire resources here."""
        pass

    async def teardown(self, ctx: SkillContext) -> None:
        """Called on agent shutdown. Release resources here."""
        pass

    # -- Composition --------------------------------------------------------

    def tools(self) -> list[Callable | ToolDef]:
        """Return tools this skill provides. Override in subclasses."""
        return []

    def middleware(self) -> list[BaseMiddleware]:
        """Return middleware this skill provides. Override in subclasses."""
        return []

    def hooks(self) -> dict[HookEvent, list[Callable]]:
        """Return hooks this skill provides. Override in subclasses."""
        return {}

    # -- Internal -----------------------------------------------------------

    def __init_subclass__(cls, **kwargs: Any) -> None:
        super().__init_subclass__(**kwargs)
        if not cls.name:
            # Auto-derive name from class: WebBrowsingSkill -> web_browsing
            raw = cls.__name__.removesuffix("Skill")
            cls.name = "".join(
                f"_{c.lower()}" if c.isupper() else c for c in raw
            ).lstrip("_")


# ---------------------------------------------------------------------------
# Skill-aware prompt middleware
# ---------------------------------------------------------------------------

class SkillPromptMiddleware(BaseMiddleware):
    """
    Injects skill instructions into the system prompt.
    Mounted automatically when skills have instructions.
    """

    def __init__(self, skills: list[Skill]):
        self._skills = skills

    async def pre(self, messages: list[dict], context: RunContext) -> list[dict]:
        fragments = [
            f"## {s.name}\n{s.instructions}"
            for s in self._skills
            if s.instructions
        ]
        if not fragments:
            return messages

        block = "\n\n---\n**Available Skills:**\n\n" + "\n\n".join(fragments)

        # Append to existing system message or create one
        for m in messages:
            if m["role"] == "system":
                m["content"] += block
                return messages

        messages.insert(0, {"role": "system", "content": block.strip()})
        return messages


# ---------------------------------------------------------------------------
# Agent extensions — monkey-free approach via composition
# ---------------------------------------------------------------------------

class SkillManager:
    """
    Manages skill lifecycle for an Agent.

    Usage:
        manager = SkillManager(agent)
        await manager.mount(MySkill())
        await manager.mount(AnotherSkill(config={"key": "val"}))
        # ... use agent ...
        await manager.shutdown()

    Or use the convenience methods added to Agent:
        await agent.mount(MySkill())
        await agent.shutdown()
    """

    def __init__(self, agent: Agent):
        self.agent = agent
        self._skills: dict[str, Skill] = {}
        self._mounted_order: list[str] = []
        self._prompt_mw: SkillPromptMiddleware | None = None

    @property
    def skills(self) -> dict[str, Skill]:
        return dict(self._skills)

    async def mount(
        self,
        skill: Skill,
        config: dict[str, Any] | None = None,
    ) -> None:
        """
        Mount a skill onto the agent.
        Resolves dependencies, calls setup(), registers tools/middleware/hooks.
        """
        # Resolve dependency tree (depth-first, skip already mounted)
        to_mount = self._resolve_deps(skill)

        for s in to_mount:
            if s.name in self._skills:
                continue

            # Create context
            ctx = SkillContext(
                skill=s,
                agent=self.agent,
                config=config or {},
            )
            s.context = ctx

            # Lifecycle setup
            await s.setup(ctx)

            # Register tools
            for t in s.tools():
                self.agent.register_tool(t)

            # Register middleware
            for mw in s.middleware():
                self.agent.use(mw)

            # Register hooks
            for event, callbacks in s.hooks().items():
                for cb in callbacks:
                    self.agent.on(event, cb)

            self._skills[s.name] = s
            self._mounted_order.append(s.name)
            log.info("Mounted skill: %s (v%s)", s.name, s.version)

        # Rebuild the prompt injection middleware
        self._rebuild_prompt_middleware()

    def _resolve_deps(self, skill: Skill) -> list[Skill]:
        """Topological sort of skill dependencies."""
        visited: set[str] = set()
        order: list[Skill] = []

        def visit(s: Skill) -> None:
            if s.name in visited:
                return
            visited.add(s.name)
            for dep_cls in s.dependencies:
                dep_instance = dep_cls()
                visit(dep_instance)
            order.append(s)

        visit(skill)
        return order

    def _rebuild_prompt_middleware(self) -> None:
        """Replace the prompt injection middleware with updated skill list."""
        skills_with_instructions = [
            s for s in self._skills.values() if s.instructions
        ]
        if not skills_with_instructions:
            return

        # Remove old middleware if present
        if self._prompt_mw is not None:
            try:
                self.agent._middleware.remove(self._prompt_mw)
            except ValueError:
                pass

        self._prompt_mw = SkillPromptMiddleware(skills_with_instructions)
        # Insert at the beginning so other middleware sees the full prompt
        self.agent._middleware.insert(0, self._prompt_mw)

    async def unmount(self, skill_name: str) -> None:
        """Teardown and remove a skill."""
        skill = self._skills.get(skill_name)
        if skill is None:
            raise ValueError(f"Skill not mounted: {skill_name}")

        if skill.context:
            await skill.teardown(skill.context)

        # Note: tools/middleware/hooks are not automatically deregistered
        # from the agent (would require agent support for removal).
        # Teardown handles resource cleanup.
        del self._skills[skill_name]
        self._mounted_order.remove(skill_name)
        self._rebuild_prompt_middleware()
        log.info("Unmounted skill: %s", skill_name)

    async def shutdown(self) -> None:
        """Teardown all skills in reverse mount order."""
        for name in reversed(self._mounted_order):
            skill = self._skills.get(name)
            if skill and skill.context:
                try:
                    await skill.teardown(skill.context)
                    log.info("Torn down skill: %s", name)
                except Exception as e:
                    log.warning("Teardown error for %s: %s", name, e)
        self._skills.clear()
        self._mounted_order.clear()


# ---------------------------------------------------------------------------
# Convenience: extend Agent with mount/shutdown
# ---------------------------------------------------------------------------

def _patch_agent() -> None:
    """Add mount() and shutdown() methods to Agent for ergonomic usage."""

    def _get_skill_manager(self: Agent) -> SkillManager:
        if not hasattr(self, "_skill_manager"):
            self._skill_manager = SkillManager(self)
        return self._skill_manager

    async def mount(
        self: Agent,
        skill: Skill,
        config: dict[str, Any] | None = None,
    ) -> None:
        """Mount a skill onto this agent."""
        mgr = _get_skill_manager(self)
        await mgr.mount(skill, config)

    async def unmount(self: Agent, skill_name: str) -> None:
        """Unmount a skill from this agent."""
        mgr = _get_skill_manager(self)
        await mgr.unmount(skill_name)

    async def shutdown(self: Agent) -> None:
        """Teardown all mounted skills."""
        mgr = _get_skill_manager(self)
        await mgr.shutdown()

    @property
    def skills(self: Agent) -> dict[str, Skill]:
        return _get_skill_manager(self).skills

    Agent.mount = mount
    Agent.unmount = unmount
    Agent.shutdown = shutdown
    Agent.skills = skills


_patch_agent()


# ===========================================================================
# Example skills
# ===========================================================================

class MathSkill(Skill):
    """Basic arithmetic operations."""

    name = "math"
    description = "Perform arithmetic operations"
    instructions = (
        "You have access to math tools for precise calculations. "
        "Always use these tools instead of doing math in your head."
    )

    def tools(self) -> list:
        @tool(description="Add two numbers")
        async def add(a: float, b: float) -> float:
            return a + b

        @tool(description="Subtract b from a")
        async def subtract(a: float, b: float) -> float:
            return a - b

        @tool(description="Multiply two numbers")
        async def multiply(a: float, b: float) -> float:
            return a * b

        @tool(description="Divide a by b")
        async def divide(a: float, b: float) -> float:
            if b == 0:
                raise ValueError("Division by zero")
            return a / b

        return [add, subtract, multiply, divide]


class MemorySkill(Skill):
    """Simple key-value memory for the agent."""

    name = "memory"
    description = "Remember and recall information across turns"
    instructions = (
        "You can store and retrieve facts using the memory tools. "
        "Use remember() to save important information and recall() to retrieve it."
    )

    async def setup(self, ctx: SkillContext) -> None:
        ctx.state["store"] = {}

    def tools(self) -> list:
        store = self.context.state["store"]

        @tool(description="Store a value with a key")
        async def remember(key: str, value: str) -> str:
            store[key] = value
            return f"Stored '{key}'"

        @tool(description="Retrieve a value by key")
        async def recall(key: str) -> str:
            return store.get(key, f"No memory found for '{key}'")

        @tool(description="List all stored memory keys")
        async def list_memories() -> list:
            return list(store.keys())

        return [remember, recall, list_memories]


class GuardrailSkill(Skill):
    """
    Example skill that adds safety middleware without any tools.
    Demonstrates that skills aren't just tool bundles.
    """

    name = "guardrails"
    description = "Content safety guardrails"

    class _ContentFilter(BaseMiddleware):
        def __init__(self, blocked_patterns: list[str]):
            self.blocked = blocked_patterns

        async def post(self, message: dict, context: RunContext) -> dict:
            content = message.get("content", "") or ""
            for pattern in self.blocked:
                if pattern.lower() in content.lower():
                    log.warning("Guardrail triggered: blocked pattern '%s'", pattern)
                    message["content"] = (
                        "I'm unable to provide that information. "
                        "Please rephrase your request."
                    )
                    break
            return message

    def __init__(self, blocked_patterns: list[str] | None = None):
        self._blocked = blocked_patterns or []

    def middleware(self) -> list:
        return [self._ContentFilter(self._blocked)]

    def hooks(self) -> dict:
        return {
            HookEvent.RUN_START: [
                lambda msgs: log.info("Guardrail active for %d messages", len(msgs))
            ],
        }


class WebBrowsingSkill(Skill):
    """
    Skill with a dependency and external resource lifecycle.
    Depends on nothing here, but shows the pattern.
    """

    name = "web_browsing"
    description = "Fetch and extract content from web pages"
    instructions = (
        "You can fetch web pages using the fetch_page tool. "
        "Use this to answer questions about URLs the user provides."
    )

    async def setup(self, ctx: SkillContext) -> None:
        import aiohttp
        ctx.state["session"] = aiohttp.ClientSession()
        log.info("Web browsing session created")

    async def teardown(self, ctx: SkillContext) -> None:
        session = ctx.state.get("session")
        if session:
            await session.close()
            log.info("Web browsing session closed")

    def tools(self) -> list:
        @tool(description="Fetch the text content of a web page")
        async def fetch_page(url: str) -> str:
            session = self.context.state["session"]
            async with session.get(url) as resp:
                text = await resp.text()
                # Truncate to avoid blowing context
                return text[:4000]

        return [fetch_page]


# ===========================================================================
# Demo
# ===========================================================================

if __name__ == "__main__":
    import os

    logging.basicConfig(level=logging.DEBUG)

    async def main() -> None:
        agent = Agent(
            model=os.getenv("AGENT_MODEL", "anthropic/claude-sonnet-4-20250514"),
            system="You are a helpful assistant.",
            max_turns=10,
            temperature=0.3,
        )

        # Mount skills — dependencies resolved automatically
        await agent.mount(MathSkill())
        await agent.mount(MemorySkill())
        await agent.mount(GuardrailSkill(blocked_patterns=["social security"]))

        # Show what's mounted
        print("Mounted skills:", list(agent.skills.keys()))

        # Run
        result = await agent.run(
            "Remember that my favorite number is 42, then compute 42 * 17."
        )
        print("\n---\n", result)

        # Cleanup
        await agent.shutdown()

    asyncio.run(main())
