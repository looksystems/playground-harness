from __future__ import annotations

import asyncio
import inspect
import logging
import re
from abc import ABC
from dataclasses import dataclass, field
from typing import Any, Callable, Self, Sequence, TypeVar

from src.python._utils import call_fn, emit_fire_and_forget
from src.python.has_hooks import HookEvent
from src.python.has_middleware import BaseMiddleware
from src.python.uses_tools import ToolDef, _build_param_schema

logger = logging.getLogger(__name__)

F = TypeVar("F", bound=Callable)


# ---------------------------------------------------------------------------
# SkillContext
# ---------------------------------------------------------------------------

@dataclass
class SkillContext:
    """Runtime context passed to a skill during setup/teardown."""

    skill: Skill
    agent: Any
    config: dict[str, Any] = field(default_factory=dict)
    state: dict[str, Any] = field(default_factory=dict)


# ---------------------------------------------------------------------------
# Skill ABC
# ---------------------------------------------------------------------------

class Skill(ABC):
    """Base class for all skills.

    Subclasses automatically derive a snake_case ``name`` from the class name
    (stripping a trailing ``Skill`` suffix).  Override properties to customise.
    """

    _auto_name: str = ""

    def __init_subclass__(cls, **kwargs: Any) -> None:
        super().__init_subclass__(**kwargs)
        # WebBrowsingSkill -> web_browsing
        raw = cls.__name__
        if raw.endswith("Skill"):
            raw = raw[: -len("Skill")]
        # CamelCase -> snake_case
        cls._auto_name = re.sub(r"(?<=[a-z0-9])([A-Z])", r"_\1", raw).lower()

    # -- identity ----------------------------------------------------------

    @property
    def name(self) -> str:
        return self._auto_name

    @property
    def description(self) -> str:
        return ""

    @property
    def version(self) -> str:
        return "0.1.0"

    @property
    def instructions(self) -> str:
        return ""

    @property
    def progressive(self) -> bool:
        """When True, the skill is lazily loaded.

        A progressive skill is *registered* at mount time (only its name +
        description enter the system prompt) and *activated* on demand via the
        ``load_skill`` tool, at which point its instructions, tools, middleware,
        hooks and commands are wired up. Defaults to False (eager).
        """
        return False

    @property
    def dependencies(self) -> Sequence[type[Skill]]:
        return ()

    # -- runtime context ---------------------------------------------------

    @property
    def context(self) -> SkillContext | None:
        return getattr(self, "_context", None)

    # -- lifecycle ---------------------------------------------------------

    async def setup(self, ctx: SkillContext) -> None:  # noqa: B027
        """Called when the skill is mounted."""

    async def teardown(self, ctx: SkillContext) -> None:  # noqa: B027
        """Called when the skill is unmounted."""

    # -- contributions -----------------------------------------------------

    def tools(self) -> list[ToolDef]:
        """Return tool definitions this skill provides."""
        return []

    def middleware(self) -> list[BaseMiddleware]:
        """Return middleware instances this skill provides."""
        return []

    def hooks(self) -> dict[HookEvent, list[Callable]]:
        """Return hook callbacks this skill provides."""
        return {}

    def commands(self) -> dict[str, Callable]:
        """Return shell commands this skill provides. {name: handler}"""
        return {}


# ---------------------------------------------------------------------------
# SkillPromptMiddleware
# ---------------------------------------------------------------------------

class SkillPromptMiddleware(BaseMiddleware):
    """Injects skill discovery/instructions into the system prompt.

    Two groups are rendered:

    * **loaded** skills (eager + activated) render their full ``instructions``.
    * **pending** progressive skills render only ``## {name}\\n{description}``
      plus a hint that the model must call ``load_skill('{name}')`` to use them.
    """

    def __init__(self, skills: list[Skill], pending: list[Skill] | None = None) -> None:
        self._skills = skills
        self._pending = pending or []

    async def pre(self, messages: list[dict], context: Any) -> list[dict]:
        sections: list[str] = []
        for sk in self._skills:
            if sk.instructions:
                sections.append(f"## {sk.name}\n{sk.instructions}")
        for sk in self._pending:
            sections.append(
                f"## {sk.name}\n{sk.description}\n\n"
                f"_Not loaded — call `load_skill('{sk.name}')` to activate._"
            )

        if not sections:
            return messages

        block = "\n\n---\n**Available Skills:**\n\n" + "\n\n".join(sections)

        messages = [dict(m) for m in messages]  # shallow copy each dict

        # Append to existing system message if found, otherwise prepend one.
        for m in messages:
            if m.get("role") == "system":
                m["content"] = m.get("content", "") + block
                return messages

        messages.insert(0, {"role": "system", "content": block.lstrip("\n")})
        return messages


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# ---------------------------------------------------------------------------
# SkillManager
# ---------------------------------------------------------------------------

