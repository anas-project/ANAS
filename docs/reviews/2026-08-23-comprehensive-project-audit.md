---
doc_type: review
status: historical
created: 2026-08-22
updated: 2026-08-28
review_baseline: 2026-08-23
---

# ANAS 综合项目审计与整改状态（2026-08-23）

> 状态：**2026-08-23 工作树历史快照**。后续只补充解决状态或勘误；新的审计结论应创建新的
> 日期前缀评审文档。

本文面向维护者和审查者，记录 2026-08-23 对全面审计报告逐条复核后的结论、当时已完成的整改、
未解决发现和被否决建议。每条结论均对照当日代码基线验证；本文不代表当前工作树状态，也不替代
需求矩阵、实施计划或操作手册。

> 复核结论摘要：原报告 25 条发现中，3 条不成立（其中 1 条引用了不存在的文件），4 条严重性被高估，
> 1 条漏报了同类问题的另一半，1 条的整改建议照做会让 CI 直接变红。原报告的「优势与良好实践」里
> 也有两处与代码不符。

## 0. 阅读边界与基线

每项未解决发现按评审当时的**定位**、**现状**、**建议整改**、**反例**、**验证**和**风险**记录。
执行整改前必须重新核对当前代码，并将正式约束和施工进度分别写入对应的需求与计划文档，不能把
这份历史快照直接当作当前任务队列。

发现之间的评审时依赖在标题后标注；它只描述 2026-08-23 的顺序关系。

### 验证基线

2026-08-23 复核时以下命令全部通过；它们是基线证据，不是长期维护的现行门禁清单：

```bash
go vet ./... && go test ./... && go run ./cmd/gen-module-docs --check && go run ./cmd/gen-contract-docs --check && npm run docs:check-requirements && npm run docs:build
```

改动 `module.yml` 的参数会连带影响四个硬编码计数的测试
（`config_inventory_test.go`、`config_schema_cli_test.go`、`config_schema_inventory_test.go`、
`module_type_coverage_test.go`），以及各 Module 的 README 与 `docs/technical.md` 中手工维护的参数表。
`gen-module-docs --check` 只校验不改写，表格要手工改。

---

## 1. 复核确认的基础事实

- 代码库：Go 1.23+（CLI `anas`、守护进程 `anasd`、辅助工具 `anas-helper`、Module Hook 与 Contract
  Provider）、Node.js（文档站与需求覆盖门禁）、Shell（安装器与测试套件）。
- 22 个 Module（15 `release`、7 `developing`），5 组 Contract（`certificate`、`compute`、`identity`、
  `object_storage`、`relational_database`，其中 `compute` 尚未提交）。
- `anasd` 在评审基线时是**纯只读** HTTP API：`internal/api/httpapi/handler.go` 对所有路由强制 GET。
  这一点是若干发现优先级判断的前提，见 §5。
- 命令执行防注入成立：所有子进程都以 `[]string` 强类型参数切片传递，没有拼接 Shell 字符串。

---

## 2. 基线时已完成的整改

截至 2026-08-23，以下整改已完成并通过验证基线：

| 改动 | 位置 | 说明 |
| --- | --- | --- |
| oauth2-proxy 不再用命令行传密钥 | `modules/oauth2_proxy/docker-compose.yml` | 删除 `--client-secret` / `--cookie-secret` 两个 flag。该服务本就有 `env_file: .env`，而 `OAUTH2_PROXY_CLIENT_SECRET` / `OAUTH2_PROXY_COOKIE_SECRET` 正是 oauth2-proxy 原生环境变量名，Hook 也已写入，因此删除 flag 后配置照常生效，密钥不再进入宿主 `ps` 进程表 |
| 部署失败清理 staging | `internal/runner/deployment.go` | `materializeDeployment` 加 `promoted` 标志与 `defer os.RemoveAll(stagingRoot)`，promote 成功后置位。写法照抄 `credential_transaction.go` 的既有模式 |
| 删除 llng / netbird 的死 adminer 配置 | `modules/{llng,netbird}/module.yml`、`hook/main.go`、README 与 technical 文档（中英各一） | 见下方说明 |
| 删除死目录 | `modules/collabora/collabora/` | 该目录下只有 4 个 `.DS_Store`，无任何真实文件，无任何引用 |
| 仓库忽略 macOS / 本地工具产物 | `.gitignore` | 增加 `.DS_Store`、`.claude/settings.local.json`、`.claude/worktrees/` |
| 侧边栏补挂 | `docs/.vitepress/config/sidebar.ts` | `module-iam-bidirectional-logout` |
| 代码块语言修正 | `docs/research/self-hosted-open-source-mail-services-research.md` | `` ```dns `` → `` ```text ``（shiki 2.5.0 没有任何 dns/zone 语言包，`text` 是正确答案而非将就） |
| **需求索引状态列改为生成** | `scripts/ci/requirement-status{,-lib,-test}.mjs`、`package.json`、`.github/workflows/docs.yml`、`docs/requirements/index.md` | 从需求矩阵与计划里程碑表算出每份文档的完成度并写入索引；`--check` 模式进文档 CI。验收依据是 `REQID-R-010`—`R-013`，见[需求 ID 矩阵采用实施计划](/plans/requirement-id-adoption) M3 |
| 里程碑状态词汇规范化 | `docs/plans/module-command-capability.md` | M3/M4 两行改为以 `实施中` / `阻塞` 开头，细节写在分号之后 |

### 关于 llng / netbird 的 adminer 配置

原报告只发现了 netbird，**llng 有完全相同的问题**。`git log -S` 显示两者的 `adminer_enabled` 参数
和 `services.optional` 声明都是提交 `f9d6d6b`（引入 `services.optional` 机制的 Contract 重构）一次
加上的，在那之前两个 Module 都没有这个参数，Compose 里也从来没有过 adminer 服务——是从
postgres/mariadb 抄来的，那两个用的是小写 `postgres_adminer` 且 Compose 里确有
`anas_postgres_adminer`。

两者性质还不同：llng 确实消费 `relational_database` Contract，配 adminer 概念上说得通但完全多余
（提供数据库的 Module 自带 adminer，指向同一个库）；netbird 根本没有数据库，management 用本地卷
里的 sqlite，adminer 连不上。

