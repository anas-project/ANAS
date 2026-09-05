---
doc_type: review
status: current
created: 2026-09-03
review_baseline: 2026-09-03
---

# ANAS 综合项目审计与整改状态（2026-09-03）

> 状态：**2026-09-03 工作树审计基线**。本文件记录对当前代码库、分支演进、架构实现、测试门禁与文档同步状态的全面审计结论。

本文面向维护者和审查者，对 2026-08-23 上次综合审计以来的演进进行复核，并对当前工作树（基于提交 `171634c` 及分支 `codex/console-m2`）进行全方位审计。

---

## 0. 阅读边界与验证基线

本文结论对照 2026-09-03 当日代码与工作树基线实测验证。

### 0.1 验证命令与结果

本次审计对以下全量门禁进行了本地执行，**全部通过**：

```bash
# 1. Go 静态检查与单元测试
go vet ./... && go test ./...

# 2. 文档生成器与规范校验
go run ./cmd/gen-module-docs --check && go run ./cmd/gen-contract-docs --check

# 3. 需求矩阵、状态列与站点构建
npm run docs:check-requirements && npm run docs:check-requirement-status && npm run docs:check-plan-status && npm run docs:build

# 4. 辅助 Shell 测试套件
bash scripts/ci/anas-release-version-test.sh && bash scripts/ci/install-test.sh && bash scripts/ci/verify-anas-release-test.sh && bash scripts/ci/module-revisions-test.sh && bash scripts/ci/scan-image-test.sh

# 5. Web 管理控制台测试与类型检查
npm --prefix web test && npm --prefix web run typecheck

# 6. Go 漏洞扫描
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
```

**验证结论统计**：
- Go 单元测试全部通过；
- 需求覆盖门禁：16 份活动文档，604 项需求全部有且仅有一个里程碑归属；
- 依赖漏洞扫描：0 项受影响漏洞（无 active symbols）；
- Web 控制台：11 个测试套件（38 项测试）全部通过，TypeScript 双工程类型检查无错误。

---

## 1. 2026-08-23 审计任务闭环核实

对照 2026-08-23 审计报告（`2026-08-23-comprehensive-project-audit.md`）登记的 16 项任务（T1–T16）及已知约束，复核确认如下：

| 任务 | 原始定位 | 最终状态 | 闭环提交/说明 |
| --- | --- | --- | --- |
| **T1 [P1]** master 分支缺少 Go CI 门禁 | `.github/workflows/` | **已完成** | 提交 `756c9b6`：新增 `ci.yml`，覆盖 Go、文档、Shell 脚本及漏洞扫描 |
| **T2 [P1]** `install.sh` 缺少 `main()` 包裹 | `install.sh` | **已完成** | 提交 `6133cce`：全脚本包裹于 `main "$@"`，防断流执行 |
| **T3 [P1]** Shell profile 原地截断写入 | `internal/runner/deployment.go` | **已完成** | 提交 `a7f8d24`：原子写并保留 symlink 目标 |
| **T4 [P2]** Adminer 公网暴露 | `modules/{postgres,mariadb}/` | **已完成** | 提交 `d6acf32`、`2c8c13d`：通过 T7 条件依赖接入管理员 ForwardAuth |
| **T5 [P2]** `config.SetScalar` 固定临时文件名 | `internal/config/config.go` | **已完成** | 提交 `e97b70e`：使用随机临时文件名，规避跨进程并发冲突 |
| **T6 [P2]** `services.optional` 无人校验 | `internal/runner/manifest.go` | **已完成** | 提交 `73a67b0`：加载期校验可选服务必须匹配 Compose 中真实服务 |
| **T7 [P1]** `requires_capabilities` 条件依赖 | `internal/runner/capability.go` | **已完成** | 提交 `019fc65`、`9a6f79e`：实现 `enabled_by` 机制 |
| **T8 [P1]** 开发中文档迁出 `docs/` | `docs/` → `dev-docs/` | **已完成** | 实施计划与需求矩阵统一收敛在 `dev-docs/`，隔离发布文档站 |
| **T9 [P2]** 依赖与容器漏洞扫描 | `.github/workflows/ci.yml` | **已完成** | 提交 `756c9b6`：集成 `govulncheck` 与镜像扫描门禁 |
| **T10 [P2]** 架构文档去日程化 | `docs/architecture/` | **已完成** | 提交 `1207d08`：保持架构描述系统本身而非跟踪排期 |
| **T11 [P2]** 英文文档对齐 | `docs/en/` | **已完成** | 基线参数与参考文档已完成同步 |
| **T12 [P3]** healthcheck 针对性补充 | 各 `docker-compose.yml` | **已完成** | 重点 Module 关键服务已补齐健康探测 |
| **T13 [P3]** CLI Flag 解析不统一 | 各 `cmd/` | **已完成** | 统一采用带错误处理与严格参数计数的标准模式 |
| **T14 [P3]** certificate/identity 缺逐操作 schema | `contracts/` | **维持已知** | 仅含 resource schema，暂缓补 operation schema（待第二 Provider） |
| **T15 [P2]** 完成的计划不归档 | `dev-docs/plans/` | **已完成** | 引入 `dev-docs/plans/archived/` 机制与自动状态生成器 |
| **T16 [P2]** `allow_groups` 用户配置面下线 | `modules/oauth2_proxy/` | **已完成** | 提交 `2c8c13d`：从配置面删除，由 `platform_admin` 角色拓扑派生 |

