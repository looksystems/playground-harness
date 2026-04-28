import { OpenAIClient } from "./llm/openai.js";
import { AnthropicClient } from "./llm/anthropic.js";
import type { AssistantMessage, ChatMessage, LlmClient, ToolSchema } from "./llm/client.js";

export interface RunContext {
  agent: BaseAgent;
  turn: number;
  metadata: Record<string, any>;
}

export type Provider = "openai" | "anthropic";

export interface AgentOptions {
  model: string;
  system?: string | null;
  maxTurns?: number;
  maxRetries?: number;
  stream?: boolean;
  /** Pre-built LlmClient. Wins over `provider` if both supplied. */
  client?: LlmClient;
  /** Build a default client for this provider. Default: "openai". */
  provider?: Provider;
  /** Forwarded to the default client (OpenAI or Anthropic) when `client` is omitted. */
  apiKey?: string;
  /** Forwarded to the default client when `client` is omitted. */
  baseURL?: string;
  [key: string]: any;
}

export class BaseAgent {
  model: string;
  system: string | null;
  maxTurns: number;
  maxRetries: number;
  stream: boolean;
  llmClient: LlmClient;
  extraOptions: Record<string, any>;

  constructor(options: AgentOptions) {
    this.model = options.model;
    this.system = options.system ?? null;
    this.maxTurns = options.maxTurns ?? 20;
    this.maxRetries = options.maxRetries ?? 2;
    this.stream = options.stream ?? true;
    this.llmClient = options.client ?? buildDefaultClient(options);
    const { model, system, maxTurns, maxRetries, stream, apiKey, baseURL, client, provider, ...rest } = options;
    this.extraOptions = rest;
  }

  async _build_system_prompt(basePrompt: string | null, context: any): Promise<string | null> {
    return basePrompt;
  }

  async _on_run_start(context: RunContext): Promise<void> {}

  async _on_run_end(context: RunContext): Promise<void> {}

  async _handle_response(response: Record<string, any>, context: RunContext): Promise<Record<string, any> | null> {
    return response;
  }

  async _call_llm(messages: ChatMessage[], toolsSchema?: ToolSchema[]): Promise<AssistantMessage> {
    for (let attempt = 0; attempt <= this.maxRetries; attempt++) {
      try {
        return await this.llmClient.callLlm({
          model: this.model,
          messages,
          tools: toolsSchema && toolsSchema.length > 0 ? toolsSchema : undefined,
          stream: this.stream,
          extraOptions: this.extraOptions,
        });
      } catch (e: any) {
        if (attempt < this.maxRetries) {
          const delay = Math.min(2 ** attempt, 10);
          console.warn(`LLM call failed (attempt ${attempt + 1}): ${e}`);
          await new Promise((r) => setTimeout(r, delay * 1000));
        } else {
          throw new Error(`LLM call failed after ${this.maxRetries + 1} attempts: ${e}`);
        }
      }
    }
    throw new Error("Unreachable");
  }

  async run(messages: Record<string, any>[], kwargs: Record<string, any> = {}): Promise<string> {
    messages = structuredClone(messages);
    const systemPrompt = await this._build_system_prompt(this.system, kwargs);

    if (systemPrompt) {
      if (messages.length > 0 && messages[0].role === "system") {
        messages[0].content = systemPrompt;
      } else {
        messages.unshift({ role: "system", content: systemPrompt });
      }
    }

    const context: RunContext = { agent: this, turn: 0, metadata: {} };
    await this._on_run_start(context);

    for (let turn = 0; turn < this.maxTurns; turn++) {
      context.turn = turn;
      const assistantMsg = await this._call_llm(messages as ChatMessage[]);
      const result = await this._handle_response(assistantMsg as any, context);

      if (result === null) {
        messages.push(assistantMsg as any);
        const content = assistantMsg.content ?? "";
        await this._on_run_end(context);
        return content;
      }

      messages.push(result);
      if (!result.tool_calls) {
        await this._on_run_end(context);
        return result.content ?? "";
      }
    }

    await this._on_run_end(context);
    return messages[messages.length - 1]?.content ?? "";
  }
}

function buildDefaultClient(options: AgentOptions): LlmClient {
  const provider = options.provider ?? "openai";
  if (provider === "anthropic") {
    return new AnthropicClient({ apiKey: options.apiKey, baseURL: options.baseURL });
  }
  return new OpenAIClient({ apiKey: options.apiKey, baseURL: options.baseURL });
}