后果是两个 Module 的 `adminer_enabled` 成了**对用户可见的死参数**——已进入生成的文档表格和
`anas config` 的参数清单，设成 `true` 什么也不会发生。

**根因值得单独处理，见 T6。**

---

## 3. 已核实不成立（不要按原报告执行）

| 原报告结论 | 复核结果 |
| --- | --- |
| P2「在 `internal/modulepackage/archive.go` 补 symlink 逃逸检查」 | **该文件不存在**，`internal/modulepackage/` 只有 `package.go` 与测试。真正的解包在 `internal/modulestore/store.go:1138-1190`，已经拒绝路径穿越（`clean != name`、`IsAbs`、`../`、反斜杠）、通过 `default:` 分支拒绝一切非 `TypeReg`/`TypeDir` 条目（symlink 落在此处直接报错）、有数量与字节上限、用 `O_EXCL` 建文件。**无需改动。** |
| 汇总表 High 项「`.gitignore` 缺失」 | `.gitignore` 一直存在且内容完整。原报告正文写的是「规则需补全」，汇总表把它升级成「缺失」是错的。 |
| 优势项「所有子进程通过 `exec.CommandContext` 传递强类型参数切片」 | 全仓 **0 处** `exec.CommandContext`，38 处 `exec.Command`。与原报告自己的缺陷条目 3 直接矛盾。「参数以 `[]string` 传递、无 Shell 拼接」这半句成立。 |
| 「netbird `iam_protocol` 枚举越界，应改为 `[auto, oidc]`」 | **不成立，宽枚举是刻意的。** `TestBundledNetBirdStructuredSAMLReachesCapabilityResolver` 明确断言：`identity.login_protocol` 是跨 Module 的通用 IAM 选择器，参数 schema 必须接受核心拓扑认识的每一种协议，好让拒绝来自 Capability 解析器的有用信息（`netbird supports [oidc]`），而不是一句干巴巴的枚举错误。本轮曾按原报告收窄枚举，被该测试拦下后已还原，并在 `module.yml` 补了说明注释。 |
| 优势项「不挂载 `/var/run/docker.sock`」 | `modules/traefik/docker-compose.yml:45` 挂了（`:ro`，Docker provider 的标准做法）。原意大概是说 samba_dc 用 `anas-helper` 而非特权容器，那部分成立。 |
| Low 项「`freeradius` 脚手架完善」 | `module.yml` 写明 `status: developing`、`description: RADIUS server module scaffold.`。这是有意状态，不是缺陷。 |
| 「vikunja 采用 `read_only: true` + `cap_drop: [ALL]`」 | vikunja 有 `read_only` 但没有 `cap_drop`。eturnal、forgejo、samba_dc、versitygw 两者都有。 |

---

## 4. 基线时未解决的发现

### T1 [P1] `master` 分支缺少 Go CI 门禁

**定位**：`.github/workflows/`

**现状**：`go test ./...` 只在 push 到 `anas-release` 分支时运行（`anas-release.yml:135`）。
`container-images.yml` 只测三个包且只在 `image-release` 分支触发。`docs.yml` 在所有 PR 上跑，但只做
文档生成器校验、`go test ./cmd/materialize-module-docs` 和站点构建。**针对 `master` 的 PR/Push 没有
任何自动化跑完整 `go test ./...` 或 `go vet ./...`。**

**要做什么**：新增 `.github/workflows/ci.yml`，在 `pull_request` 和 push to `master` 上运行：

- `go vet ./...`
- `go test ./...`
- `go run ./cmd/gen-module-docs --check` 与 `go run ./cmd/gen-contract-docs --check`（若不愿与
  `docs.yml` 重复，可从 `docs.yml` 移过来）
- `npm run docs:check-requirements`
- 关键 Shell 测试：`scripts/ci/anas-release-version-test.sh`、
  `scripts/ci/verify-anas-release-test.sh`、`scripts/ci/module-revisions-test.sh`

顺带把 T9（漏洞扫描）合并进同一个 workflow。

**不要做什么**：不要在这个 workflow 里跑 e2e 或任何需要 Docker daemon 的测试——`test-env/` 下的
脚本需要真实主机，放进 PR 门禁会长期红着。

**验证**：在一个故意改坏的分支上开 PR，确认 workflow 变红。

**风险**：低。纯新增。

---

### T2 [P1] `install.sh` 缺少 `main()` 包裹

**定位**：`install.sh`（顶层语句，文件末尾没有 `main "$@"`）

**现状**：`README.md:20` 就是 `curl ... | sh` 的用法，而 `install.sh` 会安装二进制、给
`anas-helper` 打 `setcap`、写用户 Shell profile。脚本以顶层语句直接执行，下载中途断连会执行半截，
结果是**一个装了一半的系统**，而不是一次失败的安装。

原报告给了 Medium。对 `curl | sh` 这种分发方式，函数包裹是标配，应当按 P1 做。

**要做什么**：把现有全部顶层逻辑移入 `main() { ... }`，文件最后一行写 `main "$@"`。保持
`set -eu`、`mktemp -d` 与 `trap ... EXIT HUP INT TERM` 的既有行为不变。

**不要做什么**：不要顺手重构安装逻辑。这个改动的价值在于它是纯机械的、可逐行比对的。

**验证**：`sh -n install.sh`；`scripts/ci/install-test.sh`（若该脚本需要网络，至少确认 shellcheck
无新增告警）；人工确认截断后的前 80% 内容执行时不产生任何副作用。

**风险**：低，但要逐行核对缩进后的变量作用域——顶层的赋值在函数内会变成函数局部量，凡是需要跨
函数可见的必须显式处理。

---

### T3 [P1] Shell profile 原地截断写入

**定位**：`internal/runner/init.go`，`writeShellInit`（约 460 行）与 `removeShellInit`（约 484 行）

**现状**：两处都用 `os.WriteFile(path, []byte(current), 0644)` 直接覆盖用户的
`~/.bashrc` / `~/.zshrc`。进程在写入中途被杀会损坏用户的 Shell 配置。

**要做什么**：改为「临时文件 → `Sync()` → `os.Rename`」。仓库里已有可参照的实现：
`internal/modulestore/store.go:1098` 的 `atomicWriteFile`。

**不要做什么——这一条比改动本身重要**：不要直接对 `path` 做 rename。原报告的建议照做会
**比现状更危险**：