---

## 2. 本次审计发现的核心问题清单

在当前分支与工作树基线上，发现了以下 8 项需要关注并整改的问题：

```
[P1] F1. 子进程 context 传播与进程组取消在持久任务落地后仍有死角
[P2] F2. OpenAPI 升级至 0.7.0-m2 后前端 schema.d.ts 未同步且缺少 CI 门禁
[P3] F3. dev-docs/plans/index.md 结构错误：活跃提案误放入已归档表格
[P4] F4. 多项已 100% 验收主题未归档，Vikunja 模块仍停留在 developing
[P5] F5. 根目录存在未忽略的构建二进制 actions-controller
[P6] F6. CheckCompensation 当前仅为空转加锁，与完成事件语义不符
[P7] F7. modules/lego/hook 缺少单元测试覆盖
[P8] F8. Incus compute 真实宿主 E2E 处于外部资源阻塞状态
```

---

### F1 [P1] 子进程 context 传播与进程组取消在持久任务落地后仍有死角

- **定位**：
  - `internal/runner/deployment.go:1446`
  - `internal/runner/backup_transfer.go:296, 297, 353, 382, 699`
  - `internal/runner/network.go:43, 300, 306, 386, 421, 422, 486`
  - `internal/runner/snapshot.go:517`
  - `internal/runner/module_commands.go:311`
- **现状**：
  - 上次审计 §5 指出：“`anasd` 一旦支持写操作，子进程 context 传播必须升级为前置条件”。
  - 当前 `anasd` 已经支持 Plan、Apply、Lifecycle（start/stop/restart）及 Rollback 等全量写操作，并在 `jobexecutor` 中提供了任务取消 API（`POST /api/v1/jobs/{id}/cancel`）。
  - 代码库虽然新增了 `internal/processgroup` 并在 `hook.go` 和 `compose_scope.go` 中启用了 `externalCommandContext`，但在以下关键路径中**依然使用无 context 的原生 `exec.Command`**：
    1. `deployment.go:1446` 的 `btrfsCommand = func(args ...string) error { cmd := exec.Command("btrfs", args...) ... }`：底层的快照、删除等调用完全丢弃了外部传入的 `ctx`。
    2. `backup_transfer.go` 中所有的 `btrfs send`、`btrfs receive` 以及 `rsync -aHAX` 均使用裸 `exec.Command`。
    3. `snapshot.go:517` 的 `cp -a --reflink=always`。
    4. `network.go` 中关于 Docker 网络的创建、检查与删除。
