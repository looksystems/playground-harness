/**
 * agent_harness.ts — A lightweight, async-native, single-file LLM agent harness.
 *
 * Built on the OpenAI SDK (compatible with litellm proxy, OpenAI, Anthropic via proxy, etc.)
 * Features:
 *   - Decorator-free tool registration with auto-schema from zod or manual schemas
 *   - Async middleware pipeline (pre/post processing of messages and responses)
 *   - Async hook system for lifecycle events
 *   - Streaming support
 *   - Parallel tool execution
 *   - Configurable retry with exponential backoff
 *
 * Usage:
 *   import { Agent, defineTool } from "./agent_harness";
 *
 *   const add = defineTool({
 *     name: "add",
 *     description: "Add two numbers",
 *     parameters: {
 *       type: "object",
 *       properties: { a: { type: "number" }, b: { type: "number" } },
 *       required: ["a", "b"],
 *     },
 *     execute: async ({ a, b }) => a + b,
 *   });
 *
 *   const agent = new Agent({ model: "gpt-4o" });
 *   agent.registerTool(add);
 *   const result = await agent.run("What is 2 + 3?");
 */

import OpenAI from "openai";
import type {
  ChatCompletionMessageParam,
  ChatCompletionTool,
  ChatCompletionMessageToolCall,
} from "openai/resources/chat/completions";

// ---------------------------------------------------------------------------
// Hook events
// ---------------------------------------------------------------------------

export enum HookEvent {
  RunStart = "run_start",
  RunEnd = "run_end",
  LlmRequest = "llm_request",
  LlmResponse = "llm_response",
  ToolCall = "tool_call",
  ToolResult = "tool_result",
  ToolError = "tool_error",
  Retry = "retry",
  TokenStream = "token_stream",
  Error = "error",
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ToolDef<TArgs = any, TResult = any> {
  name: string;
  description: string;
  parameters: Record<string, any>; // JSON Schema
  execute: (args: TArgs) => TResult | Promise<TResult>;
}

export interface RunContext {
  agent: Agent;
  turn: number;
  metadata: Record<string, any>;
}

type HookCallback = (...args: any[]) => void | Promise<void>;

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

export interface Middleware {
  pre?(
    messages: ChatCompletionMessageParam[],
    context: RunContext
  ): ChatCompletionMessageParam[] | Promise<ChatCompletionMessageParam[]>;

  post?(
    message: Record<string, any>,
    context: RunContext
  ): Record<string, any> | Promise<Record<string, any>>;
}

// ---------------------------------------------------------------------------
// Tool definition helper
// ---------------------------------------------------------------------------

export function defineTool<TArgs = any, TResult = any>(
  def: ToolDef<TArgs, TResult>
): ToolDef<TArgs, TResult> {
  return def;
}

// ---------------------------------------------------------------------------
// Agent options
// ---------------------------------------------------------------------------

export interface AgentOptions {
  /** OpenAI-compatible model string, e.g. "gpt-4o", "claude-sonnet-4-20250514" */
  model?: string;
  /** System prompt */
  system?: string;
  /** Max tool-use loop iterations */
  maxTurns?: number;
  /** Retries on transient LLM errors */
  maxRetries?: number;
  /** Stream tokens and emit TokenStream hooks */
  stream?: boolean;
  /** Execute multiple tool calls concurrently */
  parallelToolCalls?: boolean;
  /**
   * Pre-configured OpenAI client. Pass this to point at a litellm proxy,
   * Azure, or any OpenAI-compatible endpoint.
   */
  client?: OpenAI;
  /** Extra params forwarded to chat.completions.create() */
  completionParams?: Partial<OpenAI.ChatCompletionCreateParams>;
}

// ---------------------------------------------------------------------------
// Agent
// ---------------------------------------------------------------------------

export class Agent {
  readonly model: string;
  readonly system: string | null;
  readonly maxTurns: number;
  readonly maxRetries: number;
  readonly stream: boolean;
  readonly parallelToolCalls: boolean;
  readonly completionParams: Partial<OpenAI.ChatCompletionCreateParams>;

