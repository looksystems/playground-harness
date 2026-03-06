from src.python.base_agent import BaseAgent
from src.python.has_hooks import HasHooks
from src.python.has_middleware import HasMiddleware
from src.python.uses_tools import UsesTools
from src.python.emits_events import EmitsEvents
from src.python.has_shell import HasShell
from src.python.has_skills import HasSkills


class StandardAgent(BaseAgent, HasMiddleware, HasHooks, UsesTools, EmitsEvents, HasShell, HasSkills):
    pass