class SkillManager:
    """Manages skill lifecycle on behalf of an agent."""

    LOADER_TOOL_NAME = "load_skill"

    def __init__(self, agent: Any) -> None:
        self._agent = agent
        self._skills: dict[str, Skill] = {}
        self._mounted_order: list[str] = []
        self._prompt_mw: SkillPromptMiddleware | None = None

        # Progressive (deferred) skills awaiting activation: name -> skill,
        # plus the per-mount config captured at register time.
        self._pending: dict[str, Skill] = {}
        self._pending_config: dict[str, dict[str, Any] | None] = {}
        self._loader_registered = False

        # Track contributions per skill for clean unmount.
        self._skill_tools: dict[str, list[str]] = {}
        self._skill_middleware: dict[str, list[BaseMiddleware]] = {}
        self._skill_hooks: dict[str, list[tuple[HookEvent, Callable]]] = {}
        self._skill_commands: dict[str, list[str]] = {}

    # -- public API --------------------------------------------------------

    @property
    def skills(self) -> dict[str, Skill]:
        return dict(self._skills)

    @property
    def pending(self) -> dict[str, Skill]:
        """Progressive skills registered but not yet activated."""
        return dict(self._pending)

    async def mount(self, skill: Skill, config: dict[str, Any] | None = None) -> None:
        """Mount *skill* (and its dependencies) onto the agent.

        Progressive skills are *registered* (deferred) — only their discovery
        metadata is wired up and the ``load_skill`` loader tool is ensured.
        Dependencies of a progressive skill are resolved at activation time,
        not here. Eager skills mount exactly as before.
        """
        if skill.progressive:
            if skill.name in self._skills or skill.name in self._pending:
                return
            self._register_deferred(skill, config)
            return

        resolved = self._resolve_deps(skill)
        for sk in resolved:
            if sk.name in self._skills or sk.name in self._pending:
                continue
            cfg = config if sk is skill else None
            if sk.progressive:
                self._register_deferred(sk, cfg)
            else:
                await self._mount_single(sk, cfg)

    async def unmount(self, skill_name: str) -> None:
        """Unmount a previously-mounted skill by name."""
        sk = self._skills.get(skill_name)
        if sk is None:
            logger.warning("Skill %r not mounted; nothing to unmount.", skill_name)
            return

        ctx = sk.context
        if ctx is not None:
            await call_fn(sk.teardown, ctx)

        # Remove tools
        for tool_name in self._skill_tools.pop(skill_name, []):
            if hasattr(self._agent, "unregister_tool"):
                self._agent.unregister_tool(tool_name)

        # Remove middleware
        if hasattr(self._agent, "remove_middleware"):
            for mw in self._skill_middleware.pop(skill_name, []):
                self._agent.remove_middleware(mw)

        # Remove hooks
        if hasattr(self._agent, "remove_hook"):
            for event, cb in self._skill_hooks.pop(skill_name, []):
                self._agent.remove_hook(event, cb)

        # Remove commands
        for cmd_name in self._skill_commands.pop(skill_name, []):
            if hasattr(self._agent, "unregister_command"):
                self._agent.unregister_command(cmd_name)

        self._skills.pop(skill_name, None)
        try:
            self._mounted_order.remove(skill_name)
        except ValueError:
            pass

        self._rebuild_prompt_middleware()

    async def shutdown(self) -> None:
        """Teardown all skills in reverse mount order."""
        for name in reversed(list(self._mounted_order)):
            sk = self._skills.get(name)
            if sk is None:
                continue
            ctx = sk.context
            if ctx is not None:
                try:
                    await call_fn(sk.teardown, ctx)
                except Exception as exc:
                    logger.warning("Skill %r teardown error: %s", name, exc)
        self._skills.clear()
        self._mounted_order.clear()
        self._pending.clear()
        self._pending_config.clear()
        self._maybe_unregister_loader()
        self._skill_tools.clear()
        self._skill_middleware.clear()
        self._skill_hooks.clear()
        self._skill_commands.clear()
        if self._prompt_mw is not None and hasattr(self._agent, "remove_middleware"):
            self._agent.remove_middleware(self._prompt_mw)
            self._prompt_mw = None

    # -- internals ---------------------------------------------------------

    async def _mount_single(self, skill: Skill, config: dict[str, Any] | None) -> None:
        ctx = SkillContext(
            skill=skill,
            agent=self._agent,
            config=config or {},
        )
        skill._context = ctx  # type: ignore[attr-defined]

        await call_fn(skill.setup, ctx)

        # Register tools
        tool_names: list[str] = []
        for td in skill.tools():
            if hasattr(self._agent, "register_tool"):
                self._agent.register_tool(td)
                tool_names.append(td.name)
        self._skill_tools[skill.name] = tool_names

        # Register middleware
        mw_list = skill.middleware()
        if mw_list and hasattr(self._agent, "use"):
            for mw in mw_list:
                self._agent.use(mw)
        self._skill_middleware[skill.name] = list(mw_list)

        # Register hooks
        hook_pairs: list[tuple[HookEvent, Callable]] = []
        for event, cbs in skill.hooks().items():
            for cb in cbs:
                if hasattr(self._agent, "on"):
                    self._agent.on(event, cb)
                hook_pairs.append((event, cb))
        self._skill_hooks[skill.name] = hook_pairs

        # Register commands
        cmd_names: list[str] = []
        for cmd_name, handler in skill.commands().items():
            if hasattr(self._agent, "register_command"):
                self._agent.register_command(cmd_name, handler)
                cmd_names.append(cmd_name)
        self._skill_commands[skill.name] = cmd_names

        self._skills[skill.name] = skill
        self._mounted_order.append(skill.name)

        self._rebuild_prompt_middleware()

    # -- progressive (two-phase) -------------------------------------------

    def _register_deferred(self, skill: Skill, config: dict[str, Any] | None) -> None:
        """Phase 1: register a progressive skill without wiring contributions."""
        if not skill.description:
            raise ValueError(
                f"Progressive skill {skill.name!r} must have a non-empty description"
            )
        self._pending[skill.name] = skill
        self._pending_config[skill.name] = config
        self._ensure_loader_tool()
        self._rebuild_prompt_middleware()

    async def _activate(self, name: str) -> str:
        """Phase 2: activate a pending progressive skill, returning its body.

        Idempotent: activating an already-loaded skill simply returns its
        instructions. Resolves and mounts the skill's dependencies via the
        normal eager path, emits SKILL_SETUP/SKILL_MOUNT, and unregisters the
        loader tool once no progressive skill remains pending.
        """
        if name in self._skills:
            return self._skills[name].instructions

        skill = self._pending.get(name)
        if skill is None:
            raise ValueError(f"No pending progressive skill named {name!r}")

        for sk in self._resolve_deps(skill):
            if sk.name in self._skills:
                continue
            cfg = self._pending_config.get(sk.name)
            await self._mount_single(sk, cfg)
            self._pending.pop(sk.name, None)
            self._pending_config.pop(sk.name, None)
            emit_fire_and_forget(self._agent, HookEvent.SKILL_SETUP, sk.name)
            emit_fire_and_forget(self._agent, HookEvent.SKILL_MOUNT, sk.name)

        self._maybe_unregister_loader()
        return skill.instructions

    def _build_loader_tool(self) -> ToolDef:
        async def _load(name: str) -> str:
            return await self._activate(name)

        return ToolDef(
            name=self.LOADER_TOOL_NAME,
            description=(
                "Load (activate) a progressive skill by name. Activating a skill "
                "registers its tools and surfaces its full instructions. Pass the "
                "skill name exactly as shown in the Available Skills list."
            ),
            function=_load,
            parameters={
                "type": "object",
                "properties": {
                    "name": {
                        "type": "string",
                        "description": "Name of the progressive skill to activate.",
                    },
                },
                "required": ["name"],
            },
        )

    def _ensure_loader_tool(self) -> None:
        if self._loader_registered:
            return
        if not hasattr(self._agent, "register_tool"):
            return
        self._agent.register_tool(self._build_loader_tool())
        self._loader_registered = True

    def _maybe_unregister_loader(self) -> None:
        if self._pending:
            return
        if self._loader_registered and hasattr(self._agent, "unregister_tool"):
            self._agent.unregister_tool(self.LOADER_TOOL_NAME)
        self._loader_registered = False

    def _resolve_deps(self, skill: Skill) -> list[Skill]:
        """Topological sort of *skill* and its transitive dependencies.

        Dependency classes are instantiated automatically.  The returned list
        is ordered such that dependencies come before dependents.
        """
        resolved: list[Skill] = []
        seen: set[str] = set()

        def _visit(sk: Skill) -> None:
            if sk.name in seen:
                return
            seen.add(sk.name)
            for dep_cls in sk.dependencies:
                dep_instance = dep_cls()
                _visit(dep_instance)
            resolved.append(sk)

        _visit(skill)
        return resolved

    def _rebuild_prompt_middleware(self) -> None:
        """Replace the prompt middleware with one reflecting current skills."""
        if not hasattr(self._agent, "use"):
            return

        # Remove old
        if self._prompt_mw is not None and hasattr(self._agent, "remove_middleware"):
            self._agent.remove_middleware(self._prompt_mw)

        loaded = [self._skills[n] for n in self._mounted_order if n in self._skills]
        pending = list(self._pending.values())
        if loaded or pending:
            self._prompt_mw = SkillPromptMiddleware(loaded, pending)
            if hasattr(self._agent, "_prepend_middleware"):
                self._agent._prepend_middleware(self._prompt_mw)
            else:
                self._agent.use(self._prompt_mw)
        else:
            self._prompt_mw = None


