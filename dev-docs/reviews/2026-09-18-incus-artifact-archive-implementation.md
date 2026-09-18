# Incus 本地镜像归档接续核对

> 状态：历史记录；2026-09-18，当前未提交工作树。以下区分源码实现、已执行检查与尚未满足的
> 验收条件，不是需求级验收或发布批准。工作期间有并行写入，不能仅凭基线提交复现全部变更。

## 来源与整体状态

已读取需求/计划索引、[Incus 要求](../requirements/incus-module.md)、
[Incus 计划](../plans/incus-module.md)及[宿主供给设计](../../docs/architecture/incus-host-provisioning.md)。
本记录聚焦镜像字节归档与 M12/M13 的交付边界；动作监督、内部 dispatcher 和固定控制转发的
并行改动均保留，不在这里宣称由本次归档实现完成。

本次重新生成索引后，Incus 为 **30/75**、动作 ABI 为 **0/9**、宿主动作通道为 **0/13** 已完成。
这些数值来自里程碑验收归属，不是代码文件数量；新增源码和未运行的测试不能提高验收完成度。
生产 ingress 仍关闭，受信 `modules/incus/images/catalog.json` 仍为空。

## 已写入的归档实现

| 源码 | 当前行为 | 不代表什么 |
| --- | --- | --- |
| `internal/computeimage/artifact.go` | split/unified 指纹与片段描述、有界流式读取、规范记录编解码 | 不证明输入真的是有效或可启动镜像 |
| `release_verify.go` | 共用描述与独立冻结目标/摘要核对；归档重用该入口 | 不自动认证目录签名或构建 provenance |
| `artifact_archive.go`、平台适配 | 私有本地归档、排他锁、内容寻址对象、无覆盖 revision 提交、相同原字节恢复、旧目录历史保护 | 不恢复 Incus daemon 内的镜像，不做分发/导入/prune |
| `artifact_inputs.go` | 有界读取自包含配方与上一份可信目录；拒绝缺失历史的静默初始化 | 配方文件摘要不是构建实际使用它的证明 |
| `cmd/incus-image-artifacts` | 显式 init/record/inspect/catalog；stdout 仅 JSON 元数据 | 不是安装器、Provider 动作、浏览器接口或生产自动化入口 |

同 revision 的配方、格式或字节改变会拒绝。缺失对象只能用相同原始产物补回；损坏对象、链接、
未知元数据不能覆盖或按前缀删除。失败可保留完整孤立对象或崩溃临时文件。归档和历史目录须独立
备份，不能把创建空归档解释为解除不可变版本约束。

## 检查与待测证据

已执行：针对归档相关 Go 文件的 `gofmt -d`，最终无差异；`go run ./cmd/gen-module-docs`、
`npm run docs:plan-status`、`npm run docs:requirement-status`。这些是格式/文档工作，
不构成类型检查、运行时编译、测试、门禁或站点构建通过。

已编写但未运行：`artifact_test.go`、`artifact_archive_test.go`、
`cmd/incus-image-artifacts/main_test.go`。覆盖摘要顺序、规范记录、原字节恢复、冲突不覆盖、
损坏对象、锁/目录/符号链接、取消、历史保护及 CLI 不泄露输入。测试使用合成字节；不能替代
真实 distrobuilder、Incus 导入、guest 启动或安全验收。

本次没有运行新增 CLI、builder、编译运行时、测试、验证门禁、服务器操作、提交或合并。
完整断电/磁盘不足/中断点故障注入尚未交付；不能由同步调用的存在推断崩溃恢复已通过。

## 仍须实施与验收

M12/M13 尚缺 guest_image 契约与完整配方输入所有权、真实 distrobuilder 发布构建、可信签名与
产物分发、Provider 原摘要导入/恢复、重复 apply 不构建、旧 deployment 回滚与显式 prune。
本地目录候选不得直接替代发布信任，也不允许用测试摘要填充生产目录。

M10/M11 尚需受限宿主动作通道、安装/卸载状态机、控制网络与 endpoint 接线、真实宿主工件盘点、
生产执行所有者与消费者生命周期装配。真实容器和 KVM 测试按 M6 分别跟踪，不能以缺少 KVM
自动降级，也不能用 namespace 或假字节实验替代真实退出条件。

持续待办仍以 [Incus 计划](../plans/incus-module.md)为准。归档相关命令和限制已同步 Module 双语
技术文档、宿主架构 §6.2.3 与[待测清单](../../test-env/fixtures/incus-network-prototype/e2e-plan.md)。
