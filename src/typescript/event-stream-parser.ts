import YAML from "yaml";
import { ParsedEvent } from "./message-bus.js";

export interface StreamConfig {
  mode: "buffered" | "streaming";
  streamFields: string[];
}

export interface StructuredEvent {
  name: string;
  description: string;
  schema: Record<string, any>;
  instructions?: string;
  streaming?: StreamConfig;
}

const EVENT_START_DELIMITER = "---event";
const EVENT_END_DELIMITER = "---";

type ParserState = "TEXT" | "EVENT_BODY" | "STREAMING";

interface Channel<T> {
  [Symbol.asyncIterator](): AsyncIterableIterator<T>;
  push(value: T): void;
  close(): void;
}

function createChannel<T>(): Channel<T> {
  const buffer: (T | null)[] = [];
  let resolve: ((value: IteratorResult<T>) => void) | null = null;
  let done = false;

  return {
    [Symbol.asyncIterator](): AsyncIterableIterator<T> {
      return {
        [Symbol.asyncIterator]() { return this; },
        next(): Promise<IteratorResult<T>> {
          if (buffer.length > 0) {
            const item = buffer.shift()!;
            if (item === null) {
              done = true;
              return Promise.resolve({ value: undefined as any, done: true });
            }
            return Promise.resolve({ value: item, done: false });
          }
          if (done) {
            return Promise.resolve({ value: undefined as any, done: true });
          }
          return new Promise<IteratorResult<T>>((r) => {
            resolve = r;
          });
        },
      };
    },
    push(value: T): void {
      if (resolve) {
        const r = resolve;
        resolve = null;
        r({ value, done: false });
      } else {
        buffer.push(value);
      }
    },
    close(): void {
      if (resolve) {
        const r = resolve;
        resolve = null;
        done = true;
        r({ value: undefined as any, done: true });
      } else {
        buffer.push(null);
      }
    },
  };
}

export type EventCallback = (event: ParsedEvent) => void;

export class EventStreamParser {
  private _eventTypes: Map<string, StructuredEvent>;
  private _callbacks: EventCallback[] = [];

  constructor(eventTypes: StructuredEvent[]) {
    this._eventTypes = new Map(eventTypes.map((et) => [et.name, et]));
  }

  on_event(callback: EventCallback): void {
    this._callbacks.push(callback);
  }

  private _fireEvent(event: ParsedEvent): void {
    for (const cb of this._callbacks) {
      try {
        cb(event);
      } catch (e) {
        console.warn(`Event callback error: ${e}`);
      }
    }
  }

  async *wrap(tokenStream: AsyncIterable<string>): AsyncGenerator<string> {
    let state: ParserState = "TEXT";
    let lineBuffer = "";
    let eventLines: string[] = [];
    let streamChannel: Channel<string> | null = null;

    for await (const token of tokenStream) {
      lineBuffer += token;

      while (lineBuffer.includes("\n")) {
        const idx = lineBuffer.indexOf("\n");
        const line = lineBuffer.slice(0, idx);
        lineBuffer = lineBuffer.slice(idx + 1);

        if (state === "TEXT") {
          if (line.trim() === EVENT_START_DELIMITER) {
            state = "EVENT_BODY";
            eventLines = [];
          } else {
            yield line + "\n";
          }
        } else if (state === "EVENT_BODY") {
          if (line.trim() === EVENT_END_DELIMITER) {
            const handled = this._finalizeEvent(eventLines);
            if (!handled) {
              yield EVENT_START_DELIMITER + "\n";
              for (const el of eventLines) {
                yield el + "\n";
              }
              yield EVENT_END_DELIMITER + "\n";
            }
            state = "TEXT";
            eventLines = [];
          } else {
            eventLines.push(line);
            const parsed = this._tryDetectStreaming(eventLines);
            if (parsed !== null) {
              streamChannel = parsed.channel;
              state = "STREAMING";
            }
          }
        } else if (state === "STREAMING") {
          if (line.trim() === EVENT_END_DELIMITER) {
            streamChannel!.close();
            streamChannel = null;
            state = "TEXT";
          } else {
            streamChannel!.push(line + "\n");
          }
        }
      }
    }

    // Handle remaining content at end of stream
    if (lineBuffer) {
      if (state === "TEXT") {
        yield lineBuffer;
      } else if (state === "EVENT_BODY") {
        yield EVENT_START_DELIMITER + "\n";
        yield eventLines.join("\n") + "\n";
        if (lineBuffer.trim()) {
          yield lineBuffer;
        }
      } else if (state === "STREAMING") {
        if (streamChannel !== null) {
          if (lineBuffer.trim()) {
            streamChannel.push(lineBuffer);
          }
          streamChannel.close();
        }
      }
    } else if (state === "EVENT_BODY") {
      yield EVENT_START_DELIMITER + "\n";
      yield eventLines.join("\n");
    } else if (state === "STREAMING") {
      if (streamChannel !== null) {
        streamChannel.close();
      }
    }
  }

  private _tryDetectStreaming(
    lines: string[]
  ): { eventName: string; data: Record<string, any>; channel: Channel<string> } | null {
    try {
      const raw = lines.join("\n");
      const data = YAML.parse(raw);
      if (typeof data !== "object" || data === null || !("type" in data)) {
        return null;
      }
    } catch {
      return null;
    }

    const raw = lines.join("\n");
    const data = YAML.parse(raw);
    const eventName = data.type;
    const et = this._eventTypes.get(eventName);
    if (!et || !et.streaming || et.streaming.mode !== "streaming") {
      return null;
    }

    for (const sf of et.streaming.streamFields) {
      const parts = sf.split(".");
      let obj: any = data;
      for (let i = 0; i < parts.length - 1; i++) {
        if (typeof obj === "object" && obj !== null && parts[i] in obj) {
          obj = obj[parts[i]];
        } else {
          return null;
        }
      }
      const lastKey = parts[parts.length - 1];
      if (typeof obj === "object" && obj !== null && lastKey in obj) {
        const channel = createChannel<string>();
        const initialValue = String(obj[lastKey]);

        // Build an async iterable that yields the initial value then channel content
        const streamIter: AsyncIterable<string> = {
          [Symbol.asyncIterator](): AsyncIterableIterator<string> {
            let yieldedInitial = false;
            const channelIter = channel[Symbol.asyncIterator]();
            return {
              [Symbol.asyncIterator]() { return this; },
              async next(): Promise<IteratorResult<string>> {
                if (!yieldedInitial) {
                  yieldedInitial = true;
                  if (initialValue) {
                    return { value: initialValue, done: false };
                  }
                }
                return channelIter.next();
              },
            };
          },
        };

        const event: ParsedEvent = {
          type: eventName,
          data,
          stream: streamIter,
        };
        this._fireEvent(event);
        return { eventName, data, channel };
      }
    }

    return null;
  }

  private _finalizeEvent(lines: string[]): boolean {
    const raw = lines.join("\n");
    let data: any;
    try {
      data = YAML.parse(raw);
    } catch {
      console.warn("Malformed event YAML");
      return false;
    }

    if (typeof data !== "object" || data === null || !("type" in data)) {
      console.warn("Event missing 'type' field");
      return false;
    }

    const eventName = data.type;
    if (!this._eventTypes.has(eventName)) {
      return false;
    }

    const event: ParsedEvent = { type: eventName, data, raw };
    this._fireEvent(event);
    return true;
  }
}