  private client: OpenAI;
  private tools = new Map<string, ToolDef>();
  private middlewares: Middleware[] = [];
  private hooks = new Map<HookEvent, HookCallback[]>();

  constructor(opts: AgentOptions = {}) {
    this.model = opts.model ?? "gpt-4o";
    this.system = opts.system ?? null;
    this.maxTurns = opts.maxTurns ?? 20;
    this.maxRetries = opts.maxRetries ?? 2;
    this.stream = opts.stream ?? false;
    this.parallelToolCalls = opts.parallelToolCalls ?? true;
    this.completionParams = opts.completionParams ?? {};

    this.client =
      opts.client ??
      new OpenAI({
        // Falls back to OPENAI_API_KEY / OPENAI_BASE_URL env vars
      });

    for (const event of Object.values(HookEvent)) {
      this.hooks.set(event, []);
    }
  }

  // -- Registration --------------------------------------------------------

  registerTool(tool: ToolDef): void {
    this.tools.set(tool.name, tool);
  }

  use(mw: Middleware): void {
    this.middlewares.push(mw);
  }

  on(event: HookEvent, callback: HookCallback): void {
    this.hooks.get(event)!.push(callback);
  }

  // -- Schema generation ---------------------------------------------------

  private toolsSchema(): ChatCompletionTool[] {
    return Array.from(this.tools.values()).map((t) => ({
      type: "function" as const,
      function: {
        name: t.name,
        description: t.description,
        parameters: t.parameters,
      },
    }));
  }

  // -- Hook dispatch -------------------------------------------------------

  private async emit(event: HookEvent, ...args: any[]): Promise<void> {
    const cbs = this.hooks.get(event);
    if (!cbs?.length) return;
    const results = await Promise.allSettled(cbs.map((cb) => cb(...args)));
    for (const r of results) {
      if (r.status === "rejected") {
        console.warn(`Hook ${event} error:`, r.reason);
      }
    }
  }

  // -- Middleware pipeline --------------------------------------------------

  private async runPre(
    messages: ChatCompletionMessageParam[],
    ctx: RunContext
  ): Promise<ChatCompletionMessageParam[]> {
    for (const mw of this.middlewares) {
      if (mw.pre) {
        messages = await mw.pre(messages, ctx);
      }
    }
    return messages;
  }

  private async runPost(
    message: Record<string, any>,
    ctx: RunContext
  ): Promise<Record<string, any>> {
    for (const mw of this.middlewares) {
      if (mw.post) {
        message = await mw.post(message, ctx);
      }
    }
    return message;
  }

  // -- Tool execution ------------------------------------------------------

  private async executeTool(
    name: string,
    args: Record<string, any>
  ): Promise<string> {
    const tool = this.tools.get(name);
    if (!tool) {
      const err = `Unknown tool: ${name}`;
      await this.emit(HookEvent.ToolError, name, err);
      return JSON.stringify({ error: err });
    }

    await this.emit(HookEvent.ToolCall, name, args);
    try {
      const result = await tool.execute(args);
      await this.emit(HookEvent.ToolResult, name, result);
      return typeof result === "string" ? result : JSON.stringify(result);
    } catch (err: any) {
      await this.emit(HookEvent.ToolError, name, err);
      return JSON.stringify({ error: err?.message ?? String(err) });
    }
  }

  // -- LLM call ------------------------------------------------------------

  private async callLlm(
    messages: ChatCompletionMessageParam[],
    ctx: RunContext
  ): Promise<Record<string, any>> {
    const toolsDef = this.tools.size > 0 ? this.toolsSchema() : undefined;
    await this.emit(HookEvent.LlmRequest, messages, toolsDef);

    let lastErr: Error | null = null;

    for (let attempt = 1; attempt <= this.maxRetries + 1; attempt++) {
      try {
        if (this.stream) {
          return await this.callLlmStream(messages, toolsDef, ctx);
        }

        const resp = await this.client.chat.completions.create({
          model: this.model,
          messages,
          tools: toolsDef,
          ...this.completionParams,
        } as OpenAI.ChatCompletionCreateParams);

        const msg = resp.choices[0].message as unknown as Record<string, any>;
        await this.emit(HookEvent.LlmResponse, msg);
        return msg;
      } catch (err: any) {
        lastErr = err;
        if (attempt <= this.maxRetries) {
          await this.emit(HookEvent.Retry, attempt, err);
          await sleep(Math.min(2 ** attempt * 1000, 10_000));
        }
      }
    }

    throw new Error(
      `LLM call failed after ${this.maxRetries + 1} attempts: ${lastErr?.message}`
    );
  }

