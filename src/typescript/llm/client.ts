/**
 * LlmClient — provider-agnostic chat-completion contract for the TS harness.
 *
 * Mirrors Go's `llm.Client` interface (src/go/llm/client.go). Concrete
 * implementations (OpenAIClient, AnthropicClient) live alongside this file.
 *
 * The harness sends OpenAI-shaped messages and tool schemas (the lingua franca
 * across the four language implementations) and expects an assistant message
 * back in the same shape. Provider adapters that target non-OpenAI APIs are
 * responsible for translating in both directions.
 */

export type Role = "system" | "user" | "assistant" | "tool";

export interface ChatMessage {
  role: Role | string;
  content?: string | null;
  // Assistant turns may include tool_calls; tool turns include tool_call_id and content.
  tool_calls?: ToolCall[] | null;
  tool_call_id?: string;
  // Pass-through name field used by some providers; harmless if ignored.
  name?: string;
}

export interface ToolCall {
  id: string;
  type: "function";
  function: { name: string; arguments: string };
}

export interface ToolSchema {
  type: "function";
  function: {
    name: string;
    description?: string;
    parameters: Record<string, unknown>;
  };
}

export interface LlmRequest {
  model: string;
  messages: ChatMessage[];
  tools?: ToolSchema[];
  stream: boolean;
  /** Provider-specific pass-through options (temperature, top_p, max_tokens, …). */
  extraOptions?: Record<string, unknown>;
}

export interface AssistantMessage {
  role: "assistant";
  content: string | null;
  tool_calls: ToolCall[] | null;
}

export interface LlmClient {
  /**
   * Issue one chat completion. Honours `req.stream`: when true the
   * implementation must consume the streaming response and return the fully
   * accumulated message in OpenAI shape; when false it does a single
   * blocking call.
   *
   * Errors should propagate as thrown exceptions so the BaseAgent retry loop
   * can wrap them.
   */
  callLlm(req: LlmRequest): Promise<AssistantMessage>;
}
