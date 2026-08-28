---
doc_type: plan
status: proposed
created: 2026-08-23
updated: 2026-08-23
---

# Incus compute Provider 实施计划

验收依据是[Incus compute Provider Module 集成要求](../requirements/incus-module.md)的需求矩阵；设计依据是
[Forgejo Module 设计](../../docs/architecture/forgejo-module-design.md) §4 与
[AI Agent 编排设计](../../docs/architecture/ai-agent-orchestration-design.md) §5.2。

本计划把原属 [Forgejo Module 实施计划](forgejo-module.md) M2 的“Incus compute contract 与
Provider”拆出来独立跟踪：Contract 已经存在，缺的是 Provider Module 与多消费者隔离，而这两项同时
是 Forgejo Actions 与后续 AI Agent 执行面的前置。Forgejo 计划 M2 只保留“作为消费者接入”的部分。

**当前状态：全部未开始。** 仓库中没有 `modules/incus`，唯一的 Incus 适配代码仍在
`modules/forgejo/actions-controller/incus.go`。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：Provider Module 骨架、凭据与部署边界 | R-001—R-005 | 未开始 |
| M1：七个操作、请求校验与可观测性 | R-009、R-011—R-016、R-018 | 未开始 |
| M2：多消费者隔离与 Contract 文档修订 | R-006—R-008、R-010、R-017 | 未开始 |
| M3：Forgejo 迁移为 Contract 消费者 | R-020—R-022 | 未开始 |
| M4：真实 Incus 宿主验收 | R-019、R-023—R-026 | 未开始 |
| M5：非特权系统容器 interface | R-027—R-028 | 未开始 |

覆盖统计：28 项需求全部有且只有一个里程碑归属。

## 2. M0：Provider Module 骨架

- 新建 `modules/incus`：manifest（`category: compute`、`contracts.provides: compute@1.0.0/incus_vm`）、
  Provider 声明 `providers/compute/provider.yml`、Hook 与双语文档；
- 敏感配置：`endpoint`、`server_cert_b64`、`client_cert_b64`、`client_key_b64`，全部标注
  `sensitive: true`，Hook 在缺任一项时拒绝启用；
- 部署边界断言：无 Compose 端口、无 Traefik label、无宿主设备/socket 挂载；
- 前置校验：project 存在、`restricted=true`、配额已设置，否则 fail closed；
- 同步 Module 分类文档，登记新的 `compute` 类别。

验收：要求文档 §3。

## 3. M1：操作实现与请求校验

- 实现 `create`/`inspect`/`start`/`exec_stdin`/`stop`/`delete`/`list_managed`，逐一对齐
  `contracts/compute/schemas/`；
- 请求校验：固定 SHA-256 fingerprint、资源上限区间、guest 入口 allowlist、拒绝 device/raw
  config/挂载/网络/profile 覆盖；
- `exec_stdin` 只经 stdin 传 Secret，命令参数与日志一律不含 Secret；
- 幂等、超时与取消；取消后不遗留 running 实例；
- 结构化审计日志：消费者、project、实例 id、image fingerprint、结果。

验收：要求文档 §5、§6.3。

## 4. M2：多消费者隔离

- 把“消费者 → project + 证书 + 实例前缀”做成 Provider 的一等概念，`anas-forgejo-runners` 变成其中
  一个条目而不是硬编码；
- `list_managed` 与 janitor 按调用方前缀与 project 双重过滤；
- 越界请求拒绝并记审计；
- 修订 `contracts/compute` 的 README 与技术文档：把固定 project 表述改为 per-consumer，schema 不变；
- 证书为 project-restricted，不使用全局管理凭据。

验收：要求文档 §4、§6.1。

## 5. M3：Forgejo 迁移

