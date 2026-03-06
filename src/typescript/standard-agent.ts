import { BaseAgent } from "./base-agent.js";
import { HasHooks } from "./has-hooks.js";
import { HasMiddleware } from "./has-middleware.js";
import { UsesTools } from "./uses-tools.js";
import { EmitsEvents } from "./emits-events.js";
import { HasShell } from "./has-shell.js";
import { HasSkills } from "./has-skills.js";
import { AgentBuilder } from "./agent-builder.js";

export const StandardAgent = HasSkills(HasShell(EmitsEvents(UsesTools(HasMiddleware(HasHooks(BaseAgent))))));

/** Static helper: returns a fluent AgentBuilder that produces a StandardAgent. */
(StandardAgent as any).build = (model: string) => new AgentBuilder(model);
