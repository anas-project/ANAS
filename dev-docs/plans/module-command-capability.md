---
doc_type: plan
status: implementing
created: 2026-08-23
updated: 2026-08-23
---

# Module 专属命令能力实施计划

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

## 6. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-032 | 待新增 `test-env/scripts/server-forgejo-incus-command-e2e.sh` | Forgejo + 独立 Incus/KVM + active job | — | 待执行 |
| R-033 | 待新增 `test-env/scripts/server-forgejo-incus-command-e2e.sh` | 独立 Incus/KVM + 分离维护凭据 | — | 待执行 |

## 7. 验证记录

- `env GOCACHE=/private/tmp/anas-module-command-go-cache go test ./...`（2026-08-23，通过）
- `env GOCACHE=/private/tmp/anas-module-command-go-cache go test -race ./internal/application ./internal/api/httpapi -run 'ModuleCommand|OpenAPI' -count=1`（2026-08-23，通过）
- `env GOCACHE=/private/tmp/anas-module-command-go-cache go test -race ./internal/runner -run 'TestModule(Command|Commands|Invoke)' -count=1`（2026-08-23，通过）
- `env GOCACHE=/private/tmp/anas-module-command-go-cache go vet ./...`（2026-08-23，通过）
- `npm run docs:check-requirements`（2026-08-23，通过：5 份要求、211 项需求）
- `npm run docs:build`（2026-08-23，通过）
- `git diff --check`（2026-08-23，通过）

## 8. 当前阻塞

- anasd 当前 M0 仍是未认证只读 API；M3 不能提前开放 POST invoke。
- 当前没有可用的独立 Incus/KVM 宿主和 service-manager maintenance credential；M4 只能先做单元边界，
  不能完成真实 daemon start/stop 验收。
- 工作区存在其他进行中的 Forgejo、compute contract 与文档改动；实现必须避免覆盖这些改动，并在
  修改相同文件前按当前内容增量合并。
