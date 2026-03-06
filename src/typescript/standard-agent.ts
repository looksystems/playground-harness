import { BaseAgent } from "./base-agent.js";
import { HasHooks } from "./has-hooks.js";
import { HasMiddleware } from "./has-middleware.js";
import { UsesTools } from "./uses-tools.js";
import { EmitsEvents } from "./emits-events.js";
import { HasShell } from "./has-shell.js";
import { HasSkills } from "./has-skills.js";

export const StandardAgent = HasSkills(HasShell(EmitsEvents(UsesTools(HasMiddleware(HasHooks(BaseAgent))))));