1. `~/.bashrc` 被 dotfiles 仓库软链出去非常常见。直接 rename 会把**符号链接本身替换成普通文件**，
   静默破坏用户的 dotfiles 管理。必须先 `filepath.EvalSymlinks` 解析到真实路径，在**真实路径所在
   目录**建临时文件再 rename。
2. 现在的 `os.WriteFile` 对已存在文件不改权限，而 rename 会带来临时文件的权限。必须先
   `os.Stat` 取原文件 mode 并套用；文件不存在时才用 0644。
3. 临时文件必须与目标同目录（跨文件系统 rename 会失败）。

**验证**：新增单元测试覆盖三种情况——目标不存在、目标是普通文件（权限须保留）、目标是指向另一
目录的符号链接（链接须保留，内容写到链接目标）。

**风险**：中。改错了影响用户主目录，所以上面三点必须全部落实。

---

### T4 [P2] Adminer 公网暴露（依赖 T7）

**定位**：`modules/postgres/docker-compose.yml:37-53`、`modules/mariadb/docker-compose.yml` 同段、
`modules/oauth2_proxy/module.yml` 的 `allow_groups`

**现状**：Adminer 路由挂在公网主机名上，只有 Adminer 自己的登录表单。原报告称此为「缺少
ForwardAuth 保护」，定性不准确：仓库自己的判据写在 `modules/ddns_updater/module.yml:20-24`——
「ddns-updater 没有自己的登录，所以这道门不是可选的」，判据是**服务自身有没有认证**，而 Adminer 有。

**真正的风险不是「少一层认证」，是「公网暴露」**：Adminer 的登录表单允许填任意目标主机，一个
公网可达的 Adminer 就是进入内部 Docker 网络的 SSRF / 端口扫描跳板，此外还有 Adminer 自身的历史
CVE（SSRF、任意文件读）。

**已定方案**：保留公网入口，用 T7 的条件依赖挂上 ForwardAuth 网关，网关维持**只允许
`SAMBA_DC_ADMIN_GROUP_NAME` 组**。

复核确认这个前提本来就成立：`modules/oauth2_proxy/module.yml` 的 `allow_groups` 默认值就是
`Admins`，注释写明「默认是管理员组，因为这道门后面的都是管理界面」；Hook 在
`main.go:165-174` 强制它非空（为空直接报错拒绝部署），并把字面量 `Admins` 替换成目录里真实的
`SAMBA_DC_ADMIN_GROUP_NAME`。组成员判定由 IAM 通过 `ANAS_IAM_CLIENT__*__ALLOW_GROUPS` 契约执行。

**所以挂上现有中间件即可满足要求——不需要第二个 oauth2-proxy 实例、第二个 OIDC 客户端或第二套
Cookie。**

**唯一需要堵的口子**：`allow_groups` 目前是**用户可改的物理配置**，运维放宽它时 Adminer 会跟着
放宽且没有提示。而这件事**架构上已经有定论**，写在
[应用目录设计](/architecture/app-catalog-design) §4.1：

> 当前 `oauth2_proxy.allow_groups` 仍是物理配置且默认 `Admins`；迁移期间 Runner 必须验证它与
> `platform_admin` 的解析结果一致，最终应由角色绑定派生而不是由用户重复维护。

同一节还明确把 Adminer 列为 `access.role: platform_admin` + `access.via: forward_auth`，并规定
「用户只能隐藏，不能放宽或改执行点」。**所以正确的堵法不是让 postgres 去检查 oauth2_proxy 的配置，
而是让这个值不再由用户维护**，见 T16。

**要做什么**：

1. 落地 T7 的条件依赖后，给 postgres/mariadb 声明「`adminer_enabled=true` 时需要 `forward_auth`
   Capability」。
2. Compose 里给 Adminer 路由加
   `traefik.http.routers.adminer.middlewares=${ANAS_FORWARD_AUTH_MIDDLEWARE}`，现成模式见
   `modules/ddns_updater/docker-compose.yml:54`；`module.yml` 的 `consumes` 加
   `ANAS_FORWARD_AUTH_MIDDLEWARE`。
3. 就这两步。**不要**在 postgres/mariadb 的 Hook 里读或校验 oauth2_proxy 的组配置——那是把一个
   部署级不变量塞进一个不相关 Module 的责任，而且报错会出现在令人困惑的位置（postgres 抱怨
   oauth2_proxy 的参数）。这个不变量归 Runner 管，见 T16。

**不要做什么**：

- 不要为 Adminer 起第二个 oauth2-proxy 实例。
- 不要用无条件 `requires_capabilities`（这正是 T7 要解决的）：那会让**每一个 postgres 部署都强制
  拉起 oauth2_proxy**，哪怕 `adminer_enabled` 是默认的 `false`。
- 不要新增 `ANAS_FORWARD_AUTH_ALLOW_GROUPS` 导出（本文档早前版本的建议）。既然网关的准入策略要
  收敛成由角色派生的部署级不变量，就不需要把它做成 Consumer 可读的契约字段——那是在为一个即将
  消失的可变性建接口。

**验证**：

- `adminer_enabled=false`（默认）时，渲染产物不含 oauth2_proxy 依赖，Adminer 服务被禁用。
- `adminer_enabled=true` 时，渲染出的 Adminer 路由带 middlewares 标签，
  `docker compose config --quiet` 通过，且依赖排序中 postgres 排在 oauth2_proxy 之后。
- Hook 单元测试覆盖上述两种情况。

**风险**：低。默认值是 `false`，改动只影响主动开启 Adminer 的部署。

**依赖**：T7（条件依赖必须先落地）。T16 独立推进，不阻塞本任务——在 T16 落地前，`allow_groups`
的默认值和 Hook 的非空校验已经提供了实际保护。

---

### T5 [P2] `config.SetScalar` 固定临时文件名

**定位**：`internal/config/edit.go:149`

**现状**：`tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")`。

原报告说这是并发冲突，**理由不对**：同 workspace 有 runtime lock，并发几乎不成立。真正的问题是
**残留**：rename 失败或进程被杀会在用户配置目录留下 `.config.yml.tmp`，下次运行 `os.WriteFile`
静默复用它；如果那个残留文件是上次以 root 跑出来的，写入会直接失败且报错难懂。

**要做什么**：改用 `os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")`，失败路径
上 `defer os.Remove`。保留现有的「先 `Load(tmp)` 校验再 rename」逻辑和 `info.Mode().Perm()` 权限
沿用。

