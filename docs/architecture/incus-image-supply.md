# Incus 镜像供给

> 状态：**工件归档、完整目录打包和供给代码已连接；实验候选已完成真实烘焙、导入和容器启动，但 rootless engine 门禁失败，正式签名发布、one-job、其他架构/隔离档与破坏性 prune 尚未验收**。更新：2026-09-22。

本文记录当前 ANAS Incus guest 镜像供给边界。目标是让 deployment 使用已经冻结的
`image_allowlist` fingerprint，而不是让 Provider 在 apply 中重建、解析 alias 或把 fingerprint
通过结果通道回传 Runner。

## 发布侧

`incus-image-artifacts recipe --image forgejo-runner --architecture amd64|arm64 --interface incus_container|incus_vm`
输出受审计的默认 Forgejo one-job runner distrobuilder 配方。配方按目标架构写入
`image.architecture`，VM 目标额外启用 Incus agent 与 10 GiB ext4 VM 目标配置。配方内嵌 ANAS
拥有的 guest helper、systemd unit 和 runner 配置；发布流水线只需提供已单独固定摘要的
`forgejo-runner` 二进制文件。消费者不能传入脚本路径、URL、alias 或 root hook。

recipe 可加 `--chinese-build-speedup`，将 Debian bootstrap URL 固定为阿里云镜像，并在
`post-unpack`、软件包安装之前转换 Debian `.list`/`.sources` 主源与安全源，保留签名配置、
suites 和 components。该执行顺序对应 [distrobuilder 的包管理流程](https://github.com/lxc/distrobuilder/blob/main/distrobuilder/main_incus.go)。
发布脚本读取 `CHINESE_BUILD_SPEEDUP=true` 后传递此参数，手工 guest `provision.sh` 使用同一
换源脚本。默认不开启；不接受任意 `APT_MIRROR_URL`，也不把 `CHINESE_SPEEDUP` 隐式转换成
构建期开关。源选择进入 recipe 字节及摘要，切换必须使用新 revision，不能在 apply 时重烘焙。
本轮未执行国内源真实下载或 guest 构建验收。

`incus-image-artifacts build` 仍只在独立发布构建机运行 distrobuilder。已有 revision 会被重新校验并
复用；对象缺失、损坏或 recipe 变化时失败，不按同 revision 重建。`record` 接受已经完成的 split
产物，`export` 把归档中同一 revision 的原始 bytes 恢复到新的私有目录，并写出 `artifact.json`。
这些命令不连接目标 Incus daemon，不分发下载端点，也不在 JSON 中内联镜像 bytes。

`incus-image-artifacts bundle --archive DIR --previous-catalog FILE --output-dir NEW_DIRECTORY`
在同一归档锁下核对历史并导出全部已提交的 split revision/架构/隔离档，最后写 `catalog.json`。
首次发布须显式改用 `--first-release`，两种历史参数互斥。输出目录必须尚不存在，父目录须已准备；
空归档、unified 工件、缺失历史或损坏对象均拒绝，不自动修复或重建。导出通过已打开的目录句柄写入，
每份实际复制的字节重新核对长度和 SHA-256；目的路径替换不能重定向后续写入或被报告为成功。
失败留下的私有候选目录须显式检查，不允许下次执行直接接管。

`scripts/ci/incus-image-release-build.sh` 已使用该入口整理部署可直接安装到 Incus Provider bundle 的
布局：`images/catalog.json` 和
`images/artifacts/<catalog>/<name>/<revision>/<architecture>/<interface>/...`。脚本先检查历史参数、
新输出目录及既有归档历史，再运行构建，最后打包全部记录；不再只导出当前两份目标而遗漏目录引用的
历史产物，也不通过 shell 重定向提前截断目录文件。CLI stdout 仅含镜像数量与目录摘要，不含镜像字节。
该目录是 release
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
替换和大小不符，并按 release 记录重新计算真实字节 digest。元数据在文件读取前须匹配完整冻结
reference/target/fingerprint/recipe；split-only 供给入口拒绝 unified 描述，不会索引不存在的片段。
多个 runtime 或 named revision 可指向相同字节，每个引用先校验，再合并物理副本，不改写 deployment
的镜像列表或 bindings。供给 descriptor 统一限制为 1 MiB；哈希与复制复用 apply 的取消 context，
取消前不新建 staging，复制中取消删除本次部分文件并释放临时供给目录。
没有本地 artifact 时不会挂载 supply；
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

`ensure` 在登记证书前读回所有受管 project 配置与精确的四项配额，而非只检查非空；profile 的
配置和设备属性也必须与受管模板完全相符。完成后再次只读检查整个租约。
`inspect.ready` 要求 project 围栏、池准入、网络归属/NAT、profile、独立受限证书和全部冻结镜像
当前仍有效。缺失依赖或证书撤销不能误报 ready；`exists`、`restricted` 与 `quota_enforced` 仍分别报告。
检查不导入镜像、读取供给文件、修复配置或重新授权，这些读回不等同于 daemon 实际隔离/配额验收。

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

## 构建失败诊断与 resolver 准备（2026-09-22）

发布工具仅从完整、最长 4 KiB 的工具输出行识别固定阶段名称，失败时报告“最后观察到的阶段”。
原始行、路径、URL 和命令内容不进入文件或错误输出；不完整/过长行丢弃，归档层也只透传封闭
类型的阶段，其他错误仍统一净化。阶段文本可以由工具输出产生，因此仅用于诊断，不是完成、
可信来源或恢复授权的证据。取消身份与失败 revision 禁止隐式重试的规则保持不变。

默认 Runner 配方不再在 distrobuilder 的 `post-files` chroot 中用 `ln -sf` 改写
`/etc/resolv.conf`。实际 distrobuilder 3.2 控制复现该命令失败；新配方生成
`/usr/lib/tmpfiles.d/anas-resolver.conf`，由 guest 启动时的 systemd-tmpfiles 建立指向
`/run/systemd/resolve/resolv.conf` 的链接，并显式安装提供 systemd init 的 `systemd-sysv`。
原生合成 rootfs 测试同时验证旧 hook 失败、新规则实际打包及 tmpfiles 创建链接；它不代替
完整 Runner 镜像启动、DNS 或 one-job 验收。

## 验收边界

2026-09-22 的 `lab-r4 / amd64 / incus_container` 实验候选已经完成完整 distrobuilder 烘焙、
不可变归档、重复 build 复用，以及真实 Provider multipart 导入、重复 ensure 和受限容器启动。
真实 Runner 13.2.0 与 one-job 参数检查通过，但 rootless Podman API 子项退出 125，整个镜像
smoke 仍失败；未执行真实 workflow，也未解除正式签名发布或其他架构/隔离档的门禁。完整证据见
[构建恢复核对](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-22-incus-runner-build-recovery.md)。

本机单元测试证明的是字节校验、归档恢复、Runner 到 Provider 的只读挂载接线、Provider 控制流和
dry-run 计划。它不证明：

- distrobuilder 真实产物可启动；
- Forgejo runner guest 行为等价；
- Incus daemon 实际接受 multipart import；
- VM/KVM 或系统容器隔离真实通过；
- prune 的破坏性删除安全。
