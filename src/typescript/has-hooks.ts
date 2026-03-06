type Constructor<T = {}> = new (...args: any[]) => T;

export enum HookEvent {
  RUN_START = "run_start",
  RUN_END = "run_end",
  LLM_REQUEST = "llm_request",
  LLM_RESPONSE = "llm_response",
  TOOL_CALL = "tool_call",
  TOOL_RESULT = "tool_result",
  TOOL_ERROR = "tool_error",
  RETRY = "retry",
  TOKEN_STREAM = "token_stream",
  ERROR = "error",
  SHELL_CALL = "shell_call",
  SHELL_RESULT = "shell_result",
  SHELL_NOT_FOUND = "shell_not_found",
  SHELL_CWD = "shell_cwd",
  TOOL_REGISTER = "tool_register",
  TOOL_UNREGISTER = "tool_unregister",
  COMMAND_REGISTER = "command_register",
  COMMAND_UNREGISTER = "command_unregister",
  SLASH_COMMAND_REGISTER = "slash_command_register",
  SLASH_COMMAND_UNREGISTER = "slash_command_unregister",
  SLASH_COMMAND_CALL = "slash_command_call",
  SLASH_COMMAND_RESULT = "slash_command_result",
}

export function HasHooks<TBase extends Constructor>(Base: TBase) {
  return class extends Base {
    _hooks: Map<HookEvent, Array<(...args: any[]) => any>> = new Map();

    on(event: HookEvent, callback: (...args: any[]) => any): void {
      if (!this._hooks.has(event)) {
        this._hooks.set(event, []);
      }
      this._hooks.get(event)!.push(callback);
    }

    async _emit(event: HookEvent, ...args: any[]): Promise<void> {
      const callbacks = this._hooks.get(event);
      if (!callbacks || callbacks.length === 0) {
        return;
      }
      const results = await Promise.allSettled(
        callbacks.map((cb) => {
          try {
            return Promise.resolve(cb(...args));
          } catch (e) {
            return Promise.reject(e);
          }
        })
      );
      for (const r of results) {
        if (r.status === "rejected") {
          console.warn(`Hook ${event} error: ${r.reason}`);
        }
      }
    }
  };
}
