# python-formatter-thing

A tiny Python formatting rule checker that reads rules from a JSON file and reports violations (with an optional `--fix` mode for rules that support auto-fixing).

## Quick start

```bash
cd python-formatter-thing
python -m python_formatter_thing --help

# Check a file or directory (recursively)
python -m python_formatter_thing check path/to/file_or_dir --rules rules.example.json

# Apply auto-fixes (only for rules that define an autofix action)
python -m python_formatter_thing fix path/to/file_or_dir --rules rules.example.json

# Global flags can also come before the subcommand
python -m python_formatter_thing --rules rules.example.json check examples/bad_imports.py
```

## Rules JSON format (minimal)

Rules are checked against a tokenized view of the source (Python `tokenize`), with optional AST assistance for safe auto-fixes.

See `rules.example.json` for a complete example.


