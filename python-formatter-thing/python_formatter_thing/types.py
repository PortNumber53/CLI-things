from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Violation:
    rule_id: str
    message: str
    path: str
    line: int
    col: int
    severity: str