- **后果与风险**：
  - 当守护进程因 SIGTERM 退出或用户在控制台取消任务时，耗时较长的 `btrfs send/receive`、`rsync` 或 `cp --reflink` **无法被中止**。
  - 会产生不受控的后台孤儿进程，可能损坏正在同步的目标卷，或在锁释放后与下一任务产生并发文件写入冲突。
- **整改建议**：
  - 将上述 `exec.Command` 全部统一替换为 `externalCommandContext(ctx, ...)`，确保 `processgroup.Configure` 正确配置了进程组及宽限期终止。

---

### F2 [P2] OpenAPI 升级至 0.7.0-m2 后前端 `schema.d.ts` 未同步且缺少 CI 门禁

- **定位**：
  - `api/openapi.yaml`
  - `web/src/api/schema.d.ts`
  - `.github/workflows/ci.yml`
- **现状**：
  - `api/openapi.yaml` 在本次工作中已增加 M2 的生命周期接口（`manageWorkspaceModules`、`rollbackWorkspaceDeployment`、`cancelJob`），版本升至 `0.7.0-m2`。
  - 运行 `npm --prefix web run generate:api` 后，`web/src/api/schema.d.ts` 产生超过 200 行增量更新，表明前端类型库尚未根据最新的 OpenAPI 规范重新生成。
  - 目前的文档与前端 CI 门禁中，虽然有 `npm --prefix web run typecheck` 和 `npm --prefix web test`，但**没有检查 `schema.d.ts` 与 `openapi.yaml` 是否保持一致**。
- **后果与风险**：
  - 前端开发调用新接口时缺少类型提示；如果手动修改类型，会导致前后端契约与生成的声明脱节。
- **整改建议**：
  - 重新生成 `web/src/api/schema.d.ts` 并提交。
  - 在 `ci.yml` 或 `npm run docs:check-*` 链路中加入 `git diff --exit-code web/src/api/schema.d.ts` 的一致性门禁检查。

---

### F3 [P2] `dev-docs/plans/index.md` 结构错误：活跃提案误放入已归档表格

- **定位**：
  - `dev-docs/plans/index.md:41`
  - `dev-docs/plans/ai-agent.md`
- **现状**：
  - `ai-agent.md` 是一个处于提案状态的活跃实施计划（frontmatter 为 `status: proposed`，配套需求矩阵 `dev-docs/requirements/ai-agent.md` 已在要求索引中正确列为活跃矩阵）。
  - 但在 `dev-docs/plans/index.md` 中，该计划被写在 `## 已归档` 小节下的表格中，且占用了「结论去向」列填写需求范围。
  - CI 脚本 `scripts/ci/plan-status.mjs` 仅校验了行内状态与文件 frontmatter 是否一致，没有检查文件路径是否在 `archived/` 子目录下。
- **后果与风险**：
  - 维护者和读者在查看计划目录时，会在归档区看到一个尚未实现的计划，破坏了“归档区只放已完成交付成果”的仓库规范。
- **整改建议**：
  - 将 `[AI Agent 编排](ai-agent.md)` 移回上方的主实施计划表格中。
  - 在 `scripts/ci/plan-status.mjs` 中增加校验：`## 已归档` 表格内的链接路径必须以 `archived/` 开头。

---

### F4 [P2] 多项已 100% 验收主题未归档，Vikunja 模块仍停留在 developing

- **定位**：
  - `dev-docs/plans/vikunja-module.md`
  - `dev-docs/plans/conditional-capability-dependency.md`
  - `dev-docs/plans/weak-capability-dependency.md`
  - `modules/vikunja/module.yml:14`
- **现状**：
  1. `vikunja-module.md`：M1–M4 全部勾选完成，需求矩阵 28/28 项全部标注为已完成并通过真实端到端测试，但计划状态仍为 `status: implementing`，模块状态仍为 `status: developing`。
  2. `conditional-capability-dependency.md`：M0–M2 全部勾选完成，需求矩阵 19/19 项全部完成，计划状态仍为 `status: implementing`。
  3. `weak-capability-dependency.md`：M0–M2 代码全部完成，需求矩阵 15/15 项全部完成，计划状态仍为 `status: implementing`。
