import { describe, it, expect } from "vitest";
import { EmitsEvents } from "../../src/typescript/emits-events.js";
import { StructuredEvent } from "../../src/typescript/event-stream-parser.js";
import { MessageBus } from "../../src/typescript/message-bus.js";

class EventEmitter extends EmitsEvents(class {}) {}

describe("EmitsEvents", () => {
  it("register event", () => {
    const obj = new EventEmitter();
    const et: StructuredEvent = { name: "test", description: "a test", schema: {} };
    obj.registerEvent(et);
    expect(obj.eventRegistry.has("test")).toBe(true);
  });

  it("default events", () => {
    const obj = new EventEmitter();
    obj.defaultEvents = ["test"];
    const et: StructuredEvent = { name: "test", description: "a test", schema: {} };
    obj.registerEvent(et);
    const active = obj.resolveActiveEvents();
    expect(active.length).toBe(1);
    expect(active[0].name).toBe("test");
  });

  it("override events per run", () => {
    const obj = new EventEmitter();
    const et1: StructuredEvent = { name: "a", description: "", schema: {} };
    const et2: StructuredEvent = { name: "b", description: "", schema: {} };
    obj.registerEvent(et1);
    obj.registerEvent(et2);
    obj.defaultEvents = ["a", "b"];
    const active = obj.resolveActiveEvents(["a"]);
    expect(active.length).toBe(1);
    expect(active[0].name).toBe("a");
  });

  it("adhoc event", () => {
    const obj = new EventEmitter();
    obj.defaultEvents = [];
    const adhoc: StructuredEvent = {
      name: "adhoc",
      description: "inline",
      schema: {},
    };
    const active = obj.resolveActiveEvents([adhoc]);
    expect(active.length).toBe(1);
    expect(active[0].name).toBe("adhoc");
  });

  it("mixed registered and adhoc", () => {
    const obj = new EventEmitter();
    const registered: StructuredEvent = { name: "reg", description: "", schema: {} };
    obj.registerEvent(registered);
    const adhoc: StructuredEvent = { name: "adhoc", description: "", schema: {} };
    const active = obj.resolveActiveEvents(["reg", adhoc]);
    expect(active.length).toBe(2);
  });

  it("bus exists", () => {
    const obj = new EventEmitter();
    expect(obj.bus).toBeDefined();
    expect(obj.bus).toBeInstanceOf(MessageBus);
  });

  it("build event prompt", () => {
    const obj = new EventEmitter();
    const et: StructuredEvent = {
      name: "user_response",
      description: "Send a message to the user",
      schema: { data: { message: "string" } },
      instructions: "Always use this for replies.",
    };
    obj.registerEvent(et);
    const prompt = obj.buildEventPrompt([et]);
    expect(prompt).toContain("user_response");
    expect(prompt).toContain("---event");
    expect(prompt).toContain("Always use this for replies.");
  });
});
