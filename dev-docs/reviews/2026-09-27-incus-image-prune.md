# Incus 镜像 prune 与 Core 租约全链路实机验收

状态：实机验收记录。日期：2026-09-27。关联需求 `INCUS-R-072`（M12 的显式 prune）、`INCUS-R-055`
（部署端回滚与产物丢失）、`INCUS-R-111`、
`INCUS-R-044`（Core 路径上的来源围栏）、`INCUS-R-047`/`R-048`（卸载盘点）。

## 范围与隔离

`ln.hlong.wang` 只做只读盘点与私有 QEMU 管理；实验根 `/data/anas-incus-20260924.Hd6AM0`（0700）。每轮一台
全新 Ubuntu 26.04 amd64 一次性 VM（1 vCPU / 2 GiB，user-mode NAT，SSH 只绑物理回环端口），经审批动作从
Zabbly `lts-7.0` 安装 Incus 7.0.1，用真实 `anas` CLI、生产 Hook/Provider 和两个合成 Compose 消费者驱动。
编排脚本 `test-env/scripts/server-incus-core-projection-e2e.py`，Go 侧用例在
`internal/runner/compute_projection_native_linux_test.go`。每轮前后比较物理宿主 Docker、服务、nft 与双栈路由
十项。

## 通过轮 host-v43（`0.0.0-native.20`）

输入取自工作树快照提交 `37b3cc38`（树 `9738a9e8`，父提交 `fe85e510`，不在任何分支上）。

| 阶段 | 结果 |
| --- | --- |
| `approved_host_install_configure_enroll` | 审批安装、配置、登记三个作业成功 |
| `core_cli_compose_projection` | 11 项通过：新鲜 `anas init` 工作区、CLI render/apply、两个既有租约互相受限、重复 render/apply 保留凭据、移除一个消费者后其证书撤销（R-111）、改版本到 `lab-r2` 且激活因缺失网络失败（`start_failed`）后活动部署与回滚历史不变、两版镜像都在、还原源码并重新加锁（只 render 不 apply）、`anas stop` 记录 stopped |
| `image_prune_confirmed_delete` | `incus-prune-plan` 只计划删除 `anas-core-one` 中 `lab-r2` 一个镜像；`lab-r1` 在 `anas-core-one` 以 `current_deployment`、在 `anas-core-two` 以 `previous_deployment` 保留；确认后 `incus-prune-apply` 删除并逐项核验，`partial=false`；读回两 project 均只剩 `lab-r1` |
| `core_owned_resources_cleaned` | 逐项核对归属后删除测试实例、镜像、profile、project、网桥与同名来源围栏 ACL（ACL 标记相符且 `used_by` 为空） |
| `approved_host_connection_revocation` | 审批卸载删除受管包；删包前共享 daemon 盘点（含新增的 `network-acls` 集合）为空 |
| `revoked_automatic_connection_rejected` | 宿主连接撤销后普通 `render` 以 `calculate_failed`（incus）失败，私有凭据状态不变、无残留 staging |
| `experiment_docker_baseline_restored` | 实验 VM 内 Docker 基线恢复 |

- 证据归档 SHA-256：`1af115340147a6e3eb4ed116d96a9931b1886a7dcf8b6629dae3b5f0dfd7b56b`；
- 终态：正常关机、QEMU 退出 0，物理宿主十项对照相同。

## 回滚与产物丢失（host-v44，`0.0.0-native.21`）

在同一链路上补齐宿主供给设计 §6.2.1 的部署端验收组。输入取自快照提交 `2d7f041a`（树 `e68f56d3`，父提交
`dddae0c2`），`core_cli_compose_projection` 由 11 项增至 13 项，其余阶段与 host-v43 相同，全部通过：

| 新增步骤 | 结果 |
| --- | --- |
| `rollback_restores_lost_image_from_frozen_artifact` | 消费者改到 `lab-r3` 并成功激活，上一部署成为回滚目标，project 同时有 r1/r2/r3；管理员从 daemon 删掉 r1 后执行 `anas rollback`：回到上一部署，`lab-r3` 成为新的回滚目标，r1 以相同 fingerprint 从上一部署自己的冻结产物重新导入 |
| `lost_frozen_artifact_fails_without_rebuild` | 再次 render `lab-r3` 得到独立部署，删去 daemon 中的 r3 及该部署的冻结产物目录后 apply：以 `start_failed` 失败，活动部署与回滚历史不变，r3 未被重建或以其他字节替代 |

