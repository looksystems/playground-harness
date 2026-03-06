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
  SKILL_MOUNT = "skill_mount",
  SKILL_UNMOUNT = "skill_unmount",
  SKILL_SETUP = "skill_setup",
  SKILL_TEARDOWN = "skill_teardown",
  HOOK_ERROR = "hook_error",
}

export function HasHooks<TBase extends Constructor>(Base: TBase) {
  return class extends Base {
    hooks: Map<HookEvent, Array<(...args: any[]) => any>> = new Map();

    on(event: HookEvent, callback: (...args: any[]) => any): () => void {
      if (!this.hooks.has(event)) {
        this.hooks.set(event, []);
      }
      this.hooks.get(event)!.push(callback);
      return () => {
        const cbs = this.hooks.get(event);
        if (cbs) {
          const idx = cbs.indexOf(callback);
          if (idx !== -1) cbs.splice(idx, 1);
        }
      };
    }

    removeHook(event: HookEvent, callback: (...args: any[]) => any): this {
      const cbs = this.hooks.get(event);
      if (cbs) {
        const idx = cbs.indexOf(callback);
        if (idx !== -1) cbs.splice(idx, 1);
      }
      return this;
    }

    async emit(event: HookEvent, ...args: any[]): Promise<void> {
      const callbacks = this.hooks.get(event);
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
          if (event !== HookEvent.HOOK_ERROR) {
            void this.emit(HookEvent.HOOK_ERROR, event, r.reason);
          }
        }
      }
    }
  };
}