**验证**：`go test ./internal/config/...`。

**风险**：低。2 行改动。

---

### T6 [P2] `services.optional` 与 `disable_services` 无人校验（防复发）

**定位**：`internal/runner/manifest.go:349-356`、`internal/runner/runner.go:1368`、
`internal/runner/module_manifest_test.go:203-207`

**现状**：这是 §2 里 llng/netbird 死 adminer 配置能存在半年的**根因**。

- `services.optional[].enabled_by` 只被解析和 lint（现有测试只检查它是不是 lower snake_case），
  Runner 从不据此做任何事——它纯粹是声明性元数据。
- 真正的禁用发生在 Module Hook 返回的 `disable_services`，Runner 在 `runner.go:1369` 用
  `remove(services, resp.DisableServices...)` 从服务列表里删名字。
- **两端都不校验名字是否真的存在于该 Module 的 Compose 里。** 删一个不存在的名字是静默 no-op，
  声明一个不存在的可选服务也是静默 no-op。

**要做什么**：在 `internal/runner/module_manifest_test.go` 已有的 bundled-module 遍历里增加交叉校验：
读取每个 Module 的 `docker-compose.yml`，断言

1. 每个 `services.optional[].name` 都能对应到一个真实 Compose 服务（含 `anas_` 前缀的命名变体，
   参照 postgres 的 `postgres_adminer` ↔ `anas_postgres_adminer`）；
2. 每个 Module 声明了 `services.optional` 时，其 Hook 的 `disable_services` 返回值也在同一集合内。

第 2 点若难以静态取得（Hook 是独立进程），至少做第 1 点，并在 `module_validation.go` 的
`disable_services` 处理路径上加一条运行期校验：名字不在 Compose 服务集合中时报错而不是忽略。

**不要做什么**：不要把校验做成 warning。静默正是这个问题的成因。

**验证**：`go test ./internal/runner/...`；把 `services.optional[].name` 故意改错一个字母，确认测试
变红。

**风险**：低。可能会暴露其他 Module 的同类问题——那正是目的。

---

### T7 [P1] `requires_capabilities` 需要条件依赖（阻塞 T4）

> **2026-08-28：需求文档已立项，本条转为实施。** 验收依据见
> [条件 Capability 依赖要求](/requirements/conditional-capability-dependency)（19 条，前缀
> `CONDDEP`），实施顺序见[条件 Capability 依赖实施计划](/plans/conditional-capability-dependency)。
> 下面保留基线时的问题描述与已定决策，作为立项理由的记录；**执行以那两份文档为准，不要以本节为准**。

**定位**：`internal/runner/capability.go:800-855`（`requires_capabilities` 解析）、
`modules/*/module.yml` 的 `requires_capabilities` 段

**现状**：`requires_capabilities` 是无条件的。后果是「某个可选服务需要某个 Capability」这件事无法
表达：要么让整个 Module 强制依赖（会波及所有不开该可选服务的部署），要么完全不声明（现状，开了
可选服务也无人提醒）。第一个真实用例是 T4——postgres/mariadb 只有在 `adminer_enabled=true` 时才需要
`forward_auth`。

**已定**：条件依赖一旦成立就进入依赖序列，此后与无条件依赖**完全等价**——照常参与拓扑排序、
照常影响启动顺序、照常在 Provider 缺失时失败。「条件」只决定这条依赖**是否存在**，不改变它存在
之后的任何语义。已写入 `CONDDEP-R-002`、`CONDDEP-R-003`。

**立项时读代码新发现的三件事**（基线审计没有涉及，都进了需求矩阵）：

1. **不能复用既有的 `dependencies.requires[].optional`**，它的语义正好相反：`resolveOrder`
   （`runner.go:822`）把 optional 依赖整个排除出依赖图，**不产生排序边**。那是「碰巧在场就检查」，
   不是「条件成立就必须在场」。见要求文档 §2。
2. **条件必须在 `applyModuleDefaults` 之前可判定**。`resolveOrder` 跑在它之前
   （`deployment.go:450` vs `:454`），而 `applyModuleDefaults` 自己要遍历 `a.order` 才能填值——先有
   顺序才有默认值。所以判定时 `a.env` 里只有用户显式配置过的值，条件求值必须自己回落到
   `a.reg[name].Defaults`。见要求文档 §4、`CONDDEP-R-006`。
3. **翻转条件会让 lock 陈旧**。Provider 进 `moduleLock.Modules`、绑定进 `moduleLock.Bindings`，
   所以 `adminer_enabled` 现有的 `changes.effect: container_recreate` 描述不了这件事。见要求文档 §6、
   `CONDDEP-R-013`、`CONDDEP-R-014`。

**依赖**：无前置。**阻塞 T4**——T4 的第 1 步就是本特性的 M2。

---

### T8 [P1] 开发中文档迁出 `docs/`

**定位**：`docs/requirements/`、`docs/plans/`、`docs/reviews/`、`docs/.vitepress/config/nav.ts`、
`docs/.vitepress/config/sidebar.ts`、`scripts/ci/requirement-coverage.mjs`

**现状与决定**：`docs/` 是面向用户的 VitePress 站点。需求、实施计划和时点评审是**开发过程工件**，
不是给部署 ANAS 的人看的。本轮决定：

- 迁到仓库根 `dev-docs/`，与 `contracts/`、`modules/` 平级。
- **不再维护它们的英文版**。`docs/en/requirements/`、`docs/en/plans/`、`docs/en/reviews/`
  （目前各只有一个 `index.md`）一并删除。
- **归属规则**：单个 Module 私有的需求与计划放 `modules/<name>/dev-docs/`；只有 ANAS 自身或跨
  Module 的才放仓库根 `dev-docs/`。判据是「这份文档描述的约束会不会在该 Module 被移除后失去意义」。
- 本评审文档也是开发工件，一并迁移。

**要做什么**：

1. `git mv docs/requirements dev-docs/requirements`、`docs/plans` → `dev-docs/plans`、
   `docs/reviews` → `dev-docs/reviews`。
2. 删除 `docs/en/requirements/`、`docs/en/plans/`、`docs/en/reviews/`。
3. `nav.ts` 删掉中英双侧对应入口（`nav.ts:14-16`、`nav.ts:33-35`）；`sidebar.ts` 删掉
   `/requirements/`、`/plans/`、`/reviews/` 及其 `/en/` 对应段。
