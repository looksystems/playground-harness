import { describe, it, expect } from "vitest";
import {
  AnthropicClient,
  collectNonStreaming,
  splitSystemAndMessages,
  translateToolSchema,
} from "../../src/typescript/llm/anthropic.js";

// ---------------------------------------------------------------------------
// Translation: splitSystemAndMessages
// ---------------------------------------------------------------------------

describe("splitSystemAndMessages", () => {
  it("extracts system messages out of the messages array", () => {
    const { system, messages } = splitSystemAndMessages([
      { role: "system", content: "You are helpful." },
      { role: "user", content: "Hi" },
    ]);
    expect(system).toEqual([{ type: "text", text: "You are helpful." }]);
    expect(messages).toHaveLength(1);
    expect(messages[0].role).toBe("user");
  });

  it("concatenates multiple system messages into the system array", () => {
    const { system } = splitSystemAndMessages([
      { role: "system", content: "First." },
      { role: "system", content: "Second." },
      { role: "user", content: "Hi" },
    ]);
    expect(system).toEqual([
      { type: "text", text: "First." },
      { type: "text", text: "Second." },
    ]);
  });

  it("translates assistant messages with tool_calls to tool_use content blocks", () => {
    const { messages } = splitSystemAndMessages([
      { role: "user", content: "add 3 and 4" },
      {
        role: "assistant",
        content: null,
        tool_calls: [
          {
            id: "call_abc",
            type: "function",
            function: { name: "add", arguments: '{"a":3,"b":4}' },
          },
        ],
      },
    ]);
    expect(messages).toHaveLength(2);
    expect(messages[1].role).toBe("assistant");
    expect(messages[1].content).toEqual([
      { type: "tool_use", id: "call_abc", name: "add", input: { a: 3, b: 4 } },
    ]);
  });

  it("coalesces tool-result messages into a user-role turn after the assistant", () => {
    const { messages } = splitSystemAndMessages([
      { role: "user", content: "compute" },
      {
        role: "assistant",
        content: null,
        tool_calls: [
          { id: "c1", type: "function", function: { name: "add", arguments: "{}" } },
          { id: "c2", type: "function", function: { name: "mul", arguments: "{}" } },
        ],
      },
      { role: "tool", content: "7", tool_call_id: "c1" },
      { role: "tool", content: "12", tool_call_id: "c2" },
    ]);
    // user / assistant / user (with two tool_result blocks coalesced)
    expect(messages).toHaveLength(3);
    expect(messages[2].role).toBe("user");
    expect(messages[2].content).toHaveLength(2);
    expect(messages[2].content[0]).toMatchObject({ type: "tool_result", tool_use_id: "c1", content: "7" });
    expect(messages[2].content[1]).toMatchObject({ type: "tool_result", tool_use_id: "c2", content: "12" });
  });

  it("throws when a tool message has no tool_call_id", () => {
    expect(() =>
      splitSystemAndMessages([{ role: "tool", content: "x" } as any]),
    ).toThrow(/tool_call_id/);
  });

  it("falls back to {} when tool_calls.arguments is not valid JSON", () => {
    const { messages } = splitSystemAndMessages([
      {
        role: "assistant",
        content: null,
        tool_calls: [{ id: "c1", type: "function", function: { name: "x", arguments: "not-json" } }],
      },
    ]);
    expect(messages[0].content[0]).toMatchObject({ type: "tool_use", input: {} });
  });
});

// ---------------------------------------------------------------------------
// Translation: translateToolSchema
// ---------------------------------------------------------------------------

describe("translateToolSchema", () => {
  it("strips the OpenAI wrapper and renames parameters → input_schema", () => {
    const out = translateToolSchema({
      type: "function",
      function: {
        name: "add",
        description: "Add two integers",
        parameters: {
          type: "object",
          properties: { a: { type: "integer" }, b: { type: "integer" } },
          required: ["a", "b"],
        },
      },
    });
    expect(out).toEqual({
      name: "add",
      description: "Add two integers",
      input_schema: {
        type: "object",
        properties: { a: { type: "integer" }, b: { type: "integer" } },
        required: ["a", "b"],
      },
    });
  });

  it("omits description when not provided", () => {
    const out = translateToolSchema({
      type: "function",
      function: { name: "noop", parameters: { type: "object", properties: {} } },
    });
    expect(out).not.toHaveProperty("description");
  });
});

// ---------------------------------------------------------------------------
// Translation: collectNonStreaming
// ---------------------------------------------------------------------------