随后的 prune 计划仍只删 `lab-r2`；r3 同时带 `previous_deployment` 与 `not_present` 两个保留理由，即丢失
镜像的回滚目标被如实报告而不是被静默跳过。归档 SHA-256：
`0767df9a64a61b16e22d00173db6e3c9bee647865cea0c955f951b2c945394a9`；正常关机、QEMU 退出 0，物理宿主十项
对照相同。

“同一 revision 对应不同摘要时失败”由归档与 `BuildOnce` 单元回归覆盖（冲突不覆盖、配方变化或工件损坏
不按同 revision 重建），发布端真实烘焙的重复构建返回 `existing=true` 见
[固定策略加载器](2026-09-25-forgejo-fixed-policy-loader.md)；本轮没有在实机上构造 revision 冲突。

## 镜像白名单的强制范围（生命周期 r11）

`INCUS-R-085` 要求确认 daemon 有无逐 fingerprint 的镜像约束。容器档生命周期新增
`image-allowlist-boundary`：租约实例全部回收后，用同一夹具程序、不同元数据做出白名单外的镜像，
共享客户端 `Create` 必须拒绝且不留实例（必需项）；随后以租约证书直接导入并按 `Create` 相同参数创建。
Debian 13 / Incus 7.0.1 实测 `lease_cert_import=allowed`、`lease_cert_create=allowed`，实例与镜像随即删除。
逐摘要约束因此只在共享客户端成立，与 Module 技术文档的约束表一致；被攻破的消费者仍被 project、配额、
设备和网络围栏约束。r11 容器档 23 项、VM 档 21 项、双栈与来源围栏阶段全部通过，归档
`50420229cc0f4f73f5ea6f437073e5a0f745d96312a113c9fdf08a82f1559a36`，物理宿主十项对照相同。

## 失败轮与处理

| 轮次 | 失败点 | 原因与处理 |
| --- | --- | --- |
| host-v39、host-v40 | Core 用例要求全新工作区 | 宿主审批夹具为登记的两个工作区预建 `.anas`，与“由已安装的 `anas init` 创建”冲突。`install_fixture` 改为只处理传入的工作区，可由回调创建；Core 编排在 anasd 启动前以 `anas init --yes` 建唯一登记的工作区。此前据此对 `WorkspaceChineseSpeedup` 的改动是误判，已撤回 |
| host-v41（`native.18`） | 新鲜性检查 | 检查写成“没有配置文件”，而 `anas init` 会写配置骨架及 `updated_by: init` 的受管状态。改为要求受管状态仍是 init 且摘要与当前配置一致、部署目录为空；本机对真实 `anas init` 产物核对通过且能识别改动 |
| host-v42（`native.19`） | `revoked_automatic_connection_rejected` | prune、清理、卸载已通过。失败激活步骤以 `--update-lock` 把锁推进到 `lab-r2`，只还原源码未重新加锁，末尾不带 `--update-lock` 的 render 先因锁与源码不一致失败，未到 calculate。实际错误码当时未输出（按代码路径推断为 `lock_stale`），现改为还原后执行一次只 render 的重新加锁，失败时输出错误码。host-v43 通过 |

host-v41 与 host-v42 的物理宿主对照都只有 `routes6` 不同：分别是宿主自有网桥 `anas_bridge` 新生成一个 IPv6
临时地址、以及一个已弃用临时地址到期（该接口 `use_tempaddr=2`、`temp_prefered_lft=86400`，只读核实）。
实验 VM 只用 user-mode NAT，不接任何宿主网桥；这是宿主自身的地址轮换，不是实验留下的变化，但两轮仍按
清理未通过记录。三次失败都是测试前置条件问题，本轮没有发现产品缺陷。

## 边界

合成消费者与夹具镜像不代表 Forgejo/AI Agent 业务栈或可启动、已签名的正式 guest 镜像；只覆盖单工作区、
容器档、amd64。prune 对多工作区的“全部可读且已停止”要求只有单元覆盖。VM 档产品镜像的真实烘焙、ARM64
与正式签名分发不在本轮范围。

## 宿主审批复验

同一快照（`native.20`）在全新 Ubuntu 26.04 上重跑宿主审批 23 项（r7-n20）：23/23 通过，删 4 个受管包、
681 个原有包保留，物理宿主十项对照相同，归档 `89ebb89e…f675a`；见
[宿主供给验收](2026-09-26-incus-host-action-7.0.1-acceptance.md)。

## 本机验证

`go vet`（含 `GOOS=linux`）、`internal/runner`、`internal/incusprovision` 测试，`test_incus_core_projection_e2e`、
`test_incus_host_action_e2e` 离线门禁通过。