- `actions-controller` 的 `ComputeProvider` 实现从内嵌 Incus 客户端换成 Contract 调用；
- 删除 `incus.go` 及其测试中已由 Provider 承担的部分，仓库只保留一套适配代码；
- 行为等价性用既有 controller 测试守住：ephemeral 注册、stdin token、单作业销毁、janitor；
- Forgejo Module 不再直接持有 Incus 配置项，改由 Contract binding 注入；
- 现有部署不迁移数据、不重建 project。

验收：要求文档 §7。

## 6. M4：真实宿主验收

在独立 KVM/Incus 宿主执行，记录固定 Incus 版本、镜像 fingerprint 与 project 配置。

## 6bis. M5：非特权系统容器 interface

- 给 `compute` 增加 `incus_container` interface，schema 与操作语义不变；
- 系统容器与 VM 共用同一套 project、配额、前缀、fingerprint 与命令 allowlist 校验路径；
- 容器以非特权 user namespace 运行，不授予宿主设备与挂载；
- 文档写明两档的边界差异（共享内核 vs 独立 guest kernel），Provider 不自动降级；
- 与 M4 同一 golden task 记录两档的墙钟与内存基线。

需求来源：[AI Agent 编排设计](../../docs/architecture/ai-agent-orchestration-design.md) §5.3 把执行面默认档定为
非特权系统容器，VM 保留为可选强边界。

验收：要求文档 §7bis。

## 7. CI 门禁

| 门禁 | 最近全绿提交 |
| --- | --- |
| `go test ./...` | 待记录 |
| `go run ./cmd/gen-module-docs --check` | 待记录 |
| `go run ./cmd/gen-contract-docs --check` | 待记录 |
| `npm run docs:check-requirements` | 待记录 |
| 渲染产物 `docker compose config --quiet` | 待记录 |

## 8. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-008 | 待新增 `test-env/scripts/server-incus-isolation-e2e.sh` | 双消费者、独立 project | — | 待执行 |
| R-019 | 待新增 `test-env/scripts/server-incus-cert-rotation-e2e.sh` | 运行中实例 + 证书轮换 | — | 待执行 |
| R-021 | 待新增 `test-env/scripts/server-forgejo-one-job-e2e.sh` | Forgejo + Incus 宿主 | — | 待执行 |
| R-023 | 待新增 `test-env/scripts/server-incus-lifecycle-e2e.sh` | 单消费者全流程 | — | 待执行 |
| R-024 | 待新增 `test-env/scripts/server-incus-isolation-e2e.sh` | 双消费者并行 | — | 待执行 |
| R-025 | 待新增 `test-env/scripts/server-incus-janitor-e2e.sh` | 强制中断 Provider | — | 待执行 |
| R-026 | 待新增 `test-env/scripts/server-incus-baseline.sh` | 目标 NAS 规格 | — | 待执行 |
| R-028 | 待新增 `test-env/scripts/server-incus-container-e2e.sh` | 非特权系统容器 + 同一 golden task | — | 待执行 |

## 9. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| `contracts/compute/README.md` 与 `docs/technical.md`（中英文） | 单消费者假设改为 per-consumer project | 未开始 |
| [Forgejo Module 设计](../../docs/architecture/forgejo-module-design.md) §4 | Incus 控制面归属改为 `incus` Module | 未开始 |
| [Forgejo Module 实施计划](forgejo-module.md) M2 | 只保留“作为消费者接入”，Provider 工作引用本计划 | 未开始 |
| Module 分类文档 | 登记 `compute` 类别 | 未开始 |
| `modules/incus/README.md` 与 `README.en.md` | 新建 | 未开始 |

## 10. 当前阻塞

- 没有可用的独立 Incus/KVM 宿主，M4 全部无法执行；M0—M3 不受影响。
- Provider operation 通道对**长时执行、流式输出与取消**的支持尚未评估。`exec_stdin` 用于注入并启动
  一次性作业，若通道不支持长连接或取消，需要先确定“启动即返回 + 由消费者轮询 `inspect`”的调用
  模式，再实现 M1；这项评估是 M1 的第一步，不是 M4 的问题。
