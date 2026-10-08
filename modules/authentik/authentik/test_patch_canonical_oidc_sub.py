import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest


SCRIPT = Path(__file__).with_name("patch-canonical-oidc-sub.py")
SPEC = importlib.util.spec_from_file_location("anas_authentik_sub_patch", SCRIPT)
PATCH_MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PATCH_MODULE)


class CanonicalSubjectPatchTests(unittest.TestCase):
    def apply_claim(self, claims):
        source = "class IDToken:\n    @staticmethod\n    def new(id_token):\n" + PATCH_MODULE.TARGET + "\n        return {}\n"
        namespace = {"Any": object}
        exec(PATCH_MODULE.patch_source(source), namespace)
        token = SimpleNamespace(sub="native-user-uuid", claims=dict(claims))
        return namespace["IDToken"].new(token)

    def test_explicit_subject_becomes_canonical(self):
        token = self.apply_claim({"sub": "immutable-directory-anchor", "name": "User"})
        self.assertEqual(token.sub, "immutable-directory-anchor")
        self.assertEqual(token.claims, {"name": "User"})

    def test_unmapped_subject_preserves_native_behavior(self):
        token = self.apply_claim({"name": "User"})
        self.assertEqual(token.sub, "native-user-uuid")
        self.assertEqual(token.claims, {"name": "User"})

    def test_invalid_subject_fails_closed(self):
        for value in (None, "", " ", [], 42):
            with self.subTest(value=value), self.assertRaises(ValueError):
                self.apply_claim({"sub": value})

    def test_upstream_drift_duplicate_and_repeat_are_rejected(self):
        for source in ("unexpected upstream", PATCH_MODULE.TARGET * 2, PATCH_MODULE.REPLACEMENT):
            with self.subTest(source=source), self.assertRaises(RuntimeError):
                PATCH_MODULE.patch_source(source)


if __name__ == "__main__":
    unittest.main()
