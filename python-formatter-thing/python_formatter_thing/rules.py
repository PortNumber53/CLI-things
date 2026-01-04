from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Literal


Severity = Literal["error", "warning", "info"]


@dataclass(frozen=True)
class MatchContext:
    in_function: bool | None = None
    not_in_blocks: tuple[str, ...] = ()


@dataclass(frozen=True)
class MatchSpec:
    context: MatchContext
    statement_token_regex: str


@dataclass(frozen=True)
class AutoFixSpec:
    action: str
    dedupe: bool = True


@dataclass(frozen=True)
class Rule:
    id: str
    enabled: bool
    severity: Severity
    description: str
    match: MatchSpec
    autofix: AutoFixSpec | None = None


@dataclass(frozen=True)
class RuleSet:
    version: int
    rules: tuple[Rule, ...]


class RulesFormatError(ValueError):
    pass


def _expect(d: dict[str, Any], key: str, typ: type, where: str) -> Any:
    if key not in d:
        raise RulesFormatError(f"Missing key '{key}' in {where}")
    v = d[key]
    if not isinstance(v, typ):
        raise RulesFormatError(f"Key '{key}' in {where} must be {typ.__name__}")
    return v


def load_rules(path: Path) -> RuleSet:
    raw = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(raw, dict):
        raise RulesFormatError("Rules file must be a JSON object")

    version = _expect(raw, "version", int, "rules file")
    raw_rules = _expect(raw, "rules", list, "rules file")

    rules: list[Rule] = []
    for i, rr in enumerate(raw_rules):
        where = f"rules[{i}]"
        if not isinstance(rr, dict):
            raise RulesFormatError(f"{where} must be an object")

        rid = _expect(rr, "id", str, where)
        enabled = bool(rr.get("enabled", True))
        sev = rr.get("severity", "error")
        if sev not in ("error", "warning", "info"):
            raise RulesFormatError(f"{where}.severity must be one of error|warning|info")
        desc = _expect(rr, "description", str, where)

        match = _expect(rr, "match", dict, where)
        ctx = match.get("context", {})
        if not isinstance(ctx, dict):
            raise RulesFormatError(f"{where}.match.context must be an object")
        in_fn = ctx.get("in_function", None)
        if in_fn is not None and not isinstance(in_fn, bool):
            raise RulesFormatError(f"{where}.match.context.in_function must be boolean or null")
        not_in_blocks = ctx.get("not_in_blocks", [])
        if not isinstance(not_in_blocks, list) or not all(isinstance(x, str) for x in not_in_blocks):
            raise RulesFormatError(f"{where}.match.context.not_in_blocks must be a list of strings")
        stmt_re = _expect(match, "statement_token_regex", str, f"{where}.match")

        raw_autofix = rr.get("autofix", None)
        autofix: AutoFixSpec | None
        if raw_autofix is None:
            autofix = None
        else:
            if not isinstance(raw_autofix, dict):
                raise RulesFormatError(f"{where}.autofix must be an object or null")
            action = _expect(raw_autofix, "action", str, f"{where}.autofix")
            dedupe = bool(raw_autofix.get("dedupe", True))
            autofix = AutoFixSpec(action=action, dedupe=dedupe)

        rules.append(
            Rule(
                id=rid,
                enabled=enabled,
                severity=sev,  # type: ignore[arg-type]
                description=desc,
                match=MatchSpec(
                    context=MatchContext(
                        in_function=in_fn,
                        not_in_blocks=tuple(not_in_blocks),
                    ),
                    statement_token_regex=stmt_re,
                ),
                autofix=autofix,
            )
        )

    return RuleSet(version=version, rules=tuple(rules))