  private async callLlmStream(
    messages: ChatCompletionMessageParam[],
    toolsDef: ChatCompletionTool[] | undefined,
    _ctx: RunContext
  ): Promise<Record<string, any>> {
    const stream = await this.client.chat.completions.create({
      model: this.model,
      messages,
      tools: toolsDef,
      stream: true,
      ...this.completionParams,
    } as OpenAI.ChatCompletionCreateParams & { stream: true });

    const contentParts: string[] = [];
    const toolCallsAccum = new Map<
      number,
      { id: string; type: "function"; function: { name: string; arguments: string } }
    >();

    for await (const chunk of stream) {
      const delta = chunk.choices[0]?.delta;
      if (!delta) continue;

      if (delta.content) {
        contentParts.push(delta.content);
        await this.emit(HookEvent.TokenStream, delta.content);
      }

      if (delta.tool_calls) {
        for (const tc of delta.tool_calls) {
          const idx = tc.index;
          if (!toolCallsAccum.has(idx)) {
            toolCallsAccum.set(idx, {
              id: tc.id ?? "",
              type: "function",
              function: { name: "", arguments: "" },
            });
          }
          const entry = toolCallsAccum.get(idx)!;
          if (tc.id) entry.id = tc.id;
          if (tc.function?.name) entry.function.name += tc.function.name;
          if (tc.function?.arguments)
            entry.function.arguments += tc.function.arguments;
        }
      }
    }

    const msg: Record<string, any> = {
      role: "assistant",
      content: contentParts.join("") || null,
    };

    if (toolCallsAccum.size > 0) {
      msg.tool_calls = Array.from(toolCallsAccum.entries())
        .sort(([a], [b]) => a - b)
        .map(([, v]) => v);
    }

    await this.emit(HookEvent.LlmResponse, msg);
    return msg;
  }

  // -- Main loop -----------------------------------------------------------

