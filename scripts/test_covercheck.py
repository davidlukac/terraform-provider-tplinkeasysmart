import contextlib
import io
import os
import tempfile
import unittest

import covercheck

M = covercheck.MODULE
PROFILE = "mode: atomic\n"


def profile(*blocks):
    return PROFILE + "\n".join(f"{M}{f}:{sl}.1,{el}.2 {n} {c}" for f, sl, el, n, c in blocks) + "\n"


class CoverCheck(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)

    def write(self, name, body):
        with open(os.path.join(self.dir.name, name), "w") as fh:
            fh.write(body)

    def run_eval(self, text):
        return covercheck.evaluate(covercheck.parse_profile(text), self.dir.name)

    def test_duplicate_blocks_from_several_test_binaries_are_merged(self):
        blocks = covercheck.parse_profile(profile(("a.go", 1, 2, 3, 0), ("a.go", 1, 2, 3, 5)))
        self.assertEqual(blocks[("a.go", 1, 2)], (3, 5))

    def test_test_support_packages_are_not_measured(self):
        blocks = covercheck.parse_profile(profile(
            ("internal/escp/escptest/switch.go", 1, 2, 9, 0), ("internal/escp/client.go", 1, 2, 3, 1)))
        self.assertEqual(blocks, {("internal/escp/client.go", 1, 2): (3, 1)})

    def test_a_package_that_only_looks_like_test_support_is_measured(self):
        blocks = covercheck.parse_profile(profile(("internal/escp/escptestx/a.go", 1, 2, 3, 0)))
        self.assertEqual(list(blocks), [("internal/escp/escptestx/a.go", 1, 2)])

    def test_a_malformed_profile_is_an_error(self):
        with self.assertRaises(ValueError):
            covercheck.parse_profile(PROFILE + "garbage\n")

    def test_fully_covered_code_passes(self):
        self.write("a.go", "package a\n")
        total, covered, excluded, uncovered, _, problems = self.run_eval(profile(("a.go", 1, 2, 4, 1)))
        self.assertEqual((total, covered, excluded, uncovered, problems), (4, 4, 0, [], []))

    def test_uncovered_code_is_reported_with_its_location(self):
        self.write("a.go", "package a\n")
        _, _, _, uncovered, _, _ = self.run_eval(profile(("a.go", 7, 9, 2, 0)))
        self.assertEqual(uncovered, [("a.go", 7, 9, 2)])

    def test_a_marked_function_excludes_only_its_own_statements(self):
        self.write("a.go", "package a\n\n// main is the entry point.\n// coverage: ignore - process entry point\nfunc main() {\n\trun()\n}\n\nfunc other() {\n\tx()\n}\n")
        _, covered, excluded, uncovered, exclusions, problems = self.run_eval(
            profile(("a.go", 6, 6, 1, 0), ("a.go", 10, 10, 1, 0)))
        self.assertEqual((covered, excluded, problems), (0, 1, []))
        self.assertEqual(uncovered, [("a.go", 10, 10, 1)], "the unmarked function stays uncovered")
        self.assertEqual(list(exclusions.values()), [1])

    def test_a_marker_without_a_reason_is_a_problem(self):
        self.write("a.go", "package a\n\n// coverage: ignore\nfunc f() {\n\tx()\n}\n")
        _, _, _, _, _, problems = self.run_eval(profile(("a.go", 5, 5, 1, 0)))
        self.assertEqual(len(problems), 1)
        self.assertIn("needs a reason", problems[0])

    def test_a_marker_with_a_dash_but_no_text_is_a_problem(self):
        self.write("a.go", "package a\n\n// coverage: ignore -\nfunc f() {\n\tx()\n}\n")
        _, _, _, _, _, problems = self.run_eval(profile(("a.go", 5, 5, 1, 0)))
        self.assertEqual(len(problems), 1)

    def test_a_missing_source_file_leaves_blocks_uncovered(self):
        _, _, _, uncovered, _, _ = self.run_eval(profile(("gone.go", 1, 2, 1, 0)))
        self.assertEqual(len(uncovered), 1)

    def test_main_reports_the_policy_through_the_exit_code(self):
        self.write("a.go", "package a\n")
        good = os.path.join(self.dir.name, "good.out")
        bad = os.path.join(self.dir.name, "bad.out")
        for path, hits in ((good, 1), (bad, 0)):
            with open(path, "w") as fh:
                fh.write(profile(("a.go", 1, 2, 4, hits)))
        self.assertEqual(covercheck.main(["covercheck.py", good, self.dir.name]), 0)
        self.assertEqual(covercheck.main(["covercheck.py", bad, self.dir.name]), 1)
        with contextlib.redirect_stdout(io.StringIO()) as out:
            self.assertEqual(covercheck.main(["covercheck.py"]), 2)
        self.assertIn("Usage", out.getvalue(), "calling without arguments must explain the usage")


if __name__ == "__main__":
    unittest.main()
