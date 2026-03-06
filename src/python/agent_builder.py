from __future__ import annotations

from typing import Any, Callable, Self

from src.python.has_hooks import HookEvent
from src.python.has_middleware import BaseMiddleware
from src.python.uses_tools import ToolDef
from src.python.has_skills import Skill
from src.python.event_stream_parser import EventType


class AgentBuilder:
    """Declarative builder for StandardAgent."""

    def __init__(self, model: str) -> None:
        self._model = model
        self._system: str | None = None
        self._max_turns: int = 20
        self._max_retries: int = 2
        self._stream: bool = True
        self._litellm_kwargs: dict[str, Any] = {}
        self._tools: list[Callable | ToolDef] = []
        self._middleware: list[BaseMiddleware] = []
        self._hooks: list[tuple[HookEvent, Callable]] = []
        self._events: list[EventType] = []
        self._skills: list[tuple[Skill, dict[str, Any] | None]] = []
        self._shell_opts: dict[str, Any] | None = None
        self._driver: str | None = None
        self._commands: list[tuple[str, Callable]] = []

    def system(self, prompt: str) -> Self:
        self._system = prompt
        return self

    def max_turns(self, n: int) -> Self:
        self._max_turns = n
        return self

    def max_retries(self, n: int) -> Self:
        self._max_retries = n
        return self

    def stream(self, enabled: bool = True) -> Self:
        self._stream = enabled
        return self

    def litellm(self, **kwargs: Any) -> Self:
        self._litellm_kwargs.update(kwargs)
        return self

    def tool(self, fn_or_def: Callable | ToolDef) -> Self:
        self._tools.append(fn_or_def)
        return self

    def tools(self, *fns_or_defs: Callable | ToolDef) -> Self:
        self._tools.extend(fns_or_defs)
        return self

    def middleware(self, *mws: BaseMiddleware) -> Self:
        self._middleware.extend(mws)
        return self

    def on(self, event: HookEvent, callback: Callable) -> Self:
        self._hooks.append((event, callback))
        return self

    def event(self, event_type: EventType) -> Self:
        self._events.append(event_type)
        return self

    def events(self, *event_types: EventType) -> Self:
        self._events.extend(event_types)
        return self

    def skill(self, sk: Skill, config: dict[str, Any] | None = None) -> Self:
        self._skills.append((sk, config))
        return self

    def skills(self, *sks: Skill) -> Self:
        for sk in sks:
            self._skills.append((sk, None))
        return self

    def shell(self, **kwargs: Any) -> Self:
        self._shell_opts = kwargs
        return self

    def driver(self, name: str) -> Self:
        self._driver = name
        return self

    def command(self, name: str, handler: Callable) -> Self:
        self._commands.append((name, handler))
        return self

    async def create(self) -> Any:
        from src.python.standard_agent import StandardAgent

        agent = StandardAgent(
            model=self._model,
            system=self._system,
            max_turns=self._max_turns,
            max_retries=self._max_retries,
            stream=self._stream,
            **self._litellm_kwargs,
        )

        for fn_or_def in self._tools:
            agent.register_tool(fn_or_def)

        for mw in self._middleware:
            agent.use(mw)

        for event, cb in self._hooks:
            agent.on(event, cb)

        for et in self._events:
            agent.register_event(et)

        if self._shell_opts is not None:
            agent.__init_has_shell__(driver=self._driver, **self._shell_opts)
        elif self._driver is not None:
            agent.__init_has_shell__(driver=self._driver)

        for name, handler in self._commands:
            agent.register_command(name, handler)

        # Skills last (async mount)
        for sk, config in self._skills:
            await agent.mount(sk, config)

        return agent
