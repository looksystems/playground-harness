import { describe, it, expect, vi } from "vitest";
import { OpenAIClient } from "../../src/typescript/llm/openai.js";

/** Build an SDK-shaped non-streaming completion response. */
function fakeCompletion(message: { content?: string | null; tool_calls?: any[] | null }): any {
  return {
    id: "chatcmpl-x",
    object: "chat.completion",
    created: 0,
    model: "gpt-4",
    choices: [
      {
        index: 0,
        message: { role: "assistant", content: null, tool_calls: null, ...message },
        finish_reason: "stop",
      },
    ],
  };
}

/** Build an async iterator yielding fake streaming chunks. */
function fakeStream(chunks: any[]): AsyncIterable<any> {
  return {
    [Symbol.asyncIterator]() {
      let i = 0;
      return {
        async next() {
          if (i >= chunks.length) return { done: true, value: undefined };
          return { done: false, value: chunks[i++] };
        },
      };
    },
  };
}

function makeClient(create: (...args: any[]) => any): OpenAIClient {
  // Construct an OpenAIClient with a stubbed inner client whose
  // chat.completions.create returns whatever the test wants.
  const fakeSdk: any = { chat: { completions: { create } } };
  return new OpenAIClient({ client: fakeSdk });
}

describe("OpenAIClient (non-streaming)", () => {
  it("returns the assistant message in OpenAI shape", async () => {
    const client = makeClient(async () => fakeCompletion({ content: "pong" }));
    const out = await client.callLlm({
      model: "gpt-4",
      messages: [{ role: "user", content: "ping" }],
      stream: false,
    });
    expect(out).toEqual({ role: "assistant", content: "pong", tool_calls: null });
  });

  it("forwards tool_calls verbatim", async () => {
    const tc = [
      { id: "c1", type: "function", function: { name: "add", arguments: '{"a":1,"b":2}' } },
    ];
    const client = makeClient(async () => fakeCompletion({ content: null, tool_calls: tc }));
    const out = await client.callLlm({
      model: "gpt-4",
      messages: [{ role: "user", content: "add" }],
      stream: false,
    });
    expect(out.tool_calls).toEqual(tc);
  });
});

describe("OpenAIClient (streaming)", () => {
  it("accumulates content deltas", async () => {
    const stream = fakeStream([
      { choices: [{ delta: { role: "assistant" } }] },
      { choices: [{ delta: { content: "Hello" } }] },
      { choices: [{ delta: { content: ", " } }] },
      { choices: [{ delta: { content: "world" } }] },
    ]);
    const client = makeClient(async () => stream);
    const out = await client.callLlm({
      model: "gpt-4",
      messages: [{ role: "user", content: "hi" }],
      stream: true,
    });
    expect(out.content).toBe("Hello, world");
    expect(out.tool_calls).toBeNull();
  });

  it("accumulates tool-call deltas across chunks", async () => {
    const stream = fakeStream([
      { choices: [{ delta: { role: "assistant" } }] },
      {
        choices: [{
          delta: {
            tool_calls: [{ index: 0, id: "c1", type: "function", function: { name: "add", arguments: "" } }],
          },
        }],
      },
      {
        choices: [{
          delta: { tool_calls: [{ index: 0, function: { arguments: '{"a":' } }] },
        }],
      },
      {
        choices: [{
          delta: { tool_calls: [{ index: 0, function: { arguments: '3,"b":4}' } }] },
        }],
      },
    ]);
    const client = makeClient(async () => stream);
    const out = await client.callLlm({
      model: "gpt-4",
      messages: [{ role: "user", content: "add" }],
      stream: true,
    });
    expect(out.tool_calls).toEqual([
      { id: "c1", type: "function", function: { name: "add", arguments: '{"a":3,"b":4}' } },
    ]);
  });

  it("forwards tools schema when present", async () => {
    const create = vi.fn(async () => fakeCompletion({ content: "x" }));
    const client = makeClient(create);
    await client.callLlm({
      model: "gpt-4",
      messages: [{ role: "user", content: "x" }],
      stream: false,
      tools: [
        { type: "function", function: { name: "add", parameters: { type: "object", properties: {} } } },
      ],
    });
    expect(create).toHaveBeenCalledOnce();
    const params = create.mock.calls[0][0];
    expect(params.tools).toBeDefined();
    expect(params.tools).toHaveLength(1);
  });
});
