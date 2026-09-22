---
doc_type: review
status: current
created: 2026-09-21
updated: 2026-09-21
---

# Incus 完整镜像供给与租约就绪性核对

基线为干净 `claude/forgejo-docs-audit-20260920` / `3f5242e`，直接修改实际 checkout。
已阅读需求与计划索引、两份 `incus-module.md`、宿主供给架构及镜像供给设计。未切换分支、提交、
推送、启动生产服务或变更宿主网络。关联 R-005、R-011—R-014、R-019、R-055、R-066、R-067、R-072、R-083。

## 实际复现的反例

新增测试先在修复前运行，确认以下行为实际失败：重复 frozen image 导致目录已存在；unified
描述在 split 路径索引第二片段时 panic；已取消的 apply 仍创建 staging；供给描述接受版本/配方/
目标/摘要漂移；导出在初次校验后接受同大小的损坏字节。Provider 对全部 project 键的逐项漂移
没有完整拒绝，缺网络/profile/镜像或已撤销证书仍可报告 ready。

没有把只对无效 fixture 返回错误当作反例有效性。共享元数据校验收紧后，旧文件边界测试的初始
fixture 摘要失配，已改为真实字节摘要，并为 hostile JSON 用例补有效输入对照，没有放宽生产校验。

## 实现

共享 `ValidateArtifactResolution` 在读文件前绑定独立冻结元数据；最终字节仍由既有核验器验证，
这不提供目录签名或首次信任。供给 JSON 上限统一为 1 MiB。Core 检查整个 snapshot，逐引用核验后
合并相同物理工件，不改变 deployment 列表和 runtime bindings。split-only 入口明确拒绝 unified。
哈希和复制继承 apply context，普通文件非阻塞打开，取消或校验失败清理部分 staging。

归档导出改用已打开的目的目录句柄，并在复制时有界重新校验长度/SHA-256；目的路径被替换时不成功。
`ExportBundle`/CLI `bundle` 在同一归档锁内保留全部已提交的 revision 和目标，检查可信历史，按
Provider `images/` 布局输出，catalog 最后提交。空归档、unified、损坏对象、缺失历史或既有目的目录
均拒绝，不覆盖、重建或删除历史。失败候选目录保留供显式检查，不承诺磁盘故障后自动收敛。

原发布脚本先验证历史和新目的地，再构建，最后调用 bundle。它不再为已存在的导出目录直接跳过
核验，也不只导出本轮目标而生成包含其他历史工件的目录。脚本测试用隔离的 fake Go 调用记录验证
顺序和失败边界；字节/目录语义由真实本地文件集成测试覆盖，不执行 distrobuilder。

Provider `ensure` 在授权前检查精确 project 围栏和配额，profile 校验完整配置/设备属性；最终进行
完整只读 inspect。`ready` 依赖当前网络归属/NAT、profile、证书和所有冻结镜像；证书撤销后 project
仍可存在并受限，但租约不 ready。inspect 不导入、读取 supply 文件、修复或授权。无新增 API、
数据库或 Provider 结果通道。Module 修订为 `7.3.0-r2`，同步 Compose 标签与 localization，状态仍为 developing。

## 验证记录

| 检查 | 本轮结果 |
| --- | --- |
| 新增镜像及 Provider 反例 | 修复前实际失败，专项集成修复后通过 |
| 归档到 bundle 到 Core staging | 合成字节集成通过，不是实际 guest 镜像 |
| `go test ./...` | 最终源码通过，未变化的包允许 Go 缓存 |
| `go vet ./...` | 通过 |
| `go test -race ./internal/computeimage ./internal/runner ./modules/incus/provisioner ./cmd/incus-image-artifacts -count=1` | 四包通过，不使用测试结果缓存 |
| Linux amd64 / arm64 四包测试二进制 | 八份交叉编译通过，没有在 Linux 上执行 |
| 发布脚本 `bash -n` 与调用顺序/失败 fixture | 通过，没有实际烘焙或发布 |
| `check-shared-build`、`check-upgrade-tests` | 静态目录检查通过，不代表 Docker build 或升级 E2E |
| Module / Contract 生成检查、需求覆盖、需求与计划索引、文档状态 | 通过；索引仍为 30/75 项完成 |
| `npm run docs:build` | 最终双语源构建通过，v0.1.1；只有非阻断的 chunk 大小警告 |
| `git diff --check HEAD` | 通过，包含已暂存与未暂存修改 |
| Linux 原生、实际 Incus/Docker/KVM/双栈/one-job | 未执行 |

本轮增加 16 个顶层 Go 测试入口并扩展既有测试，包含逐配置键、文件格式、目标、历史和故障子用例。
中间全仓检查曾因修订号提升但 Compose 仍为 r1 而失败，升级目录检查随后发现 baseline 仍为 r1；
均已同步为 r2 并重新运行门禁。升级 catalog 继续使用 developing Module 的首次发布基线，不伪造
r1 → r2 的实机升级证据，也未发布镜像或创建 Git commit。

## 剩余边界

当前连接为 macOS arm64，Docker daemon 不可用，未发现 `test-env/targets.local.yml`。没有自行选择
生产 SSH 主机、安装守护进程或运行破坏性验收。本轮不将交叉编译或合成字节视为镜像可启动证据。

生产 ingress 仍缺完整生命周期/ifindex 复用证明、独立 Incus 身份、VM/TAP、health 和服务装配；
正式镜像签名分发、真实烘焙、guest 启动、配额、双栈、one-job、回滚和 prune 继续未完成。
M12/M13 保持实施中，需求完成统计不因回归修复增加；正式目录保持为空，生产 publication 仍关闭。