4. `scripts/ci/requirement-coverage.mjs:17-18` 的 `requirementsDir` / `plansDir` 常量改到新位置，
   并按 `REQID-R-006` 的要求**不保留旧路径回退**。同时按 `REQID-R-007` 增加对
   `modules/*/dev-docs/requirements/` 的扫描。
5. 处理 20 余处交叉引用，**分两类，不要一律机械替换**：
   - `docs/developer/` 下的引用（`module-development.md:79`、`module-documentation.md:77`、
     `capability-development.md:103-104`、`requirement-authoring.md:83-84`、
     `documentation-standard.md:121`）是**规范引用**，改成仓库相对路径（GitHub 上可点，站内不渲染）。
   - `docs/architecture/` 与 `docs/research/` 下的引用（`admin-account-system.md:37`、
     `object-storage-capability-design.md:95`、`forgejo-module-design.md:9`、
     `samba-ad-user-planning.md:125`、`iam-logout-application-session-sync.md:11-12`、
     `self-hosted-open-source-iam-research.md:88,168`、`bind9-open-source-web-management-research.md:23`）
     先判断**被引用的结论是不是本来就该写在引用方**。一份架构文档需要读者去翻实施计划才能理解，
     本身就是个信号。这一步是判断工作，是整个任务的主要成本。
   - `docs/index.md:38-39` 直接改写，去掉指向开发工件的链接。
6. 更新 `docs/developer/repository-layout.md` 的目录树与 `documentation-standard.md` §4 的目录分工，
   写入新的归属规则。

**不要做什么**：不要给 VitePress 加 `ignoreDeadLinks`。现在没有配置它，死链会让构建失败——这正是
你需要的信号，用它来确认没有漏改。

**验证**：`npm run docs:build`（死链会报错）、`npm run docs:check-requirements`、
`grep -rn "/requirements/\|/plans/\|/reviews/" docs/` 应当只剩有意保留的仓库相对路径。

**风险**：中。工作量约半天，主要花在第 5 步的判断而非机械替换。建议第 5 步单独一个提交。

**依赖**：`requirement-id-adoption` 计划的 M2 必须与本任务放在同一个提交，否则中间会有一次门禁扫
不到任何文档的窗口。

---

### T9 [P2] 缺少依赖与容器漏洞扫描

**定位**：`.github/workflows/`

**现状**：没有 `govulncheck`、Trivy 或等价工具。

**要做什么**：在 T1 新建的 `ci.yml` 中加一个 job 跑 `govulncheck ./...`。容器镜像扫描放
`container-images.yml`（那里才有构建产物）。

**不要做什么**：初期不要让漏洞扫描 fail the build——先让它出报告，确认误报率后再决定门禁。

**依赖**：T1。

---

### T10 [P2] 按「架构是副产物」原则整理架构文档

**定位**：`docs/architecture/`（20 篇）

**原则**（本轮确定）：架构文档是 requirements 与 plans 的**副产物**。需求涉及架构变更时才更新或新建
架构文档；不涉及就不动。架构文档只写「系统现在是怎么组织的、为什么」，**不写「计划做什么」**。

**现状**：有几篇违反了这条：

- `forgejo-module-design.md:9` 直接把读者导向实施计划
- `object-storage-capability-design.md:95` 引用「M2 执行」——里程碑是计划词汇，不该进架构文档
- `admin-account-system.md:37` 在讨论「管理控制台一旦部署会扩大这条契约的含义」，是计划语气

**要做什么**：这三处（以及 T8 第 5 步里发现的同类）把里程碑口径留在 plans，架构文档只保留最终
形态的描述。T8 的迁移会让这些引用变成死链，正好是一次强制清理的机会——**建议与 T8 合并执行**。

**不要做什么**：不要为了「补齐」而新写架构文档。按上面的原则，20 篇里有多少该存在，取决于有多少
需求真的改变了系统组织方式，不取决于 Module 数量。

**依赖**：与 T8 合并执行最省事。

---

### T11 [P2] 英文文档对齐（范围已收窄）

**定位**：`docs/en/`

**现状（2026-08-23 更新）**：第 1、2 项已完成，第 3 项完成了选中的三篇。

- 第 1 项已完成：`docs/en/reference/contracts/` 补齐 `index`、`commands`、`backup`、`snapshot`；
  `docs/en/reference/cli-contract.md` 已删除，三处引用已改。删除前把该文件仅有的两处 anasd
  英文事实（M3 配置端点共享应用层 schema、M0 只读 DTO 无 invoke 端点）补进了中英两侧的
  `commands.md`，未净损失内容。
- 第 2 项已完成：`docs/en/reference/module-environment-variables.md`。
- 第 3 项按本节判据选出并翻译了描述稳定形态的三篇：`module-contract-resource-design`
  （实施基线）、`iam-capability-design`（阶段 A–D 已落地）、`object-storage-capability-design`
  （M1.1/M1.2 已实施）。`core-implementation-standard` 此前已有英文版。
- 明确排除：`config-state-lifecycle` 与 `runtime-release-state-design`（自述为历史基线）、
  `kanban-ai-agent-orchestration-design`（提案）、`credential-rotation`（目标方案）、
  三篇 `*-draft`（仅部分实现）、`forgejo-module-design`（T10 已判定其违反「架构文档不写计划」）。
- 仍未做：`docs/en/governance/` 不存在。

原始现状：中文 20 篇架构文档，英文 2 篇；`docs/en/governance/` 不存在；英文
`docs/en/reference/contracts/` 缺 `index`、`commands`、`backup`、`snapshot`（中文都有）；英文仍保留
已被中文侧拆解掉的单体 `docs/en/reference/cli-contract.md`；`docs/reference/module-environment-variables.md`
无英文版。

**范围收窄**：T8 已经决定需求、计划、评审不再维护英文版。剩下真正需要对齐的只有**面向用户的
部分**，按优先级：

1. `docs/en/reference/contracts/`：补 `index`、`commands`、`backup`、`snapshot`，删除已废弃的
   `cli-contract.md` 并更新 `docs/en/reference/index.md:5`、`docs/en/guide/index.md:10`、
   `sidebar.ts:227` 三处引用。这是**用户直接依赖的接口文档**，陈旧的危害最大。
