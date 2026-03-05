import { BaseAgent } from "./base-agent.js";
import { HasHooks } from "./has-hooks.js";
import { HasMiddleware } from "./has-middleware.js";
import { UsesTools } from "./uses-tools.js";
import { EmitsEvents } from "./emits-events.js";

export const StandardAgent = EmitsEvents(UsesTools(HasMiddleware(HasHooks(BaseAgent))));
