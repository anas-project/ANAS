# Incus 镜像供给

> 状态：**工件供给代码已连接，真实烘焙、签名发布、guest 启动和破坏性 prune 尚未验收或完成**。更新：2026-09-19。

本文记录当前 ANAS Incus guest 镜像供给边界。目标是让 deployment 使用已经冻结的
`image_allowlist` fingerprint，而不是让 Provider 在 apply 中重建、解析 alias 或把 fingerprint
通过结果通道回传 Runner。

## 发布侧

`incus-image-artifacts recipe --image forgejo-runner --architecture amd64|arm64 --interface incus_container|incus_vm`
输出受审计的默认 Forgejo one-job runner distrobuilder 配方。配方按目标架构写入
`image.architecture`，VM 目标额外启用 Incus agent 与 10 GiB ext4 VM 目标配置。配方内嵌 ANAS
拥有的 guest helper、systemd unit 和 runner 配置；发布流水线只需提供已单独固定摘要的
`forgejo-runner` 二进制文件。消费者不能传入脚本路径、URL、alias 或 root hook。

`incus-image-artifacts build` 仍只在独立发布构建机运行 distrobuilder。已有 revision 会被重新校验并
复用；对象缺失、损坏或 recipe 变化时失败，不按同 revision 重建。`record` 接受已经完成的 split
产物，`export` 把归档中同一 revision 的原始 bytes 恢复到新的私有目录，并写出 `artifact.json`。
这些命令不连接目标 Incus daemon，不分发下载端点，也不在 JSON 中内联镜像 bytes。

`scripts/ci/incus-image-release-build.sh` 把发布输出整理成部署可直接安装到 Incus Provider bundle 的
布局：`images/catalog.json` 和
`images/artifacts/anas/forgejo-runner/<revision>/<architecture>/<interface>/...`。该目录是 release
输入；部署时仍必须来自明确安装的发布产物或等价的受信发布介质，不能由 Provider 在 apply 中临时
生成，也不能把 artifact descriptor 的 fingerprint 当作首次信任来源。

## 部署侧

Runner 在执行 compute Provider `ensure` 前，只把 deployment 中已经冻结的 `compute_images` 与
Provider bundle 中的 release artifact 组合为 Provider supply JSON。它不会重新查询最新 catalog，
也不会从 artifact descriptor 学习 fingerprint。存在匹配 artifact 时，Runner 把 supply JSON 与
临时复制出的 artifact bytes 以只读方式挂入 `anas-incus-provisioner`，并设置：

```text
ANAS_RESOURCE_IMAGE_SUPPLY_FILE=/run/anas/compute-image-supply.json
```

复制不是信任来源：Runner 打开 descriptor 与 artifact bytes 时拒绝符号链接、特殊文件、可写文件、
替换和大小不符，并按 release 记录重新计算真实字节 digest。没有本地 artifact 时不会挂载 supply；
Provider 仍会在目标 daemon 缺少该 frozen fingerprint 时 fail closed。

## Provider 行为

Provider 对每个冻结 allowlist fingerprint 执行：

1. 读取目标 project 中的 image metadata；fingerprint、架构和 isolation 必须匹配；
2. 镜像缺失时，从 supply JSON 找到同 fingerprint、架构、interface 的 split artifact；
3. 使用冻结 `Resolution` 调用共享 `computeimage.VerifySuppliedImage`，不信任 descriptor 自报摘要；
4. 以 multipart 流式导入 `incus.tar.xz` 和 `rootfs.squashfs`/`disk.qcow2`，不使用 base64；
5. 只为 image import 接受 Incus async operation，校验 operation UUID/path，并在有界轮询后再继续；
6. 导入完成后再次读回 image metadata，确认架构和 isolation。

普通 Incus API helper 仍只接受同步完成；`202 Accepted` 不会被当成成功。
Provider 读取 supply descriptor 和文件片段时固定路径根，拒绝可写目录、符号链接中间目录、特殊文件、
替换、越界大小和导入期间的字节变化；错误输出不包含 supply 文件内容、证书、私钥或 runner token。

## Prune

`incus.image-prune.plan` 与 `incus.image-prune` 已接入受认证的共享 job、一次性确认和宿主执行器。
客户端不能提交任意删除集合；root 执行器依据安装配置登记的 workspace、冻结 deployment 和 Incus
库存生成计划，保留当前与上一个 deployment、其他 workspace、instance/snapshot 的 base image
引用和未知外部镜像。存在候选删除时要求相关 compute 消费者已经停止，不隐式暂停业务。

Apply 在锁内重新计算状态与计划摘要，对批准集合逐次重新检查引用，持久化 intent，执行删除并读回
镜像缺失后记录 receipt。普通 apply、ensure 与导入没有自动清理。代码和夹具验证不构成真实
Linux/Incus 破坏性删除验收。


界面只使用实际 public job DTO 的 `kind`、`mutating`、workspace/id 和 result，不依赖内部 `job.action`。
显示的计划还需与批准 binding 的 schema、workspace、计划/状态摘要和待删集合一致。合法的空库存
显示“无变更”且不能执行；输入变更、计划过期或组件卸载均不能沿用旧确认。确认 token 不进入公开状态，
执行结果不确定时不自动重试。公开响应缺字段或绑定漂移会拒绝，不通过类型断言兜底。

## 验收边界

本机单元测试证明的是字节校验、归档恢复、Runner 到 Provider 的只读挂载接线、Provider 控制流和
dry-run 计划。它不证明：

- distrobuilder 真实产物可启动；
- Forgejo runner guest 行为等价；
- Incus daemon 实际接受 multipart import；
- VM/KVM 或系统容器隔离真实通过；
- prune 的破坏性删除安全。