2. `docs/reference/module-environment-variables.md` 的英文版。
3. 架构文档：按 T10 的原则，英文化的对象是「系统当前形态」而不是演进历史，因此**不是 20 篇都要
   翻**。先确定哪几篇描述的是稳定形态，只翻那些。

**不要做什么**：不要按「中文有 N 篇所以英文也要有 N 篇」来推进度。

---

### T12 [P3] `healthcheck` 针对性补充

**定位**：`modules/*/docker-compose.yml`

**现状**：完全没有 healthcheck 的 Module：`ddns_updater`、`eturnal`、`lam`、`lego`、`netbird`、
`oauth2_proxy`。全仓只有 6 处 `condition: service_healthy`。

**要做什么——不要一刀切补**。healthcheck 有真实成本（周期性起进程、日志噪音、`depends_on` 语义
变化）。只补两类：

1. **被别人 `depends_on` 的服务**（现有 `depends_on` 使用者：authentik、forgejo、casdoor、
   nextcloud、samba_dc）。
2. **`oauth2_proxy`** —— 它是 ForwardAuth 的守门人，它挂了所有受保护服务返 500 而不是降级。

**不要做什么**：不要给 `lego` 补 healthcheck——它是一次性签发任务，healthcheck 语义不适用。

---

### T13 [P3] CLI Flag 解析不统一

**定位**：`internal/runner/runner.go:295`（`run`）、`runner.go:335`（`runRollback`）、
`internal/runner/deployment.go:243`、`deployment.go:892`

**现状**：仓库里 20 余处子命令已用支持穿插参数的 `parseInterspersed`，这四处仍用 `fs.Parse`。这几个
命令本来就不接受位置参数，所以后果不是「参数被误读」，而是**多余的位置参数被静默忽略**——
`anas plan foo --verbose` 里的 `--verbose` 会被悄悄丢掉。

**要做什么**：比统一改成 `parseInterspersed` 更值得做的是**对多余位置参数报错**。两者可以一起：
改用 `parseInterspersed` 拿到 `positional`，非空即返回 usage 错误。

**风险**：低，但会改变现有（错误的）宽容行为，属于面向用户的行为变化，值得在提交信息里写明。

---

### T14 [P3] `certificate` / `identity` Contract 缺逐操作 schema

**定位**：`contracts/certificate/contract.yml`、`contracts/identity/contract.yml`

**现状**：**不是「缺失」**——两者都有 resource schema（`schemas/resource.yml` 与
`schemas/client.yml`），没有悬空引用。只是没有逐操作的 `request_schema` / `result_schema`，而
`object_storage`、`relational_database`、`compute` 都有。代价是 `gen-contract-docs` 给这两个契约生
不出参数表。

**建议：暂缓。** 等到出现第二个 Provider 实现、操作签名开始出现分歧时再补，比现在凭空定义更准。
这条留在清单里是为了记录「这是已知的不一致，不是没人看见」。

---

### T15 [P2] 完成的计划不归档、计划状态两套写法（与 T8 一起做）

**定位**：`docs/plans/*.md` 的状态声明、`docs/plans/index.md`、
`docs/developer/documentation-standard.md` §4

**现状**：仓库已有两条相关规则，但都没执行到位。

规则一（`documentation-standard.md` §4）：「`plans/` 文档在标题下用引用块声明状态、日期与目标，
**不使用 frontmatter**」。实际是 7 份计划里 3 份用 frontmatter（`forgejo-module`、`versitygw-module`、
`requirement-id-adoption`），4 份用引用块，而这 4 份的引用块又是 4 种不同形状：

| 文档 | 状态写法 |
| --- | --- |
| `changelog-rollout.md` | `> 日期：…` 后另起一段 `> 状态：**提案**。…` |
| `domain-separation.md` | `> 状态：实施中（WP1-WP3 代码与 WP4 验收入口已落地；…）` |
| `web-api-admin-console.md` | `> 状态：**部分实施**。M0、M0.5、M0.6 已落地；…` |
| `workspace-backup.md` | `> [!NOTE]` 块 + 另一段 `> 状态：一、二、三、四、五已落地。…` |

**状态在这个仓库里写了 7 遍、7 种格式，没有一种是机器能读的。** 用 frontmatter 的三份里，值本身也
不统一（`not-started` 与 `not_started` 都出现过）。

规则二（同节规则 5）：「计划全部完成后归档或删除，不留下一份看起来仍在进行的文件」。
`workspace-backup.md` 是最好的反例——426 行，自己的 `[!NOTE]` 写着「这是已实施功能的设计与迁移
记录，正文中的『当前』指制定计划时的历史基线」，却仍然躺在活跃计划目录里。

**要做什么**：

1. **计划文档统一用 frontmatter 的 `status` 字段**，取值限定为 `proposed` / `implementing` /
   `partial` / `done` 四个之一。正文的引用块保留给人读的细节（哪个 WP 落地了、基线 commit 是多少），
   但**机器读的唯一来源是 frontmatter**。相应修改 `documentation-standard.md` §4 里「不使用
   frontmatter」那条规则——现在的规则和现实相反，改规则比改 7 份文档更诚实。
2. **`status: done` 的计划移入 `dev-docs/plans/archived/`**，并在移动前执行规则 5：把稳定结论沉淀
   到需求、架构、开发或运维文档。`workspace-backup.md` 是第一个候选。
3. **需求覆盖门禁跳过 `archived/`**，避免归档计划里的历史归属表继续参与一致性校验。
4. `plans/index.md` 的状态列改成从 frontmatter 生成，不再手工维护——手工维护的状态列迟早和文档
   正文对不上。

**明确不做：需求文档不按已实现/未实现分目录或改文件名。** 三条理由：

- **需求实现之后不会失效**，它变成回归判据。把已实现的需求移出视野，等于让下一次改动看不见它必须
  维持的约束——这正是需求矩阵存在的意义。
- **「已实现」不是文档级属性，是条目级的。** `forgejo-module.md` 的 36 条里 M0/M1 的 11 条已完成、
  M2/M3 的 14 条在施工，`web-api-admin-console.md` 的 100 条同理。文件名和目录名放不下这个状态。
  状态属于计划文档的归属表，那里已经有了。
- 仓库现有规则已经写明「两者都使用稳定文件名，进度通过正文状态表达，**不通过重命名表达**」。这条
  是对的，应当保留。

