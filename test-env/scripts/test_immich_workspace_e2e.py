#!/usr/bin/env python3
"""Harness unit checks only; these do not claim a real-host acceptance pass."""
import argparse
import ast
import contextlib
import datetime
import importlib.util
import io
import json
import pathlib
import sys
import tempfile
import types
import unittest
from unittest import mock

path = pathlib.Path(__file__).with_name("immich-workspace-e2e.py")
spec = importlib.util.spec_from_file_location("immich_workspace_e2e", path)
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)






class HarnessTests(unittest.TestCase):
    def test_casdoor_restored_workspace_uses_its_native_directory_checks(self):
        module_spec = importlib.util.spec_from_file_location("casdoor_workspace", path.with_name("immich-casdoor-workspace-e2e.py"))
        casdoor = importlib.util.module_from_spec(module_spec)
        module_spec.loader.exec_module(casdoor)
        suite = object.__new__(casdoor.CasdoorSuite)
        suite.report = {}
        calls = mock.Mock()
        suite.extension_failure_restore = calls.failure
        suite.extension_crash_retry = calls.crash
        suite.directory_cases = calls.casdoor
        suite.directory_revocation = mock.Mock(side_effect=AssertionError("provider-specific probe must not use the shared base"))
        suite.verify_directory_event_delivery = mock.Mock(side_effect=AssertionError("provider-specific probe must not use the shared base"))
        suite.verify_post_restore_lifecycle({}, "point", {}, "failed", "backup", {}, b"photo")
        self.assertEqual([call[0] for call in calls.mock_calls], ["failure", "crash", "casdoor"])
        self.assertEqual(suite.report["status"], "passed")
        suite.directory_revocation.assert_not_called()
        suite.verify_directory_event_delivery.assert_not_called()

    def test_stopped_restore_accepts_empty_runtime_only_when_explicit(self):
        suite = object.__new__(harness.Suite)
        suite.prefix = "anas-test-"
        suite.docker = mock.Mock(return_value=b"")
        with self.assertRaisesRegex(AssertionError, "no running containers"):
            suite.runtime()
        self.assertEqual(suite.runtime(allow_empty=True), {})

    def test_rollback_barrier_targets_non_active_old_artifact(self):
        with tempfile.TemporaryDirectory() as directory:
            suite = object.__new__(harness.Suite)
            suite.workspace = pathlib.Path(directory)
            for candidate, revision, status in (("active", 3, "active"), ("pending", 3, "previous"), ("older", 3, "previous"), ("newer", 4, "previous"), ("failed", 3, "failed")):
                root = suite.workspace / ".anas/deployments" / candidate
                root.mkdir(parents=True)
                harness.write_json(root / "deployment.yml", {"modules": {"postgres": {"version": "18.4.0", "revision": revision}}})
                state = suite.workspace / ".anas/state/deployments" / (candidate + ".yml")
                state.parent.mkdir(parents=True, exist_ok=True)
                harness.write_json(state, {"status": status})
            with mock.patch.object(harness, "read_yaml", side_effect=lambda path: json.loads(path.read_text())):
                self.assertEqual(suite.older_postgres_artifact("active", "pending"), "older")
                (suite.workspace / ".anas/state/deployments/older.yml").unlink()
                with self.assertRaisesRegex(RuntimeError, "non-active"):
                    suite.older_postgres_artifact("active", "pending")

    def test_archive_digest_streams_without_read_bytes(self):
        payload = b"actual archive contents" * 100000
        with tempfile.TemporaryDirectory() as directory:
            archive = pathlib.Path(directory) / "archive.tar"
            archive.write_bytes(payload)
            with mock.patch.object(pathlib.Path, "read_bytes", side_effect=AssertionError("unbounded archive read")):
                self.assertEqual(harness.digest(archive), harness.hashlib.sha256(payload).hexdigest())


    def test_native_http_200_policy_page_requires_origin_state_client_and_markers(self):
        url = "https://auth.private.test:9443/application/o/authorize/?client_id=immich&state=expected"
        body = '<title>Permission denied - ANAS</title>Request has been denied.<a id="ak-back-home">Go home</a>'
        self.assertTrue(harness.native_policy_denied_page(url, body, url, "expected"))
        for target, html in ((url.replace("auth.", "photos."), body), (url + "&code=grant", body),
                             (url.replace("state=expected", "state=wrong"), body),
                             (url.replace("client_id=immich", "client_id=other"), body),
                             (url + "&state=expected", body), (url, "ordinary authorization HTML"),
                             (url, body.replace('id="ak-back-home"', 'id="other"')),
                             (url.replace("/application/o/authorize/", "/if/flow/login/"), body)):
            with self.subTest(target=target):
                self.assertFalse(harness.native_policy_denied_page(target, html, url, "expected"))

    def test_policy_denial_requires_matching_native_callback(self):
        app = "https://photos.private.test:9443"
        good = app + "/auth/login?error=access_denied&state=expected"
        self.assertTrue(harness.directory_denied_callback(good, app, "expected"))
        for bad in (good.replace("state=expected", "state=other"), good + "&state=expected",
                    good + "&code=", good + "&code=grant", good + "&error=access_denied",
                    good.replace("access_denied", "server_error"), good.replace("photos.", "auth."),
                    good.replace("https:", "http:"), good.replace("/auth/login", "/photos"),
                    good + "#fragment"):
            with self.subTest(callback=bad):
                self.assertFalse(harness.directory_denied_callback(bad, app, "expected"))


    def test_fixed_album_membership_uses_metadata_query(self):
        suite = object.__new__(harness.Suite)
        suite.app, suite.prefix = "https://photos.test", "anas_iw_unit_"
        suite.wait = lambda callback: self.assertTrue(callback())
        suite.verify_database_access = lambda: None
        state = {"assets": {"photo": harness.hashlib.sha256(b"photo").hexdigest()},
                 "vector_media_hash": "seed-hash", "album_id": "album"}
        for role in ("admin", "user"):
            state[role] = {"username": role, "user_id": role + "-id", "anchor": role + "-anchor"}
        state["password"] = "fixture-password"
        suite.login = lambda username, password: (200, {"isAdmin": username == "admin", "userId": username + "-id", "accessToken": "fixture"})
        def sql(consumer, statement):
            if 'SELECT "oauthId"' in statement:
                return "admin-anchor" if "admin-id" in statement else "user-anchor"
            if "EXPLAIN" in statement:
                return "Index Scan using anas_workspace_vectors_hnsw"
            if "embedding <->" in statement:
                return "1"
            return "seed-hash" if "media_hash" in statement else "before-backup"
        suite.sql = sql
        suite.docker = lambda *args: b"before-backup\n"
        membership = {"items": [{"id": "photo"}], "nextCursor": None}
        calls = []
        def http(url, body=None, **kwargs):
            calls.append((url, body))
            if url.endswith("/ping"):
                return 200, {}, url
            if url.endswith("/version"):
                return 200, {"major": 3, "minor": 2, "patch": 4}, url
            if url.endswith("/original"):
                return 200, b"photo", url
            if url.endswith("/api/albums/album"):
                return 200, {"albumName": "ANAS recovery album", "assetCount": 1}, url
            self.assertEqual(url, suite.app + "/api/search/metadata")
            self.assertEqual(body, {"filter": {"albumIds": {"any": ["album"]}}})
            return 200, {"assets": membership}, url
        suite.http = http
        suite.verify_state(state)
        self.assertTrue(any(url.endswith("/api/search/metadata") for url, _ in calls))
        for invalid in ({"items": [{"id": "other"}], "nextCursor": None},
                        {"items": [{"id": "photo"}], "nextCursor": "extra-page"}):
            membership = invalid
            with self.assertRaises(AssertionError):
                suite.verify_state(state)




    def test_non_linux_preflight_never_invokes_a_command(self):
        with mock.patch.object(harness.platform, "system", return_value="Darwin"), mock.patch.object(harness, "command") as command:
            with self.assertRaisesRegex(harness.Blocked, "Linux with real Btrfs"):
                harness.preflight(argparse.Namespace())
            command.assert_not_called()

    def test_existing_or_unscoped_workspaces_are_rejected(self):
        for path in ("/", "/data/old-deployment", "/srv/anas-immich-workspace-nothex", "relative/anas-immich-workspace-deadbeef"):
            with self.subTest(path=path), self.assertRaises(harness.Blocked):
                harness.fresh_workspace(path)
        # The scope check happens while absent; no rm or init is performed.
        candidate = pathlib.Path("/tmp/anas-immich-workspace-deadbeef")
        with mock.patch.object(pathlib.Path, "resolve", lambda self: self), mock.patch.object(pathlib.Path, "exists", return_value=True):
            with self.assertRaisesRegex(harness.Blocked, "must all be absent"):
                harness.fresh_workspace(candidate)

    def test_media_damage_can_only_target_managed_media(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory) / "data/immich/media"
            root.mkdir(parents=True)
            target = root / "original.png"
            target.touch()
            self.assertEqual(harness.checked_media_path(directory, "/data/original.png"), target)
            for bad in ("/etc/passwd", "/data/../postgres", "/data", "relative.png"):
                with self.subTest(path=bad), self.assertRaises(RuntimeError):
                    harness.checked_media_path(directory, bad)
            (root / "outside").symlink_to(pathlib.Path(directory))
            with self.assertRaisesRegex(RuntimeError, "escapes"):
                harness.checked_media_path(directory, "/data/outside/file")

    def test_env_is_read_without_shell_evaluation(self):
        with tempfile.TemporaryDirectory() as directory:
            env = pathlib.Path(directory) / ".env"
            env.write_text("KEY='$(touch /not-a-command)'\nURL=https://photos.test:9443\n")
            self.assertEqual(harness.env_file(env), {"KEY": "$(touch /not-a-command)", "URL": "https://photos.test:9443"})

    def test_anas_driver_uses_real_binary_and_json_commands(self):
        with tempfile.TemporaryDirectory() as directory:
            args = argparse.Namespace(anas="/test/bin/anas")
            with mock.patch.dict(harness.os.environ, {"DOCKER_HOST": "unix:///run/anas-workspace-e2e.sock"}):
                suite = harness.Suite(args, pathlib.Path(directory), pathlib.Path(directory))
            suite.reports = pathlib.Path(directory)
            response = types.SimpleNamespace(returncode=0, stdout=b'{"api_version":"anas.cli/v1","ok":true}', stderr=b"successful diagnostic")
            with mock.patch.object(harness.subprocess, "run", return_value=response) as run:
                self.assertTrue(suite.anas("repeat-apply", "apply", "-w", directory)["ok"])
                run.assert_called_once_with(["/test/bin/anas", "apply", "-w", directory, "--json"], stdout=harness.subprocess.PIPE, stderr=harness.subprocess.PIPE, timeout=1800)
            self.assertEqual(json.loads((suite.reports / "repeat-apply.json").read_text())["ok"], True)
            self.assertEqual((suite.reports / "repeat-apply.stderr").read_bytes(), response.stderr)
            self.assertEqual((suite.reports / "repeat-apply.json").stat().st_mode & 0o777, 0o600)

    def test_nonzero_real_process_keeps_private_streams_without_printing_secrets(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(harness.os.environ, {"DOCKER_HOST": "unix:///run/anas-workspace-e2e.sock"}):
            root = pathlib.Path(directory)
            executable = root / "anas-test-failure"
            executable.write_text("#!" + sys.executable + "\nimport sys\nprint('{\"error\":{\"detail\":\"private-secret\"}}')\nprint('private-stderr-secret', file=sys.stderr)\nsys.exit(37)\n")
            executable.chmod(0o700)
            suite = harness.Suite(argparse.Namespace(anas=str(executable)), root, root)
            suite.reports = root
            # Replacing an existing permissive file must still leave mode 0600.
            for suffix in (".json", ".stderr"):
                target = root / ("unexpected-failure" + suffix)
                target.write_text("stale")
                target.chmod(0o644)
            output, errors = io.StringIO(), io.StringIO()
            with contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
                with self.assertRaisesRegex(RuntimeError, "anas-test-failure exited 37") as caught:
                    suite.anas("unexpected-failure", "apply", "--json-argument-is-data")
            self.assertNotIn("secret", str(caught.exception))
            self.assertEqual(output.getvalue() + errors.getvalue(), "")
            self.assertEqual(json.loads((root / "unexpected-failure.json").read_text())["error"]["detail"], "private-secret")
            self.assertEqual((root / "unexpected-failure.stderr").read_text().strip(), "private-stderr-secret")
            for suffix in (".json", ".stderr"):
                self.assertEqual((root / ("unexpected-failure" + suffix)).stat().st_mode & 0o777, 0o600)

    def test_anas_timeout_keeps_partial_streams_without_secret_exception_details(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(harness.os.environ, {"DOCKER_HOST": "unix:///run/anas-workspace-e2e.sock"}):
            root = pathlib.Path(directory)
            suite = harness.Suite(argparse.Namespace(anas="/test/bin/anas"), root, root)
            suite.reports = root
            for stdout, stderr in ((b"partial-stdout-secret", b"partial-stderr-secret"), (None, None)):
                with self.subTest(streams=stdout is not None):
                    timeout = harness.subprocess.TimeoutExpired(["/test/bin/anas", "--token=argument-secret"], 1800, output=stdout, stderr=stderr)
                    output, errors = io.StringIO(), io.StringIO()
                    with mock.patch.object(harness.subprocess, "run", side_effect=timeout), contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
                        with self.assertRaisesRegex(RuntimeError, "^anas timed out$") as caught:
                            suite.anas("timeout", "--token=argument-secret")
                    self.assertTrue(caught.exception.__suppress_context__)
                    self.assertEqual(output.getvalue() + errors.getvalue(), "")
                    for suffix, expected in ((".json", stdout), (".stderr", stderr)):
                        target = root / ("timeout" + suffix)
                        self.assertEqual(target.read_bytes(), expected or b"")
                        self.assertEqual(target.stat().st_mode & 0o777, 0o600)

    def test_command_timeout_sanitizes_sensitive_arguments_and_output(self):
        timeout = harness.subprocess.TimeoutExpired(["/test/bin/curl", "--password=argument-secret"], 30,
                                                   output=b"output-secret", stderr=b"stderr-secret")
        output, errors = io.StringIO(), io.StringIO()
        with mock.patch.object(harness.subprocess, "run", side_effect=timeout), contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            with self.assertRaisesRegex(RuntimeError, "^curl timed out$") as caught:
                harness.command(["/test/bin/curl", "--password=argument-secret"], timeout=30)
        self.assertTrue(caught.exception.__suppress_context__)
        self.assertEqual(output.getvalue() + errors.getvalue(), "")

    def test_failed_cleanup_captures_guard_before_stop_and_excludes_other_workspace_data(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(harness.os.environ, {"DOCKER_HOST": "unix:///run/anas-workspace-e2e.sock"}):
            root = pathlib.Path(directory)
            workspace = root / "workspace"
            states = workspace / ".anas/state/deployments"
            states.mkdir(parents=True)
            active = workspace / ".anas/state/active.yml"
            active.write_text("active_deployment: old\nruntime_status: running\n")
            (states / "old.yml").write_text("failure_detail:\n  postgres_maintenance_target: frozen-candidate\n  data_restore_source: snapshot-test-point\n")
            (workspace / "config.yml").write_text("configuration-secret-must-not-be-copied")
            (workspace / ".anas/secrets.yml").write_text("provider-password-must-not-be-copied")
            suite = harness.Suite(argparse.Namespace(keep_workspace=True), workspace, root)
            suite.reports.mkdir(mode=0o700)
            suite.initialized = True
            suite.report.update(status="failed", failed_phase="extension-maintenance-failure")

            def stop(*args):
                evidence = json.loads((suite.reports / "failure-workspace-state.json").read_text())
                self.assertEqual(evidence["failed_phase"], "extension-maintenance-failure")
                contents = "\n".join(x.get("content", "") for x in evidence["files"])
                self.assertIn("runtime_status: running", contents)
                self.assertIn("postgres_maintenance_target: frozen-candidate", contents)
                self.assertIn("data_restore_source: snapshot-test-point", contents)
                active.write_text("runtime_status: stopped\n")
                return {}

            with mock.patch.object(suite, "anas", side_effect=stop) as anas, mock.patch.object(suite, "docker", return_value=b""):
                suite.cleanup()
                anas.assert_called_once_with("cleanup-stop", "stop", "-w", workspace)
            target = suite.reports / "failure-workspace-state.json"
            self.assertEqual(target.stat().st_mode & 0o777, 0o600)
            self.assertNotIn("must-not-be-copied", target.read_text())
            self.assertEqual(suite.report["failure_evidence"]["files"], 2)
            self.assertTrue(suite.report["cleanup"]["retained_by_request"])

    def test_failure_state_evidence_is_bounded_and_skips_symlinks(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(harness.os.environ, {"DOCKER_HOST": "unix:///run/anas-workspace-e2e.sock"}):
            root = pathlib.Path(directory)
            workspace = root / "workspace"
            states = workspace / ".anas/state/deployments"
            states.mkdir(parents=True)
            outside = root / "outside-secret.yml"
            outside.write_text("outside-file-must-not-be-read")
            (states / "00-symlink.yml").symlink_to(outside)
            (states / "01-oversized.yml").write_text("X" * 65537)
            for number in range(35):
                (states / (f"deployment-{number:02}.yml")).write_text("status: failed\n")
            suite = harness.Suite(argparse.Namespace(), workspace, root)
            suite.reports.mkdir(mode=0o700)
            suite.preserve_failure_evidence()
            evidence = json.loads((suite.reports / "failure-workspace-state.json").read_text())
            self.assertEqual(len(evidence["files"]), 33)  # active + at most 32 states
            self.assertEqual(evidence["omitted_deployment_files"], 5)
            self.assertEqual(evidence["files"][1]["status"], "unsafe_path_skipped")
            self.assertEqual(evidence["files"][2]["status"], "oversized_skipped")
            self.assertNotIn("outside-file-must-not-be-read", json.dumps(evidence))

    def test_cleanup_retains_workspace_if_any_scoped_container_remains(self):
        with tempfile.TemporaryDirectory() as directory:
            args = argparse.Namespace(anas="/test/bin/anas", keep_workspace=False)
            with mock.patch.dict(harness.os.environ, {"DOCKER_HOST": "unix:///run/anas-workspace-e2e.sock"}):
                suite = harness.Suite(args, pathlib.Path(directory), pathlib.Path(directory))
            suite.initialized = True
            sentinel = pathlib.Path(directory) / "data.txt"
            sentinel.write_text("must remain")
            with mock.patch.object(suite, "anas", return_value={}) as anas, mock.patch.object(suite, "docker", return_value=b"owned-container\n"), mock.patch.object(harness.shutil, "rmtree") as remove:
                suite.cleanup()
                anas.assert_called_once_with("cleanup-stop", "stop", "-w", pathlib.Path(directory))
                remove.assert_not_called()
            self.assertTrue(sentinel.exists())
            self.assertIn("containers remain", suite.report["cleanup"]["error"])



    def test_receiver_cutoff_is_read_only_and_rejects_invalid_epochs(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(harness.os.environ, {"DOCKER_HOST": "unix:///run/anas-workspace-e2e.sock"}):
            suite = harness.Suite(argparse.Namespace(), pathlib.Path(directory), pathlib.Path(directory))
            anchor = "23a9ce49-41ed-4640-ac4a-a131535b153f"
            with mock.patch.object(suite, "sql", return_value="1788480001.5") as sql:
                self.assertEqual(suite.receiver_signed_cutoff(anchor), {"oauthId": anchor, "epoch": 1788480001.5})
                statement = sql.call_args.args[1]
                self.assertTrue(statement.startswith("SELECT "))
                self.assertIn(anchor, statement)
            for invalid in ("", "nan", "inf", "-1", "0"):
                with self.subTest(epoch=invalid), mock.patch.object(suite, "sql", return_value=invalid), self.assertRaises(ValueError):
                    suite.receiver_signed_cutoff(anchor)


    def test_real_old_vector_fixture_preserves_managed_auth_and_provider_paths(self):
        modules = path.parents[2] / "modules"
        before = {p: (modules / p).read_bytes() for p in ("postgres/postgres/Dockerfile", "postgres/postgres/extensions.sh", "postgres/hook/main.go")}
        fixture = harness.postgres_fixture_sources(modules)
        dockerfile = fixture["postgres/postgres/Dockerfile"]
        self.assertIn("/v0.8.1.tar.gz", dockerfile)
        self.assertIn("sha256:" + harness.OLD_VECTOR_SOURCE, dockerfile)
        self.assertIn("COPY entrypoint.sh /usr/local/bin/anas-postgres-entrypoint", dockerfile)
        self.assertIn("password_encryption=scram-sha-256", dockerfile)
        self.assertNotIn("POSTGRES_HOST_AUTH_METHOD=trust", dockerfile)
        self.assertIn("vector:0.8.1|cube:1.5|earthdistance:1.2", fixture["postgres/postgres/extensions.sh"])
        self.assertNotIn("vector:0.8.2", fixture["postgres/postgres/extensions.sh"])
        self.assertEqual(fixture["postgres/docker-compose.yml"].count("/anas-postgres:18.4.0-r3"), 2)
        self.assertIn("\nrevision: 3\n", fixture["postgres/module.yml"])
        self.assertIn("\nmodule_revision: 3\n", fixture["postgres/localization.yml"])
        for p, value in before.items():
            self.assertEqual((modules / p).read_bytes(), value)

    def test_fixture_anchor_drift_is_rejected_before_any_module_copy(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            target = root / "postgres/tests/pgvector-0.8.1.fixture"
            target.parent.mkdir(parents=True)
            target.write_text("ADD unreviewed-new-source")
            with mock.patch.object(harness.shutil, "copytree") as copy, self.assertRaisesRegex(harness.Blocked, "source checksum changed"):
                harness.postgres_fixture_sources(root)
            copy.assert_not_called()
        for source in ("absent", "anchor anchor"):
            with self.assertRaises(harness.Blocked):
                harness.replace_once(source, "anchor", "replacement", "fixture")

    def test_failure_hook_can_only_fail_after_real_native_maintenance(self):
        modules = path.parents[2] / "modules"
        source = harness.postgres_fixture_sources(modules, fail_after_maintenance=True)["postgres/hook/main.go"]
        start = source.index('case "after_start":')
        completed = source.index('if err := maintainExtensions(env);', start)
        failure = source.index('ANAS workspace test-only failure after completed PostgreSQL maintenance', completed)
        self.assertLess(completed, failure)
        self.assertIn('if env["ANAS_POSTGRES_EXTENSION_MAINTENANCE"] == "true"', source[completed:failure])

    def test_anas_rejected_requires_expected_guard_code(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(harness.os.environ, {"DOCKER_HOST": "unix:///run/anas-workspace-e2e.sock"}):
            suite = harness.Suite(argparse.Namespace(anas="/test/bin/anas"), pathlib.Path(directory), pathlib.Path(directory))
            suite.reports = pathlib.Path(directory)
            response = types.SimpleNamespace(returncode=3, stdout=b'{"error":{"code":"postgres_recovery_required"}}', stderr=b"")
            with mock.patch.object(harness.subprocess, "run", return_value=response) as run:
                result = suite.anas_rejected("blocked-start", "start", "-w", directory, codes={"postgres_recovery_required"})
                self.assertEqual(result["error"]["code"], "postgres_recovery_required")
                self.assertEqual(run.call_args.args[0], ["/test/bin/anas", "start", "-w", directory, "--json"])
            for code, exit_status in (("unrelated_failure", 3), ("postgres_recovery_required", 0)):
                response = types.SimpleNamespace(returncode=exit_status, stdout=json.dumps({"error": {"code": code}}).encode(), stderr=b"")
                with mock.patch.object(harness.subprocess, "run", return_value=response), self.assertRaises(AssertionError):
                    suite.anas_rejected("invalid-rejection", "start", codes={"postgres_recovery_required"})

    def test_observed_pg_start_before_all_consumers_stop_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(harness.os.environ, {"DOCKER_HOST": "unix:///run/anas-workspace-e2e.sock"}):
            suite = harness.Suite(argparse.Namespace(), pathlib.Path(directory), pathlib.Path(directory))
            suite.reports = pathlib.Path(directory)
            before = {("immich", "anas_immich"): {"id": "old-immich", "image": "sha256:old"},
                      ("casdoor", "anas_casdoor"): {"id": "old-iam", "image": "sha256:iam"}}
            def event(identifier, action, module):
                return {"Actor": {"ID": identifier, "Attributes": {"com.docker.compose.project": suite.prefix + module, "name": suite.prefix + module}}, "Action": action, "timeNano": 1234}
            rows = [event("old-immich", "die", "immich"), event("old-iam", "die", "casdoor"), event("new-postgres", "start", "postgres")]
            def watcher(*args, **kw):
                for row in rows:
                    kw["stdout"].write((json.dumps(row) + "\n").encode())
                return types.SimpleNamespace(poll=lambda: None, terminate=lambda: None, communicate=lambda **kw: None)
            with mock.patch.object(harness.subprocess, "Popen", side_effect=watcher), mock.patch.object(harness.time, "sleep"):
                self.assertEqual(suite.transition_with_stop_evidence("ordered", lambda: "real-CLI-result", before), "real-CLI-result")
                rows[1], rows[2] = rows[2], rows[1]
                with self.assertRaisesRegex(AssertionError, "before the full previous workspace stopped"):
                    suite.transition_with_stop_evidence("bad-order", lambda: "CLI-succeeded", before)
                rows[:] = [event("old-immich", "die", "immich"), event("old-iam", "die", "casdoor"), event("new-immich", "start", "immich")]
                with self.assertRaisesRegex(AssertionError, "restore unexpectedly resumed"):
                    suite.transition_with_stop_evidence("bad-restore", lambda: "CLI-succeeded", before, expect_pg_start=False)

    def test_fixture_generation_does_not_create_a_second_catalog_mutation_path(self):
        modules = path.parents[2] / "modules"
        for failure in (False, True):
            for source in harness.postgres_fixture_sources(modules, fail_after_maintenance=failure).values():
                self.assertNotIn("UPDATE pg_extension SET extversion", source)
        # The old fixture has real SQL/control/binary installation; extension
        # catalogs are queried by the production provider, never forged here.
        old = harness.postgres_fixture_sources(modules)["postgres/postgres/Dockerfile"]
        self.assertIn("make -C /tmp/vector install", old)
        self.assertIn("/usr/local/share/postgresql/extension/vector*", old)

    def test_pg18_preload_uses_provider_without_settings_grant_to_application(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(harness.os.environ, {"DOCKER_HOST": "unix:///run/anas-workspace-e2e.sock"}):
            suite = harness.Suite(argparse.Namespace(), pathlib.Path(directory), pathlib.Path(directory))
            def app_sql(consumer, statement, password=None):
                if "shared_preload_libraries" in statement:
                    raise RuntimeError("PG18 ordinary role cannot read restricted setting")
                if password is not None:
                    raise RuntimeError("wrong password")
                if "pg_roles" in statement:
                    return consumer + ":0:0:0"
                if "pg_extension" in statement:
                    return "cube=1.5\nearthdistance=1.2\nvector=0.8.1"
                return "180004"
            with mock.patch.object(suite, "sql", side_effect=app_sql) as sql, mock.patch.object(suite, "provider_sql", return_value="") as provider:
                suite.verify_database_access()
                provider.assert_called_once_with("SELECT current_setting('shared_preload_libraries');")
                self.assertFalse(any("GRANT" in call.args[1] or "shared_preload_libraries" in call.args[1] for call in sql.call_args_list))
                self.assertTrue(any(call.args[0] == "immich" and "server_version_num" in call.args[1] for call in sql.call_args_list))


if __name__ == "__main__":
    unittest.main()
