/**
 * OpenAIClient — LlmClient implementation backed by the official `openai` SDK.
 *
 * The OpenAI Chat Completions wire format is the harness's canonical message
 * shape, so this adapter is a near-passthrough: it forwards messages and tool
 * schemas verbatim and re-shapes the response into the harness's
 * `AssistantMessage`. Streaming consumes the SDK's async iterator and
 * accumulates `delta.content` and `delta.tool_calls` into the same shape as
 * the non-streaming path.
 *
 * To target an OpenAI-compatible endpoint (Anthropic's compatibility shim,
 * OpenRouter, litellm-proxy, …) pass `baseURL` to the constructor — the SDK
 * routes there transparently.
 */

import OpenAI from "openai";

import type { AssistantMessage, LlmClient, LlmRequest, ToolCall } from "./client.js";

export interface OpenAIClientOptions {
  apiKey?: string;
  baseURL?: string;
  /** Inject a pre-built SDK client (e.g. for tests). Overrides apiKey/baseURL. */
  client?: OpenAI;
}

export class OpenAIClient implements LlmClient {
  readonly client: OpenAI;

  constructor(options: OpenAIClientOptions = {}) {
    if (options.client) {
      this.client = options.client;
    } else {
      const sdkOpts: Record<string, unknown> = {
        apiKey: options.apiKey ?? process.env.OPENAI_API_KEY ?? "sk-placeholder",
      };
      if (options.baseURL) sdkOpts.baseURL = options.baseURL;
      this.client = new OpenAI(sdkOpts);
    }
  }

  async callLlm(req: LlmRequest): Promise<AssistantMessage> {
    const params: Record<string, unknown> = {
      model: req.model,
      messages: req.messages,
      ...(req.extraOptions ?? {}),
    };
    if (req.tools && req.tools.length > 0) {
      params.tools = req.tools;
    }

    if (req.stream) {
      params.stream = true;
      const stream = await this.client.chat.completions.create(params as any);
      return this.consumeStream(stream as any);
    }

    const resp: any = await this.client.chat.completions.create(params as any);
    const msg = resp.choices[0].message;
    return {
      role: "assistant",
      content: msg.content ?? null,
      tool_calls: msg.tool_calls ?? null,
    };
  }

  private async consumeStream(stream: AsyncIterable<any>): Promise<AssistantMessage> {
    const contentParts: string[] = [];
    const toolCallsByIndex = new Map<number, ToolCall>();

    for await (const chunk of stream) {
      const delta = chunk.choices?.[0]?.delta;
      if (!delta) continue;
      if (delta.content) {
        contentParts.push(delta.content);
      }
      if (delta.tool_calls) {
        for (const tc of delta.tool_calls) {
          const idx = tc.index;
          if (!toolCallsByIndex.has(idx)) {
            toolCallsByIndex.set(idx, {
              id: tc.id ?? "",
              type: "function",
              function: { name: "", arguments: "" },
            });
          }
          const entry = toolCallsByIndex.get(idx)!;
          if (tc.id) entry.id = tc.id;
          if (tc.function) {
            if (tc.function.name) entry.function.name += tc.function.name;
            if (tc.function.arguments) entry.function.arguments += tc.function.arguments;
          }
        }
      }
    }

    const message: AssistantMessage = {
      role: "assistant",
      content: contentParts.length > 0 ? contentParts.join("") : null,
      tool_calls: null,
    };
    if (toolCallsByIndex.size > 0) {
      const sorted = [...toolCallsByIndex.keys()].sort((a, b) => a - b);
      message.tool_calls = sorted.map((i) => toolCallsByIndex.get(i)!);
    }
    return message;
  }
}
