# 开源自部署 Git 服务全景调研（2026-08-15）

本报告按[开源自部署应用研究 Module 规范](./application-research-module-spec.md)研究团队协作型 Git 服务，为 ANAS 后续 Runtime Module 选型提供依据。动态版本与维护状态采集于 2026-08-15；报告是研究快照，不是当前部署说明。

## 1. 结论先行

1. **ANAS 默认候选应是 Forgejo LTS**。它覆盖仓库、Issue、Pull Request、Wiki、项目板、软件包/容器仓库、Actions、REST API、OIDC 和 LDAP，单机部署与备份边界清楚；治理在非营利组织 Codeberg e.V. 下，Forgejo 9 起采用 GPL-3.0-or-later。当前建议 PoC `15.0.6` LTS，而不是自动跟随 `latest`。
2. **Gitea 是同等级备选，不是“旧版 Forgejo”**。它同样轻量、生态更大、MIT 许可，并有公司提供 Enterprise 与支持；当前社区版 `1.27.2` 功能很完整。若团队重视宽松许可、既有 Gitea 运维经验或商业支持路径，优先验证 Gitea；若重视非营利治理、完整自由软件边界和 LTS，优先 Forgejo。
3. **不要假设二者还能无损互换**。Forgejo 自 2024 年成为硬分叉；从 Gitea 1.23+ 迁入 Forgejo 已不再是官方保证的透明升级路径。Git 仓库本身易迁移，Issue、PR、用户、Actions、制品和权限元数据并不天然可逆。
4. **OneDev 是“较轻的一体化 DevOps”候选**。社区版原生提供 CI、代码搜索、包仓库、OpenID/LDAP、AI users/workspaces，约 2 GB 内存级别即可试验；但 HA、S3/独立存储、审计和多种安全扫描在 Enterprise，项目与维护者生态也明显小于 Gitea/GitLab。
5. **GitLab 只在确实需要集成 DevSecOps 时选择**。它的 CI/CD、权限、制品和企业能力最成熟，但官方单节点基准为 8 vCPU、16 GB RAM，升级有强制停靠版本，恢复要求完全相同的版本与 CE/EE 类型，日常运维重量远高于 Forgejo/Gitea。
6. **Gerrit 是代码审查系统，不是通用 GitHub 替代品**。它适合强制 pre-submit review、submit requirement、细粒度 ref 权限和大规模评审；Issue、看板、包仓库与 CI 需要外部系统或插件。只有工作流明确要求 Gerrit 时才应进入 PoC。
7. **Gogs 与 GitBucket 不作为新部署默认项**。Gogs 仍轻量且在维护，但功能面窄，2026 年 `0.14.3` 集中修复了多项安全问题；GitBucket 对 JVM 团队、GitHub 兼容 API 或既有插件有价值，但 CI/包仓库依赖插件，升级还需管理 JVM、H2 与插件兼容性。
8. **Gitolite 与 Soft Serve 属于轻量 Git-only 邻近项**。前者是 SSH/HTTP Git 的强授权层，后者是单二进制、SSH TUI 风格 Git 服务；二者都不提供完整 Issue/PR/CI/包仓库协作面，适合极简或基础设施内部场景。
9. **CI runner 必须与代码托管服务解耦**。Forgejo/Gitea Actions、GitLab Runner 和 OneDev executor 都会执行仓库提供的代码；把宿主机 Docker socket 挂给共享 runner 等价于给作业接近宿主 root 的能力。ANAS 首版 Module 应默认关闭内置 CI，只提供独立、短生命周期、受限网络 runner 的后续扩展点。
10. **推荐实施顺序是 `forgejo` → `gitea` 对照 PoC → 按需 OneDev/GitLab**。先验证 OIDC、SSH、备份恢复、版本升级、GitHub/GitLab 导入和元数据导出，再决定是否正式进入 Module 目录；不要同时维护多个功能重叠的代码托管 Module。

## 2. 主题卡、范围与方法