  async run(
    prompt: string,
    options?: {
      messages?: ChatCompletionMessageParam[];
      contextMeta?: Record<string, any>;
    }
  ): Promise<string> {
    const ctx: RunContext = {
      agent: this,
      turn: 0,
      metadata: options?.contextMeta ?? {},
    };

    const msgs: ChatCompletionMessageParam[] = [];
    if (this.system) {
      msgs.push({ role: "system", content: this.system });
    }
    if (options?.messages) {
      msgs.push(...structuredClone(options.messages));
    }
    msgs.push({ role: "user", content: prompt });

    await this.emit(HookEvent.RunStart, msgs);

    for (let turn = 0; turn < this.maxTurns; turn++) {
      ctx.turn = turn;

      // Middleware pre
      const callMsgs = await this.runPre(structuredClone(msgs), ctx);

      // LLM call
      let assistantMsg: Record<string, any>;
      try {
        assistantMsg = await this.callLlm(
          callMsgs as ChatCompletionMessageParam[],
          ctx
        );
      } catch (err: any) {
        await this.emit(HookEvent.Error, err);
        throw err;
      }

      // Middleware post
      assistantMsg = await this.runPost(assistantMsg, ctx);
      msgs.push(assistantMsg as ChatCompletionMessageParam);

      // No tool calls → done
      const toolCalls = assistantMsg.tool_calls as
        | ChatCompletionMessageToolCall[]
        | undefined;

      if (!toolCalls?.length) {
        const final = (assistantMsg.content as string) ?? "";
        await this.emit(HookEvent.RunEnd, msgs, final);
        return final;
      }

      // Execute tool calls
      if (this.parallelToolCalls && toolCalls.length > 1) {
        const results = await Promise.all(
          toolCalls.map((tc) =>
            this.executeTool(
              tc.function.name,
              safeJsonParse(tc.function.arguments)
            )
          )
        );
        for (let i = 0; i < toolCalls.length; i++) {
          msgs.push({
            role: "tool",
            tool_call_id: toolCalls[i].id,
            content: results[i],
          });
        }
      } else {
        for (const tc of toolCalls) {
          const result = await this.executeTool(
            tc.function.name,
            safeJsonParse(tc.function.arguments)
          );
          msgs.push({
            role: "tool",
            tool_call_id: tc.id,
            content: result,
          });
        }
      }
    }

    throw new Error(`Agent exceeded maxTurns (${this.maxTurns})`);
  }
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

function safeJsonParse(s: string): Record<string, any> {
  try {
    return JSON.parse(s);
  } catch {
    return {};
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// ---------------------------------------------------------------------------
// Built-in middleware
// ---------------------------------------------------------------------------

export class TruncationMiddleware implements Middleware {
  constructor(private maxMessages = 40) {}

  async pre(
    messages: ChatCompletionMessageParam[],
    _ctx: RunContext
  ): Promise<ChatCompletionMessageParam[]> {
    if (messages.length <= this.maxMessages) return messages;
    const system = messages.filter((m) => m.role === "system");
    const rest = messages.filter((m) => m.role !== "system");
    return [...system, ...rest.slice(-(this.maxMessages - system.length))];
  }
}

export class LoggingMiddleware implements Middleware {
  async pre(
    messages: ChatCompletionMessageParam[],
    ctx: RunContext
  ): Promise<ChatCompletionMessageParam[]> {
    console.debug(`[turn ${ctx.turn}] Sending ${messages.length} messages`);
    return messages;
  }

  async post(
    message: Record<string, any>,
    ctx: RunContext
  ): Promise<Record<string, any>> {
    const tc = message.tool_calls;
    if (tc) {
      const names = tc.map((t: any) => t.function.name);
      console.debug(`[turn ${ctx.turn}] Tool calls: ${names.join(", ")}`);
    } else {
      const snippet = ((message.content as string) ?? "").slice(0, 120);
      console.debug(`[turn ${ctx.turn}] Response: ${snippet}...`);
    }
    return message;
  }
}

// ---------------------------------------------------------------------------
// Demo
// ---------------------------------------------------------------------------

async function main() {
  const add = defineTool({
    name: "add",
    description: "Add two numbers together",
    parameters: {
      type: "object",
      properties: {
        a: { type: "number", description: "First number" },
        b: { type: "number", description: "Second number" },
      },
      required: ["a", "b"],
    },
    execute: async ({ a, b }: { a: number; b: number }) => a + b,
  });

  const getWeather = defineTool({
    name: "get_weather",
    description: "Get the current weather for a city (stub)",
    parameters: {
      type: "object",
      properties: {
        city: { type: "string", description: "City name" },
      },
      required: ["city"],
    },
    execute: async ({ city }: { city: string }) => ({
      city,
      temp_f: 72,
      condition: "sunny",
    }),
  });

  const agent = new Agent({
    model: process.env.AGENT_MODEL ?? "gpt-4o",
    system: "You are a helpful assistant. Use your tools when appropriate.",
    maxTurns: 10,
    parallelToolCalls: true,
    completionParams: { temperature: 0.3 },
  });

  agent.registerTool(add);
  agent.registerTool(getWeather);

  agent.use(new LoggingMiddleware());
  agent.use(new TruncationMiddleware(30));

  agent.on(HookEvent.ToolCall, (name, args) =>
    console.log(`  🔧 ${name}(${JSON.stringify(args)})`)
  );
  agent.on(HookEvent.ToolResult, (name, res) =>
    console.log(`  ✅ ${name} → ${JSON.stringify(res)}`)
  );
  agent.on(HookEvent.TokenStream, (tok) => process.stdout.write(tok));

  const result = await agent.run(
    "What's the weather in Tokyo, and what's 17 + 38?"
  );
  console.log("\n---\n", result);
}

// Run if executed directly
main().catch(console.error);
