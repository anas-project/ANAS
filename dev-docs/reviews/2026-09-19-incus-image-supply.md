# Incus 镜像供给接续核对

日期：2026-09-19。范围限于 `internal/computeimage`、镜像 artifact CLI、Incus provisioner image/client、
Forgejo runner image 输入、runner 新增 image supply helper 及架构文档。未修改 host action、
jobexecutor、anasd、API/frontend、安装脚本、计划索引或现有 runner parent wiring。

## 已实现

| 范围 | 结果 | 边界 |
| --- | --- | --- |
| 默认 recipe | `incus-image-artifacts recipe --image forgejo-runner` 生成 amd64/arm64 × container/vm 的确定性 distrobuilder YAML；VM 目标含 Incus agent 与 VM target | 发布流水线仍需提供已固定摘要的 `forgejo-runner` 二进制；本机未运行真实 distrobuilder |
| artifact export | `ArtifactArchive.Export` 与 CLI `export` 从归档恢复同 revision 的原始 bytes 和 `artifact.json` | 不生成自证 supply，不下载、不导入、不内联 base64 |
| supply 描述 | `computeimage.ImageSupplyDocument`/`SuppliedImage` 复用冻结 `Resolution` 与 `ArtifactRelease` | 信任来自 release/catalog 冻结；descriptor fingerprint 不能 TOFU |
| Provider import | 镜像缺失时读取只读 supply，验证 artifact bytes 后 multipart 导入 split image；只 image import 等待 Incus async operation | 未改 compose/env/mount wiring；无 supply 时仍 fail closed |
| metadata 复核 | import 后再次 GET image metadata，验证 fingerprint、目标架构和 container/vm type | 不声称 guest 可启动 |
| prune 计划 | 新增 current/previous/running 保留集合与 dry-run delete 候选纯函数 | 未开放破坏性 prune；等待宿主二段确认动作 |

## 集成契约

后续 parent wiring 需要在 ensure compute resource 前准备：

1. 使用当前 deployment 的冻结 `ComputeImages`，不要重新查询最新 catalog；
2. 从 release artifact archive `export` 对应 revision 到只读目录；
3. 用 `computeImageSupplyJSON` 生成 Provider supply JSON；
4. 将 supply JSON 与 export 出来的 split 文件只读挂入 `anas-incus-provisioner`；
5. 给 provisioner 设置 `ANAS_RESOURCE_IMAGE_SUPPLY_FILE=/run/anas/compute-image-supply.json`。

Provider 不接受消费者 alias、URL、可变 tag 或构建输出；消费者仍只收到 frozen fingerprint allowlist。

## 验证

已运行：

```text
GOCACHE=$PWD/.cache/go-build go test ./internal/computeimage ./cmd/incus-image-artifacts
GOCACHE=$PWD/.cache/go-build go test ./internal/runner -run 'TestForgejoRunnerRecipe|TestArtifactArchiveExport|TestArtifactArchiveRecord|TestComputeImageSupply|TestComputeImagePrune'
GOCACHE=$PWD/.cache/go-build go test ./modules/incus/provisioner -run 'TestImagePrunePlanKeepsCurrentPreviousAndRunning|^$'
```

结果均通过。直接运行 `go test ./modules/incus/provisioner` 在当前 macOS 沙箱失败于 `httptest`
监听 `tcp6 [::1]:0` 被拒绝；未用提权或放宽沙箱绕过。该失败限制了本机对 Provider import fake
daemon 流程的执行证据，但包已通过不监听测试的编译。

## 未完成

- 真实 distrobuilder 构建、真实 Incus multipart import、guest 启动和 Forgejo one-job 等价验收；
- Runner parent wiring、compose mount/env 投影和 release artifact 分发签名；
- `incus.image-prune` 的宿主二段确认动作与破坏性删除；
- 当前/previous/running 保留计划与真实 daemon running image 盘点接线。

本轮不改变 Incus Module 的 developing 状态，也不把纯单元测试解释为真实宿主验收。