# ---------------------------------------------------------------------------
# HasSkills mixin
# ---------------------------------------------------------------------------

class HasSkills:
    """Mixin that gives an agent the ability to mount and manage skills."""

    def _ensure_has_skills(self) -> None:
        if not hasattr(self, "_skill_manager"):
            self._skill_manager = SkillManager(self)

    @property
    def skills(self) -> dict[str, Skill]:
        self._ensure_has_skills()
        return self._skill_manager.skills

    async def mount(self, skill: Skill, config: dict[str, Any] | None = None) -> Self:
        """Mount a skill onto this agent.

        Eager skills emit SKILL_SETUP/SKILL_MOUNT here. Progressive skills are
        merely registered — they emit those events from the manager when
        activated via ``load_skill``.
        """
        self._ensure_has_skills()
        await self._skill_manager.mount(skill, config)
        if not skill.progressive:
            emit_fire_and_forget(self, HookEvent.SKILL_SETUP, skill.name)
            emit_fire_and_forget(self, HookEvent.SKILL_MOUNT, skill.name)
        return self

    async def unmount(self, name: str) -> Self:
        """Unmount a skill by name."""
        self._ensure_has_skills()
        emit_fire_and_forget(self, HookEvent.SKILL_TEARDOWN, name)
        await self._skill_manager.unmount(name)
        emit_fire_and_forget(self, HookEvent.SKILL_UNMOUNT, name)
        return self

    async def shutdown_skills(self) -> None:
        """Teardown all mounted skills."""
        self._ensure_has_skills()
        await self._skill_manager.shutdown()

    def register_skill_function(self, fn: Callable) -> None:
        """Create and mount a minimal skill from a ``@skill``-decorated function."""
        meta = getattr(fn, "_skill_meta", None)
        if meta is None:
            raise ValueError(f"{fn} is not decorated with @skill")

        sk_name = meta["name"]
        sk_desc = meta["description"]
        sk_instr = meta["instructions"]
        schema = meta["schema"] or _build_param_schema(fn)

        async def _tool_fn(**kwargs: Any) -> Any:
            result = fn(**kwargs)
            if inspect.isawaitable(result):
                return await result
            return result

        td = ToolDef(
            name=sk_name,
            description=sk_desc,
            function=_tool_fn,
            parameters=schema,
        )

        class _FnSkill(Skill):
            _auto_name = sk_name

            @property
            def name(self) -> str:
                return sk_name

            @property
            def description(self) -> str:
                return sk_desc

            @property
            def instructions(self) -> str:
                return sk_instr

            def tools(self) -> list[ToolDef]:
                return [td]

        self._ensure_has_skills()

        async def _do_mount() -> None:
            await self.mount(_FnSkill())

        try:
            loop = asyncio.get_running_loop()
            loop.create_task(_do_mount())
        except RuntimeError:
            asyncio.run(_do_mount())


# ---------------------------------------------------------------------------
# @skill decorator
# ---------------------------------------------------------------------------

def skill(
    description: str,
    name: str | None = None,
    instructions: str = "",
    schema_override: dict | None = None,
) -> Callable[[F], F]:
    """Decorator that marks a function as a simple single-tool skill."""

    def decorator(fn: F) -> F:
        fn._skill_meta = {  # type: ignore[attr-defined]
            "name": name or fn.__name__,
            "description": description,
            "instructions": instructions,
            "schema": schema_override,
        }
        return fn

    return decorator
