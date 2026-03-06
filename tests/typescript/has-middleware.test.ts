import { describe, it, expect } from "vitest";
import { HasMiddleware, Middleware } from "../../src/typescript/has-middleware.js";

class MiddlewareUser extends HasMiddleware(class {}) {}

const uppercaseMiddleware: Middleware = {
  async pre(messages, _context) {
    return messages.map((m: any) =>
      m.content ? { ...m, content: m.content.toUpperCase() } : m
    );
  },
  async post(message, _context) {
    if (message.content) {
      return { ...message, content: message.content.toUpperCase() };
    }
    return message;
  },
};

const prefixMiddleware: Middleware = {
  async pre(messages, _context) {
    return [{ role: "system", content: "PREFIX" }, ...messages];
  },
};

describe("HasMiddleware", () => {
  it("use adds middleware", () => {
    const obj = new MiddlewareUser();
    obj.use(uppercaseMiddleware);
    expect(obj.middlewareStack.length).toBe(1);
  });

  it("runPre transforms messages", async () => {
    const obj = new MiddlewareUser();
    obj.use(uppercaseMiddleware);
    const messages = [{ role: "user", content: "hello" }];
    const result = await obj.runPre(messages, null);
    expect(result[0].content).toBe("HELLO");
  });

  it("runPost transforms message", async () => {
    const obj = new MiddlewareUser();
    obj.use(uppercaseMiddleware);
    const msg = { role: "assistant", content: "hello" };
    const result = await obj.runPost(msg, null);
    expect(result.content).toBe("HELLO");
  });

  it("middleware runs in order", async () => {
    const obj = new MiddlewareUser();
    obj.use(uppercaseMiddleware);
    obj.use(prefixMiddleware);
    const messages = [{ role: "user", content: "hello" }];
    const result = await obj.runPre(messages, null);
    expect(result[0].content).toBe("PREFIX");
    expect(result[1].content).toBe("HELLO");
  });

  it("no middleware", async () => {
    const obj = new MiddlewareUser();
    const messages = [{ role: "user", content: "hello" }];
    const result = await obj.runPre(messages, null);
    expect(result).toEqual(messages);
  });

  it("removeMiddleware removes middleware", async () => {
    const obj = new MiddlewareUser();
    obj.use(uppercaseMiddleware);
    expect(obj.middlewareStack.length).toBe(1);
    obj.removeMiddleware(uppercaseMiddleware);
    expect(obj.middlewareStack.length).toBe(0);
    const messages = [{ role: "user", content: "hello" }];
    const result = await obj.runPre(messages, null);
    expect(result[0].content).toBe("hello");
  });
});
