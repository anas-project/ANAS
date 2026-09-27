# Incus 镜像 prune 与 Core 租约全链路实机验收

状态：实机验收记录。日期：2026-09-27。关联需求 `INCUS-R-072`（M12 的显式 prune 部分）、`INCUS-R-111`、
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
容器档、amd64。prune 对多工作区的“全部可读且已停止”要求只有单元覆盖。M12 其余项（VM/ARM64 镜像、正式
签名分发、回滚）未由本轮推进。

## 宿主审批复验

同一快照（`native.20`）在全新 Ubuntu 26.04 上重跑宿主审批 23 项（r7-n20）：23/23 通过，删 4 个受管包、
681 个原有包保留，物理宿主十项对照相同，归档 `89ebb89e…f675a`；见
[宿主供给验收](2026-09-26-incus-host-action-7.0.1-acceptance.md)。

## 本机验证

`go vet`（含 `GOOS=linux`）、`internal/runner`、`internal/incusprovision` 测试，`test_incus_core_projection_e2e`、
`test_incus_host_action_e2e` 离线门禁通过。
