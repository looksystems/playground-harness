import { EventType, StreamConfig } from "./event-stream-parser.js";
import { MessageBus } from "./message-bus.js";

export { EventType, StreamConfig };

type Constructor<T = {}> = new (...args: any[]) => T;

export function EmitsEvents<TBase extends Constructor>(Base: TBase) {
  return class extends Base {
    _event_registry: Map<string, EventType> = new Map();
    default_events: string[] = [];
    _bus: MessageBus = new MessageBus();

    register_event(eventType: EventType): void {
      this._event_registry.set(eventType.name, eventType);
    }

    _resolve_active_events(events?: (string | EventType)[]): EventType[] {
      if (events === undefined) {
        return this.default_events
          .filter((name) => this._event_registry.has(name))
          .map((name) => this._event_registry.get(name)!);
      }
      const result: EventType[] = [];
      for (const item of events) {
        if (typeof item === "string") {
          const et = this._event_registry.get(item);
          if (et) result.push(et);
        } else {
          result.push(item);
        }
      }
      return result;
    }

    _build_event_prompt(eventTypes: EventType[]): string {
      if (eventTypes.length === 0) return "";
      const sections: string[] = [];
      sections.push("# Event Emission");
      sections.push("");
      sections.push(
        "You can emit structured events inline in your response using the following format:"
      );
      sections.push("");

      for (const et of eventTypes) {
        sections.push(`## Event: ${et.name}`);
        sections.push(`Description: ${et.description}`);
        sections.push("Format:");
        sections.push("```");
        sections.push("---event");
        sections.push(`type: ${et.name}`);
        if (et.schema) {
          for (const [key, val] of Object.entries(et.schema)) {
            if (typeof val === "object" && val !== null && !Array.isArray(val)) {
              sections.push(`${key}:`);
              for (const [k, v] of Object.entries(val)) {
                sections.push(`  ${k}: <${v}>`);
              }
            } else {
              sections.push(`${key}: <${val}>`);
            }
          }
        }
        sections.push("---");
        sections.push("```");
        if (et.instructions) {
          sections.push(et.instructions);
        }
        sections.push("");
      }

      return sections.join("\n");
    }

    get bus(): MessageBus {
      return this._bus;
    }

    set bus(value: MessageBus) {
      this._bus = value;
    }
  };
}
