import { HookEvent } from "./has-hooks.js";

export function tryEmit(target: any, event: HookEvent, ...args: any[]): void {
  if (typeof target.emit === "function") {
    void target.emit(event, ...args);
  }
}