### 需求索引的状态列：已解决（2026-08-23）

`docs/requirements/` 下 8 份文档全部在 `index.md` 里，反向也没有指向不存在文件的条目——清单一直是
完整的。问题在状态列：原先全是「当前」，那是**文档级**状态（这份文档仍然有效），不是完成度。

**已实现为生成列**，见 §2。完成度从既有内容算出：需求矩阵给出全部 ID，配套计划的里程碑归属表给出
归属与里程碑状态，两者一 join 即得。当前输出：

```text
forgejo-module.md                        11/36 已完成
module-command-capability.md             23/34 已完成
requirement-id-adoption.md                4/14 已完成
versitygw-module.md                      30/32 已完成
web-api-admin-console.md                 11/100 已完成
iam-provider.md / module-iam-bidirectional-logout.md / vikunja-module.md   无矩阵（未采用 ID）
```

**剩下的不是索引的问题**：三份没有配套计划的需求文档（`iam-provider`、
`module-iam-bidirectional-logout`、`vikunja-module`）没有归属表，因此**没有任何完成度可算**——索引
如实显示「无矩阵（未采用 ID）」，这一行本身就是待办提示。`requirement-id-adoption` 的 M1 会解决其中
最重要的一份。反向还有三份「有计划无需求」：`changelog-rollout`、`domain-separation`、
`workspace-backup`——ID 机制之前的产物，本轮不动。

实现过程中撞到一个已有的坑并已记录为 `REQID-R-014`：`parseRequirementMatrix` 用整行文本扫描判定
需求是否退出，于是**一条描述退出规则的需求会把自己判成已退出**。本轮先绕开措辞，专用标记留给 M0。

**那 Agent 怎么跳过？** 用比目录结构更便宜的三个杠杆，按有效性排序：

1. **T8 的归属规则本身就是最强的过滤器。** Module 私有需求放 `modules/<name>/dev-docs/`，只有做那个
   Module 的人（或 Agent）会读到；跨 Module 的才在根 `dev-docs/`。Agent 的实际检索是**按主题**而不是
   按状态，主题分区比状态分区有效得多。
2. **`index.md` 已经是状态表**，两个目录的索引都有「状态」列。读一个索引就能决定读哪份，成本是几十行
   而不是四千行。第 4 步让它变成可信的。
3. **规范化后的 frontmatter `status`** 让「跳过所有 `done` 的计划」变成一次 grep。

如果还要给 Agent 一个显式入口，正确做法是在 `AGENTS.md` 里写一句「读 `dev-docs/` 前先看 index 的
状态表」，而不是重排目录。

**验证**：`npm run docs:check-requirements` 通过且不再扫描 `archived/`；
`grep -L "^status:" dev-docs/plans/*.md` 只剩 `index.md`；`npm run docs:build` 通过。

**依赖**：与 T8 同时做最省事（都要动 `plans/` 的路径）。第 1 步可以独立先做。

---

### T16 [P2] `oauth2_proxy.allow_groups` 应由角色绑定派生，而不是由用户维护

**定位**：`modules/oauth2_proxy/module.yml` 的 `config.allow_groups`、
`modules/oauth2_proxy/hook/main.go:165-174`、`docs/architecture/app-catalog-design.md` §4.1

**问题**：网关的准入策略现在是一个**用户可改的部署级物理配置**。它的默认值正确（`Admins`，经
Hook 解析成真实的 `SAMBA_DC_ADMIN_GROUP_NAME`），Hook 也拒绝空值，但没有任何东西阻止运维把它改宽。
一旦改宽，**所有**挂了这道门的服务同时改宽——包括 Adminer，而且没有提示。

**这个问题的答案架构上已经定了**，写在[应用目录设计](/architecture/app-catalog-design) §4.1：

> `access.role: platform_admin` 由 Runner 按消费位置解析为管理员组名或 DN；Manifest 不应把
> `category: admin`、`Admins` 字符串和完整 LDAP DN 混写。
>
> 当前 `oauth2_proxy.allow_groups` 仍是物理配置且默认 `Admins`；迁移期间 Runner 必须验证它与
> `platform_admin` 的解析结果一致，最终应由角色绑定派生而不是由用户重复维护。

同一节的表格明确把 **Adminer 列为 `audience: administrators` + `access.role: platform_admin` +
`access.via: forward_auth`**，并规定「Module 作者声明；**用户只能隐藏，不能放宽或改执行点**」。
消费位置到物理事实的映射也已定：ForwardAuth 用 `SAMBA_DC_ADMIN_GROUP_NAME`，直接 LDAP `memberOf`
过滤用 `SAMBA_DC_ADMIN_GROUP_DN`。

**结论：不需要把 `forward_auth` 从 Capability 改造成 Contract/Resource。** 本文档早前版本提出过那条
路（让每个 Consumer 声明自己需要的组，走 `resources.requires[].id` 的多实例模型）。它在抽象上成立，
但**方向是错的**：ANAS 的设计意图不是「每个 Consumer 各要一组」，而是「入口按语义角色分类，物理组名
由 Runner 从身份拓扑派生」。角色词汇（`platform_admin`）比 per-Consumer 组列表更收敛，也和
`web-api-admin-console` 的角色模型一致——那份需求文档 §
「MVP 只有 `owner` 一种角色，`Admins` 组成员即 `owner`」明确说这个规模不引入分层。

**要做什么——本轮已确定不考虑兼容迁移，所以直接做终态，不分两步**：

1. **`allow_groups` 从用户配置面下线。** 删除 `modules/oauth2_proxy/module.yml` 的
   `config.defaults.allow_groups` 与 `config.types.allow_groups`，删除
   `test-env/scripts/test-parameters.sh:143` 的对应断言，同步更新四个硬编码参数计数的测试与
   oauth2_proxy 的中英 README / technical 参数表（改 `module.yml` 参数的连锁清单见 §0）。
2. **由角色绑定派生。** Hook 不再读 `OAUTH2_PROXY_ALLOW_GROUPS`，改为从 `platform_admin` 的解析结果
   取值——按 §4.1 的映射表，ForwardAuth 这个消费位置对应 `SAMBA_DC_ADMIN_GROUP_NAME`。
   `ANAS_APP_ENTRY__*__ALLOW_GROUPS` 是设计好的输出契约名。