```yaml
topic: self-hosted-open-source-git-services
title: 开源自部署 Git 服务
snapshot_date: 2026-08-15
decision_for: ANAS module 候选
must_be:
  - 可获得源代码并允许自行部署
  - 以 Git 仓库托管、协作或代码审查为核心用途
  - 上游仍可访问，或作为停止维护样本明确标注
core_categories:
  - 完整软件 forge：仓库、Issue、Pull/Merge Request、用户与权限
  - 集成 DevOps forge：在上述能力上内置 CI、制品或部署能力
adjacent_categories:
  - 评审优先系统
  - Git-only 授权层、SSH 服务或只读浏览器
  - 邮件补丁、P2P 或完整 ALM 工作流
excluded:
  - SaaS-only 或专有本地部署产品
  - 只有 Git 客户端、镜像同步或代码搜索而不托管仓库的项目
target_users:
  - 个人开发者
  - 家庭、工作室与小团队
expected_scale: 1 至 20 用户，低至中等并发
deployment_target:
  os: Linux
  runtime: Docker Engine + Docker Compose v2
  ingress: Traefik HTTPS
  architectures: [amd64, arm64]
questions:
  - 哪个项目适合作为 ANAS 默认 Git forge？
  - 社区版的身份、CI、包仓库、备份和 HA 边界是什么？
  - 如何避免 runner、迁移与恢复造成不可逆锁定？
search_date: 2026-08-15
```

### 2.1 “所有”的口径

本报告中的“全景”表示：在已声明目录、商业产品反向检索、既有开源 forge 对照页和项目官方资料中发现的候选，都被分配到核心、专用/邻近、源码可见、停止维护或排除项；不表示永久穷尽。90 天后应重新核验版本、许可证、维护状态和商业版边界。

目录只用于发现候选，功能、许可证、部署和价格结论均回到上游核验。主要入口包括：

