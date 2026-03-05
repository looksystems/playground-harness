import { describe, it, expect } from "vitest";
import { HasHooks, HookEvent } from "../../src/typescript/has-hooks.js";

class HookUser extends HasHooks(class {}) {}

describe("HasHooks", () => {
  it("subscribe and emit", async () => {
    const obj = new HookUser();
    const received: string[] = [];
    obj.on(HookEvent.RUN_START, () => received.push("started"));
    await obj._emit(HookEvent.RUN_START);
    expect(received).toEqual(["started"]);
  });

  it("emit with args", async () => {
    const obj = new HookUser();
    const received: [string, any][] = [];
    obj.on(HookEvent.TOOL_CALL, (name: string, args: any) =>
      received.push([name, args])
    );
    await obj._emit(HookEvent.TOOL_CALL, "add", { a: 1 });
    expect(received).toEqual([["add", { a: 1 }]]);
  });

  it("multiple hooks same event", async () => {
    const obj = new HookUser();
    const received: string[] = [];
    obj.on(HookEvent.RUN_END, () => received.push("a"));
    obj.on(HookEvent.RUN_END, () => received.push("b"));
    await obj._emit(HookEvent.RUN_END);
    expect(new Set(received)).toEqual(new Set(["a", "b"]));
  });

  it("async hook", async () => {
    const obj = new HookUser();
    const received: string[] = [];
    obj.on(HookEvent.RUN_START, async () => {
      received.push("async");
    });
    await obj._emit(HookEvent.RUN_START);
    expect(received).toEqual(["async"]);
  });

  it("hook error does not propagate", async () => {
    const obj = new HookUser();
    const received: string[] = [];
    obj.on(HookEvent.RUN_START, () => {
      throw new Error("boom");
    });
    obj.on(HookEvent.RUN_START, () => received.push("ok"));
    await obj._emit(HookEvent.RUN_START);
    expect(received).toEqual(["ok"]);
  });

  it("no hooks registered", async () => {
    const obj = new HookUser();
    await obj._emit(HookEvent.RUN_START); // should not throw
  });
});
