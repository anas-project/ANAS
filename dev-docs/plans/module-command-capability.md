---
doc_type: plan
status: implementing
created: 2026-08-23
updated: 2026-09-13
---

# Module 专属命令能力实施计划

> [!IMPORTANT]
> M1/M2 已实现的 executor 协议与取消语义已被[统一动作 ABI](action-abi.md) 取代。已完成的状态
> 描述的是旧协议下的实现，迁移工作由那份计划承载，不在本计划范围内。

验收依据是[Module 专属命令能力要求](../requirements/module-command-capability.md)的需求矩阵，现状分析和
架构决策见[Module 专属命令能力设计](../../docs/architecture/module-command-capability-design.md)。M1/M2 已完成；
当前下一里程碑是 M3，但受 anasd 认证/job 基础设施约束，M4 受独立 Incus/KVM 宿主约束。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：manifest、deployment 冻结与只读发现 | R-001—R-014 | 已完成 |
| M2：共享执行服务、ABI、锁与 CLI invoke | R-015—R-023 | 已完成 |
| M3：anasd 认证后 job/API 与权限边界 | R-024—R-029 | 实施中；只读 list/detail 已完成，invoke 阻塞于 Web API 管理面对应里程碑 |
| M4：Forgejo/Incus 命令与真实宿主验收 | R-030—R-034 | 阻塞；等待独立 Incus/KVM 宿主 |

## 2. M1 检查表

- [x] 扩展严格 Module manifest，增加 command executor、descriptor、参数和输入白名单模型。
- [x] 复用 `configschema.Parameter` 规范化参数定义和默认值，覆盖非法枚举、路径、重复 ID/参数。
- [x] 扩展 Module 与 deployment manifest，冻结公开 descriptor、内部 handler/executor 和摘要。
- [x] 把 executor 纳入 Module 包多平台预编译与 artifact digest。
- [x] 在 `internal/application` 增加只读 list/describe use case，只读取 active deployment。
- [x] 接入 `anas module commands [MODULE]` 人类输出与 CLI JSON 契约。
- [x] 增加无命令旧 Module 零变化、源码/cache 漂移和摘要篡改测试。
- [x] 同步 CLI 与 Module 开发参考文档。

## 3. M2 检查表

- [x] 实现类型化 invoke request/result、参数规范化、稳定错误枚举和 EventSink。
- [x] 实现冻结 executor、最小进程环境、stdin 请求和严格有界 JSONL decoder。
- [x] 贯穿 context/timeout，补齐进程组取消和 unknown outcome 语义。
- [x] 复用 workspace 锁并实现 module_read/module_write/workspace_write 冲突表。
- [x] 接入 `anas module invoke`、TTY/destructive 确认、`-y` 和 stderr progress。
- [x] 覆盖幂等 `changed:false`、协议畸形、超限、timeout、cancel 和 secret 不泄漏测试。

## 4. M3 检查表

- [ ] 等 anasd 认证、角色、job、审计与非阻塞锁基础设施完成。
- [ ] 扩展 application service 的 actor/authorization adapter，不在 HTTP handler 复制命令逻辑。
- [x] 增加 commands list/detail GET 与 OpenAPI；公开 DTO 去除内部 handler、路径和输入键。
- [ ] 认证/job 基础设施就绪后增加 invoke POST，不在 M0 未认证监听器提前开放。
- [ ] change/长 query 创建 job，保存脱敏 request、事件、result 和 unknown outcome。
- [ ] 实现 command digest `If-Match`、destructive 重认证/确认和 idempotency key。
- [ ] 保持 M0 未认证监听器无写入口，并完成 API 契约与安全测试。

## 5. M4 检查表

- [ ] Forgejo command executor 实现 `incus-doctor` 与 `incus-runner-reconcile`。
- [ ] 为远程 daemon maintenance 定义独立凭据及创建、轮换、撤销和恢复流程。
- [ ] 实现 `incus-daemon-status|start|stop`，不复用 restricted project credential。
- [ ] `incus-daemon-stop` 实现阻止新 job、drain/force 守卫、终态验证与幂等结果。
- [ ] 同步 Forgejo requirements/plan、双语 Module 文档和配置/Secret inventory。
- [ ] 在独立 KVM 宿主完成命令、权限隔离、中断恢复和日志泄漏 E2E。

## 6. CI 门禁

