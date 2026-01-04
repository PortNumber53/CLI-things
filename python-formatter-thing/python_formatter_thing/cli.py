from __future__ import annotations

import argparse
import json
import os
import sys
from dataclasses import asdict
from pathlib import Path

from .engine import check_paths, fix_paths
from .rules import load_rules


def _build_global_parser() -> argparse.ArgumentParser:
    gp = argparse.ArgumentParser(add_help=False)
    gp.add_argument(
        "--rules",
        required=False,
        help="Path to a JSON rules file.",
    )
    gp.add_argument(
        "--json",
        dest="json_out",
        action="store_true",
        help="Emit machine-readable JSON results.",
    )
    gp.add_argument(
        "--extensions",
        default=".py",
        help="Comma-separated extensions to scan in directories (default: .py).",
    )
    gp.add_argument(
        "--group",
        default="rule,file",
        choices=("none", "rule", "file", "rule,file"),
        help="Text output grouping (ignored with --json). Default: rule,file.",
    )
    return gp


def _build_parser() -> argparse.ArgumentParser:
    # Note: global flags are handled via a pre-parse step so they can appear
    # before or after the subcommand (argparse subparsers don't support this well).
    p = argparse.ArgumentParser(
        prog="python-formatter-thing",
        add_help=True,
        parents=[_build_global_parser()],
    )
    sub = p.add_subparsers(dest="cmd", required=True)

    def add_common(sp: argparse.ArgumentParser) -> None:
        sp.add_argument(
            "paths",
            nargs="+",
            help="Files or directories to check (directories are scanned recursively).",
        )

    sp_check = sub.add_parser(
        "check",
        help="Check formatting rules and report violations.",
    )
    add_common(sp_check)

    sp_fix = sub.add_parser(
        "fix",
        help="Apply auto-fixes for fixable rules, then re-check.",
    )
    add_common(sp_fix)
    sp_fix.add_argument(
        "--unsafe",
        action="store_true",
        help="Allow potentially lossy fix operations (not used by current rules).",
    )

    return p


def main(argv: list[str] | None = None) -> int:
    gp = _build_global_parser()
    global_args, remaining = gp.parse_known_args(argv)

    parser = _build_parser()
    args = parser.parse_args(remaining)

    # Merge globals back in (so downstream code doesn't care where flags appeared).
    args.rules = global_args.rules
    args.json_out = global_args.json_out
    args.extensions = global_args.extensions
    args.group = global_args.group

    if not args.rules:
        parser.error("the following arguments are required: --rules")

    rules_path = Path(args.rules)
    ruleset = load_rules(rules_path)

    exts = tuple(
        e.strip() if e.strip().startswith(".") else f".{e.strip()}"
        for e in str(args.extensions).split(",")
        if e.strip()
    )

    if args.cmd == "fix":
        fix_paths([Path(p) for p in args.paths], ruleset=ruleset, exts=exts, unsafe=args.unsafe)
        results = check_paths([Path(p) for p in args.paths], ruleset=ruleset, exts=exts)
    else:
        results = check_paths([Path(p) for p in args.paths], ruleset=ruleset, exts=exts)

    if args.json_out:
        payload = {
            "cwd": os.getcwd(),
            "rules_file": str(rules_path),
            "violations": [asdict(v) for v in results.violations],
            "files_checked": results.files_checked,
        }
        sys.stdout.write(json.dumps(payload, indent=2, sort_keys=True))
        sys.stdout.write("\n")
    else:
        if args.group == "none":
            for v in results.violations:
                sys.stdout.write(f"{v.path}:{v.line}:{v.col}: {v.rule_id} {v.message}\n")
        else:
            _print_grouped(results.violations, group=args.group)
        if results.violations:
            sys.stdout.write(f"\n{len(results.violations)} violation(s)\n")

    return 1 if results.violations else 0


if __name__ == "__main__":
    raise SystemExit(main())


def _print_grouped(violations: list[object], *, group: str) -> None:
    # Local import to avoid circular import typing issues.
    from .types import Violation  # noqa: WPS433

    vios = [v for v in violations if isinstance(v, Violation)]
    if not vios:
        return

    if group == "file":
        by_file: dict[str, list[Violation]] = {}
        for v in vios:
            by_file.setdefault(v.path, []).append(v)
        for path in sorted(by_file):
            sys.stdout.write(f"{path}\n")
            for v in sorted(by_file[path], key=lambda x: (x.rule_id, x.line, x.col)):
                sys.stdout.write(f"  {v.line}:{v.col} {v.rule_id} {v.message}\n")
        return

    # Default: rule or rule,file
    by_rule: dict[str, list[Violation]] = {}
    for v in vios:
        by_rule.setdefault(v.rule_id, []).append(v)

    for rid in sorted(by_rule):
        rv = by_rule[rid]
        severity = rv[0].severity
        message = rv[0].message
        sys.stdout.write(f"{rid} ({severity}) {message}\n")

        if group == "rule":
            for v in sorted(rv, key=lambda x: (x.path, x.line, x.col)):
                sys.stdout.write(f"  {v.path}:{v.line}:{v.col}\n")
            continue

        # group == "rule,file"
        by_file: dict[str, list[Violation]] = {}
        for v in rv:
            by_file.setdefault(v.path, []).append(v)
        for path in sorted(by_file):
            locs = sorted(by_file[path], key=lambda x: (x.line, x.col))
            loc_str = ", ".join(f"{x.line}:{x.col}" for x in locs)
            sys.stdout.write(f"  {path} ({len(locs)}): {loc_str}\n")


