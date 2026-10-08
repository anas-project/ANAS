import importlib.util
import os
from pathlib import Path
import unittest
from types import SimpleNamespace


ROOT = Path(__file__).parent
SPEC = importlib.util.spec_from_file_location("anas_directory_verify", ROOT / "verify-directory-admission.py")
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)


class DirectoryAdmissionTests(unittest.TestCase):
    def fixture(self):
        return VERIFY.Fixture(ROOT / "directory_admission.py")

    def test_loss_notifies_only_affected_subjects_without_oauth_grants(self):
        fixture = self.fixture()
        with fixture.installed():
            fixture.ns["notify_admission_loss"](fixture.source)
            self.assertEqual([args[2] for args in fixture.queued], ["disabled-anchor", "denied-anchor"])
            self.assertTrue(all(args[3] is None and args[4] == fixture.time for args in fixture.queued))
            self.assertEqual(fixture.history, ["begin", "clock", "enqueue", "enqueue"])

    def test_unregistered_application_and_other_source_are_untouched(self):
        fixture = self.fixture()
        with fixture.installed():
            del os.environ["ANAS_IAM_CLIENT__IMMICH__OIDC_CAEP_EVENTS"]
            fixture.ns["notify_admission_loss"](fixture.source)
            self.assertEqual(fixture.history, [])
            os.environ.update(VERIFY.ENV)
            fixture.source.slug = "other-source"
            fixture.ns["notify_admission_loss"](fixture.source)
            self.assertEqual(fixture.history, [])
            fixture.source.slug = "samba-ad"
            os.environ["ANAS_IDENTITY_OIDC_CLIENTS"] = "other"
            fixture.ns["notify_admission_loss"](fixture.source)
            self.assertEqual(fixture.history, [])

    def test_inconsistent_registration_fails_closed(self):
        for key, value in {
            "ANAS_IAM_CLIENT__IMMICH__OIDC_CAEP_EVENTS": "magic",
            "ANAS_IAM_BINDING__IMMICH__OIDC_CAEP_EVENTS": "",
            "ANAS_IAM_BINDING__IMMICH__INTERFACE": "saml",
            "ANAS_IAM_CLIENT__IMMICH__ATTRIBUTES": "sub:mail:1",
            "ANAS_IAM_CLIENT__IMMICH__OIDC_LOGOUT_METHODS": "frontchannel",
            "ANAS_IAM_CLIENT__IMMICH__OIDC_LOGOUT_SESSION_REQUIRED": "true",
            "ANAS_IAM_CLIENT__IMMICH__CLIENT_ID": "other",
        }.items():
            fixture = self.fixture()
            with self.subTest(key=key), fixture.installed():
                os.environ[key] = value
                with self.assertRaises(ValueError):
                    fixture.ns["notify_admission_loss"](fixture.source)
                self.assertEqual(fixture.queued, [])

    def test_source_anchor_drift_and_policy_error_do_not_revoke(self):
        fixture = self.fixture()
        with fixture.installed():
            fixture.source.object_uniqueness_field = "objectGUID"
            with self.assertRaises(ValueError):
                fixture.ns["notify_admission_loss"](fixture.source)
            fixture.source.object_uniqueness_field = "anasIdentityAnchor"
            fixture.policy_result_override = "policy failed"
            with self.assertRaises(ValueError):
                fixture.ns["notify_admission_loss"](fixture.source)
            self.assertEqual(fixture.queued, [])
            self.assertIn("rollback", fixture.history)

    def test_deleted_anchor_is_captured_before_user_disappears(self):
        fixture = self.fixture()
        with fixture.installed(), fixture.atomic():
            subjects = fixture.ns["capture_deleted_users"](fixture.source, (2,))
            fixture.links[:] = [link for link in fixture.links if link.user.pk != 2]
            fixture.ns["notify_deleted_users"](fixture.source, subjects)
            self.assertEqual([args[2] for args in fixture.queued], ["denied-anchor"])

    def test_trusted_role_loss_only_notifies_previous_admins(self):
        fixture = self.fixture()
        with fixture.installed(), fixture.atomic():
            group = SimpleNamespace(member_ids={1, 2, 3})
            fixture.links[1].user.trusted_admin = True
            fixture.links[2].user.trusted_admin = True
            holders = fixture.ns["capture_trusted_role_holders"](fixture.source, [group])
            self.assertEqual(holders, [(2, "denied-anchor"), (3, "protected-anchor")])
            fixture.links[1].user.trusted_admin = False
            fixture.links[1].user.admitted = True
            # Another marked superuser group is irrelevant to anasRole.
            fixture.links[1].user.is_superuser = True
            fixture.ns["notify_trusted_role_loss"](fixture.source, holders)
            self.assertEqual([args[2] for args in fixture.queued], ["denied-anchor"])
            fixture.queued.clear()
            holders = fixture.ns["capture_trusted_role_holders"](fixture.source, [group])
            fixture.ns["notify_trusted_role_loss"](fixture.source, holders)
            self.assertEqual(fixture.queued, [])

    def test_role_notification_requires_reserved_role_mapping_and_stable_subject(self):
        fixture = self.fixture()
        with fixture.installed(), fixture.atomic():
            group = SimpleNamespace(member_ids={2})
            fixture.links[1].user.trusted_admin = True
            os.environ["ANAS_IAM_CLIENT__IMMICH__ATTRIBUTES"] = "sub:anasIdentityAnchor:1"
            self.assertEqual(fixture.ns["capture_trusted_role_holders"](fixture.source, [group]), [])
            os.environ.update(VERIFY.ENV)
            holders = fixture.ns["capture_trusted_role_holders"](fixture.source, [group])
            fixture.links[1].user.trusted_admin = False
            fixture.links[1].identifier = "changed-anchor"
            with self.assertRaises(ValueError):
                fixture.ns["notify_trusted_role_loss"](fixture.source, holders)
            self.assertEqual(fixture.queued, [])

    def test_event_payload_scope_and_time_are_explicit(self):
        fixture = self.fixture()
        with fixture.installed():
            issuer = VERIFY.ENV["ANAS_IAM_BINDING__IMMICH__OIDC_ISSUER_URL"]
            payload = {"iat": int(fixture.time), "events": {"http://schemas.openid.net/event/backchannel-logout": {}}}
            fixture.ns["add_caep_session_revocation"](payload, fixture.provider, issuer, "directory-anchor", None, fixture.time)
            self.assertEqual(payload["sub_id"], {"format": "iss_sub", "iss": issuer, "sub": "directory-anchor"})
            self.assertEqual(payload["events"][fixture.ns["CAEP_EVENT"]], {"initiating_entity": "policy", "event_timestamp": fixture.time})
            for value in (None, True, -1, float("nan"), float("inf"), "123"):
                with self.subTest(value=value), self.assertRaises(ValueError):
                    fixture.ns["add_caep_session_revocation"](payload, fixture.provider, issuer, "directory-anchor", None, value)
            with self.assertRaises(ValueError):
                fixture.ns["add_caep_session_revocation"](payload, fixture.provider, issuer, "directory-anchor", "ordinary-session", fixture.time)
            with self.assertRaises(ValueError):
                fixture.ns["add_caep_session_revocation"](payload, fixture.provider, "https://wrong.example", "directory-anchor", None, fixture.time)
            with self.assertRaises(ValueError):
                fixture.ns["add_caep_session_revocation"](payload, fixture.provider, issuer, "directory-anchor", None, fixture.time + 10)


if __name__ == "__main__":
    unittest.main()
