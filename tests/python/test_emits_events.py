import asyncio
import pytest
from src.python.emits_events import EmitsEvents
from src.python.event_stream_parser import EventType, StreamConfig
from src.python.message_bus import MessageBus, ParsedEvent


class EventEmitter(EmitsEvents):
    pass


class TestEmitsEvents:
    def test_register_event(self):
        obj = EventEmitter()
        et = EventType(name="test", description="a test", schema={})
        obj.register_event(et)
        assert "test" in obj.events

    def test_default_events(self):
        obj = EventEmitter()
        obj.default_events = ["test"]
        et = EventType(name="test", description="a test", schema={})
        obj.register_event(et)
        active = obj._resolve_active_events()
        assert len(active) == 1
        assert active[0].name == "test"

    def test_override_events_per_run(self):
        obj = EventEmitter()
        et1 = EventType(name="a", description="", schema={})
        et2 = EventType(name="b", description="", schema={})
        obj.register_event(et1)
        obj.register_event(et2)
        obj.default_events = ["a", "b"]
        active = obj._resolve_active_events(events=["a"])
        assert len(active) == 1
        assert active[0].name == "a"

    def test_adhoc_event(self):
        obj = EventEmitter()
        obj.default_events = []
        adhoc = EventType(name="adhoc", description="inline", schema={})
        active = obj._resolve_active_events(events=[adhoc])
        assert len(active) == 1
        assert active[0].name == "adhoc"

    def test_mixed_registered_and_adhoc(self):
        obj = EventEmitter()
        registered = EventType(name="reg", description="", schema={})
        obj.register_event(registered)
        adhoc = EventType(name="adhoc", description="", schema={})
        active = obj._resolve_active_events(events=["reg", adhoc])
        assert len(active) == 2

    def test_bus_exists(self):
        obj = EventEmitter()
        assert obj.bus is not None
        assert isinstance(obj.bus, MessageBus)

    def test_build_event_prompt(self):
        obj = EventEmitter()
        et = EventType(
            name="user_response",
            description="Send a message to the user",
            schema={"data": {"message": "string"}},
            instructions="Always use this for replies.",
        )
        obj.register_event(et)
        prompt = obj._build_event_prompt([et])
        assert "user_response" in prompt
        assert "---event" in prompt
        assert "Always use this for replies." in prompt
