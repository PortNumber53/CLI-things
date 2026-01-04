from __future__ import annotations

import ast
import textwrap
from dataclasses import dataclass
from typing import Iterable

from .rules import RuleSet
from .token_context import compute_insertion_line_for_imports


@dataclass(frozen=True)
class _ImportSpan:
    lineno: int
    end_lineno: int
    text: str


class _ImportCollector(ast.NodeVisitor):
    def __init__(self) -> None:
        self.in_function = 0
        self.in_try = 0
        self.offending: list[ast.stmt] = []
        self.module_docstring_end: int | None = None

    def visit_Module(self, node: ast.Module) -> None:  # noqa: N802
        if node.body and isinstance(node.body[0], ast.Expr):
            v = node.body[0].value
            if isinstance(v, ast.Constant) and isinstance(v.value, str):
                # end_lineno is available on Python 3.8+, guarded by fallback
                self.module_docstring_end = getattr(node.body[0], "end_lineno", node.body[0].lineno)
        self.generic_visit(node)

    def visit_FunctionDef(self, node: ast.FunctionDef) -> None:  # noqa: N802
        self.in_function += 1
        self.generic_visit(node)
        self.in_function -= 1

    def visit_AsyncFunctionDef(self, node: ast.AsyncFunctionDef) -> None:  # noqa: N802
        self.in_function += 1
        self.generic_visit(node)
        self.in_function -= 1

    def visit_Try(self, node: ast.Try) -> None:  # noqa: N802
        self.in_try += 1
        self.generic_visit(node)
        self.in_try -= 1

    def visit_Import(self, node: ast.Import) -> None:  # noqa: N802
        if self.in_function > 0 and self.in_try == 0:
            self.offending.append(node)

    def visit_ImportFrom(self, node: ast.ImportFrom) -> None:  # noqa: N802
        if self.in_function > 0 and self.in_try == 0:
            self.offending.append(node)


def apply_fixes(source: str, *, ruleset: RuleSet, unsafe: bool = False) -> str:
    # Only implement known fix actions for enabled rules.
    actions: list[tuple[str, bool]] = []
    seen_actions: set[str] = set()
    for r in ruleset.rules:
        if not r.enabled or r.autofix is None:
            continue
        if r.autofix.action in seen_actions:
            continue
        seen_actions.add(r.autofix.action)
        actions.append((r.autofix.action, r.autofix.dedupe))

    out = source
    for action, dedupe in actions:
        if action == "move_imports_to_top":
            out = _fix_move_imports_to_top(out, dedupe=dedupe)
        else:
            if not unsafe:
                # Unknown action: skip by default.
                continue
    return out


def _fix_move_imports_to_top(source: str, *, dedupe: bool) -> str:
    tree = ast.parse(source)
    collector = _ImportCollector()
    collector.visit(tree)
    if not collector.offending:
        return source

    lines = source.splitlines(keepends=True)
    # Compute exact spans using lineno/end_lineno when available.
    spans: list[_ImportSpan] = []
    for n in collector.offending:
        lineno = getattr(n, "lineno", None)
        end_lineno = getattr(n, "end_lineno", None) or lineno
        if lineno is None or end_lineno is None:
            continue
        text = "".join(lines[lineno - 1 : end_lineno])
        text = textwrap.dedent(text)
        spans.append(_ImportSpan(lineno=lineno, end_lineno=end_lineno, text=text))

    # Remove duplicates while preserving order.
    moved: list[str] = []
    seen: set[str] = set()
    for sp in spans:
        k = sp.text.strip()
        if not k:
            continue
        if dedupe and k in seen:
            continue
        seen.add(k)
        moved.append(sp.text.rstrip("\n") + "\n")

    if not moved:
        return source

    # Remove from bottom up to avoid shifting line numbers.
    to_remove = sorted({(sp.lineno, sp.end_lineno) for sp in spans}, key=lambda x: (x[0], x[1]), reverse=True)
    for start, end in to_remove:
        # delete [start-1:end)
        del lines[start - 1 : end]
        # Also remove a single now-extra blank line at the old location
        if start - 1 < len(lines) and lines[start - 1].strip() == "":
            del lines[start - 1]

    # Find insertion point (after docstring + initial top-level import block).
    doc_end = collector.module_docstring_end
    insert_i = compute_insertion_line_for_imports("".join(lines), module_docstring_end=doc_end)

    # Extend insertion to after the consecutive top-level import block.
    # We rely on AST ordering: after docstring, module.body may start with imports.
    top_import_end: int | None = None
    body = tree.body
    start_idx = 0
    if body and isinstance(body[0], ast.Expr):
        v = body[0].value
        if isinstance(v, ast.Constant) and isinstance(v.value, str):
            start_idx = 1
    for stmt in body[start_idx:]:
        if isinstance(stmt, (ast.Import, ast.ImportFrom)):
            top_import_end = max(top_import_end or 0, getattr(stmt, "end_lineno", stmt.lineno))
            continue
        break
    if top_import_end is not None:
        insert_i = max(insert_i, top_import_end)

    # Avoid inserting inside the file end.
    insert_i = min(insert_i, len(lines))

    # Ensure there's a blank line separation around insertion block in a reasonable way.
    prefix = []
    if insert_i > 0 and lines[insert_i - 1].strip() != "":
        prefix.append("\n")
    suffix = []
    if insert_i < len(lines) and lines[insert_i].strip() != "":
        suffix.append("\n")

    insertion_block = "".join(prefix) + "".join(moved) + "".join(suffix)
    lines[insert_i:insert_i] = [insertion_block]

    out = "".join(lines)
    if out and not out.endswith("\n"):
        out += "\n"
    return out


