import { describe, it, expect } from "vitest";
import { EmitsEvents } from "../../src/typescript/emits-events.js";
import { EventType } from "../../src/typescript/event-stream-parser.js";
import { MessageBus } from "../../src/typescript/message-bus.js";

class EventEmitter extends EmitsEvents(class {}) {}

describe("EmitsEvents", () => {
  it("register event", () => {
    const obj = new EventEmitter();
    const et: EventType = { name: "test", description: "a test", schema: {} };
    obj.register_event(et);
    expect(obj._event_registry.has("test")).toBe(true);
  });

  it("default events", () => {
    const obj = new EventEmitter();
    obj.default_events = ["test"];
    const et: EventType = { name: "test", description: "a test", schema: {} };
    obj.register_event(et);
    const active = obj._resolve_active_events();
    expect(active.length).toBe(1);
    expect(active[0].name).toBe("test");
  });

  it("override events per run", () => {
    const obj = new EventEmitter();
    const et1: EventType = { name: "a", description: "", schema: {} };
    const et2: EventType = { name: "b", description: "", schema: {} };
    obj.register_event(et1);
    obj.register_event(et2);
    obj.default_events = ["a", "b"];
    const active = obj._resolve_active_events(["a"]);
    expect(active.length).toBe(1);
    expect(active[0].name).toBe("a");
  });

  it("adhoc event", () => {
    const obj = new EventEmitter();
    obj.default_events = [];
    const adhoc: EventType = {
      name: "adhoc",
      description: "inline",
      schema: {},
    };
    const active = obj._resolve_active_events([adhoc]);
    expect(active.length).toBe(1);
    expect(active[0].name).toBe("adhoc");
  });

  it("mixed registered and adhoc", () => {
    const obj = new EventEmitter();
    const registered: EventType = { name: "reg", description: "", schema: {} };
    obj.register_event(registered);
    const adhoc: EventType = { name: "adhoc", description: "", schema: {} };
    const active = obj._resolve_active_events(["reg", adhoc]);
    expect(active.length).toBe(2);
  });

  it("bus exists", () => {
    const obj = new EventEmitter();
    expect(obj.bus).toBeDefined();
    expect(obj.bus).toBeInstanceOf(MessageBus);
  });

  it("build event prompt", () => {
    const obj = new EventEmitter();
    const et: EventType = {
      name: "user_response",
      description: "Send a message to the user",
      schema: { data: { message: "string" } },
      instructions: "Always use this for replies.",
    };
    obj.register_event(et);
    const prompt = obj._build_event_prompt([et]);
    expect(prompt).toContain("user_response");
    expect(prompt).toContain("---event");
    expect(prompt).toContain("Always use this for replies.");
  });
});