describe("collectNonStreaming", () => {
  it("concatenates text blocks into content", () => {
    const out = collectNonStreaming({
      content: [
        { type: "text", text: "Hello " },
        { type: "text", text: "world" },
      ],
    });
    expect(out.content).toBe("Hello world");
    expect(out.tool_calls).toBeNull();
  });

  it("turns tool_use blocks into OpenAI-shaped tool_calls", () => {
    const out = collectNonStreaming({
      content: [
        { type: "tool_use", id: "call_a", name: "add", input: { a: 3, b: 4 } },
      ],
    });
    expect(out.content).toBeNull();
    expect(out.tool_calls).toEqual([
      {
        id: "call_a",
        type: "function",
        function: { name: "add", arguments: '{"a":3,"b":4}' },
      },
    ]);
  });

  it("handles empty content", () => {
    const out = collectNonStreaming({ content: [] });
    expect(out.content).toBeNull();
    expect(out.tool_calls).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// Streaming end-to-end via fake fetch
// ---------------------------------------------------------------------------

/** Build an SSE-formatted Response body from a list of Anthropic stream events. */
function sseResponse(events: Array<{ event: string; data: any }>): Response {
  const parts: string[] = [];
  for (const e of events) {
    parts.push(`event: ${e.event}\ndata: ${JSON.stringify(e.data)}\n\n`);
  }
  return new Response(parts.join(""), {
    status: 200,
    headers: { "content-type": "text/event-stream" },
  });
}

describe("AnthropicClient streaming", () => {
  it("accumulates text_delta chunks into content", async () => {
    const fakeFetch = async () =>
      sseResponse([
        { event: "message_start", data: { type: "message_start", message: { id: "m", role: "assistant", content: [], model: "claude-sonnet-4-6", stop_reason: null, stop_sequence: null, usage: { input_tokens: 1, output_tokens: 0 } } } },
        { event: "content_block_start", data: { type: "content_block_start", index: 0, content_block: { type: "text", text: "" } } },
        { event: "content_block_delta", data: { type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "Hello" } } },
        { event: "content_block_delta", data: { type: "content_block_delta", index: 0, delta: { type: "text_delta", text: ", " } } },
        { event: "content_block_delta", data: { type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "world" } } },
        { event: "content_block_stop", data: { type: "content_block_stop", index: 0 } },
        { event: "message_delta", data: { type: "message_delta", delta: { stop_reason: "end_turn", stop_sequence: null }, usage: { output_tokens: 5 } } },
        { event: "message_stop", data: { type: "message_stop" } },
      ]);

    const client = new AnthropicClient({ apiKey: "test", fetch: fakeFetch as any });
    const out = await client.callLlm({
      model: "claude-sonnet-4-6",
      messages: [{ role: "user", content: "hi" }],
      stream: true,
    });
    expect(out.content).toBe("Hello, world");
    expect(out.tool_calls).toBeNull();
  });

  it("accumulates input_json_delta chunks for tool calls", async () => {
    const fakeFetch = async () =>
      sseResponse([
        { event: "message_start", data: { type: "message_start", message: { id: "m", role: "assistant", content: [], model: "claude-sonnet-4-6", stop_reason: null, stop_sequence: null, usage: { input_tokens: 1, output_tokens: 0 } } } },
        { event: "content_block_start", data: { type: "content_block_start", index: 0, content_block: { type: "tool_use", id: "toolu_abc", name: "add", input: {} } } },
        { event: "content_block_delta", data: { type: "content_block_delta", index: 0, delta: { type: "input_json_delta", partial_json: '{"a":' } } },
        { event: "content_block_delta", data: { type: "content_block_delta", index: 0, delta: { type: "input_json_delta", partial_json: '3,"b":4}' } } },
        { event: "content_block_stop", data: { type: "content_block_stop", index: 0 } },
        { event: "message_delta", data: { type: "message_delta", delta: { stop_reason: "tool_use", stop_sequence: null }, usage: { output_tokens: 5 } } },
        { event: "message_stop", data: { type: "message_stop" } },
      ]);

    const client = new AnthropicClient({ apiKey: "test", fetch: fakeFetch as any });
    const out = await client.callLlm({
      model: "claude-sonnet-4-6",
      messages: [{ role: "user", content: "add 3 and 4" }],
      stream: true,
      tools: [
        {
          type: "function",
          function: {
            name: "add",
            parameters: { type: "object", properties: { a: { type: "integer" }, b: { type: "integer" } } },
          },
        },
      ],
    });
    expect(out.content).toBeNull();
    expect(out.tool_calls).toEqual([
      { id: "toolu_abc", type: "function", function: { name: "add", arguments: '{"a":3,"b":4}' } },
    ]);
  });
});
