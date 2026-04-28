/**
 * AnthropicClient — LlmClient implementation backed by `@anthropic-ai/sdk`.
 *
 * The harness emits OpenAI-shaped messages and tool schemas; this adapter
 * translates them to Anthropic's native Messages API in both directions.
 * Mirrors the Go adapter at `src/go/llm/anthropic/anthropic.go`.
 *
 * Translations performed:
 *   1. system messages are extracted out of the messages array into the
 *      top-level `system` field; multiple system messages concatenate
 *   2. role mapping: only "user" / "assistant" survive; "tool" results
 *      become ToolResultBlock content blocks coalesced into a user-role
 *      turn
 *   3. tool definitions: strip the `{type:"function", function:{...}}`
 *      OpenAI wrapper and emit Anthropic's flatter `Tool` shape
 *   4. tool-call response: ToolUseBlock content blocks become OpenAI-shaped
 *      `tool_calls[]`; concatenated text blocks become `content`
 *   5. streaming: map Anthropic SSE events to the unified `{role, content,
 *      tool_calls}` shape
 */

import Anthropic from "@anthropic-ai/sdk";

import type { AssistantMessage, ChatMessage, LlmClient, LlmRequest, ToolCall, ToolSchema } from "./client.js";

/** Anthropic's API requires max_tokens; harness fallback if neither request nor option supplies one. */
const DEFAULT_MAX_TOKENS = 4096;

export interface AnthropicClientOptions {
  apiKey?: string;
  baseURL?: string;
  /** Default max_tokens used when LlmRequest.extraOptions.max_tokens is unset. */
  maxTokens?: number;
  /** Inject a pre-built SDK client (e.g. for tests with a custom fetch). */
  client?: Anthropic;
  /** Custom fetch passed to the SDK constructor; ignored if `client` is set. */
  fetch?: typeof fetch;
}

export class AnthropicClient implements LlmClient {
  readonly client: Anthropic;
  readonly defaultMaxTokens: number;

  constructor(options: AnthropicClientOptions = {}) {
    this.defaultMaxTokens = options.maxTokens ?? DEFAULT_MAX_TOKENS;
    if (options.client) {
      this.client = options.client;
    } else {
      const sdkOpts: Record<string, unknown> = {
        apiKey: options.apiKey ?? process.env.ANTHROPIC_API_KEY ?? "sk-ant-placeholder",
      };
      if (options.baseURL) sdkOpts.baseURL = options.baseURL;
      if (options.fetch) sdkOpts.fetch = options.fetch;
      this.client = new Anthropic(sdkOpts as any);
    }
  }

  async callLlm(req: LlmRequest): Promise<AssistantMessage> {
    const { system, messages } = splitSystemAndMessages(req.messages);
    const params: Record<string, unknown> = {
      model: req.model,
      messages,
      max_tokens: this.resolveMaxTokens(req),
      ...(req.extraOptions ?? {}),
    };
    // System and messages can also live in extraOptions; the explicit values above win.
    params.messages = messages;
    params.max_tokens = this.resolveMaxTokens(req);
    if (system.length > 0) {
      params.system = system;
    }
    if (req.tools && req.tools.length > 0) {
      params.tools = req.tools.map(translateToolSchema);
    }

    if (req.stream) {
      params.stream = true;
      const stream = await this.client.messages.create(params as any);
      return this.consumeStream(stream as any);
    }

    const resp: any = await this.client.messages.create(params as any);
    return collectNonStreaming(resp);
  }

  private resolveMaxTokens(req: LlmRequest): number {
    const fromExtra = req.extraOptions?.max_tokens;
    if (typeof fromExtra === "number" && fromExtra > 0) return fromExtra;
    return this.defaultMaxTokens;
  }

  private async consumeStream(stream: AsyncIterable<any>): Promise<AssistantMessage> {
    const contentParts: string[] = [];
    const toolCallsByIndex = new Map<number, ToolCall>();

    for await (const event of stream) {
      switch (event.type) {
        case "content_block_start": {
          const block = event.content_block;
          if (block?.type === "tool_use") {
            toolCallsByIndex.set(event.index, {
              id: block.id ?? "",
              type: "function",
              function: { name: block.name ?? "", arguments: "" },
            });
          }
          break;
        }
        case "content_block_delta": {
          const delta = event.delta;
          if (delta?.type === "text_delta" && typeof delta.text === "string") {
            contentParts.push(delta.text);
          } else if (delta?.type === "input_json_delta" && typeof delta.partial_json === "string") {
            const tc = toolCallsByIndex.get(event.index);
            if (tc) {
              tc.function.arguments += delta.partial_json;
            }
          }
          break;
        }
        // message_start, content_block_stop, message_delta, message_stop carry
        // metadata only; nothing to accumulate. message_stop terminates the
        // stream naturally when the iterator finishes.
        default:
          break;
      }
    }

    const result: AssistantMessage = {
      role: "assistant",
      content: contentParts.length > 0 ? contentParts.join("") : null,
      tool_calls: null,
    };
    if (toolCallsByIndex.size > 0) {
      const sorted = [...toolCallsByIndex.keys()].sort((a, b) => a - b);
      result.tool_calls = sorted.map((i) => toolCallsByIndex.get(i)!);
    }
    return result;
  }
}

