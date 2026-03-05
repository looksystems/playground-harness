import { describe, it, expect } from "vitest";
import { MessageBus, ParsedEvent } from "../../src/typescript/message-bus.js";

describe("MessageBus", () => {
  it("subscribe and publish", async () => {
    const bus = new MessageBus();
    const received: string[] = [];
    bus.subscribe("greeting", async (event) => {
      received.push(event.type);
    });
    await bus.publish({ type: "greeting", data: { msg: "hi" } });
    expect(received).toEqual(["greeting"]);
  });

  it("wildcard subscriber", async () => {
    const bus = new MessageBus();
    const received: string[] = [];
    bus.subscribe("*", async (event) => {
      received.push(event.type);
    });
    await bus.publish({ type: "a", data: {} });
    await bus.publish({ type: "b", data: {} });
    expect(received).toEqual(["a", "b"]);
  });

  it("multiple handlers", async () => {
    const bus = new MessageBus();
    const received: string[] = [];
    bus.subscribe("test", async () => received.push("h1"));
    bus.subscribe("test", async () => received.push("h2"));
    await bus.publish({ type: "test", data: {} });
    expect(new Set(received)).toEqual(new Set(["h1", "h2"]));
  });

  it("handler can publish", async () => {
    const bus = new MessageBus();
    const received: string[] = [];
    bus.subscribe("first", async (event, b) => {
      received.push(event.type);
      if (event.type === "first") {
        await b.publish({ type: "second", data: {} });
      }
    });
    bus.subscribe("second", async (event) => {
      received.push(event.type);
    });
    await bus.publish({ type: "first", data: {} });
    expect(received).toEqual(["first", "second"]);
  });

  it("cycle detection", async () => {
    const bus = new MessageBus(3);
    let callCount = 0;
    bus.subscribe("loop", async (_event, b) => {
      callCount++;
      await b.publish({ type: "loop", data: {} });
    });
    await bus.publish({ type: "loop", data: {} });
    expect(callCount).toBeLessThanOrEqual(3);
  });

  it("handler error does not propagate", async () => {
    const bus = new MessageBus();
    const received: string[] = [];
    bus.subscribe("test", async () => {
      throw new Error("boom");
    });
    bus.subscribe("test", async () => received.push("ok"));
    await bus.publish({ type: "test", data: {} });
    expect(received).toEqual(["ok"]);
  });

  it("no subscribers", async () => {
    const bus = new MessageBus();
    await bus.publish({ type: "orphan", data: {} }); // should not throw
  });
});
