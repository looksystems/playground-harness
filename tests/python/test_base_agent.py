import asyncio
import pytest
from src.python.base_agent import BaseAgent, RunContext


class TestBaseAgent:
    def test_init_defaults(self):
        agent = BaseAgent(model="gpt-4")
        assert agent.model == "gpt-4"
        assert agent.max_turns == 20
        assert agent.max_retries == 2
        assert agent.stream is True

    def test_init_custom(self):
        agent = BaseAgent(
            model="claude-3-opus",
            system="You are helpful.",
            max_turns=5,
            max_retries=0,
            stream=False,
        )
        assert agent.model == "claude-3-opus"
        assert agent.system == "You are helpful."
        assert agent.max_turns == 5

    def test_build_system_prompt(self):
        agent = BaseAgent(model="gpt-4", system="Be helpful.")
        result = asyncio.run(agent._build_system_prompt("Be helpful.", None))
        assert result == "Be helpful."

    def test_run_context_creation(self):
        agent = BaseAgent(model="gpt-4")
        ctx = RunContext(agent=agent, turn=0, metadata={})
        assert ctx.agent is agent
        assert ctx.turn == 0
