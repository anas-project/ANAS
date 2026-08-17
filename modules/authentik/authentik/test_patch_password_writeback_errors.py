import importlib.util
from pathlib import Path
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("patch-password-writeback-errors.py")
SPEC = importlib.util.spec_from_file_location("anas_authentik_password_patch", SCRIPT)
PATCH_MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PATCH_MODULE)


class PasswordWritebackPatchTests(unittest.TestCase):
    def test_patches_only_the_pinned_upstream_targets(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ldap = root / "sources/ldap"
            user_write = root / "stages/user_write"
            ldap.mkdir(parents=True)
            user_write.mkdir(parents=True)

            (ldap / "password.py").write_text(
                '            if password.lower() in user_attributes["sAMAccountName"].lower():\n'
            )
            (ldap / "signals.py").write_text(
                "from django.utils.translation import gettext_lazy as _\n"
                "LOGGER = get_logger()\n\n\n@receiver(password_validate)\n"
                "        raise ValidationError(\"Failed to set password\") from exc\n"
            )
            (user_write / "stage.py").write_text(
                '        except ValidationError as exc:\n'
                '            self.logger.warning("failed to update user", exc=exc)\n'
                '            return self.executor.stage_invalid(_("Failed to update user. Please try again later."))\n'
            )

            PATCH_MODULE.patch(root)

            self.assertIn(
                'user_attributes["sAMAccountName"].lower() in password.lower()',
                (ldap / "password.py").read_text(),
            )
            signals = (ldap / "signals.py").read_text()
            self.assertIn("def _anas_password_writeback_message", signals)
            self.assertIn("result in (19, 53)", signals)
            self.assertIn("ValidationError(_anas_password_writeback_message(exc))", signals)
            self.assertIn(
                "stage_invalid(str(exc.detail[0]))", (user_write / "stage.py").read_text()
            )

    def test_refuses_an_unexpected_upstream_source(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "sources/ldap").mkdir(parents=True)
            (root / "stages/user_write").mkdir(parents=True)
            (root / "sources/ldap/password.py").write_text("upstream changed\n")
            (root / "sources/ldap/signals.py").write_text("upstream changed\n")
            (root / "stages/user_write/stage.py").write_text("upstream changed\n")
            with self.assertRaises(RuntimeError):
                PATCH_MODULE.patch(root)


if __name__ == "__main__":
    unittest.main()
