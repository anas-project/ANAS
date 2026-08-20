---
doc_type: research
created: 2026-08-20
updated: 2026-08-20
evidence_as_of: 2026-08-20
---

# Nextcloud 搜索方案调研

本文研究 ANAS 当前 Nextcloud `34.0.2-r10` 的搜索能力、Nextcloud 34 可用的全文与语义搜索路径，以及适合本仓库的后续实现方案。动态资料采集于 2026-08-20；版本和兼容状态是当日快照，不是永久承诺。

## 1. 结论先行

1. **当前 ANAS 没有文件内容全文检索。** Nextcloud 核心搜索和 WebDAV SEARCH 已经可用，能按文件名、类型、时间、大小、人员及各 App 注册的 Provider 聚合结果；但仓库没有安装 `fulltextsearch`、`files_fulltextsearch` 或搜索平台 App，也没有 Elasticsearch/向量数据库服务。因此当前“文件搜索”本质上仍是元数据与文件名搜索，不会命中 PDF、Office、Markdown 等文件正文。
2. **生产目标应采用 Nextcloud Full Text Search Framework + Files Provider + Elasticsearch Platform。** 这是 Nextcloud 上游长期维护、AIO 也采用的经典路径：`files_fulltextsearch` 提取文档与权限，`fulltextsearch` 管理索引状态并接入统一搜索，`fulltextsearch_elasticsearch` 负责索引和查询。
3. **截至采集日，不能直接把该组合加入 ANAS release。** `fulltextsearch` 已在 App Store 提供 Nextcloud 34 稳定版 `34.0.1`，但 `files_fulltextsearch` 和 `fulltextsearch_elasticsearch` 的 App Store 稳定版最高仍是 Nextcloud 33。两者的 Nextcloud 34 修复已合并并回移到 `stable34`，也已经过 Nextcloud 34 + Elasticsearch 9.4.2 的社区端到端验证，但尚未形成 App Store 的 34 稳定发布契约。
4. **等待发布，不等于方向不确定。** Nextcloud AIO 主分支的全文搜索镜像已经切换到 Elasticsearch `9.5.1`，两个缺失 App 的仓库也已发布 `35.0.0beta1`；上游明显正在完成 34/35 代际迁移。ANAS 应现在完成设计和 PoC 准备，在三个 App 都有 Nextcloud 34 稳定包后锁定版本实施，不应从 `master` 或 fork 构建生产 App。
5. **Elasticsearch 必须固定精确版本，不使用 `latest`。** 2025/2026 年间 App 客户端、Elasticsearch 8/9 和 AIO 镜像曾出现实际不兼容。目标版本应由锁定的 `fulltextsearch_elasticsearch` Composer client、上游 AIO 对应版本及本地真实批量索引测试共同决定。当前方向是 Elasticsearch 9，而不是继续新建 Elasticsearch 8 部署。
6. **SQL Platform 不作为默认方案。** `fulltextsearch_sql 1.3.6` 已正式声明兼容 Nextcloud 34，也兼容 ANAS 的 PostgreSQL 18/MariaDB 12；但作者明确称其为 proof of concept。它把明文和索引大致各存一份到 Nextcloud 主数据库，不支持 Office 文档索引，PostgreSQL 路径固定按 English text search 配置处理。它可用于小型纯文本/PDF PoC，不适合 ANAS 的默认多语言文件搜索。
7. **Context Chat 是另一条产品线，不是全文搜索替代品。** Nextcloud 34 的 Context Chat 已能进行向量语义搜索与文档问答，但 CPU 部署建议至少 12 GB RAM、4+ 核且推荐专机，还需要 AppAPI、ExternalApp、Assistant 和文本生成 Provider；索引会复制全部文本到 pgvector，并存在不执行 `files_accesscontrol` 规则的明确安全限制。它只能作为以后默认关闭的 experimental AI 功能。
8. **建议的交付顺序：** 保持当前核心搜索 → 等待 34 稳定三件套 → Elasticsearch 9 + 中英文分析器 PoC → 以 `nextcloud.search_enabled: false` 的可选能力发布 → 完成恢复、权限撤销、中文分词和资源压力验证后再考虑升为 stable。即使稳定，也不建议默认开启，因为索引成本和明文副本是管理员必须主动接受的取舍。

## 2. 当前仓库基线