| 门禁 | 最近全绿提交 |
| --- | --- |
| `go test ./...`（M1/M2 的 manifest、冻结、执行服务与 CLI，M3 的 list/detail） | `25433bd`（GitHub CI，2026-08-29），已包含 `eae51cd`。`5306b63` 上 `internal/application`、`internal/api/httpapi`、`cmd/anasd` 通过，本计划 CLI 侧用例所在的 `internal/runner` 包因无关用例失败；其后提交均未经 CI |
| `go vet ./...` | `5306b63`（GitHub CI，2026-09-05） |
| §9 的两组 `go test -race` | 不在 CI 中；本地记录 2026-08-23 |
| `npm run docs:check-requirements` | `5306b63`（GitHub CI）；`40a8b2e`（2026-09-09）加入 ABI 取代说明后未经 CI，本地 `6823232` 通过 |
| `npm run docs:build` | 不在 `ci.yml` 中；本地记录 2026-08-23（§9） |

M3 的 HTTP invoke 实际已随 Web API 管理面的 `CONSOLE-R-181` 落地：路由由 `2b44a2b`（2026-09-05）加入，
所在的 `internal/api/httpapi` 包在 `5306b63` 上通过；`test-env/scripts/server-console-command-invoke-e2e.sh`
由 `9888ae1` 加入，尚未执行。§1 的 M3 状态、§4 检查表与 §10 阻塞仍写着 invoke 未开放，需要对照
`MCMD-R-024`—`R-029` 复核后更新。

## 7. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-032 | 待新增 `test-env/scripts/server-forgejo-incus-command-e2e.sh` | Forgejo + 独立 Incus/KVM + active job | — | 待执行 |
| R-033 | 待新增 `test-env/scripts/server-forgejo-incus-command-e2e.sh` | 独立 Incus/KVM + 分离维护凭据 | — | 待执行 |

## 8. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| [Module 专属命令参考](../../docs/reference/module-commands.md)与英文镜像 | M1/M2 的声明、发现、CLI invoke 与错误枚举 | 已完成 |
| 同上 | M3 的 HTTP list/detail 与持久 job 形式的 invoke（`command_digest`、`412 module_command_changed`） | 已完成：两份参考页已按 `CONSOLE-R-181` 的实现写入，领先于本计划的 M3 状态 |
| [部署与配置命令 JSON 契约](../../docs/reference/contracts/commands.md)与英文镜像 | `module commands`、`module invoke` 的 JSON 信封与 `module_command_*` 错误码 | 已完成 |
| [Module 专属命令能力设计](../../docs/architecture/module-command-capability-design.md) §7、§10 与[要求文档](../requirements/module-command-capability.md)开头 | 标注 executor 协议与取消语义被[统一动作 ABI](action-abi.md) 取代 | 已完成（要求文档开头由 `40a8b2e` 补入）；逐条 ID 的取代标注由统一动作 ABI 计划跟踪 |
| Forgejo 要求与计划、`forgejo`/`incus` 双语 Module 文档、配置与 Secret inventory | M4 的 `incus-*` 命令与独立维护凭据 | 未开始 |

## 9. 验证记录

- `env GOCACHE=/private/tmp/anas-module-command-go-cache go test ./...`（2026-08-23，通过）
- `env GOCACHE=/private/tmp/anas-module-command-go-cache go test -race ./internal/application ./internal/api/httpapi -run 'ModuleCommand|OpenAPI' -count=1`（2026-08-23，通过）
- `env GOCACHE=/private/tmp/anas-module-command-go-cache go test -race ./internal/runner -run 'TestModule(Command|Commands|Invoke)' -count=1`（2026-08-23，通过）
- `env GOCACHE=/private/tmp/anas-module-command-go-cache go vet ./...`（2026-08-23，通过）
- `npm run docs:check-requirements`（2026-08-23，通过：5 份要求、211 项需求）
- `npm run docs:build`（2026-08-23，通过）
- `git diff --check`（2026-08-23，通过）

## 10. 当前阻塞

- anasd 当前已具备 M1A 认证边界与 M1B 持久任务/只读查询底座，但 job execution 与写操作审计尚未开放；M3 不能提前开放 POST invoke。
- 当前没有可用的独立 Incus/KVM 宿主和 service-manager maintenance credential；M4 只能先做单元边界，
  不能完成真实 daemon start/stop 验收。
- 工作区存在其他进行中的 Forgejo、compute contract 与文档改动；实现必须避免覆盖这些改动，并在
  修改相同文件前按当前内容增量合并。
