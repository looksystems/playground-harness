import { Middleware } from "./has-middleware.js";

export class SlashCommandMiddleware implements Middleware {
  pre(
    messages: Record<string, any>[],
    context: any
  ): Record<string, any>[] {
    if (!messages.length) return messages;
    const last = messages[messages.length - 1];
    if (last.role !== "user") return messages;
    const content = last.content;
    if (typeof content !== "string" || !content.startsWith("/")) return messages;
    const agent = context.agent;
    if (!agent || typeof agent.interceptSlashCommand !== "function")
      return messages;
    const result = agent.interceptSlashCommand(content);
    if (!result) return messages;
    const { name, args } = result;
    const output = agent.executeSlashCommand(name, args);
    const newMessages = [...messages];
    newMessages[newMessages.length - 1] = {
      ...last,
      content: `[Slash command /${name} result]: ${output}`,
    };
    return newMessages;
  }
}
