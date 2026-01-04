from __future__ import annotations

import io
import re
import token
import tokenize
from dataclasses import dataclass
from typing import Iterable


@dataclass(frozen=True)
class StatementContext:
    in_function: bool
    block_stack: tuple[str, ...]

    def has_any_block(self, names: Iterable[str]) -> bool:
        s = set(self.block_stack)
        return any(n in s for n in names)


@dataclass(frozen=True)
class TokenStatement:
    start_line: int
    start_col: int
    end_line: int
    end_col: int
    token_string: str
    context: StatementContext


_KW_SUITE = {"def", "class", "try", "except", "finally", "with", "for", "while", "if", "elif", "else"}


def iter_statements(source: str) -> list[TokenStatement]:
    """
    Tokenize `source` and yield a best-effort statement segmentation with context:
    - function scope is tracked by pushing 'def' on INDENT after a def header
    - try/except/finally blocks are tracked similarly

    The returned `token_string` is a stable representation for regex matching:
    e.g. "NAME:from NAME:os NAME:import NAME:path"
    """
    reader = io.StringIO(source).readline
    toks = list(tokenize.generate_tokens(reader))

    statements: list[TokenStatement] = []
    buf: list[tokenize.TokenInfo] = []
    stmt_start = (1, 0)

    paren_depth = 0
    block_stack: list[str] = []

    seen_suite_kw: str | None = None
    suite_armed: bool = False
    last_sig: tokenize.TokenInfo | None = None

    def flush(end_tok: tokenize.TokenInfo | None = None) -> None:
        nonlocal buf, stmt_start
        sig = [t for t in buf if t.type not in (tokenize.NL, tokenize.NEWLINE, tokenize.INDENT, tokenize.DEDENT, tokenize.COMMENT)]
        if not sig:
            buf = []
            return
        start_line, start_col = stmt_start
        if end_tok is None:
            end_line, end_col = sig[-1].end
        else:
            end_line, end_col = end_tok.end
        ctx = StatementContext(in_function=("def" in block_stack), block_stack=tuple(block_stack))
        token_string = " ".join(_fmt_token(t) for t in sig if _keep_token(t))
        statements.append(
            TokenStatement(
                start_line=start_line,
                start_col=start_col,
                end_line=end_line,
                end_col=end_col,
                token_string=token_string,
                context=ctx,
            )
        )
        buf = []

    for t in toks:
        if t.type == tokenize.OP and t.string in "([{":
            paren_depth += 1
        elif t.type == tokenize.OP and t.string in ")]}":
            paren_depth = max(0, paren_depth - 1)

        # Detect suite starters (we push on INDENT after the header line).
        if t.type == tokenize.NAME and t.string in _KW_SUITE and paren_depth == 0:
            seen_suite_kw = t.string
            suite_armed = False
        # Async def: treat as def when "async" precedes "def"
        if (
            last_sig is not None
            and last_sig.type == tokenize.NAME
            and last_sig.string == "async"
            and t.type == tokenize.NAME
            and t.string == "def"
            and paren_depth == 0
        ):
            seen_suite_kw = "def"
            suite_armed = False

        # Arm when we see ":" at top-level paren depth, meaning a suite will indent next.
        if t.type == tokenize.OP and t.string == ":" and paren_depth == 0 and seen_suite_kw is not None:
            suite_armed = True
            last_sig = t  # ensure "async def" tracking doesn't leak across lines

        # Disarm for single-line suites like: "if x: y" or "def f(): pass"
        if suite_armed and t.type not in (tokenize.NL, tokenize.NEWLINE, tokenize.COMMENT, tokenize.INDENT, tokenize.DEDENT):
            # If we see a significant token after ":", it's not a block suite.
            if not (t.type == tokenize.OP and t.string == ":"):
                suite_armed = False
                seen_suite_kw = None

        if t.type == tokenize.INDENT:
            if suite_armed and seen_suite_kw is not None:
                block_stack.append(seen_suite_kw)
            seen_suite_kw = None
            suite_armed = False
        elif t.type == tokenize.DEDENT:
            if block_stack:
                block_stack.pop()
            seen_suite_kw = None
            suite_armed = False

        if not buf:
            stmt_start = t.start
        buf.append(t)

        # End of statement when NEWLINE at paren_depth 0
        if t.type == tokenize.NEWLINE and paren_depth == 0:
            flush(end_tok=t)
            # Do not clear seen_suite_kw/suite_armed here: for multi-line suites the INDENT token
            # comes after NEWLINE; we clear/disarm elsewhere when appropriate.

        if t.type not in (tokenize.NL, tokenize.NEWLINE, tokenize.INDENT, tokenize.DEDENT, tokenize.COMMENT):
            last_sig = t

    flush(end_tok=None)
    return statements


def _keep_token(t: tokenize.TokenInfo) -> bool:
    if t.type in (tokenize.ENCODING, tokenize.ENDMARKER):
        return False
    if t.type == tokenize.STRING:
        # Normalize strings so rules don't have to deal with full literal content
        return True
    return True


def _fmt_token(t: tokenize.TokenInfo) -> str:
    tname = token.tok_name.get(t.type, str(t.type))
    if t.type == tokenize.NAME:
        return f"{tname}:{t.string}"
    if t.type == tokenize.OP:
        return f"{tname}:{t.string}"
    if t.type == tokenize.STRING:
        # Avoid dumping large string literals into the matching surface.
        return f"{tname}:<str>"
    if t.type == tokenize.NUMBER:
        return f"{tname}:<num>"
    if t.type == tokenize.NEWLINE:
        return f"{tname}:\\n"
    if t.type == tokenize.NL:
        return f"{tname}:\\n"
    if t.type == tokenize.INDENT:
        return f"{tname}:<indent>"
    if t.type == tokenize.DEDENT:
        return f"{tname}:<dedent>"
    if t.type == tokenize.COMMENT:
        return f"{tname}:<comment>"
    return f"{tname}:{t.string}"


_RE_PEP263 = re.compile(r"coding[:=]\s*([-\w.]+)")


def compute_insertion_line_for_imports(source: str, module_docstring_end: int | None) -> int:
    """
    Return a 0-based line index where new top-level imports should be inserted,
    accounting for shebang, encoding comment, and module docstring.
    """
    lines = source.splitlines(keepends=True)
    i = 0
    if i < len(lines) and lines[i].startswith("#!"):
        i += 1
    # PEP 263 encoding declaration can be on line 1 or 2
    for j in range(i, min(i + 2, len(lines))):
        if _RE_PEP263.search(lines[j]):
            i = j + 1
    # Skip blank lines
    while i < len(lines) and lines[i].strip() == "":
        i += 1
    if module_docstring_end is not None and module_docstring_end > 0:
        i = max(i, module_docstring_end)
        # Leave a single blank line after docstring if file already has it
        while i < len(lines) and lines[i].strip() == "":
            i += 1
    return i


