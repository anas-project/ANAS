#!/usr/bin/env python3
# TEST_CASES: TEMP-T-009
"""Read-only call-chain evidence for the manual TEMP-R-042 review."""
import hashlib
import json
from pathlib import Path
import re


ROOT = Path(__file__).resolve().parents[2]


def section(path, declaration):
    text = (ROOT / path).read_text()
    start = text.index(declaration)
    next_function = text.find("\nfunc ", start + len(declaration))
    body = text[start:next_function if next_function >= 0 else len(text)]
    return body, {"path": path, "line": text.count("\n", 0, start) + 1,
                  "sha256": hashlib.sha256(text.encode()).hexdigest()}


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def main():
    evidence = []
    activation, location = section("internal/runner/deployment.go", "func activateDeployment(")
    require(activation.index("requireLocalDeploymentWorkspace(base, id)") <
            activation.index("detectComposeForExecution("),
            "activation must reject a foreign frozen workspace before execution")
    require("stopSelection = append([]string{}, current.ModuleOrder...)" in activation,
            "switch must stop the complete frozen module order")
    require(activation.index("oldApp.stopModules(") < activation.index("oldApp.confirmTemporaryStorageReleased()") <
            activation.index('recordTemporaryTransitionPhase("starting")'),
            "switch must use normal stop before release confirmation and candidate start")
    evidence.append(dict(location, check="full switch uses oldApp.stopModules before release/start"))
    for path, name, reverse in [("internal/runner/lifecycle_select.go", "stopModules", "len(ordered) - 1"),
                                ("internal/runner/runner.go", "stopRelease", "len(modules) - 1")]:
        body, location = section(path, f"func (a *app) {name}(")
        require(reverse in body, f"{name} must preserve dependency reverse order")
        require(body.index("a.beforeStopModule(") < body.index('a.runCompose('),
                f"{name} must invoke the declared normal stop hook before Compose")
        require('a.moduleEnv(dir), "down")' in body, f"{name} must use Compose down")
        evidence.append(dict(location, check="before_stop then compose down in reverse dependency order"))
    hook, location = section("internal/runner/hook.go", "func (a *app) beforeStopModule(")
    require('hookSupportsPhase(mod.Hook, "before_stop")' in hook and 'runHook(mod, "before_stop", dir, env)' in hook,
            "normal stop hook must remain optional and run with frozen environment")
    evidence.append(dict(location, check="only a declared before_stop hook runs with its frozen environment"))
    wrapper, location = section("modules/collabora/hook/container_start.go", "func startContainer(")
    require('[]string{"/usr/bin/coolwsd"}' in wrapper and
            "execCollabora(args, os.Environ(), systemCollaboraProcessOps())" in wrapper,
            "Collabora wrapper must replace itself with coolwsd so stop reaches PID 1")
    process_ops, _ = section("modules/collabora/hook/container_start.go", "func systemCollaboraProcessOps(")
    execute, _ = section("modules/collabora/hook/container_start.go", "func execCollabora(")
    require("collaboraProcessOps{syscall.Setgroups, syscall.Setgid, syscall.Setuid, syscall.Exec}" in process_ops and
            execute.index("ops.setgroups([]int{})") < execute.index("ops.setgid(1001)") <
            execute.index("ops.setuid(1001)") < execute.index("ops.exec(args[0], args, env)"),
            "Collabora must clear groups, drop gid/uid 1001, then invoke the native syscall.Exec")
    evidence.append(dict(location, check="startContainer -> execCollabora -> syscall.Exec coolwsd as PID 1 after gid/uid 1001"))
    compose = ROOT / "modules/collabora/docker-compose.yml"
    content = compose.read_text()
    require(re.search(r"^\s+stop_grace_period:\s+180s\s*$", content, re.M) is not None,
            "Collabora must retain its 180s normal stop grace period")
    require('entrypoint: ["/usr/local/bin/anas-collabora-start", "--container-start"]' in content,
            "Compose must select the reviewed wrapper")
    evidence.append({"path": str(compose.relative_to(ROOT)),
                     "line": content[:content.index("stop_grace_period:")].count("\n") + 1,
                     "sha256": hashlib.sha256(content.encode()).hexdigest(),
                     "check": "180s stop grace period and direct wrapper entrypoint"})
    foreign, location = section("internal/runner/temp_switch.go", "func deploymentWorkspaceIsForeign(")
    recovery, _ = section("internal/runner/temp_switch.go", "func recoverTemporaryTransition(")
    require('data := strings.TrimSpace(env["DATA_PATH"])' in foreign and
            "len(module.TemporaryDirectories) != 0" in foreign and
            "managed temporary directories but no frozen workspace binding" in foreign,
            "managed frozen deployments must have a valid original workspace binding")
    require(recovery.index("deploymentWorkspaceIsForeign(base, active.ActiveDeployment)") <
            recovery.index('probe.detachCopiedTemporaryRuntime("imported-" + active.ActiveDeployment)'),
            "missing source registry must not authorize lifecycle execution from an imported active pointer")
    evidence.append(dict(location, check="original DATA_PATH is checked; imported active without source registry detaches before execution"))
    lifecycle_preview, _ = section("internal/runner/deployment_application.go", "func (service *workspaceDeploymentPlanApplication) previewLifecycleLocked(")
    lifecycle, location = section("internal/runner/deployment_application.go", "func (service *workspaceDeploymentPlanApplication) ExecuteLifecycle(")
    rollback_preview, _ = section("internal/runner/deployment_application.go", "func (service *workspaceDeploymentPlanApplication) previewRollbackLocked(")
    rollback, _ = section("internal/runner/deployment_application.go", "func (service *workspaceDeploymentPlanApplication) Rollback(")
    require(lifecycle_preview.index("requireLocalDeploymentWorkspace(base, active.ActiveDeployment)") <
            lifecycle_preview.index("detectComposeForExecution("),
            "lifecycle preview must reject foreign runtime authority before Compose")
    require(lifecycle.index("acquireRuntimeLockForApplication(") < lifecycle.index("service.previewLifecycleLocked(") <
            lifecycle.index("recoverTemporaryTransition(") < lifecycle.index("a.stopModules("),
            "lifecycle execution must hold the workspace lock and verify authority/recovery before normal stop")
    require(rollback_preview.index("requireLocalDeploymentWorkspace(base, target)") <
            rollback_preview.index("loadDeploymentManifest(") and "activateDeployment(base, targetID," in rollback,
            "historical activation must reject foreign frozen bindings and reuse normal activation")
    evidence.append(dict(location, check="locked lifecycle and historical preview/activation preserve local binding guards before normal stop"))
    snapshot_stop, location = section("internal/runner/snapshot_restore.go", "func stopActiveDeployment(")
    require(snapshot_stop.index("requireLocalDeploymentWorkspace(base, active.ActiveDeployment)") <
            snapshot_stop.index("compose.Detect()") < snapshot_stop.index("app.stopRelease(root, false)"),
            "snapshot stop must reject foreign authority before entering normal stop")
    evidence.append(dict(location, check="snapshot stop rejects foreign bindings before Compose and reuses stopRelease"))
    print(json.dumps({"schema": "anas.workspace-temp-stop-review/v1", "case_id": "TEMP-T-009",
                      "status": "evidence_generated", "oracle": "manual",
                      "review": "dev-docs/plans/archived/workspace-temp-storage.md",
                      "evidence": evidence}, indent=2))


if __name__ == "__main__":
    main()
