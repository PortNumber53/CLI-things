import unittest

from python_formatter_thing.fixers import apply_fixes
from python_formatter_thing.rules import load_rules


class TestPYFMT001(unittest.TestCase):
    def test_move_imports_to_top_preserves_try_imports(self):
        ruleset = load_rules(
            __import__("pathlib").Path(__file__).parents[1] / "rules.example.json"
        )
        src = (
            '"""doc"""\n'
            "\n"
            "def f():\n"
            "    import os\n"
            "    try:\n"
            "        import json\n"
            "    except Exception:\n"
            "        pass\n"
            "    return os, json\n"
        )
        out = apply_fixes(src, ruleset=ruleset)
        self.assertIn("import os\n", out.splitlines(keepends=True)[:10])
        self.assertIn("        import json\n", out)  # still inside try
        self.assertNotIn("    import os\n", out)


if __name__ == "__main__":
    unittest.main()