3. **保留「解析结果为空即拒绝部署」的行为。** 现在的 Hook 对空 `allow_groups` 直接报错
   （`main.go:165-174`），那条保护不能因为值变成派生的就消失——派生不出组名同样必须失败，错误信息
   改为指向身份拓扑而不是指向一个已经不存在的参数。

**迁移成本实测接近零**：复核时全仓**没有任何配置把 `allow_groups` 设成非默认值**——
`config.example.yml`、`config.full.example.yml`、`test-env/` 下都没有，只有
`test-env/scripts/test-parameters.sh:143` 断言它的 `\S` 模式。当前 `forward_auth` 也只有
`ddns_updater` 一个 Consumer，而它守的是「能改写本部署每一条 DNS 记录」的界面，本来就该是管理员
专用。所以这是一次删除，不是一次迁移。

**不要做什么**：

- 不要给 `forward_auth` Capability 加 request schema 来承载 per-Consumer 组列表。仓库的 ABI 硬边界
  （`docs/developer/capability-development.md` §1 第 4 条）排除了这条路，而 §4.1 的角色模型让它也
  变得不必要。
- 不要把策略编码进 `forward_auth` 的 `interfaces` 词汇表。interface 是协议词汇（`http`），不是授权
  策略。
- 不要在 `platform_admin` 之外先发明第二个角色。真出现「某个入口要给非管理员」的需求时，
  §4.1 的 `audience` 已经预留了 `assigned_users` / `authenticated_users`，届时按那套词汇扩展。

**顺带发现的分叉风险**：§4.1 的 launcher schema 里有 `enabled_if`（「引用一个布尔或非空判定的变量，
**解决 Adminer 这类可选服务**」），而 T7 要引入的是 `requires_capabilities` 的条件字段。两者都在表达
「这个东西只在某个开关打开时才存在」。**做 T7 的需求文档时必须和 `enabled_if` 对齐**——要么复用同一
个字段语义，要么写清楚为什么是两件事。否则同一个概念会在 Manifest 里有两种写法。

**依赖**：无前置。与 T4 独立——T4 落地后 Adminer 已受现有默认值保护，本任务把「默认值」升级成
「不变量」。若两者同一轮做，先做本任务，T4 就不必再解释「默认值是对的但可以被改宽」。

---

## 5. 基线时未处理的已知约束

### 子进程执行没有 `context.Context` 传播

**定位**：全仓 38 处 `exec.Command`，0 处 `exec.CommandContext`。集中在
`internal/runner/hook.go`（88、141、244、300、474、567 行）与
`internal/runner/backup_transfer.go`（292、293、349、378、695 行）。

**为什么现在不修**：

- `anas` 是一次性 CLI。Ctrl-C 发给进程组的信号本来就会传给子进程，实际收益有限。
- `anasd` 虽然有 `signal.NotifyContext`（`cmd/anasd/main.go:41`），但它目前是**纯只读** API
  （`internal/api/httpapi/handler.go` 对所有路由强制 GET），根本走不到这些路径。
- 全面改造要贯穿 `runner.Main` 及所有命令，覆盖 38 处调用点。

**什么时候必须修**：**一旦 `anasd` 支持写操作，这条立即升级为前置条件。** 守护进程收到 SIGTERM 时
必须能中止正在跑的 `btrfs send/receive`、`rsync` 或容器拉取，否则会留下半完成的备份和无人回收的
子进程。

**动作项**：在 `anasd` 写操作的需求文档里把这条写成显式前置条件，而不是等到实现时才发现。

### 环境变量中的明文凭据

**为什么不作为缺陷**：`environment:` 与 `env_file:` 在容器内完全等价，都会出现在 `docker inspect`
里，所以原报告的「改用环境变量传递」零收益。`modules/collabora/docker-compose.yml:13` 的
`password:` 与 `.env` 里是同一个值，没有额外泄漏。

**正确的长期方向**是 `_FILE` 文件注入，`modules/nextcloud/docker-compose.yml:16,47` 已经示范
（`NEXTCLOUD_ADMIN_PASSWORD_FILE` + `/run/secrets/` 挂载）。但 collabora 上游镜像不支持
`password_FILE`——这是上游能力问题，不是本仓库的缺陷。逐个 Module 评估上游是否支持 `_FILE`，支持
的就迁移。

---

## 6. 被否决的建议及理由

| 原报告建议 | 否决理由 |
| --- | --- |
| 校验 `dockerCopy` 的容器名格式，防注入 | **没有安全意义。** 参数以 argv 切片传递，无 Shell 注入；而这些值来自 Module Hook，Hook 本身就是任意可执行程序，早就在同一信任域内。加正则只是安慰剂。 |
| 给 postgres/mariadb 的 Adminer 加 ForwardAuth 中间件（按原报告写法） | 结论保留但路径改写，见 T4。风险定性应是「公网暴露」而非「缺一层认证」；实现必须走 T7 的条件依赖，直接用无条件 `requires_capabilities` 会波及所有 postgres 部署。另外原报告没有提到组限制——现有网关默认就是管理员专用，这是方案能成立的关键。 |
| 把 netbird `iam_protocol` 枚举收窄为 `[auto, oidc]` | 宽枚举是刻意的设计，见 §3。 |
| 为 `iam-provider.md`、`vikunja-module.md` 补 ID 矩阵 | 两份的收益远低于成本，本轮登记为豁免。判断依据与豁免机制见[需求 ID 矩阵采用范围与门禁要求](/requirements/requirement-id-adoption)。另外原建议**照做会让 CI 直接变红**——这三份在 `docs/plans/` 下都没有配套计划文件，门禁对「有矩阵但无配对计划」是报错而非跳过。 |
| 在 `internal/modulepackage/archive.go` 补 symlink 检查 | 文件不存在，检查已在别处实现，见 §3。 |
| 给所有缺失的容器补 `healthcheck` | 范围过宽，见 T12。 |

---

## 7. 仓库卫生状态（事实记录，非缺陷）

截至 2026-08-23：工作区有 50+ 项未提交变更（Forgejo Module、compute Contract、需求文档等），本地
`master` 领先 `origin/master` 11 个提交。这是进行中的工作，不是缺陷；记录在此是为了让「工作区不干净」
不被后续审计重复报成问题。建议按功能拆分提交后推送。
