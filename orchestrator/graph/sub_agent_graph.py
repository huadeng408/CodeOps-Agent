from __future__ import annotations

from dataclasses import dataclass

from .main_graph import MainGraph


@dataclass(slots=True)
class SubAgentGraph:
    name: str = "sub-agent"

    def run(self) -> MainGraph:
        return MainGraph(name=self.name)


def build_sub_agent_graph() -> SubAgentGraph:
    return SubAgentGraph()