- **后果与风险**：
  - 已完成的工程未按约定移入 `archived/`，导致未归档计划列表过长，掩盖了正在进行的真实任务（如 M2 控制台、Forgejo 接入、Samba OID 迁移）。
  - Vikunja 实际上已完全满足 release 标准，但对外展示仍为开发中。
- **整改建议**：
  - 将上述三份计划的 frontmatter 改为 `status: done` 并移入 `dev-docs/plans/archived/`。
  - 运行 `npm run docs:plan-status` 重新生成索引。
  - 评估将 `modules/vikunja/module.yml` 的 status 提升为 `release`。

---

### F5 [P2] 根目录存在未忽略的构建二进制 `actions-controller`

- **定位**：
  - `.gitignore`
  - 仓库根目录 `/actions-controller`
- **现状**：
  - 根目录存在由本地开发编译生成的 Mach-O 二进制文件 `actions-controller`（约 9.2MB）。
  - 查阅 `.gitignore`：
    ```gitignore
    /anas
    /anasd
    /anas-helper
    ```
    未包含 `/actions-controller`。
- **后果与风险**：
  - 二进制文件长期出现在 `git status` 的 untracked 列表中，极易发生误提交，造成仓库体积膨胀。
- **整改建议**：
  - 将 `/actions-controller` 追加至 `.gitignore`。

---

### F6 [P3] `CheckCompensation` 当前仅为空转加锁，与完成事件语义不符

- **定位**：
  - `internal/runner/deployment_application.go:560-573`
  - `internal/jobexecutor/executor.go:351-363`
- **现状**：
  - 在 `jobexecutor` 中，当任务被取消或异常中断时，会调用 `service.CheckCompensation(ctx)`，成功后写入 `compensation_checked` 事件并确认补偿完毕。
  - 但在 `deployment_application.go` 中，该函数的实现仅为：
    ```go
    unlocked, err := acquireRuntimeLockForApplication(ctx, stateDir(service.workspace), service.events, service.daemon)
    if err != nil {
        return deploymentApplicationError(service.applicationLockCode(), err)
    }
    unlocked()
    return nil
    ```
- **分析**：
  - 虽因 `compose-execution-boundary`（Compose 执行边界）尚在提案阶段（2/17 已完成），目前暂未实装真正的容器补偿逻辑，但向日志和审计输出 `result: clean` 的 `compensation_checked` 事件可能误导操作员。
- **整改建议**：
  - 在事件输出中准确注明为“运行时锁可用性核查”，或在 `dev-docs/plans/compose-execution-boundary.md` 中排期落实真正的补偿状态扫描。

---

### F7 [P3] `modules/lego/hook` 缺少单元测试覆盖

- **定位**：
  - `modules/lego/hook/`
  - `internal/deploymentaudit/`
- **现状**：
  - 仓库内 23 个 Module 中，除有意保留的脚手架 `freeradius` 外，其余 21 个拥有 Hook 的 Module 均配有 `hook/main_test.go`。
  - `modules/lego/hook` 是全仓唯一一个没有专属测试文件的功能性模块 Hook。
  - 此外，`internal/deploymentaudit` 作为审计事件映射核心包，也缺少包内直接的 `_test.go` 文件。
- **整改建议**：
  - 为 `modules/lego/hook` 补齐针对配置环境变量解析与 DNS Provider 分发的单元测试。
  - 为 `internal/deploymentaudit` 补充针对 `ObserveJobCommit` 与模板覆盖的测试。

---

### F8 [P3] Incus compute 真实宿主 E2E 处于外部资源阻塞状态

- **定位**：
  - `dev-docs/plans/incus-module.md`
  - `dev-docs/plans/forgejo-module.md`
  - `modules/incus/`
