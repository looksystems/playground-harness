import OpenAI from "openai";

export interface RunContext {
  agent: BaseAgent;
  turn: number;
  metadata: Record<string, any>;
}

export interface AgentOptions {
  model: string;
  system?: string | null;
  maxTurns?: number;
  maxRetries?: number;
  stream?: boolean;
  apiKey?: string;
  [key: string]: any;
}

export class BaseAgent {
  model: string;
  system: string | null;
  maxTurns: number;
  maxRetries: number;
  stream: boolean;
  client: OpenAI;
  extraOptions: Record<string, any>;

  constructor(options: AgentOptions) {
    this.model = options.model;
    this.system = options.system ?? null;
    this.maxTurns = options.maxTurns ?? 20;
    this.maxRetries = options.maxRetries ?? 2;
    this.stream = options.stream ?? true;
    this.client = new OpenAI({ apiKey: options.apiKey ?? "sk-placeholder" });
    const { model, system, maxTurns, maxRetries, stream, apiKey, ...rest } = options;
    this.extraOptions = rest;
  }

  async _build_system_prompt(basePrompt: string | null, context: any): Promise<string | null> {
    return basePrompt;
  }

  async _on_run_start(context: RunContext): Promise<void> {}

  async _on_run_end(context: RunContext): Promise<void> {}

  async _handle_stream(stream: AsyncIterable<any>): Promise<Record<string, any>> {
    const contentParts: string[] = [];
    const toolCallsMap: Map<number, Record<string, any>> = new Map();

    for await (const chunk of stream) {
      const delta = chunk.choices?.[0]?.delta;
      if (!delta) continue;
      if (delta.content) {
        contentParts.push(delta.content);
      }
      if (delta.tool_calls) {
        for (const tc of delta.tool_calls) {
          const idx = tc.index;
          if (!toolCallsMap.has(idx)) {
            toolCallsMap.set(idx, {
              id: tc.id || "",
              type: "function",
              function: { name: "", arguments: "" },
            });
          }
          const entry = toolCallsMap.get(idx)!;
          if (tc.id) entry.id = tc.id;
          if (tc.function) {
            if (tc.function.name) entry.function.name += tc.function.name;
            if (tc.function.arguments) entry.function.arguments += tc.function.arguments;
          }
        }
      }
    }

    const message: Record<string, any> = { role: "assistant" };
    if (contentParts.length > 0) {
      message.content = contentParts.join("");
    }
    if (toolCallsMap.size > 0) {
      const sorted = [...toolCallsMap.keys()].sort((a, b) => a - b);
      message.tool_calls = sorted.map((i) => toolCallsMap.get(i)!);
    }
    return message;
  }

  async _handle_response(response: Record<string, any>, context: RunContext): Promise<Record<string, any> | null> {
    return response;
  }

  async _call_llm(messages: Record<string, any>[], toolsSchema?: Record<string, any>[]): Promise<Record<string, any>> {
    for (let attempt = 0; attempt <= this.maxRetries; attempt++) {
      try {
        const params: any = {
          model: this.model,
          messages,
          ...this.extraOptions,
        };
        if (toolsSchema && toolsSchema.length > 0) {
          params.tools = toolsSchema;
        }

        if (this.stream) {
          params.stream = true;
          const resp = await this.client.chat.completions.create(params);
          return await this._handle_stream(resp as any);
        } else {
          const resp: any = await this.client.chat.completions.create(params);
          const msg = resp.choices[0].message;
          return {
            role: "assistant",
            content: msg.content,
            tool_calls: msg.tool_calls ?? null,
          };
        }
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
      const assistantMsg = await this._call_llm(messages);
      const result = await this._handle_response(assistantMsg, context);

      if (result === null) {
        messages.push(assistantMsg);
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
