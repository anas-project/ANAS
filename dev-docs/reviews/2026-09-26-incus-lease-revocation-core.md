# 租约结束路径的真实 Core 验收（INCUS-R-111）

状态：实机验收记录。日期：2026-09-26。基线：`fe85e510` 加本轮工作树，输入取自快照提交
`c0119e85`（树 `947de9a6`，不在任何分支上），版本 `0.0.0-native.15`。

## 做了什么

沿用既有的 Core 投影原生入口（`test-env/scripts/server-incus-core-projection-e2e.py` 与
`TestNativeCoreComputeProjection`），在全新 Ubuntu 26.04 amd64 一次性 VM（`anas-incus-host-29c69b`，
1 vCPU / 2 GiB，user-mode NAT）里：经真实 CLI/HTTPS 审批链路安装、配置并登记 Incus（Zabbly `lts-7.0`），
用公开 CLI 初始化工作区、导入配置、render/apply 两个合成 compute 消费者，再执行新增的
`removed_consumer_lease_is_revoked`：

1. 先确认 `core_two` 的受限证书在 daemon 信任库中；
2. 用公开 `config import` 去掉 `core_two`，`render --update-lock` 后确认新部署只剩 `core_one` 的租约；
3. `apply` 新部署；
4. 断言 `core_two` 的证书已从 daemon 消失（`GET /1.0/certificates/<fp>` 为 404），`anas-core-two` project
   仍存在且 `restricted=true`，resource state 为 `status: retained`、`revocation: confirmed`；
5. 从仍在运行的 `core_one` 容器重新探测，确认它自己的受限租约不受影响。

清理阶段对已撤销的租约断言证书不存在而不再删除，其余资源按原有精确归属清理。

## 结果

| 项 | 结果 |
| --- | --- |
| 外层阶段 | 5/5 通过（审批安装/配置/登记、Core CLI/Compose 投影、宿主连接撤销、撤销后拒绝旧连接、实验 Docker 基线恢复） |
| `TestNativeCoreComputeProjection` | 主测试与 9 个子测试全部 pass，含 `removed_consumer_lease_is_revoked` |
| `TestNativeCoreRejectsRevokedAutomaticConnection` | pass |
| 终态 | 正常关机、QEMU 退出 0、清理通过；物理宿主十项对照相同 |
| 归档 SHA-256 | `def8edce8628f0e91a871aa119088f90e655953ed732f07cdcf5fe3c7980e19e` |

它证明 Core 在真实 apply 中经上一部署冻结的 Provider 撤销被移除消费者的证书、保留 project，并如实记录；
不覆盖撤销失败时的中止与 `--allow-risky` 路径（单元层 `TestFailedRevocationBlocksActivationUnlessRiskIsAccepted`），
也不是 Forgejo/AI Agent 真实业务部署。
