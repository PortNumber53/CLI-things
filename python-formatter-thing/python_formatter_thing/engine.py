from __future__ import annotations

import re
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable

from .fixers import apply_fixes
from .rules import RuleSet
from .token_context import iter_statements
from .types import Violation


@dataclass
class CheckResults:
    violations: list[Violation]
    files_checked: int


DEFAULT_EXCLUDE_DIRS = {
    ".git",
    ".hg",
    ".svn",
    ".mypy_cache",
    ".pytest_cache",
    ".ruff_cache",
    ".tox",
    ".venv",
    "venv",
    "__pycache__",
    "node_modules",
    "dist",
    "build",
}


def _iter_files(paths: Iterable[Path], exts: tuple[str, ...]) -> Iterable[Path]:
    for p in paths:
        if p.is_dir():
            for fp in p.rglob("*"):
                if fp.is_file() and fp.suffix in exts:
                    if any(part in DEFAULT_EXCLUDE_DIRS for part in fp.parts):
                        continue
                    yield fp
        else:
            if p.is_file():
                yield p


def check_paths(paths: list[Path], *, ruleset: RuleSet, exts: tuple[str, ...]) -> CheckResults:
    violations: list[Violation] = []
    files_checked = 0

    compiled: list[tuple[str, re.Pattern[str], object]] = []
    enabled_rules = [r for r in ruleset.rules if r.enabled]
    for r in enabled_rules:
        try:
            compiled.append((r.id, re.compile(r.match.statement_token_regex), r))
        except re.error as e:
            raise ValueError(f"Invalid regex for rule {r.id}: {e}") from e

    for fp in _iter_files(paths, exts):
        try:
            src = fp.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            # Best-effort: skip non-utf8 python files.
            continue
        files_checked += 1
        stmts = iter_statements(src)

        for st in stmts:
            for _rid, rx, rule in compiled:
                ctx = rule.match.context
                if ctx.in_function is not None and st.context.in_function != ctx.in_function:
                    continue
                if ctx.not_in_blocks and st.context.has_any_block(ctx.not_in_blocks):
                    continue
                if not rx.search(st.token_string):
                    continue

                msg = rule.description
                violations.append(
                    Violation(
                        rule_id=rule.id,
                        message=msg,
                        path=str(fp),
                        line=st.start_line,
                        col=st.start_col + 1,
                        severity=rule.severity,
                    )
                )

    return CheckResults(violations=violations, files_checked=files_checked)


def fix_paths(
    paths: list[Path], *, ruleset: RuleSet, exts: tuple[str, ...], unsafe: bool = False
) -> None:
    for fp in _iter_files(paths, exts):
        try:
            src = fp.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        try:
            out = apply_fixes(src, ruleset=ruleset, unsafe=unsafe)
        except SyntaxError as e:
            sys.stderr.write(f"Skipping (syntax error): {fp}: {e}\n")
            continue
        if out != src:
            fp.write_text(out, encoding="utf-8")