// ---------------------------------------------------------------------------
// Translation helpers (exported for unit tests)
// ---------------------------------------------------------------------------

export interface AnthropicSystemBlock {
  type: "text";
  text: string;
}

export interface AnthropicMessageParam {
  role: "user" | "assistant";
  content: any[]; // ContentBlockParam[] — keep loose for forward-compat
}

/**
 * Walk the OpenAI-shaped messages array and produce Anthropic's
 * `{system, messages}` split. Tool-result messages collapse into a
 * ToolResultBlock content block on the most recent user-role turn (creating
 * one if needed). Adjacent tool results coalesce to keep wire shape tidy.
 */
export function splitSystemAndMessages(input: ChatMessage[]): {
  system: AnthropicSystemBlock[];
  messages: AnthropicMessageParam[];
} {
  const system: AnthropicSystemBlock[] = [];
  const messages: AnthropicMessageParam[] = [];

  for (let i = 0; i < input.length; i++) {
    const m = input[i];
    switch (m.role) {
      case "system":
        if (m.content) system.push({ type: "text", text: String(m.content) });
        break;
      case "user":
        messages.push({
          role: "user",
          content: [{ type: "text", text: String(m.content ?? "") }],
        });
        break;
      case "assistant": {
        const blocks: any[] = [];
        if (m.content) {
          blocks.push({ type: "text", text: String(m.content) });
        }
        if (m.tool_calls) {
          for (const tc of m.tool_calls) {
            const argsStr = tc.function.arguments || "{}";
            let parsed: unknown = {};
            try {
              parsed = JSON.parse(argsStr);
            } catch {
              parsed = {};
            }
            blocks.push({
              type: "tool_use",
              id: tc.id,
              name: tc.function.name,
              input: parsed,
            });
          }
        }
        messages.push({ role: "assistant", content: blocks });
        break;
      }
      case "tool": {
        if (!m.tool_call_id) {
          throw new Error(`anthropic: message ${i} has role=tool but no tool_call_id`);
        }
        const block = {
          type: "tool_result",
          tool_use_id: m.tool_call_id,
          content: m.content == null ? "" : String(m.content),
        };
        const last = messages[messages.length - 1];
        if (last && last.role === "user") {
          last.content.push(block);
        } else {
          messages.push({ role: "user", content: [block] });
        }
        break;
      }
      default:
        // Unknown roles are silently dropped (forward-compat with future
        // OpenAI-shaped roles like "function" or "developer").
        break;
    }
  }

  return { system, messages };
}

/**
 * Translate an OpenAI-shaped tool schema to Anthropic's `Tool` shape:
 *   `{type:"function", function:{name, description, parameters}}`
 *      → `{name, description, input_schema: parameters}`
 */
export function translateToolSchema(t: ToolSchema): {
  name: string;
  description?: string;
  input_schema: Record<string, unknown>;
} {
  const fn = t.function;
  const out: ReturnType<typeof translateToolSchema> = {
    name: fn.name,
    input_schema: fn.parameters ?? { type: "object", properties: {} },
  };
  if (fn.description) out.description = fn.description;
  return out;
}

/**
 * Collapse a non-streaming Anthropic Message response into the harness's
 * `AssistantMessage` shape. Concatenates all `text` blocks into `content`
 * and emits each `tool_use` block as an OpenAI-shaped tool_call.
 */
export function collectNonStreaming(resp: any): AssistantMessage {
  const contentParts: string[] = [];
  const toolCalls: ToolCall[] = [];
  for (const block of resp.content ?? []) {
    if (block.type === "text" && typeof block.text === "string") {
      contentParts.push(block.text);
    } else if (block.type === "tool_use") {
      const argsStr =
        block.input == null ? "{}" : typeof block.input === "string" ? block.input : JSON.stringify(block.input);
      toolCalls.push({
        id: block.id ?? "",
        type: "function",
        function: { name: block.name ?? "", arguments: argsStr },
      });
    }
  }
  return {
    role: "assistant",
    content: contentParts.length > 0 ? contentParts.join("") : null,
    tool_calls: toolCalls.length > 0 ? toolCalls : null,
  };
}
