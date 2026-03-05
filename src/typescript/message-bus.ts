export interface ParsedEvent {
  type: string;
  data: Record<string, any>;
  stream?: AsyncIterable<string>;
  raw?: string;
}

export type EventHandler = (event: ParsedEvent, bus: MessageBus) => Promise<void>;

export class MessageBus {
  private _handlers: Map<string, EventHandler[]> = new Map();
  private _maxDepth: number;
  private _depth = 0;

  constructor(maxDepth = 10) {
    this._maxDepth = maxDepth;
  }

  subscribe(eventType: string, handler: EventHandler): void {
    if (!this._handlers.has(eventType)) {
      this._handlers.set(eventType, []);
    }
    this._handlers.get(eventType)!.push(handler);
  }

  async publish(event: ParsedEvent): Promise<void> {
    if (this._depth >= this._maxDepth) {
      console.warn(`Max publish depth ${this._maxDepth} reached, dropping event: ${event.type}`);
      return;
    }

    const handlers: EventHandler[] = [
      ...(this._handlers.get(event.type) ?? []),
      ...(this._handlers.get("*") ?? []),
    ];

    if (handlers.length === 0) {
      return;
    }

    this._depth++;
    try {
      const results = await Promise.allSettled(
        handlers.map((h) => h(event, this))
      );
      for (const r of results) {
        if (r.status === "rejected") {
          console.warn(`Handler error for ${event.type}: ${r.reason}`);
        }
      }
    } finally {
      this._depth--;
    }
  }
}
