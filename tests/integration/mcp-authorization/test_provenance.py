#!/usr/bin/env python3
"""Deterministic source-digest tests for the MCP authorization E2E."""

from __future__ import annotations

import os
import pathlib
import sys
import tempfile
import unittest

FIXTURE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(FIXTURE))
import e2e_support as support  # noqa: E402


class SourceDigestTests(unittest.TestCase):
    def test_identical_source_has_identical_digest(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root / "a.txt").write_text("alpha\n", encoding="utf-8")
            (root / "sub").mkdir()
            (root / "sub" / "b.txt").write_text("beta\n", encoding="utf-8")
            first = support.source_tree_digest(root)[0]
            second = support.source_tree_digest(root)[0]
            self.assertEqual(first, second)

    def test_bytecode_and_pycache_do_not_change_digest(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root / "a.txt").write_text("alpha\n", encoding="utf-8")
            before = support.source_tree_digest(root)[0]
            cache = root / "__pycache__"
            cache.mkdir()
            (cache / "a.cpython-313.pyc").write_bytes(b"generated")
            (root / "b.pyc").write_bytes(b"generated")
            after = support.source_tree_digest(root)[0]
            self.assertEqual(before, after)

    def test_real_source_change_changes_digest(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            fixture = root / "fixture.txt"
            fixture.write_text("alpha\n", encoding="utf-8")
            before = support.source_tree_digest(root)[0]
            fixture.write_text("beta\n", encoding="utf-8")
            after = support.source_tree_digest(root)[0]
            self.assertNotEqual(before, after)

    def test_symlink_records_target_without_following_it(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            outside = pathlib.Path(tmp).parent / f"outside-{os.getpid()}.txt"
            outside.write_text("outside\n", encoding="utf-8")
            try:
                (root / "link.txt").symlink_to(outside)
                before = support.source_tree_digest(root)[0]
                outside.write_text("changed outside\n", encoding="utf-8")
                after = support.source_tree_digest(root)[0]
                self.assertEqual(before, after)
            finally:
                outside.unlink(missing_ok=True)


if __name__ == "__main__":
    unittest.main()