- [AlternativeTo 的 Gitea alternatives](https://alternativeto.net/software/gitea/)与开源/自部署筛选，用于发现 Forgejo、Gogs、GitBucket 等；
- [awesome-selfhosted](https://github.com/awesome-selfhosted/awesome-selfhosted)的 Software Development 类别；
- 每日更新并说明排序方法的 [selfh.st Apps](https://selfh.st/apps-about/)；
- [Forgejo 的 forge 对照页](https://forgejo.org/compare/)及其引用的 Wikidata software forges 数据；
- 各项目仓库、release、许可证、管理员文档、定价和安全文档。

### 2.2 从商业基准反向发现

| 基准 | 要保留的能力 | 反向发现或确认的开源候选 |
| --- | --- | --- |
| GitHub / GitHub Enterprise Server | PR、Issue、Actions、Packages、生态/API | Forgejo、Gitea、GitLab、OneDev、Gogs、GitBucket |
| GitLab Ultimate | SCM 到 CI/CD、制品、安全扫描的一体化 | GitLab CE/Free、OneDev；Forgejo/Gitea + 外置 runner/扫描器 |
| Bitbucket Data Center | 企业权限、代码评审、Jira 工作流 | GitLab、Gerrit、Forgejo/Gitea；严格开源没有完全等价的 Jira 套件 |
| Azure DevOps Server | Repos、Pipelines、Boards、企业目录 | GitLab、OneDev；其余通常需要外置项目管理和 CI |

反向检索揭示了一个关键边界：GitHub 风格的轻量 forge 与完整 DevOps 平台不是同一部署等级。若只需要安全保存代码和协作评审，不应为了“以后也许会用”而承担 GitLab 的资源与升级成本。

## 3. 动态版本与维护快照

版本仅表示 2026-08-15 当日可见状态，不是永久推荐版本；正式 Module 必须固定镜像 digest 并维护升级测试。

| 项目 | 当日稳定版本信号 | 许可证 / 治理信号 | 快照判断 |
| --- | --- | --- | --- |
| Forgejo | [`16.0.2` stable、`15.0.6` LTS](https://forgejo.org/releases/)；LTS 支持至 2027-07-15 | 9+ 为 [GPL-3.0-or-later](https://forgejo.org/2024-08-gpl/)；域名与治理在非营利 [Codeberg e.V.](https://forgejo.org/faq/) | 活跃，首选 LTS 路径 |
| Gitea | [`1.27.2`](https://github.com/go-gitea/gitea/releases/tag/v1.27.2)，2026-08-13 | [MIT](https://github.com/go-gitea/gitea/blob/main/LICENSE)，公司支持、社区版 + Enterprise | 活跃，生态规模大 |
| OneDev | [`16.5.0`](https://github.com/theonedev/onedev/releases/tag/v16.5.0)，2026-08-09 | [MIT 社区核心](https://github.com/theonedev/onedev)，公司提供 Enterprise | 活跃，发行频率高但生态较小 |
| GitLab | [`19.2`](https://about.gitlab.com/whats-new/19-2/)，2026-07-16 | CE 为 MIT；统一 EE 仓库含 source-available 专有目录 | 活跃，开放核心边界最复杂 |
| Gerrit | `3.14` 稳定系列，`3.15` 开发中 | [Apache-2.0](https://gerrit-review.googlesource.com/Documentation/licenses.html)，社区项目 | 活跃，专用评审系统 |
| Gogs | [`0.14.3`](https://github.com/gogs/gogs/releases/tag/v0.14.3)，2026-06-07 | [MIT](https://github.com/gogs/gogs) | 仍维护，但新部署优先级低 |
| GitBucket | [`4.46.1`](https://github.com/gitbucket/gitbucket/blob/master/CHANGELOG.md)，2026-04-18 | [Apache-2.0](https://github.com/gitbucket/gitbucket) | 仍维护，JVM/插件型利基 |

Star、fork 与跨平台仓库的关注数不直接可比，本报告不把它们纳入质量评分。发行节奏、受支持版本、恢复约束和社区版边界比单日 Star 更能预测自部署成本。

## 4. 核心功能横向比较

符号：✅ 社区版官方原生；◐ 有限制、兼容层、插件或需外置组件；❌ 社区版没有；— 不适用或本轮未确认。

| 项目 | Repo / 协作 | CI | 包与容器仓库 | 身份接入 | API / 自动化 | 官方社区版 HA | 典型重量 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| **Forgejo** | ✅ Issue、PR、Wiki、项目、保护分支 | ◐ Actions，需独立 runner | ✅ OCI 与多种包格式 | ✅ OIDC、LDAP、2FA、反代认证 | ✅ REST、Webhook、镜像 | ❌ 无开箱即用集群 | 轻 |
| **Gitea CE** | ✅ Issue、PR、Wiki、项目、保护分支 | ◐ Actions，需 `act_runner` | ✅ OCI 与多种包格式 | ✅ OAuth2/OIDC、LDAP、2FA、反代认证 | ✅ REST/OpenAPI、Webhook、镜像 | ❌ 官方 HA 文档属于 Enterprise | 轻 |
| **OneDev CE** | ✅ Issue、PR、看板、策略、代码搜索 | ✅ 内置 CI | ✅ Docker、npm、NuGet、Maven、PyPI、Cargo、Helm 等 | ✅ OpenID、LDAP/AD、2FA | ✅ REST、Webhook、CLI/AI users | ❌ HA 属 Enterprise | 中 |
| **GitLab CE/Free** | ✅ Issue、MR、Wiki、计划能力 | ✅ GitLab CI + Runner | ✅ Container/Package Registry | ✅ LDAP、OIDC；实例级 SAML 可用 | ✅ REST/GraphQL、Webhook、Terraform Provider | ◐ 可扩展但架构复杂，部分能力分层 | 重 |
| **Gogs** | ✅ 基础 Issue、PR、Wiki、保护分支 | ❌ | ❌ | ◐ LDAP、SMTP、PAM、反代头；未确认通用 OIDC | ◐ Webhook、基础 API | ❌ | 很轻 |
| **GitBucket** | ✅ Issue、PR、Wiki、LFS | ◐ 插件 | ◐ 插件 | ◐ LDAP、OIDC；组映射能力有限 | ◐ GitHub 兼容 API | ❌ | 中（JVM） |

### 4.1 Forgejo：ANAS 默认候选

[Forgejo 安装文档](https://forgejo.org/docs/latest/admin/installation/)提供官方二进制和容器路径，稳定版每三个月发布、LTS 每年发布。它支持 SQLite、PostgreSQL、MySQL 和 MariaDB；附件、LFS、软件包、Actions 日志/制品可放本地或 S3-compatible 存储，但 **Git bare repositories 仍需 POSIX 文件系统**，不能把对象存储误写成全部数据的后端。

身份方面，官方文档覆盖 [LDAP 与同步](https://forgejo.org/docs/v15.0/user/authentication/)、OIDC，以及 v16 的 [OIDC group 到组织/团队映射](https://forgejo.org/docs/v16.0/admin/advanced/oidc-group-mappings/)。反向代理头认证可以自动注册用户，但必须显式限制可信代理 CIDR；API 仍使用 token，不能假设浏览器反代登录自动覆盖 API。

运维方面，`forgejo dump` 可导出数据库与本地状态，但[升级文档](https://forgejo.org/docs/latest/admin/upgrade/)明确提示一致备份可能需要停机。使用 S3 时还必须单独备份/版本化对象存储。Codeberg 的[公开运维复盘](https://blog.codeberg.org/letter-from-codeberg-looking-into-2025.html)也承认 Forgejo 尚未提供开箱即用 HA/cluster，因此本报告不把“可接数据库主从”写成应用 HA。

主要风险有三个：

- [Forgejo Actions](https://forgejo.org/docs/latest/user/actions/)与 GitHub Actions 语法相近但不是完全等价；迁移工作流必须实跑；
- runner 若挂载 Docker socket，官方文档明确指出作业可控制整个 daemon；不可信仓库必须使用隔离执行面；
- 与 Gitea 已持续分叉，[Gitea 1.22 是最后一个透明迁移锚点](https://forgejo.org/2024-12-gitea-compatibility/)，不能以替换镜像名作为长期退出方案。

### 4.2 Gitea：宽松许可和商业支持备选

Gitea 的产品面与 Forgejo 相近：[Actions](https://docs.gitea.com/usage/actions/overview)、[Packages](https://docs.gitea.com/usage/packages/overview)、[REST/OpenAPI](https://docs.gitea.com/development/api-usage)、LDAP/OIDC 和仓库镜像都在社区版可用。官方 Docker 路径可以从单容器 + SQLite 起步，也可以连接 PostgreSQL/MySQL；附件、LFS、packages 与 Actions 制品支持本地或对象存储。

它与 Forgejo 的实质选择不是当前功能表中一两个勾，而是治理、许可和支持路径：

- Gitea 为 MIT，企业可更自由地嵌入或二次分发；Forgejo 9+ 为 GPL-3.0-or-later；
- Gitea 有官方 [Enterprise](https://about.gitea.com/products/gitea-enterprise/) 与商业支持；Forgejo 以社区、非营利治理和第三方专业服务为主；
- Gitea Enterprise 把 SAML SSO、审计日志、IP allowlist、Kubernetes runner autoscaling 和官方 HA 等列为增值能力；社区版的 LDAP/OIDC 不等于这些企业治理功能。

[备份恢复文档](https://docs.gitea.com/usage/backup-and-restore)要求为一致性停服务，`dump` 后恢复是手工流程，并建议数据库使用原生 dump 工具。PoC 必须实际演练“数据库 + Git 仓库 + LFS/附件/包/Actions 数据 + 配置/密钥”的整套恢复，不能只验证网页能打开。

### 4.3 OneDev：中等重量的一体化选择

OneDev 的差异点是社区版直接提供原生 CI、提交级代码搜索、包仓库、可视化 pipeline、OpenID、LDAP/AD、2FA，以及 [AI users/workspaces](https://onedev.io/pricing)。[Docker 安装文档](https://docs.onedev.io/installation-guide/run-as-docker-container)给出的起点约为 2 核、2 GB 内存，默认内嵌数据库，也可用 PostgreSQL、MySQL 或 MariaDB。

其 Enterprise 边界同样必须写清：官方当前定价页把 HA/扩展、跨项目搜索、依赖/安全/许可证/secret 扫描、审计、S3/独立存储、统计和支持列入付费版。社区版可以备份数据库，并按[官方备份说明](https://docs.onedev.io/administration-guide/backup-restore)备份 `site` 目录中的仓库、附件等状态，但不能把 Enterprise 的集群和远端存储能力计入开源方案。

OneDev 适合“希望比 GitLab 轻，但 CI 和代码搜索必须同箱”的团队。风险是供应商/维护者集中度更高、版本发布频繁、Java 栈与 ANAS 其他轻量 Go 服务不同；升级前必须查看每个 release 的 incompatibilities，并把 CI 执行器移出主服务宿主。

### 4.4 GitLab：成熟但重的 DevSecOps 平台

GitLab 不是单纯的 Git Web UI。它把仓库、MR、Issue、CI/CD、Runner、Container/Package Registry、Pages 和多级企业治理整合在一起，因此适用于多个开发团队、复杂流水线或合规要求明确的组织。

代价是显著的资源和运维复杂度：[官方安装要求](https://docs.gitlab.com/install/requirements/)给单节点基准 8 vCPU、16 GB RAM，受限环境最低约 8 GB 内存；这与 Forgejo/Gitea 的小型 NAS 目标不是同一等级。[必经升级路径](https://docs.gitlab.com/update/upgrade_paths/)要求跨大版本经过指定停靠版本并等待后台迁移完成；[恢复](https://docs.gitlab.com/administration/backup_restore/restore_gitlab/)要求与备份完全相同的 GitLab 版本和 CE/EE 类型，配置、secrets 和对象存储还需另行保护。

许可证边界也要精确：`gitlab-foss` 是移除专有代码的只读 CE 镜像；GitLab 日常开发在统一仓库中进行，EE 包含 source-available 专有目录。若 ANAS 声明“严格开源镜像”，应选择 CE 并接受功能差异；不能把未激活许可证的 EE 镜像整体写成 MIT 开源。

### 4.5 Gerrit：评审优先的专用系统

[Gerrit](https://www.gerritcodereview.com/)以 change 为中心，开发者通常推送到 `refs/for/*`，通过 label、submit requirement 和细粒度 ref 权限决定是否进入目标分支。它的 REST API、NoteDb、复制与索引重建机制成熟，非常适合强制评审、大型单仓或长期沿用 Gerrit 工作流的组织。

它不是完整 forge：Issue、看板、包仓库与 CI 依赖外置系统；插件与 Gerrit major/minor 版本紧密耦合，升级必须验证整套插件。3.14 提供 Review Agent UI，但仍需 AI provider 插件，不能写成内置免费模型。ANAS 只有在需求明确为“Gerrit 工作流”而非“自建 GitHub”时才应维护它。

## 5. 商业版与开放核心边界

价格是 2026-08-15 公开页面快照，采购前必须复核税费、最低席位、支持范围和许可证条款。

| 项目 | 严格开源核心 | 付费层主要增加 | 对选型的影响 |
| --- | --- | --- | --- |
| Forgejo | 全部 Forgejo 主线，GPL-3.0-or-later | 无官方专有功能层；专业服务可另购 | 功能边界最清楚，HA 也不能靠买同项目专有版补齐 |
| Gitea | Gitea CE，MIT | Enterprise 公开标价为每用户月费；SAML、审计、官方 HA、IP allowlist、进阶安全/runner 能力等 | 小团队 OIDC/LDAP 足够；合规需求要提前预算 Enterprise |
| OneDev | Community Edition，MIT | 当前公开价约 US$6/用户/月；HA、S3、审计、扫描、支持等 | 社区版很强，但规模化与合规会进入商业层 |
| GitLab | CE 为 MIT；Free self-managed 还可能运行统一 EE 包 | Premium/Ultimate 的治理、安全、组合管理与支持 | 必须逐项对照 tier，不能把 GitLab 品牌能力全部归入 CE |
| Gogs / GitBucket / Gerrit | 主线均为严格开源 | 无对应官方专有功能层，支持/插件生态各异 | 功能缺口通常靠自建集成而不是升级付费版 |

## 6. 专用、邻近、停止维护与排除项

| 项目 | 归类 | 去向与理由 |
| --- | --- | --- |
| [Gitolite](https://gitolite.com/gitolite/overview) | 邻近：Git 授权层 | SSH/HTTP 认证之后提供 repo/ref/file 级授权、镜像和可编程规则；没有完整 Web 协作面 |
| [Soft Serve](https://github.com/charmbracelet/soft-serve) | 邻近：SSH-first Git 服务 | 单二进制、SSH TUI、HTTP/SSH/Git、LFS 与基础 ACL；适合极简内部服务，不是 PR/Issue 平台 |
| [SourceHut](https://docs.sourcehut.org/) | 专用：邮件补丁与模块化服务 | git、builds、todo、lists、pages 分服务，工作流独特；自部署拓扑和运维面不适合 ANAS 首轮通用 Module |
| [Pagure](https://pagure.io/pagure) | 邻近：Fedora 风格 forge | Issue/PR 与 dist-git 场景有价值；生态和通用自部署路径不如 Forgejo/Gitea |
| [Kallithea](https://kallithea-scm.org/) | 邻近：Git + Mercurial | 兼顾 Mercurial 是差异点；若只需 Git，新部署收益不足 |
| [Phorge](https://we.phorge.it/) | 邻近：Phabricator 社区继任 | 差异化在 Differential/Herald/完整工程套件；部署与工作流较重，不是 GitHub 风格默认项 |
| [Tuleap Community](https://www.tuleap.org/) | 邻近：完整 ALM / 开放核心 | 需求、测试、敏捷与合规覆盖更广，已经超出 Git 服务 Module 范围 |
| Gitweb / cgit | 邻近：只读浏览器 | 可浏览裸仓库，但不提供团队协作、身份生命周期和 API 契约 |
| Phabricator 原项目 | 停止维护 | 上游已停止，若确有存量应评估 Phorge 迁移，而不是新部署原项目 |
| GitHub Enterprise Server、Bitbucket Data Center、Azure DevOps Server | 排除：专有本地部署 | 仅作为商业基准，不进入严格开源推荐 |
| Radicle 等 P2P forge | 排除：架构不匹配 | 不是 ANAS 中心化 HTTP/SSH 服务与 IAM/备份契约的直接候选 |

Gogs 与 GitBucket虽然在核心表中有功能对照，但新部署等级为 `C`：只为存量迁移、极低资源或明确的 JVM/插件需求保留。Gogs 若继续使用必须至少固定 `0.14.3`，该版本修复了反向代理头信任等安全问题，并应持续订阅仓库 security advisories。

## 7. 部署、身份、存储与恢复比较

| 项目 | 推荐数据库 | 非数据库关键状态 | 认证接入 | 恢复难点 |
| --- | --- | --- | --- | --- |
| Forgejo | PostgreSQL；个人试用可 SQLite | Git repos、LFS、附件、packages、Actions、`app.ini` 与密钥 | OIDC/LDAP；Traefik 只做入口，不代替应用授权 | dump/卷/对象存储必须同一恢复点；无自动 restore 命令 |
| Gitea | PostgreSQL；个人试用可 SQLite | 与 Forgejo 类似，另含 runner token/Actions 状态 | OIDC/LDAP；SAML/审计属 Enterprise | 官方要求停机一致性；手工恢复、DB 建议原生 dump |
| OneDev | PostgreSQL | `site` 目录含 repos/附件等；CI artifacts/workspaces | OpenID/LDAP | 数据库备份与 `site` 卷必须成对；逐版本核验 incompatibilities |
| GitLab | PostgreSQL + Redis 等官方栈 | repos、uploads、artifacts、LFS、packages、registry、配置/secrets、对象存储 | LDAP/OIDC/SAML，具体能力按 tier | 精确同版本/同 edition；对象存储、config、secrets 不在普通备份闭环内 |
| Gerrit | NoteDb 在 Git repos；辅助 DB/索引按部署 | site、repos、plugins、配置、secrets；索引通常可重建 | HTTP/LDAP/OAuth 插件等 | 插件版本耦合；快照与复制策略需要专门设计 |

对所有候选，**能创建新仓库不等于可恢复**。验收至少要覆盖：提交与 refs、LFS、Issue/PR 评论和附件、Wiki、packages、Actions/CI 记录、组织/团队权限、Webhook、Deploy Key、OIDC subject 映射、应用密钥和 SSH host key。

## 8. 安全模型

### 8.1 代码托管面

- 默认关闭公开注册；若允许公开实例，必须额外设计邮件验证、限流、配额、滥用处理与内容清理。
- HTTPS 由 Traefik 终止时，显式设置 canonical root URL、trusted proxy 和真实客户端 IP 网段；不能信任任意来源的认证头。
- Git SSH 端口应独立暴露，固定并备份 SSH host key；不要用“重建容器后自动生成”破坏客户端已知主机信任。
- 仓库、LFS 和 packages 都可能含秘密或恶意文件；备份、对象存储、日志和管理员导出要继承相同敏感等级。
- 强制 2FA、最小权限、受保护分支、签名提交/标签和 deploy key 生命周期应在 PoC 中验证，而不是部署后再补。

### 8.2 CI 执行面

[Forgejo 的 Docker access 文档](https://forgejo.org/docs/v15.0/admin/actions/docker-access/)、[Gitea act_runner 文档](https://docs.gitea.com/usage/actions/act-runner)和 [GitLab Runner 安全文档](https://docs.gitlab.com/runner/security/)都指向同一事实：pipeline 是远程代码执行服务。最低边界应为：

1. forge 容器不挂宿主 Docker socket；
2. runner 运行在独立 VM/主机或真正隔离的短生命周期 executor；
3. public/fork PR 不获得生产 secret，受保护变量只在受信任 refs 可见；
4. runner 网络默认不能访问 NAS 管理面、数据库、对象存储管理端和其他 Module；
5. cache、workspace 与 token 在作业后销毁，禁止跨不可信项目复用非临时 runner；
6. Actions/GitHub 兼容只表示语法迁移起点，不表示 action 供应链可信，第三方 action 需固定 commit SHA。

因此，首个 Forgejo/Gitea Module 的功能验收可以包含“连接 runner 的接口”，但默认配置不得自动部署共享 runner。

## 9. ANAS 适配与 Module 设计建议

### 9.1 推荐等级

| 等级 | 候选 | ANAS 决策 |
| --- | --- | --- |
| **A** | Forgejo LTS | 默认 PoC；通过恢复与升级门禁后进入稳定 Module 候选 |
| **A-** | Gitea CE | 保留对照 PoC；明确宽松许可或官方商业支持需求时选择 |
| **B** | OneDev CE | 集成 CI/搜索是硬需求且不能接受 GitLab 重量时试验 |
| **B-/C+** | GitLab CE/Free | 仅中大型 DevOps/合规需求；不作为普通 NAS 默认项 |
| **C（专用）** | Gerrit | 仅明确评审工作流需求 |
| **C** | Gogs、GitBucket、SourceHut、Gitolite、Soft Serve 等 | 存量或利基场景，不进入首轮目录 |

### 9.2 Forgejo Module 建议拓扑

```text
Internet/LAN
    │ HTTPS
 Traefik ───────────────► Forgejo Web / Git HTTP
                              │
                              ├── PostgreSQL resource
                              ├── critical volume: repos/config/local objects
                              ├── optional S3 resource: LFS/packages/artifacts
                              ├── identity resource: OIDC
                              └── SMTP resource

LAN/VPN ── TCP 22xx ───► Forgejo SSH

isolated runner host/VM ── registration token ──► Actions API
        └── ephemeral jobs; no ANAS host Docker socket
```

Module 契约应明确：

- `relational_database`：默认 PostgreSQL，凭据由 resource 注入；SQLite 只保留个人实验模式；
- `identity`：OIDC discovery、client ID/secret、group claim 映射；保留 break-glass 本地管理员；
- `certificate` / ingress：Web 走 Traefik，SSH 端口不经 HTTP router；
- `critical`：配置、密钥、Git repos、LFS/附件/package/Actions 本地状态、SSH host key；
- `rebuildable`：索引、缓存和临时仓库归档；必须按上游真实路径逐项核验；
- backup hook：停写或停服务后，取得 PostgreSQL、文件卷和对象存储同一恢复点；记录镜像 digest、Module revision 与 schema 版本；
- upgrade hook：升级前备份并阻断跨越未验证大版本；先 clone 新 deployment 验证，再切换；
- health：区分 HTTP 存活、数据库迁移完成、SSH 可 clone、后台队列无阻塞；
- runner：独立可选 Module，不继承 forge 的主机权限，不默认启用。

### 9.3 为什么首选 PostgreSQL 而非 SQLite

SQLite 对单人小实例完全可用，但 ANAS 正式 Module 更适合复用 relational database Contract：数据库备份、监控、升级回滚和应用数据分层更清楚，也避免未来启用队列、并发写或迁移到多节点时再做数据库切换。选择 PostgreSQL 不改变 Git 仓库和附件仍需独立备份的事实。

## 10. PoC 与验收计划

### 阶段 0：冻结输入

- Forgejo 固定 `15.0.6` LTS 的多架构镜像 digest；分别核验 amd64、arm64 manifest；
- Gitea 对照固定 `1.27.2` digest；不使用 `latest`；
- 记录许可证、SBOM/签名可用性、上游支持期、最低跨版本升级路径；
- 明确测试域名、SSH 端口、OIDC client、PostgreSQL database 和备份目标。

### 阶段 1：基础功能

1. 通过 Traefik HTTPS 完成 OIDC 登录、登出、本地管理员回退和组到团队映射；
2. 通过 HTTP/SSH clone、push、LFS push/pull，验证 deploy key 与 token；
3. 创建组织、私有仓库、保护分支、Issue、PR、Wiki、项目板、Webhook、OCI package；
4. 验证邮件通知、时区、中文界面、仓库配额和关闭公开注册；
5. 对 amd64/arm64 都运行 smoke test，而不是只检查 manifest 存在。

### 阶段 2：迁移与退出

1. 从 GitHub/GitLab 导入一个带 Issue、PR、Wiki、LFS 和 release 的测试项目；
2. 比较缺失项，特别是评论作者、时间戳、review 状态、labels、milestones、packages 和 Actions secrets；
3. 做 push mirror 到第二个 forge，确认 Git 数据退出路径；
4. 用 REST API 导出组织、团队、Issue/PR 等元数据，保存可重复脚本；
5. 不把“仓库可 clone”判定为“平台可迁出”。

### 阶段 3：备份、恢复与升级

1. 写入并发测试数据后停写，备份 PostgreSQL、critical volume、对象存储、配置/secret 和 SSH host key；
2. 在全新 workspace、不同容器名与网络中恢复；
3. 按第 7 节清单逐项验收数据和权限，不复用原运行卷；
4. 从上一受支持 patch/major 升级到目标版本，再回滚整个 deployment 快照；
5. 记录 RPO、RTO、实际备份大小、停机时间和最慢恢复步骤。

### 阶段 4：隔离 runner（可选）

- 只在独立 runner 环境运行受信任仓库的无 secret 作业；
- 验证作业无法访问 ANAS Docker socket、管理网、数据库和其他 Module；
- 验证 fork PR、cache poisoning、artifact retention、token 撤销和作业后清理；
- 未通过隔离测试时，Module 仍可发布，但 Actions 默认关闭且文档明确不支持共享 runner。

### 通过门槛

- `P0`：完整恢复、OIDC 登录/回退管理员、HTTP/SSH Git、升级回滚全部通过；
- `P1`：LFS、packages、Webhook、导入/导出、amd64/arm64、监控通过；
- `P2`：独立 runner、安全扫描和对象存储可后置，不阻塞纯 forge Module；
- 任一版本无法在干净环境恢复，或必须挂宿主 Docker socket才能完成基础功能，则不得进入稳定目录。

## 11. 最终决策

ANAS 应把“代码托管”和“CI 执行”拆为两个安全域，以 **Forgejo LTS + PostgreSQL + Traefik + OIDC + 一致性备份**作为默认目标。Gitea 保留为治理/许可/商业支持偏好不同的对照项，但不同时默认维护两套。OneDev 与 GitLab 只有在用户明确购买其一体化 DevOps 价值时才值得承担额外重量；Gerrit 与 Git-only 项目按专用需求处理。

这不是对功能数量的简单排名。默认候选胜出是因为它在目标规模下同时满足：严格开源、活跃维护、低资源、完整协作面、可接 ANAS 身份和数据库、可设计可验证的退出与恢复路径。最终上线门禁仍是 PoC 的干净恢复和 runner 隔离，而不是演示页面是否好看。

## 12. 主要一手资料索引

- Forgejo：[安装](https://forgejo.org/docs/latest/admin/installation/)、[版本与支持期](https://forgejo.org/releases/)、[数据库](https://forgejo.org/docs/latest/admin/installation/database-preparation/)、[存储](https://forgejo.org/docs/v15.0/admin/setup/storage/)、[升级](https://forgejo.org/docs/latest/admin/upgrade/)、[Actions](https://forgejo.org/docs/latest/user/actions/)、[治理 FAQ](https://forgejo.org/faq/)。
- Gitea：[Docker 安装](https://docs.gitea.com/installation/install-with-docker)、[Actions](https://docs.gitea.com/usage/actions/overview)、[Packages](https://docs.gitea.com/usage/packages/overview)、[认证](https://docs.gitea.com/usage/authentication)、[API](https://docs.gitea.com/development/api-usage)、[备份恢复](https://docs.gitea.com/usage/backup-and-restore)、[Enterprise](https://about.gitea.com/products/gitea-enterprise/)。
- OneDev：[仓库](https://github.com/theonedev/onedev)、[Docker 安装](https://docs.onedev.io/installation-guide/run-as-docker-container)、[包仓库](https://docs.onedev.io/tutorials/package/working-with-packages)、[OpenID 示例](https://docs.onedev.io/tutorials/security/sso-with-okta)、[备份恢复](https://docs.onedev.io/administration-guide/backup-restore)、[定价与版本边界](https://onedev.io/pricing)。
- GitLab：[安装要求](https://docs.gitlab.com/install/requirements/)、[LDAP](https://docs.gitlab.com/administration/auth/ldap/)、[OmniAuth/OIDC](https://docs.gitlab.com/integration/omniauth/)、[SAML](https://docs.gitlab.com/integration/saml/)、[升级路径](https://docs.gitlab.com/update/upgrade_paths/)、[备份恢复](https://docs.gitlab.com/administration/backup_restore/)、[Runner 安全](https://docs.gitlab.com/runner/security/)、[CE 许可证说明](https://gitlab.com/gitlab-org/gitlab-foss)。
- Gerrit：[安装](https://gerrit-review.googlesource.com/Documentation/install.html)、[版本](https://www.gerritcodereview.com/releases-readme.html)、[REST API](https://gerrit-review.googlesource.com/Documentation/rest-api.html)、[插件](https://gerrit-review.googlesource.com/Documentation/config-plugins.html)、[备份](https://gerrit-review.googlesource.com/Documentation/backup.html)。
- 次要候选：[Gogs](https://github.com/gogs/gogs)、[GitBucket](https://github.com/gitbucket/gitbucket)、[Gitolite](https://gitolite.com/gitolite/overview)、[Soft Serve](https://github.com/charmbracelet/soft-serve)、[SourceHut 文档](https://docs.sourcehut.org/)。

## 13. 局限与复核清单

- 本轮没有为每个项目搭建实例，部署与功能结论来自 2026-08-15 可访问的一手文档；PoC 结果优先于本报告。
- 价格、版本、支持期、镜像架构、Actions 兼容性和 tier 会变化；写入 Module lock 前必须再次核验。
- 没有把商业宣传页中的“enterprise ready”自动解释为社区版 HA、审计或合规能力。
- 没有把源代码可见的 GitLab EE 专有目录计入严格开源 CE。
- 没有验证所有第三方迁移器、插件和 mobile app；它们不进入默认能力承诺。
- 中国大陆的镜像可达性、镜像同步与依赖下载应复用[中国大陆镜像与 CNB 发行方案](./china-mainland-mirrors-and-cnb-distribution-2026-08-11.md)另行实测。
