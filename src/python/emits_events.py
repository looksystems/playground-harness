from __future__ import annotations

from typing import Any, Self

from src.python.event_stream_parser import EventType, StructuredEvent, StreamConfig
from src.python.message_bus import MessageBus


class EmitsEvents:
    def __init_emits_events__(self) -> None:
        self._event_registry: dict[str, EventType] = {}
        if not hasattr(self, "default_events"):
            self.default_events: list[str] = []
        self._bus: MessageBus = MessageBus()

    def _ensure_events_init(self) -> None:
        if not hasattr(self, "_event_registry"):
            self.__init_emits_events__()

    def register_event(self, event_type: EventType) -> Self:
        self._ensure_events_init()
        self._event_registry[event_type.name] = event_type
        return self

    def unregister_event(self, name: str) -> Self:
        self._ensure_events_init()
        self._event_registry.pop(name, None)
        return self

    @property
    def events(self) -> dict[str, EventType]:
        self._ensure_events_init()
        return dict(self._event_registry)

    def _resolve_active_events(
        self, events: list[str | EventType] | None = None
    ) -> list[EventType]:
        self._ensure_events_init()
        if events is None:
            return [
                self._event_registry[name]
                for name in self.default_events
                if name in self._event_registry
            ]
        result: list[EventType] = []
        for item in events:
            if isinstance(item, str):
                if item in self._event_registry:
                    result.append(self._event_registry[item])
            elif isinstance(item, EventType):
                result.append(item)
        return result

    def _build_event_prompt(self, event_types: list[EventType]) -> str:
        if not event_types:
            return ""
        sections: list[str] = []
        sections.append("# Event Emission")
        sections.append("")
        sections.append("You can emit structured events inline in your response using the following format:")
        sections.append("")

        for et in event_types:
            sections.append(f"## Event: {et.name}")
            sections.append(f"Description: {et.description}")
            sections.append("Format:")
            sections.append("```")
            sections.append("---event")
            sections.append(f"type: {et.name}")
            if et.schema:
                for key, val in et.schema.items():
                    if isinstance(val, dict):
                        sections.append(f"{key}:")
                        for k, v in val.items():
                            sections.append(f"  {k}: <{v}>")
                    else:
                        sections.append(f"{key}: <{val}>")
            sections.append("---")
            sections.append("```")
            if et.instructions:
                sections.append(et.instructions)
            sections.append("")

        return "\n".join(sections)

    @property
    def bus(self) -> MessageBus:
        self._ensure_events_init()
        return self._bus

    @bus.setter
    def bus(self, value: MessageBus) -> None:
        self._ensure_events_init()
        self._bus = value
