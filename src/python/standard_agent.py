from src.python.base_agent import BaseAgent
from src.python.has_hooks import HasHooks
from src.python.has_middleware import HasMiddleware
from src.python.uses_tools import UsesTools


class StandardAgent(BaseAgent, HasMiddleware, HasHooks, UsesTools):
    pass