权威实现来自 [`modules/nextcloud/module.yml`](https://github.com/anas-project/ANAS/blob/master/modules/nextcloud/module.yml)、[`docker-compose.yml`](https://github.com/anas-project/ANAS/blob/master/modules/nextcloud/docker-compose.yml)、[`Dockerfile`](https://github.com/anas-project/ANAS/blob/master/modules/nextcloud/nextcloud/Dockerfile) 和 [`task.sh`](https://github.com/anas-project/ANAS/blob/master/modules/nextcloud/nextcloud/root/usr/local/bin/task.sh)。

| 项目 | 当前值 | 对搜索的含义 |
| --- | --- | --- |
| Nextcloud | `34.0.2-r10`，基于 `nextcloud:34.0.2-apache` | 必须使用明确支持 NC 34 的 App |
| 数据库 | PostgreSQL 18.4 或 MariaDB 12.3，默认 PostgreSQL | 核心文件名/元数据查询可用；SQL Platform 的最低版本要求也满足 |
| Redis | 8.10 | 锁与缓存，不是全文索引 |
| cron | 独立 `anas_nextcloud-cron` | 可运行 Full Text Search 后台增量任务 |
| 已有辅助服务 | notify_push、Imaginary、Talk、可选 Collabora | 均不提供正文索引 |
| 已安装 App 调和 | richdocuments、spreed、previewgenerator、notify_push、memories、LDAP、OIDC/SAML 等 | 没有 `fulltextsearch*` App |
| 容器软件 | `ffmpeg`、`jq`、`netcat` 等 | 不含 Elasticsearch、Tika 或独立全文索引服务 |

因此当前用户可以使用 Nextcloud 统一搜索，但文件 Provider 只在数据库中查找可见的文件元数据。Nextcloud 34 用户手册也把 Files 页搜索描述为“按名称跨文件查找”。这不是配置遗漏，而是核心功能边界。

## 3. Nextcloud 的三层搜索架构

### 3.1 核心文件与 WebDAV 搜索

Nextcloud 核心维护 file cache、文件属性和权限关系。Files UI 可以按名称搜索，WebDAV `SEARCH` 实现 RFC 5323 风格的属性过滤与排序，适合桌面/移动客户端或自动化按文件名、MIME、时间、大小等属性查询。

优点是零额外服务、结果与当前权限实时一致、备份恢复不增加派生数据。缺点是没有文档正文倒排索引，无法回答“哪个 PDF 里出现了某句话”。

### 3.2 Unified Search：聚合 UI，不是搜索引擎

Nextcloud 统一搜索是可插拔 Provider 聚合层。客户端先获取 Provider 列表，再并行请求 Files、Talk、Calendar、Contacts 或其他 App 的结果；Nextcloud 28 起 Provider 可声明时间、人员、MIME、大小等高级过滤器，Nextcloud 32 起还能标记外部第三方 Provider 并默认关闭以保护隐私。

统一搜索解决“一个入口展示多个数据源”，不负责生成文件正文索引。部署 Full Text Search 后，它会注册新的 Provider 并把正文结果送入同一个 UI。

### 3.3 Full Text Search Framework：Provider + Platform

经典全文检索由三类组件构成：

| 角色 | App/服务 | 职责 |
| --- | --- | --- |
| 框架 | `fulltextsearch` | 管理索引队列、状态、命令、统一搜索接入和权限过滤模型 |
| 内容 Provider | `files_fulltextsearch` | 读取文件、评论、共享/Group 权限，决定 PDF/Office/外部存储等索引范围 |
| Platform App | `fulltextsearch_elasticsearch` | 把文档映射到 Elasticsearch，执行倒排查询并按访问字段过滤 |
| 搜索服务 | Elasticsearch | 保存派生的标题、正文、权限字段和倒排索引；解析 PDF/Office 内容 |

框架要求至少一个内容 Provider 和一个 Platform；只安装 `fulltextsearch` 不会产生可搜索正文。初次部署必须至少执行一次 `occ fulltextsearch:index`，随后 live index/后台任务维护增量变化。

Nextcloud 27 起还提供 FullTextSearch Collection OCS API，可让外部程序拉取待更新文档、正文和访问控制数据，并在任意外部搜索引擎维护索引。这证明框架并不强绑定 Elasticsearch，但自建适配器必须自己实现索引、查询、权限过滤、删除同步、监控和升级兼容。

## 4. 2026-08-20 兼容矩阵

| 组件 | 当日稳定状态 | Nextcloud 34 | 判断 |
| --- | --- | --- | --- |
| Nextcloud core Files / Unified Search | 随 Nextcloud 34.0.2 | ✅ | 当前已用；文件名/元数据/App 聚合 |
| `fulltextsearch` | App Store `34.0.1` | ✅ | 框架可用，但单独安装没有文件正文 |
| `files_fulltextsearch` | App Store `33.0.0`；`stable34` 修复已合并 | **尚无 34 稳定包** | 当前生产阻塞项 |
| `fulltextsearch_elasticsearch` | App Store `33.0.0`；`stable34` 修复已合并 | **尚无 34 稳定包** | 当前生产阻塞项 |
| Elasticsearch | AIO main 已使用 `9.5.1`；34 修复曾以 9.4.2 验证 | ◐ 依 App 精确版本 | 锁定组合后做真实 bulk index，不凭 `fulltextsearch:test` 单独判定 |
| `fulltextsearch_sql` | App Store `1.3.6` | ✅ | POC；功能和多语言能力不足 |
| `context_chat` + backend | App Store 5.4.x | ✅ | 语义搜索/问答，资源重且有不同安全边界 |

### 4.1 为什么“代码可运行”仍不等于可以发版

Nextcloud 34 移除了 App 管理页依赖的全局 jQuery，三件套的旧管理 UI 因而无法正确加载或保存设置。对应修复已在 2026-07-17 合并：

- `fulltextsearch` 管理页修复；
- `files_fulltextsearch` Provider 设置修复并回移 `stable34`；
- `fulltextsearch_elasticsearch` Platform 设置修复并提升 NC 34 兼容上限。

贡献者对 patched branches 做过 Nextcloud 34.0.0、Elasticsearch 9.4.2、真实文件批量索引和 UI/权限测试，结果成功；但 ANAS 的 release Module 依赖可重复下载、签名、App Store 元数据和稳定升级路径。直接从分支或个人 fork 打包会把上游尚未发布的状态变成 ANAS 自己的长期维护责任。

### 4.2 Elasticsearch 8/9 的版本陷阱

旧 README 仍写“App 26+ 只兼容 Elasticsearch 8”，AIO 12 曾携带 Elasticsearch 8.19；但 2025 年底以来 Platform App 的 PHP client 迁移造成过 header/bulk API 不兼容。简单的 `occ fulltextsearch:test` 可能只覆盖模拟文档和单文档路径，真实 `_bulk`、附件解析和大数据集仍会失败。

当前 AIO 主分支已经改为 Elasticsearch 9.5.1、默认 JVM heap 512 MB，说明上游方向已切到 9。ANAS 实施时必须同时锁定：

1. 三个 Nextcloud App 的签名稳定版本；
2. Elasticsearch 精确 patch 版本；
3. 文档解析/中文分析插件的完全相同版本；
4. amd64 与 arm64 镜像 digest；
5. 至少一个包含 PDF、DOCX、ODT、Markdown 和中文内容的真实批量索引测试。

## 5. 候选方案比较

| 方案 | 正文与格式 | 资源/运维 | 多语言 | 成熟度 | ANAS 结论 |
| --- | --- | --- | --- | --- | --- |
| 核心 Files + Unified Search | 文件名、元数据、App 数据；无正文 | 最低 | 文件名依数据库 collation | 核心稳定 | **继续作为默认基线** |
| FTS + Elasticsearch 9 | 纯文本、PDF、Office 等；取决于 Provider 配置 | 新增 JVM 服务、索引盘、版本矩阵 | 可装语言分析器 | 上游主路径，但 NC34 正处发布窗口 | **目标生产方案，等待稳定包** |
| FTS + SQL Platform | 纯文本、PDF；不支持 Office | 无新服务，但显著放大主 DB 与备份 | PostgreSQL 当前假设 English | 作者标 POC | **仅小型实验，不作为默认** |
| FTS Collection API + 自选引擎 | 取决于自研抓取和 Engine | 最高；需自建权限/增量/查询适配 | 可完全自定义 | API 稳定，集成需自负 | **有统一搜索平台时再评估** |
| Context Chat / pgvector | 文档切片、语义向量、问答 | 很高；ExternalApp、模型、pgvector | 取决于 embedding/LLM | NC34 可用但 AI 栈复杂 | **独立 experimental，不替代 FTS** |

### 5.1 为什么推荐 Elasticsearch

- 它是 `fulltextsearch_elasticsearch` 的直接上游目标，也是 Nextcloud AIO 的可选组件；
- `files_fulltextsearch` 的 Office 解析路径依赖 Elasticsearch attachment processor/Apache Tika 能力，SQL Platform 明确不实现该部分；
- 访问控制字段、分享/Group 过滤、索引 mapping 和 OCC 运维命令已有现成实现；
- ANAS 只需维护锁定镜像和调和逻辑，不需要发明新的 Search Platform App。

代价是 Elasticsearch 索引保存可读正文副本，JVM 内存与磁盘占用不可忽略，且 App/Engine 的 major 版本必须同步验证。

### 5.2 为什么不选 SQL Platform

`fulltextsearch_sql` 的优点是真正做到“无额外容器”，当前 ANAS 的 PostgreSQL 18.4 和 MariaDB 12.3 也都满足其最低版本。问题更实质：

- 作者明确标为 proof of concept；
- 同一份内容大致以明文和全文索引两种形式进入 Nextcloud 主数据库，文档多时数据库可增长数百 MB 乃至数 GB；
- 不索引 Office 文档；
- PostgreSQL 路径固定使用 English 配置，中文召回不满足 ANAS 默认语言场景；
- 主业务数据库的备份、恢复、VACUUM、迁移和查询负载都被派生索引放大。

它可作为低资源设备上的独立 PoC 选项，但不应为了少一个容器牺牲搜索质量和主数据库边界。

### 5.3 OpenSearch、Solr、Meilisearch 和 Typesense

本轮没有发现 Nextcloud 34 App Store 中与经典文件 Provider 配套、维护状态和权限语义达到 Elasticsearch Platform 同等水平的 OpenSearch、Meilisearch 或 Typesense Platform App。旧 Nextant/Solr 路线也不是当前 Nextcloud 34 的上游主路径。

可以通过 Collection API 把内容送入这些引擎，但还要另写“搜索结果回到 Nextcloud UI”及访问控制适配器。除非 ANAS 后续建立跨应用统一搜索 Contract，否则不应只为 Nextcloud 引入自研 Platform。

### 5.4 Context Chat 的定位

Context Chat 适合“找概念相近的资料”和“基于我的文件回答问题”，传统 FTS 适合精确关键词、短语、文件名和可解释的确定性结果。二者可以并存，但不能互相替代。

上游给出的 CPU 规划是至少 12 GB 系统 RAM、4+ 核用于 embedding，推荐专用机器；GPU 路径至少 2 GB VRAM 和 8 GB 系统 RAM，文本生成 Provider 资源另算。它还会把文本切片和向量复制到内置 PostgreSQL/pgvector。更重要的是，上游明确说明 Context Chat 不执行 `files_accesscontrol` 规则：只要文件在 Files UI 对用户可见，即使下载/访问被 Flow 规则拒绝，也可能通过 AI 结果泄露内容。

因此 Context Chat 只能在独立风险评审后作为 `experimental`，不能借“搜索增强”名义默认安装。

## 6. 推荐的 ANAS 实现设计

本节是后续实施提案，不表示当前仓库已经具备这些配置。

### 6.1 归属与拓扑

第一版把 Elasticsearch 作为 `nextcloud` Module 的 optional service，而不是新建通用 `elasticsearch` Module：它只服务一个 Nextcloud 实例，索引是该实例的可重建派生数据，生命周期、App 版本和权限 mapping 都与 Nextcloud 强绑定。如果以后出现第二个真实 Consumer，再抽象 `search_index` Contract。

```text
Browser / Client
       |
       v
Nextcloud Unified Search
       |
       v
fulltextsearch -> files_fulltextsearch
       |
       v
fulltextsearch_elasticsearch
       |
       v
private nextcloud network -> Elasticsearch 9 + language plugins
                              |
                              v
                       rebuildable index volume
```

Elasticsearch 不接 Traefik、不发布主机端口，只加入 `nextcloud` 私有网络。即使在私网内也使用生成的最小权限凭据；如未来连接外部集群，必须启用 TLS、CA 验证和专用 index 权限，不能允许通配 cluster 管理权限。

### 6.2 建议配置契约

建议先保持少而稳定的用户配置，内部版本细节由 Module 锁定：

| 配置 | 建议类型/默认值 | 变更效果 | 说明 |
| --- | --- | --- | --- |
| `nextcloud.search_enabled` | bool / `false` | `container_recreate` + reconcile | 启停可选服务和三个 App |
| `nextcloud.search_local_files` | bool / `true` | `reindex` | 索引本地文件 |
| `nextcloud.search_external_files` | bool / `false` | `reindex` | 外部存储故障会拖累索引，默认关闭 |
| `nextcloud.search_group_folders` | bool / `false` | `reindex` | 需先完成共享/所有者回归测试 |
| `nextcloud.search_pdf` | bool / `true` | `reindex` | 提取带文本层 PDF；OCR 另议 |
| `nextcloud.search_office` | bool / `true` | `reindex` | DOCX/ODT 等依 attachment processor |
| `nextcloud.search_max_file_size` | size / PoC 后定 | `reindex` | 避免超大文件拖垮 PHP/ES |
| `nextcloud.search_analyzer` | enum / `standard` 或区域默认 | `reindex` | analyzer 改变必须重建索引 |
| `nextcloud.search_java_options` | string / 内部默认 | `container_recreate` | 仅作为高级资源控制，需格式校验 |

不要暴露 `elastic_host`、index 名、App class name 或 App 版本为普通用户配置；它们是实现细节。Elasticsearch 密码进入现有 Secret Store，绝不写 lock、普通 `.env` 文档或命令行参数日志。

### 6.3 中文与混合语言

Elasticsearch `standard` analyzer 对中文词语召回不理想。官方 `analysis-smartcn` 插件提供简体中文及中英混合分词，插件版本必须与 Elasticsearch 节点完全一致并在每个节点安装。ANAS 的 PoC 至少比较：

- `standard`：跨语言最简单，但中文通常退化为单字 token；
- `smartcn`/`smartcn_tokenizer`：简体中文更好；
- 繁体中文、简繁混合、英文缩写、文件名和正文短语的真实召回。

不要直接承诺 `smartcn` 为所有地区的默认值：它面向简体中文，Nextcloud Platform App 的 analyzer 配置能力也必须在锁定版本上验证。若 App 不能稳定声明自定义 analyzer，应保持 `standard`，把中文优化列为后续工作，而不是手工修改运行中 index mapping。

### 6.4 App 安装与调和

复用当前 `task.sh` 的签名 App Store 安装、精确版本 pin、完整性检查和 enable/disable 抽象，新增三个 App 的固定版本。启用顺序应为：

1. Elasticsearch 健康并通过鉴权；
2. 安装/启用 `fulltextsearch`；
3. 安装/启用 `files_fulltextsearch`；
4. 安装/启用 `fulltextsearch_elasticsearch`；
5. 用 OCC 设置 Platform、host、index、凭据和文件范围；
6. 执行 `occ fulltextsearch:check` 与 `occ fulltextsearch:test`；
7. 启动异步首次索引操作。

首次全量索引可能运行数小时甚至数天，不应阻塞 Nextcloud Web health 或 Module 安装 15 分钟后失败。Runner 应把它建模为带进度、可重试、可取消的长操作；Nextcloud 服务可先 ready，但管理面必须显示“索引未完成/进度/错误数”。

关闭功能时先停止索引任务，再禁用三个 App 和 Elasticsearch service。默认保留索引目录以便短期重新启用，但提供显式清理操作；不能在普通 `config set false` 中静默删除数据。

### 6.5 备份、恢复和回滚

Elasticsearch index 是可从 Nextcloud 文件与数据库重建的派生数据。推荐契约：

- Nextcloud 文件、数据库、Secret Store 和搜索配置仍是权威备份对象；
- 搜索 index 默认不作为必须一致恢复的权威数据，恢复后执行 `fulltextsearch:reset` + 全量重建；
- 如果底层 snapshot 顺带包含 index，也不能假设它与数据库恢复点一致；恢复 verify 必须校验 generation，不能通过就清空重建；
- Elasticsearch major、Platform App major、analyzer 或 mapping 发生变化时，升级计划必须显式包含 reindex；
- 回滚到旧 App/Engine 时不得直接复用新 mapping，优先重建而不是试图原地降级。

该选择会延长灾后“搜索完全可用”的时间，但显著降低一致性和备份体积风险。Web、同步和分享恢复不应等待索引重建。

### 6.6 安全边界

全文索引包含文件标题、可读正文、评论片段和访问控制字段，等价于第二份敏感数据副本：

- 只允许 Nextcloud/cron/受控管理操作访问 Elasticsearch；
- 禁止主机端口和 Traefik route；
- Elasticsearch 凭据最小权限、可轮换，不复用 Nextcloud DB 或管理员密码；
- 日志不得输出文档正文、Authorization header 或带密码 URL；
- 文件分享、Group 变更、取消分享和删号后必须快速更新/删除权限字段；
- E2EE 文件无法由服务器可靠索引；服务端加密内容在索引时可能被解密并形成明文副本，启用前必须向管理员说明；
- 不默认索引 external/federated/encrypted/group folders，分别通过验证后开放；
- 搜索结果仍需在打开文件时由 Nextcloud 做最终权限判定，不能把 ES 命中视为授权。

## 7. PoC 与验收门槛

### 7.1 上游发布门槛

开始 release 实施前必须同时满足：

- App Store 存在 Nextcloud 34 稳定版 `fulltextsearch`；
- App Store 存在 Nextcloud 34 稳定版 `files_fulltextsearch`；
- App Store 存在 Nextcloud 34 稳定版 `fulltextsearch_elasticsearch`；
- 三者签名包可由默认与中国镜像路径重复下载；
- 锁定 Elasticsearch 9 patch 与 App 内 PHP client 相容；
- AIO 或上游维护说明不再把该组合列为 NC34 升级阻塞项。

### 7.2 功能测试

准备至少两个普通用户、一个 LDAP Group、个人目录、共享目录和 Group Folder，覆盖：

- 文件名搜索与正文搜索结果可区分；
- TXT、Markdown、PDF、DOCX、ODT 的标题和正文；
- 简体、繁体、中英混合、英文大小写、短语和特殊字符；
- 上传、修改、重命名、移动、删除、回收站恢复后的增量索引；
- 用户分享、Group 分享、取消分享、Group 成员移除后的可见性；
- 同名文件、不同所有者、外部存储暂时离线；
- 真实 `occ fulltextsearch:index` bulk 路径，不只运行 `fulltextsearch:test`；
- 搜索命中后资源 URL 可以正确打开，已撤权结果不可打开也不可继续出现。

### 7.3 运维与故障测试

- Elasticsearch 未启动、启动慢、鉴权错误、只读磁盘和磁盘满；
- Nextcloud/cron 在 Platform 不可用时仍能提供文件访问，不进入重启风暴；
- 初始索引中断后可续跑，重复操作幂等；
- App 小版本升级、Elasticsearch patch 升级、analyzer 变化和完整 reindex；
- PostgreSQL/MariaDB 两种 Nextcloud 后端；
- amd64 与 arm64；
- snapshot 恢复后不恢复 index、恢复陈旧 index 两种路径；
- Secret 轮换期间不中断或能原子回滚；
- 资源基线：空闲/索引/查询三种状态的 RSS、CPU、IO、index 大小和完成时间。

### 7.4 发布阶段

| 阶段 | 状态 | 进入条件 |
| --- | --- | --- |
| M0 当前 | 研究完成，保持现状 | 核心文件名/元数据搜索 |
| M1 | 本地 PoC | 34 稳定三件套齐全，锁定 ES 9 组合 |
| M2 | `experimental` optional | 权限、中文、恢复、双架构、资源测试通过 |
| M3 | `stable` optional | 至少一个升级周期无 mapping/权限回归，有明确运维文档 |
| 默认开启 | **不建议** | 只有产品明确选择“搜索优先”且资源门槛可声明时再决策 |

## 8. 明确不做的事情

- 不在当前 release 中强制安装 NC33 App 或设置 `app_install_overwrite` 绕过兼容检查；
- 不从 `master`、个人 fork 或 `35.0.0beta1` 构建生产 App；
- 不使用 Elasticsearch `latest`；
- 不把 Elasticsearch 9200 暴露给 LAN/Internet；
- 不把长时间首次索引塞进 Web 容器健康检查；
- 不把索引当作唯一数据副本或必须原样恢复的权威状态；
- 不因 SQL Platform 少一个容器就把 POC 升为默认；
- 不把 Context Chat 的向量搜索宣传为精确全文搜索，也不忽略其访问控制限制。

## 9. 资料来源

### Nextcloud 核心与 API

- [Nextcloud 34 用户手册：Files 搜索按名称查找](https://docs.nextcloud.com/server/stable/user_manual/en/files/access_webgui.html)
- [Nextcloud 34 开发手册：Unified Search 架构与 Provider](https://docs.nextcloud.com/server/stable/developer_manual/digging_deeper/search.html)
- [Nextcloud 34 WebDAV SEARCH API](https://docs.nextcloud.com/server/stable/developer_manual/client_apis/WebDAV/search.html)
- [Nextcloud 34 FullTextSearch Collection OCS API](https://docs.nextcloud.com/server/stable/developer_manual/client_apis/OCS/ocs-fulltextsearch-collections-api.html)
- [Full Text Search Framework 仓库](https://github.com/nextcloud/fulltextsearch)
- [Files Full Text Search 仓库](https://github.com/nextcloud/files_fulltextsearch)
- [Elasticsearch Platform 仓库](https://github.com/nextcloud/fulltextsearch_elasticsearch)
- [Full Text Search OCC commands](https://github.com/nextcloud/fulltextsearch/wiki/Commands)

### 版本与兼容状态

- [App Store：Full text search](https://apps.nextcloud.com/apps/fulltextsearch)
- [App Store：Full text search - Files](https://apps.nextcloud.com/apps/files_fulltextsearch)
- [App Store：Full text search - Elasticsearch Platform](https://apps.nextcloud.com/apps/fulltextsearch_elasticsearch)
- [Files Provider 的 Nextcloud 34 管理页修复与实测](https://github.com/nextcloud/files_fulltextsearch/pull/365)
- [Elasticsearch Platform 的 Nextcloud 34 管理页修复与实测](https://github.com/nextcloud/fulltextsearch_elasticsearch/pull/473)
- [Nextcloud AIO 的 NC34/全文搜索升级跟踪](https://github.com/nextcloud/all-in-one/issues/8327)
- [Nextcloud AIO 当前全文搜索 Dockerfile](https://github.com/nextcloud/all-in-one/blob/main/Containers/fulltextsearch/Dockerfile)

### 候选后端与语义搜索

- [App Store：Full text search - SQL Platform 1.3.6](https://apps.nextcloud.com/apps/fulltextsearch_sql)
- [SQL Platform README 与限制](https://github.com/jplitza/fulltextsearch_sql)
- [Nextcloud 34 Context Chat 管理手册](https://docs.nextcloud.com/server/stable/admin_manual/ai/app_context_chat.html)
- [App Store：Nextcloud Assistant Context Chat](https://apps.nextcloud.com/apps/context_chat)
- [Elasticsearch 官方 Smart Chinese analysis plugin](https://www.elastic.co/docs/reference/elasticsearch/plugins/analysis-smartcn)

## 10. 最终建议

ANAS 现在不应修改 Nextcloud 运行功能，但应把目标方案冻结为“可选 Elasticsearch 9 文件全文检索”，并以 App Store 的 Nextcloud 34 稳定三件套发布作为实施触发器。实现时先发布默认关闭的 `experimental`，用精确版本、私有网络、派生索引重建、异步初始索引和权限撤销测试控制风险。

若近期业务必须拥有全文搜索，风险最低的临时选择不是绕过 NC34 兼容，而是继续使用当前文件名/元数据搜索并等待上游稳定包；只有隔离测试环境可以尝试 `stable34`/beta 组合。语义搜索另开 Context Chat 专项，不与本方案合并承诺。

## 11. 补充：资源规划与轻量替代

### 11.1 Elasticsearch 的实际资源口径

Elasticsearch 没有一个适用于所有文件库的固定“最低配置”。需要同时预算 JVM heap、JVM/原生内存、off-heap buffer 和操作系统文件缓存，不能把容器内存限制直接等同于 `Xmx`。Elastic 的官方上限规则是 `Xms = Xmx`，且 heap 不超过容器可用内存的 50%；实际 RSS 高于 `Xmx` 属于正常现象。

Nextcloud AIO 当前全文搜索镜像使用 Elasticsearch `9.5.1`，并以 `-Xms512M -Xmx512M` 启动。这证明 512 MiB heap 是上游采用的小型单节点起点，但不是“容器只给 512 MiB 就能稳定运行”的承诺。

以下是 ANAS 的工程预算，不是 Elasticsearch 官方容量保证；“提取文本量”是 Tika/PDF 解析后送入索引的正文和元数据量，不是原始照片、视频或 Office 文件总体积：

| 场景 | 代表性规模 | ES 容器内存 | JVM heap | CPU | SSD 可用空间 |
| --- | --- | ---: | ---: | ---: | ---: |
| 本地 PoC | 提取文本小于 1 GiB、约 2 万文件以内 | 1.5 GiB | 512 MiB | 1–2 vCPU | 至少 10 GiB |
| 小型家庭生产 | 提取文本约 1–5 GiB、约 2–10 万文件 | 2–3 GiB | 1 GiB | 2 vCPU | 至少 20 GiB，且不低于实测索引的 2–3 倍 |
| 中型实例 | 提取文本超过 5 GiB、10 万文件以上或重度 PDF/Office | 4–8 GiB | 2–4 GiB | 4 vCPU 以上 | 不低于实测索引的 3 倍 |

文件数只是便于理解的参考，不能代替基准测试。大量短文件和少量超长 PDF 可能拥有相同文件数但完全不同的峰值内存、CPU 和索引大小。首次全量索引是资源峰值，日常查询通常明显更轻。

对整机容量可采用更直接的判断：

- 4 GiB NAS：不建议再运行 Elasticsearch；保留核心搜索或使用受限的 SQL 方案；
- 8 GiB NAS：只有在没有重型 Office、Talk、AI/图片识别负载，并能给搜索独占 2 GiB 左右时才考虑；
- 16 GiB 及以上：更适合把 Elasticsearch 作为默认关闭的可选服务，并从 1 GiB heap、2–3 GiB 容器限制开始实测。

磁盘不要按原始文件库大小直接拍比例。PoC 应抽取有代表性的 5%–10% 文件，完成全量索引后记录 index store size，再外推总量并保留 2–3 倍空间用于 segment merge、重建和版本升级。索引是可重建派生数据，不进入用户文件备份的同等恢复承诺。

Linux 主机还必须满足 `vm.max_map_count=1048576` 的当前 Elastic 建议值，并避免 swap。对共享型 NAS 而言，CPU 限制、低优先级首次索引、最大可索引文件大小和磁盘水位告警都应是产品配置，而不是让 Elasticsearch 无限制争用整机。

### 11.2 小型开源替代方案

必须区分“引擎本身更小”和“能直接接入 Nextcloud”。截至 2026-08-20，Nextcloud App Store 的 Full Text Search Platform 成品主要是 Elasticsearch 与 SQL；未找到可直接替换为 Meilisearch、Typesense 或 Sonic 的 Nextcloud 34 Platform App。因此后三者都需要实现 Nextcloud Platform App、文件内容抽取、权限过滤、增量同步、删除/撤权和结果高亮，不能只更换一个容器镜像。

| 方案 | 资源特征 | Nextcloud 34 接入 | 关键限制 | 结论 |
| --- | --- | --- | --- | --- |
| 核心文件名/元数据搜索 | 无新增服务 | 原生 | 不搜文件正文 | 最小系统的默认选择 |
| `fulltextsearch_sql` | 无新增常驻服务，但把正文和索引压进现有数据库 | App Store 已有 NC34 稳定版 | 作者标为 POC；数据库约存两份索引内容；无 Office 文档；PostgreSQL 假定 English 配置 | **唯一可直接试用的轻量替代**，只适合受限场景 |
| Meilisearch | 单 Rust 服务；查询轻，但索引过程多线程且内存密集，官方建议索引时内存可容纳完整数据集，可限制 indexing memory | 无现成 Platform App | 需自研抽取、ACL 与同步；低内存大批量索引仍可能 OOM | 自研路线的首选候选，不是即插即用替代 |
| Typesense | 空库进程约 20 MiB，但全文字段通常需要其数据量的 2–3 倍 RAM，且官方最低 2 vCPU | 无现成 Platform App | 索引常驻内存；长文正文变大后未必比 ES 省内存 | 更适合结构化短文档，不优先用于 NAS 文件正文 |
| Sonic | 项目实测负载下约 30 MiB RAM，返回外部对象 ID 而非完整文档 | 无现成 Platform App | 只是轻量词到 ID 索引；高级过滤、高亮、附件解析和 ACL 都要自己补 | 真正很小，但功能差距最大，仅适合实验 |
| OpenSearch | 仍是 JVM/Lucene 同类服务，官方示例同样从 512 MiB heap、约半数系统内存规则起步 | 不能假定与 Elastic 9 adapter 兼容 | 资源与运维级别未显著下降；需要维护兼容矩阵 | 不是“小型替代”，不纳入 ANAS 轻量方案 |

### 11.3 选型结论

如果目标是“不开发 Nextcloud App”：

1. 4 GiB 设备保留核心搜索；
2. 8 GiB 设备可以把 SQL Platform 作为实验选项，但必须接受 POC、无 Office 和数据库膨胀；
3. 能提供额外 2–3 GiB 内存的设备，继续采用已验证版本矩阵的单节点 Elasticsearch，整体风险低于自研一个轻量引擎适配层。

如果 ANAS 愿意承担长期开发维护，Meilisearch 是比 Typesense/Sonic 更均衡的首个 PoC 候选，但只有在真实语料证明总 RSS、索引耗时和检索质量均显著优于 Elasticsearch 后才值得产品化。对于 Nextcloud 文件全文检索，权限正确性和增量一致性比单个引擎空载时节省几百 MiB 更重要。

### 11.4 补充来源

- [Elastic JVM heap 与容器内存规则](https://www.elastic.co/docs/reference/elasticsearch/jvm-settings)
- [Elastic `vm.max_map_count` 当前建议](https://www.elastic.co/docs/deploy-manage/deploy/self-managed/vm-max-map-count)
- [Nextcloud AIO 当前全文搜索 Dockerfile](https://github.com/nextcloud/all-in-one/blob/main/Containers/fulltextsearch/Dockerfile)
- [Nextcloud App Store：SQL Platform](https://apps.nextcloud.com/apps/fulltextsearch_sql)
- [SQL Platform 的资源与功能限制](https://github.com/jplitza/fulltextsearch_sql)
- [Meilisearch 索引内存与线程说明](https://www.meilisearch.com/docs/resources/self_hosting/performance/ram_multithreading)
- [Typesense 官方系统资源估算](https://typesense.org/docs/guide/system-requirements.html)
- [Sonic 官方仓库与资源定位](https://github.com/valeriansaliou/sonic)
- [OpenSearch Docker 与 JVM 配置示例](https://docs.opensearch.org/latest/install-and-configure/install-opensearch/docker/)
