import { StructuredEvent, StreamConfig } from "./event-stream-parser.js";
import { MessageBus } from "./message-bus.js";

export { StructuredEvent, StreamConfig };

type Constructor<T = {}> = new (...args: any[]) => T;

export function EmitsEvents<TBase extends Constructor>(Base: TBase) {
  return class extends Base {
    eventRegistry: Map<string, StructuredEvent> = new Map();
    defaultEvents: string[] = [];
    private _bus: MessageBus = new MessageBus();

    registerEvent(eventType: StructuredEvent): void {
      this.eventRegistry.set(eventType.name, eventType);
    }

    resolveActiveEvents(events?: (string | StructuredEvent)[]): StructuredEvent[] {
      if (events === undefined) {
        return this.defaultEvents
          .filter((name) => this.eventRegistry.has(name))
          .map((name) => this.eventRegistry.get(name)!);
      }
      const result: StructuredEvent[] = [];
      for (const item of events) {
        if (typeof item === "string") {
          const et = this.eventRegistry.get(item);
          if (et) result.push(et);
        } else {
          result.push(item);
        }
      }
      return result;
    }

    buildEventPrompt(eventTypes: StructuredEvent[]): string {
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
