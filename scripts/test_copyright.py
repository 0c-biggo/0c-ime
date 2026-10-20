"""Keep legal/copyright (DEP-5) in step with the files it describes."""

from fnmatch import fnmatchcase
from pathlib import Path
import re
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[1]
COPYRIGHT = ROOT / "legal/copyright"
DEFAULT = "AGPL-3"
# Only in the source release tarball: the compiled dictionary is not committed.
TARBALL_ONLY = "assets/data/"
IMAGES = re.compile(r"\.(png|ico|icns|tiff?|svg|webp|jpe?g|gif)$", re.IGNORECASE)
THIRD_PARTY = ("platforms/linux-fcitx5/third_party/", "internal/simplified/opencc/")


def paragraphs(text):
    """DEP-5 paragraphs as {field: value}; continuation lines join with newlines."""
    result = []
    for block in re.split(r"\n\s*\n", text.strip()):
        fields, name = {}, None
        for line in block.split("\n"):
            if line[:1] in (" ", "\t"):
                fields[name] += "\n" + line.strip()
            else:
                name, _, value = line.partition(":")
                fields[name] = value.strip()
        result.append(fields)
    return result


def tracked():
    names = subprocess.check_output(["git", "-C", str(ROOT), "ls-files", "-z"], text=True).split("\0")
    return [name for name in names if name]


class CopyrightTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.paragraphs = paragraphs(COPYRIGHT.read_text(encoding="utf-8"))
        cls.files = [p for p in cls.paragraphs if "Files" in p]
        cls.licenses = {p["License"].split("\n")[0]: p["License"] for p in cls.paragraphs
                        if "License" in p and "Files" not in p}
        cls.tracked = tracked()

    def license_of(self, path):
        # The last matching Files paragraph wins.
        for paragraph in reversed(self.files):
            if any(fnmatchcase(path, pattern) for pattern in paragraph["Files"].split()):
                return paragraph["License"].split("\n")[0]
        self.fail(f"no Files paragraph matches {path}")

    def test_header_and_default(self):
        header = self.paragraphs[0]
        self.assertEqual(header["Format"], "https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/")
        self.assertEqual(header["Upstream-Name"], "0c-ime")
        self.assertEqual(self.files[0]["Files"], "*")
        self.assertEqual(self.files[0]["License"], DEFAULT)

    def test_every_pattern_matches_a_file(self):
        for paragraph in self.files:
            for pattern in paragraph["Files"].split():
                if pattern == "*" or pattern.startswith(TARBALL_ONLY):
                    continue
                with self.subTest(pattern=pattern):
                    self.assertTrue(any(fnmatchcase(path, pattern) for path in self.tracked),
                                    f"{pattern} matches no tracked file")

    def test_every_license_has_its_text(self):
        for paragraph in self.files:
            self.assertTrue(paragraph.get("Copyright"), paragraph["Files"])
            for name in re.split(r" and | or ", paragraph["License"].split("\n")[0]):
                with self.subTest(license=name):
                    self.assertIn(name, self.licenses)
                    self.assertGreater(len(self.licenses[name].split("\n")), 1, f"{name} has no text")

    def test_images_and_third_party_files_are_declared(self):
        for path in self.tracked:
            if IMAGES.search(path) or path.startswith(THIRD_PARTY) and not path.endswith("README.md"):
                with self.subTest(path=path):
                    self.assertNotEqual(self.license_of(path), DEFAULT, f"{path} needs its own Files entry")


if __name__ == "__main__":
    unittest.main()