- **现状**：
  - 本轮工作中，`contracts/compute` 已经成功完成了从单实例执行向“多租约沙盒围栏”的架构重塑，`modules/incus` 作为 Provider 已经具备完整的 Client 与 Provisioner 单元验证，Forgejo 也已切至通过 Contract 声明消费资源。
  - 但由于测试体系需要真实的外部 KVM/Incus 虚拟化宿主，M6 里程碑（真实 Incus 宿主验收）目前处于声明性阻塞状态（等待物理/独立虚拟化测试节点）。
- **整改建议**：
  - 保持 `incus` 和 `forgejo` 模块为 `developing` 状态；
  - 待测试宿主具备条件后，执行 `test-env/scripts/forgejo-agent-api-probe.sh` 及端到端验证。

---

## 3. 架构重大演进与技术事实确认

本次审计核实了当前工作树中以下几项关键架构落地事实：

1. **Web 控制台与任务执行引擎（Console M1.5 / M2）**：
   - 实现了基于 mTLS 的受信反向代理身份入口（Traefik 接入），与直连本地认证相互隔离。
   - 实现了持久化任务引擎 `jobexecutor`，支持任务入库、FIFO 消费、Crash 状态恢复与取消。
   - 实现了生命周期操作（start/stop/restart/rollback）的依赖链预演（Preview）与幂等执行。
2. **Compute 契约重塑与 Incus Provider**：
   - 彻底废弃原有的 `create`/`exec` 实例级操作，统一重塑为以 `sandbox`（Incus 项目）与 `quota`（CPU/内存/磁盘配额）为核心的租约围栏模型。
   - 实现了多 Consumer 之间项目沙盒的强隔离校验（`contracts.go` 拒绝沙盒重名）。
3. **Samba AD 身份锚点正式 OID 迁移**：
   - IANA 正式将 PEN `66678` 授予 ANAS 维护者。
   - `samba_dc` 新安装已全面切换至正式 OID `1.3.6.1.4.1.66678.1.2.1` 与稳定 `schemaIDGUID`。
   - 提供了离线迁移脚本 `migrate-identity-anchor-oid.sh` 与新旧 ACL 平滑过渡机制。

---

## 4. 建议整改实施路线

| 优先级 | 动作项 | 涉及路径 | 预期收益 |
| --- | --- | --- | --- |
| **P1** | 全面替换 runner 中的裸 `exec.Command` | `internal/runner/{deployment,backup_transfer,network,snapshot}.go` | 解决任务取消与进程退出的死角，防产生孤儿进程 |
| **P2** | 重新生成前端类型定义并增加一致性门禁 | `web/src/api/schema.d.ts`、`.github/workflows/ci.yml` | 保证前后端类型同步，防契约隐蔽失步 |
| **P3** | 修复 `dev-docs/plans/index.md` 活跃提案误挂入归档区 | `dev-docs/plans/index.md` | 纠正索引误导，恢复规范结构 |
| **P4** | 归档 Vikunja 及条件/无序依赖计划，升级 Vikunja 状态 | `dev-docs/plans/archived/`、`modules/vikunja/module.yml` | 沉淀交付事实，闭环任务看板 |
| **P5** | 在 `.gitignore` 中补充 `/actions-controller` | `.gitignore` | 保持工作区干净，防二进制入库 |
| **P6** | 为 `modules/lego/hook` 补齐单元测试 | `modules/lego/hook/main_test.go` | 消除模块 Hook 唯一的测试盲区 |

---

## 5. 勘误与历史对比说明

与 2026-08-23 报告对比，本报告明确指出：
- **`anasd` 角色发生本质变化**：从前期的“纯只读 HTTP API”正式演变为“具备持久写操作与任务调度的控制面守护进程”。因此，此前作为延后项处理的子进程 context 传播与取消控制，在本次审计中正式升级为必须整改的 P1 问题。
- **模块数量**：由 22 个增至 23 个（新增 `incus` 作为 `compute` contract 的 provider）。
