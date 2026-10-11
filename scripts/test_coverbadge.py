import contextlib
import io
import os
import tempfile
import unittest

import coverbadge
import covercheck

M = covercheck.MODULE


def profile(*blocks):
    return "mode: atomic\n" + "\n".join(f"{M}{f}:{sl}.1,{el}.2 {n} {c}" for f, sl, el, n, c in blocks) + "\n"


class CoverBadge(unittest.TestCase):
    def test_counts_covered_over_all_statements(self):
        got = coverbadge.badge(profile(("a.go", 1, 2, 9, 1), ("a.go", 3, 4, 1, 0)))
        self.assertEqual(got["message"], "90.0%")
        self.assertEqual(got["schemaVersion"], 1)
        self.assertEqual(got["label"], "coverage")

    def test_duplicate_blocks_from_several_test_binaries_are_merged(self):
        covered, total = coverbadge.measure(profile(("a.go", 1, 2, 4, 0), ("a.go", 1, 2, 4, 3)))
        self.assertEqual((covered, total), (4, 4))

    def test_excluded_statements_are_not_forgiven(self):
        # covercheck would exclude the uncovered block via `coverage: ignore`; the badge must not.
        self.assertEqual(coverbadge.badge(profile(("a.go", 1, 2, 99, 1), ("a.go", 5, 6, 1, 0)))["message"], "99.0%")

    def test_empty_profile_is_zero_not_a_division_error(self):
        self.assertEqual(coverbadge.badge("mode: atomic\n")["message"], "0.0%")

    def test_colour_bands(self):
        self.assertEqual(
            [coverbadge.colour(p) for p in (99, 95, 94.9, 90, 80, 70, 60, 59.9)],
            ["brightgreen", "brightgreen", "green", "green", "yellowgreen", "yellow", "orange", "red"],
        )

    def test_command_line(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "cover.out")
            with open(path, "w") as fh:
                fh.write(profile(("a.go", 1, 2, 1, 1)))
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                self.assertEqual(coverbadge.main(["coverbadge.py", path]), 0)
            self.assertIn('"message": "100.0%"', out.getvalue())
            with contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(coverbadge.main(["coverbadge.py"]), 2)


if __name__ == "__main__":
    unittest.main()
