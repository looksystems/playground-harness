import { describe, it, expect } from "vitest";
import {
  EventStreamParser,
  EventType,
  StreamConfig,
} from "../../src/typescript/event-stream-parser.js";
import { ParsedEvent } from "../../src/typescript/message-bus.js";

async function* tokenStream(text: string): AsyncGenerator<string> {
  for (const char of text) {
    yield char;
  }
}

async function collectText(
  parser: EventStreamParser,
  stream: AsyncIterable<string>
): Promise<string> {
  const chunks: string[] = [];
  for await (const chunk of parser.wrap(stream)) {
    chunks.push(chunk);
  }
  return chunks.join("");
}

async function collectStreamField(
  stream: AsyncIterable<string>
): Promise<string> {
  const parts: string[] = [];
  for await (const token of stream) {
    parts.push(token);
  }
  return parts.join("");
}

describe("EventStreamParser", () => {
  it("plain text passes through", async () => {
    const parser = new EventStreamParser([]);
    const text = "Hello world, no events here.";
    const result = await collectText(parser, tokenStream(text));
    expect(result).toBe(text);
  });

  it("buffered event extraction", async () => {
    const eventType: EventType = {
      name: "log_entry",
      description: "A log entry",
      schema: { data: { level: "string", message: "string" } },
    };
    const parser = new EventStreamParser([eventType]);
    const events: ParsedEvent[] = [];
    parser.on_event((e) => events.push(e));

    const text =
      "Before.\n---event\ntype: log_entry\ndata:\n  level: info\n  message: something happened\n---\nAfter.";
    const result = await collectText(parser, tokenStream(text));

    expect(result).toContain("Before.");
    expect(result).toContain("After.");
    expect(result).not.toContain("---event");
    expect(events.length).toBe(1);
    expect(events[0].type).toBe("log_entry");
    expect(events[0].data.data.level).toBe("info");
  });

  it("streaming event", async () => {
    const eventType: EventType = {
      name: "user_response",
      description: "Response to user",
      schema: { data: { message: "string" } },
      streaming: { mode: "streaming", streamFields: ["data.message"] },
    };
    const parser = new EventStreamParser([eventType]);
    const events: ParsedEvent[] = [];
    parser.on_event((e) => events.push(e));

    const text =
      "Hi.\n---event\ntype: user_response\ndata:\n  message: Hello there friend\n---\nDone.";
    const result = await collectText(parser, tokenStream(text));

    expect(events.length).toBe(1);
    expect(events[0].stream).toBeDefined();
    const streamed = await collectStreamField(events[0].stream!);
    expect(streamed).toContain("Hello there friend");
    expect(result).toContain("Hi.");
    expect(result).toContain("Done.");
  });

  it("unrecognized event passes as text", async () => {
    const parser = new EventStreamParser([]);
    const text =
      "Before.\n---event\ntype: unknown_thing\ndata:\n  x: 1\n---\nAfter.";
    const result = await collectText(parser, tokenStream(text));
    expect(result).toContain("---event");
    expect(result).toContain("unknown_thing");
  });

  it("malformed yaml passes as text", async () => {
    const eventType: EventType = {
      name: "test",
      description: "test",
      schema: {},
    };
    const parser = new EventStreamParser([eventType]);
    const text =
      "Before.\n---event\n: this is not valid yaml [\n---\nAfter.";
    const result = await collectText(parser, tokenStream(text));
    expect(result).toContain("Before.");
    expect(result).toContain("After.");
  });

  it("incomplete event at end of stream", async () => {
    const eventType: EventType = {
      name: "test",
      description: "test",
      schema: {},
    };
    const parser = new EventStreamParser([eventType]);
    const text = "Before.\n---event\ntype: test\ndata:\n  x: 1";
    const result = await collectText(parser, tokenStream(text));
    expect(result).toContain("Before.");
    expect(result).toContain("---event");
  });

  it("multiple events", async () => {
    const eventType: EventType = {
      name: "log",
      description: "A log",
      schema: { data: { msg: "string" } },
    };
    const parser = new EventStreamParser([eventType]);
    const events: ParsedEvent[] = [];
    parser.on_event((e) => events.push(e));

    const text =
      "A\n---event\ntype: log\ndata:\n  msg: first\n---\nB\n---event\ntype: log\ndata:\n  msg: second\n---\nC";
    const result = await collectText(parser, tokenStream(text));

    expect(events.length).toBe(2);
    expect(events[0].data.data.msg).toBe("first");
    expect(events[1].data.data.msg).toBe("second");
  });
});
