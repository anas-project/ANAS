---
doc_type: plan
status: implementing
created: 2026-10-03
updated: 2026-10-11
---

# 应用目录实施计划

验收依据为 [应用目录要求](../requirements/app-catalog.md)，实现机制见
[协议设计](../../docs/architecture/app-catalog-design.md)。未指定发布日期，不沿用旧方案的兼容阶段。

## 1. 落地快照与待确认项

2026-10-03：本轮重写协议、补需求和计划、运行固定 Casdoor 12 个权限/目录源码子场景。
源码实验通过，报告见 [调研](../../docs/research/casdoor-app-catalog.md)；它不计作新版目录实现或 E2E。
当时生产代码仍消费 APPS_LIST，尚无新 schema、目录制品、Casdoor 新 API/页面。
2026-10-04：增加首期中英文语言映射、逐语言覆盖与回退规则，同步五条验收要求；仍为文档规划，
尚未实现或运行双语目录 API/浏览器验收。
同日明确：所选语言不受支持时直接使用英文，不再回退到浏览器次选或部署默认语言。
2026-10-08：用户确认“用户应用 / 后台管理 → 分类小组”的两级展示，补充 R-036—R-038，
保留主分类归属、附加分类筛选与去重。复用 kind/categories，分组部分复杂度低，不增加字段或组件。
该次仅更新规划；分组实现及验收均未执行，不能计作新增目录功能通过。

PostgreSQL/MariaDB compose 均已挂接 ForwardAuth middleware，oauth2_proxy 已从目录管理员组派生
platform_admin 门禁。运行的 TestGate* 与 TestBundledAdminerResolvesOnASingleDatabase 通过；
后者目前使用 LLNG/PostgreSQL fixture，不能代替 Casdoor + 两种数据库的真实访问验收。
第一阶段复用这些运行路径，增加目录声明、授权事实校验和双数据库渲染检查。

2026-10-08：用户确认先使用各 IAM 原生功能，第二阶段实现自己的门户。第一阶段仅实现统一
声明、制品和原生对象投射，不改 Casdoor 的目录 API 或页面。完整门户仍按原需求验收；
原生功能缺口不能记作通过，也不再阻塞第一阶段。自己的门户的部署位置在第二阶段设计时确定。

第一阶段复杂度中等：一个 Core 目录包、两个保留的 Hook 适配、现有 deployment 文件共享，
没有新增服务或后台同步器。新增 Go 依赖 x/image v0.46.0 用于固定重采样，同时由其模块依赖
升级 x/text、x/sys 和 x/sync；不安装系统图片转换工具。

## 2. 需求归属与里程碑

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：声明与配置 | R-001—R-009、R-019、R-020、R-030—R-034 | 实施中；验收未完成 |
| M2：图标与共享制品 | R-010—R-015 | 实施中；验收未完成 |
| M3：原生适配与第二阶段门户 | R-016—R-018、R-021、R-022、R-026、R-035—R-039 | 实施中；Casdoor/LLNG 原生适配，独立门户第二阶段 |
| M4：覆盖、切换与验收 | R-023—R-025、R-027—R-029 | 实施中；验收未完成 |

### M1：声明与配置

验收：要求 §1、§3。实现 manifest applications、消费者声明、workspace 分类/覆盖/外部 entries，
关联已有 IAM/ForwardAuth/native 执行点事实，并把仅由目录引用的组纳入 IAM 同步集合。使用同一类型化解析结果服务 plan 和 render。
对未声明值、冲突、未知分类、非法 URL、非法覆盖和空授权增加反例。
按要求 §1.1 实现中英文映射、内置文案完整性检查、外部单语回退和逐语言覆盖；
验证区域语言、未支持语言、缺译、空值与切换后身份/权限一致性。特别覆盖用户选择法语且
浏览器/部署默认中文、浏览器首选日语且次选中文时均选英文，以及无用户/浏览器偏好时才使用
部署默认的情况。两种语言同时冻结进同一制品。
先使用 synthetic Module 测试通用行为，再补真实 Module；Core 不增加产品名分支。

### M2：图标与共享制品

验收：要求 §2。实现本地 PNG 校验、固定重采样、七档占位图、资产清单、摘要和 scoped 路径。
复用 deployment 制品发布、runtime 投影和历史保留。验证暂存路径 promote 后不泄漏，
远程运行时读取的是目标文件。补单改图片、文件越界、只读挂载和回滚测试。
新增 x/image/draw 编译依赖前在变更说明中列明理由及固定版本，不安装系统图像转换工具。

### M3：原生适配与第二阶段门户

验收：要求 §1.2、§3—4。

第一阶段：Casdoor 投射 displayName/homepageUrl/description/logo/order 与原生 Permission；
LLNG 投射 applicationList 的用途/主分类组合组名和精确组 OR（默认 Portal 模板仅一层，见 §3.5）。原生对象使用部署默认语言，未支持语言选英文。
完整中英文和七档图标继续冻结在同一目录制品；不声称原生 UI 支持即时文案切换。

第二阶段：自己的门户完成 R-005、R-014、R-018、R-022、R-026、R-035—R-038 的完整行为，
包括原样 URL、认证后先过滤再返回文案、无管理员绕过、策略失效拒绝、主分类/附加筛选、
空组隐藏及即时语言切换。保持只读目录为事实来源，不以原生数据库投影回填目录。
用户已改为独立网站；目录交付见设计 §10，账号首版已确认，另由[门户计划](../../modules/user_portal/dev-docs/plans/user-portal.md)调度；其设计见[独立用户门户](../../docs/architecture/user-portal-design.md)。

原生限制：Casdoor 原生字段最大 100 字符，超长标题/简介/URL 明确拒绝；只有一个图标，
原生点击会追加 silentSignin，原生权限缺失默认允许且管理员绕过。hidden/禁用的 IAM client
仍可能由原生列表枚举；不可当作严格元数据保密。LLNG 默认 Portal 模板仅支持一层分类，精确组规则不自动加管理员。Casdoor/LLNG 继续真实
HTTP/浏览器验收；2026-10-10 用户要求不测试 Authentik，保留其单元/构建证据，不计运行验收通过。

### M4：覆盖与无兼容切换

验收：要求 §4。从 inventory 派生全量入口表，核实 LAM、DDNS、Collabora、Traefik 等本地/原生后台，
给两套 Adminer 补声明并用 Casdoor 做真实网关验收。对恢复入口与无人员界面 Module 记录不发布原因。
同一交付移除 APPS_LIST 的 Module 发布、Runner 聚合、IAM 消费和旧文档引用；不在半成品上宣称完成。
LLNG 新协议缺口显式列出。2026-10-10 用户决定移除 Authentik，覆盖模块、发布清单、
可执行配置、专属脚本和推荐；不得静默迁移已有账号、身份绑定或删除业务数据。

## 3. 第一阶段执行记录（2026-10-08）

代码已实现，阶段运行验收尚未完成。统一声明、七档 PNG、只读共享、三个原生适配及旧协议移除
已进入当前工作树；不把单元或渲染通过记作真实登录、HTTP 或浏览器通过。

### 3.1 测试基线与制品

源码基线为 HEAD `aa1944a035c7f96f3afda4cfd1b904852e191b6f` 加本地工作树。
测试使用了工作树既有的无关变更，没有将其重置或纳入本任务提交。
Go、Module YAML、Shell、Python 源文件的逐文件清单在本机
`/tmp/anas-app-catalog-source-baseline-20261008.json`，规范化清单摘要为
`sha256:9b143747bc48176efa500d4ec722bb3c128c10d4910b6bd39bc644017afe26b2`。
实际 CLI `/tmp/anas-app-catalog-cli` 的 SHA-256 为
`3b210b1ab65c2b116a1569a1701ffeedda711e22b0d4695062e0fc6731963f51`。
固定原生适配版本：Casdoor `3.143.0-r11`、LLNG `2.23.2-r13`、Authentik `2026.5.6-r15`；
仅编译和渲染，未构建或启动这些版本的容器，尚无已运行镜像摘要。

三个 fixture 分别执行真实 `init`、`plan`、`render --update-lock`；临时 workspace 位于
`/tmp/anas-app-catalog-{casdoor,llng,authentik}-20261008`。每个目录包含四条声明、三条启用入口。
检查了七档实际 PNG 像素/哈希、只有声明消费者得到目录变量、最终路径没有 staging、
目录不含凭据、原生对象保留已有 IAM client、纯目录对象不生成 OAuth 凭据。

| Provider | deployment | catalog.json SHA-256 | assets.json SHA-256 |
| --- | --- | --- | --- |
| Casdoor | `20261008T103244Z-a107a33f` | `13999b89a117a576211a939d25c61714e015de88ce902d9042e6c358347c510e` | `7672abb8124fbd6b3ed32c8de9d6f2ae2929136ce4c571248eff7a477ef08b1c` |
| LLNG | `20261008T103244Z-707c4ca9` | `8d45949d9a2136c1da8052ae4d0d30b5985450c8a132815728747c27deb19f1d` | `f0f9120e6ac7cf74655b10ad0164288c990372063035ca331ad5c82051169a75` |
| Authentik | `20261008T103244Z-53d7f7c0` | `82235f05b574ee9a9f10e62b5d33f4a78846739370998de494782498c8f02e58` | `75642598559c75f0502989992f82f451658d7d1b74be433a7ff87d914fbc10ca` |

补充 Casdoor fixture 同时启用 PostgreSQL/MariaDB Adminer，并仅用源配置添加外部管理入口。
`plan` 解析出 oauth2_proxy，两个网关规则与目录均使用实际 `Admins` 组；法语部署默认选英文。
新增、改名/权重更新、删除依次导入后渲染通过，旧 deployment 保留原条目；删除后的原生应用
和 Permission 输入不再包含该外部条目。原生数据库实际更新/删除仍待容器验证。
直接修改受管 workspace/config.yml 会被既有完整性检查拒绝，操作流程明确为编辑源 YAML →
`config import` → apply，无需 IAM 后台操作。

| 操作 | deployment | catalog.json SHA-256 |
| --- | --- | --- |
| 添加外部条目、启用两套 Adminer | `20261008T103942Z-25ea7567` | `a9db11b9fc762218b59cbc9c36a55fcfea9e6a660b381de042abe0c0c5f84c04` |
| 更新显示名和权重 | `20261008T104133Z-0432761b` | `d63e4379cdff0dd735bc78d56807cbaf05b443f3a7daec4affad54d4e21bea16` |
| 删除外部条目 | `20261008T104228Z-265bf09a` | `a7cd0f92b42b2de3c9ef855dc0a3303714fc13e1c5d2da5423de6332708e45e5` |

添加时 assets.json SHA-256：`3e287314587529d291c0333797c3714bad4dbd34bf8af4e3d85ebf131ea1147d`。
制品摘要与检查记录分别在 `/tmp/anas-app-catalog-render-evidence-20261008.json` 和
`/tmp/anas-app-catalog-adminer-external-evidence-20261008.json`。
这些路径仅是本机证据，没有共享持久归档，不能作为最终验收归档。

### 3.2 按需求 ID 的结果（截至 2026-10-08）

“检查通过”仅指列出的单元/渲染/CI，未表示整个判据最终通过。保留全部 38 项最终范围，
第一阶段原生缺口不被豁免；有 E2E 或第二阶段依赖的里程碑不完成、不归档。

| 需求 ID | 当前证据与结果 | 剩余工作 |
| --- | --- | --- |
| APPCAT-R-001 | 非 IAM 本地管理入口与外部入口生成原生对象，渲染检查通过 | 实际打开与原生对象协调 E2E |
| APPCAT-R-002、APPCAT-R-003、APPCAT-R-004 | 同 Module 多入口、规范化字段、稳定身份/重名拒绝单元通过；实际制品检查通过 | 随阶段运行验收复核 |
| APPCAT-R-005 | 目录与 LLNG 原生输入原样保留 query/fragment | Casdoor 原生点击追加 silentSignin；第二阶段门户及浏览器验收 |
| APPCAT-R-006、APPCAT-R-007、APPCAT-R-037 | 保留主分类、附加分类 OR/去重、分类及条目权重/同权重稳定排序单元通过 | 原生显示能力有缺口，第二阶段门户使用同一规则 |
| APPCAT-R-008、APPCAT-R-009 | 非法展示覆盖拒绝；外部新增/更新/删除配置渲染通过 | 原生数据库更新/删除 E2E |
| APPCAT-R-010、APPCAT-R-011、APPCAT-R-012 | 七档实际尺寸/摘要、确定生成、占位、越界/符号链接/非法尺寸反例通过 | 七档 HTTP 加载、缓存及容器边界 E2E |
| APPCAT-R-013 | 本地固定私有只读挂载、消费作用域和最终路径检查通过；SSH/TCP endpoint 明确拒绝 | 容器内读取验收；远程制品投影尚未实现 |
| APPCAT-R-014、APPCAT-R-018 | 私有完整目录仅挂消费者；原生 Casdoor 仍可能枚举隐藏/禁用 IAM 条目且管理员绕过 | 第二阶段服务端过滤及元数据不泄漏 E2E |
| APPCAT-R-015 | 单改图标改变输入/消费者摘要，旧不可变制品不变，单元通过 | 实际部署更新和回滚 E2E |
| APPCAT-R-016、APPCAT-R-021 | 配置空/未知组拒绝，Provider 缺口进入 plan；原生 Casdoor 失效策略仍默认允许 | 第二阶段严格拒绝；当前不标完整支持 |
| APPCAT-R-017、APPCAT-R-019、APPCAT-R-020 | 实际 IAM/网关/native 事实来源及管理角色校验单元通过；双 Adminer 与外部管理入口同源组渲染通过 | 原生登录、组 OR 与撤组验证 |
| APPCAT-R-022 | 仅目录引用组也加入同步配置，无跨用户缓存组件 | 实际撤组/用户隔离 E2E，第二阶段门户缓存策略 |
| APPCAT-R-023、APPCAT-R-024 | 两套 Adminer 已有 ForwardAuth/数据库认证路径保留，启用后实际绑定与组检查通过 | 管理员/普通用户直接访问及数据库第二层 E2E |
| APPCAT-R-025、APPCAT-R-028 | 24 个 Module 全部声明入口或原因；运行代码/YAML/Shell 的 APPS_LIST 为零，CI 通过 | 历史调研保留旧协议名称，不提供兼容路径 |
| APPCAT-R-026、APPCAT-R-029 | Casdoor 优先并固定原生版本，权限限制明确；尚无完整支持声明 | 第二阶段完整字段/过滤与固定镜像/API/浏览器证据 |
| APPCAT-R-027 | Authentik deprecated，原生适配后冻结门户扩展，推荐 Casdoor；移除需验收/显式迁移/确认版本 | 没有自动切换既有绑定，也未确定移除日期 |
| APPCAT-R-030 | 纯目录投射输入不含 clientId/clientSecret，私有制品无 Secret，渲染检查通过 | HTTP/API 及容器运行复核；原生初始化的客户端字段另见 §3.4 |
| APPCAT-R-031、APPCAT-R-032、APPCAT-R-033、APPCAT-R-034 | 双语完整、单语字段回退、未知/空译文拒绝、语言优先级、逐语言覆盖及身份/URL/图标/权限事实不变单元通过 | 原生显示固定部署语言；即时切换在第二阶段 |
| APPCAT-R-035、APPCAT-R-036、APPCAT-R-038 | 两级展示/空组隐藏纯函数与 LLNG 原生菜单单元和渲染通过；Casdoor/Authentik 分组有缺口 | 第二阶段门户即时切换、全空提示及浏览器 E2E |

### 3.3 阻塞与后续动作

2026-10-08 本机 Docker daemon 无法连接；当日真实容器、HTTP、浏览器与撤组均未执行。
2026-10-09 用户指定 `ssh whl@finance.hlong.wang`，已执行下述隔离运行检查。
原生显示和严格过滤缺口继续保留为第二阶段要求，不据此放宽最终验收。

### 3.4 远端运行记录（2026-10-09，部分通过，环境阻塞）

测试主机为 `finance.hlong.wang`（Ubuntu 26.04、Linux amd64、Docker 29.7.2）。本轮独立根目录
`/home/whl/anas-catalog-test-20261009-5bcb4bf3`，容器前缀 `anas_cat5bcb4bf3_`，
网络 namespace `anas-catalog-test-5bcb4bf3`、入口 `10.253.43.2:29449`，专用 Docker socket
`/run/anas-catalog-test-5bcb4bf3.sock`。使用独立 Docker/containerd 数据目录，不向生产 daemon
部署测试应用，不使用生产账号或业务数据。现有镜像只读导出到本轮 daemon。

源码为 HEAD `aa1944a035c7f96f3afda4cfd1b904852e191b6f` 加测试开始时的工作树；
2131 个文件在远端逐项校验通过。清单 `source-baseline.json` SHA-256 为
`ca5cd61c9820b9ec79ba6f16330814bbc6609a69e13578aa15f5efb7f138e5ef`。
使用远端 Go 1.26.6、CGO_ENABLED=0/Linux amd64 编译 CLI、Hook、Casdoor helper 与网关 helper。
`bin/anas` SHA-256 为 `dd4bb412c217953b0ffc187cd9c3165faa00c56099ee2b7df9f505f53982dc71`；
Casdoor helper 为 `5089d109adfd9a0b78783c0eb849f2597ac59cf263e880370154192e9e86dfe1`；
网关 helper 为 `eb3579d15fab7dbdbe90aff41b3cb37fa3f6b0f6d0f59efe8f04da91de7e7d23`。
编译首次因 proxy.golang.org TLS 失败，改用 goproxy.cn 后通过，两个日志均保留。

测试镜像把当前运行脚本与新 helper 覆盖到既有固定版本镜像；没有重建 Casdoor 上游 server
或三种 IAM 的前端，不能替代正式 Dockerfile 全量构建或发布镜像验收。
Casdoor 基础镜像 `3.143.0-r11` 的 image ID 为
`sha256:91c8c9a3b023e4279e02e2adc72ff730bbd156021b4417185ecad5c65b8908ad`。
七档静态图标与私有目录实际读取当前 deployment 的只读挂载。

实际运行的测试镜像 config/image ID（来自最终构建日志，均在本轮专用 registry 下）：

| 镜像 | image ID | 执行范围 |
| --- | --- | --- |
| Casdoor 3.143.0-r11 | `9783884909bbe2bb9d95eb09560a97b02928ccd5c4754a1ee1cdba7b7b52ad17` | 最小部署、API、图标 HTTP |
| Samba DC 4.23.6-r13 | `b65fa5d3de058386e49103f97cafa1c56ee58f876bb403e16f3397711c6bfe1e` | 独立测试目录 |
| Samba anchor 4.23.6-r13 | `9e599fedcf43ceeeaa86f07703441d31f2259232c2d110f74e20cdbe97556f5c` | 测试账号锚点与事件 |
| Traefik 3.7.10-r7 | `ddbf44be2f6de9059d0c94a6c17c69f6918b2548818f771e08a8dcf3c00010ed` | HTTPS 与网关路由 |
| OAuth2 Proxy 7.15.3-r6 | `f832510bec47cc19751a84bd9a6a6060973fcd33c80e6726b532064970ea75b4` | 启动通过；网关 E2E 未完成 |
| Nextcloud 34.0.2-r12 | `a03e70679f4523fbb496603d3009aeb883db33af6d992b9d401241695ca1117f` | 完整部署健康检查失败 |
| LLNG 2.23.2-r13 | `10248796130e000f47e027f4937d7587e5e8d4a571bb8204ca06a654492fc12c` | 仅测试镜像准备，未运行 |
| Authentik 2026.5.6-r15 | `5f2449c8e31d109aa02e99d58a0044b5511ced8447521423666999da251c8f19` | 仅测试镜像准备，未运行 |

完整 fixture 包含 Samba、Casdoor、Nextcloud、两套 Adminer 与外部条目。
首次 Samba 冷启动健康检查超时；重试时服务曾通过健康检查。
`20261008T184923Z-8448c5ba` 最终因 Nextcloud 未达到健康状态而失败，CLI 已执行 candidate_stop
恢复，不能记作整套部署通过。镜像补齐期间的中断和恢复记录一并保留。
之后移除 Nextcloud，使用最小 fixture 独立验证目录与 Adminer；没有缩小 38 项最终验收范围。
最小部署 `20261008T192818Z-73c74375` 的真实 CLI apply 通过，初始服务健康检查通过。
目录含六个启用条目：Casdoor、Traefik、两套 Adminer 管理入口，以及外部管理/用户入口。
该部署 catalog.json SHA-256 为
`d20d1e47a230129d01f04bdc1256bbb292c624ce16207929fccc69e1e2ebeaaa`，assets.json 为
`8232a4ab033a557d71e8047102a013fd292a0bcb70eab9df705643c3bf896fff`。

| 需求 ID | 实际检查与结论 | 尚未通过的部分 |
| --- | --- | --- |
| APPCAT-R-001、APPCAT-R-009 | 外部条目经源 YAML → config import → apply 写入真实 Casdoor；URL、显示名、图标路径、权重与目录一致 | 点击、运行时更新/删除；原生协调仍需增删 E2E |
| APPCAT-R-010—APPCAT-R-013 | 最小部署七个去重 PNG 的 HTTPS 响应均为 200；检查七档实际像素、PNG 格式、SHA-256、响应类型及只读挂载的真实 Source；完整 fixture 曾验证 14 个图标文件，但该部署最终失败 | LLNG/Authentik 实际 HTTP 路径、远程 Docker 投影；不将完整 fixture 记为通过 |
| APPCAT-R-014 | 私有 catalog.json/assets.json 和路径越界请求均未返回目录 schema；原生列表 clientSecret 为掩码且不等于实际密钥 | 原生列表仍返回目录登录对象和已停用 Nextcloud 的旧 IAM 对象；严格元数据过滤属第二阶段 |
| APPCAT-R-017、APPCAT-R-020 | 真实 API 观察直接/嵌套 APP_all 成员只获得用户条目，Admins 获得六个条目，无组用户没有本轮条目；用户均非 Casdoor IsAdmin，管理员分支来自明确组 OR | 严格管理员不绕过、策略删除/停用、接口失败等反例仍待验证/第二阶段 |
| APPCAT-R-018 | 禁用目录账号的真实密码登录被拒绝 | hidden/禁用条目与原生 IsAdmin 绕过无法算完整通过 |
| APPCAT-R-022 | 已启动旧 Cookie 撤组及交替用户查询探针 | 主机 I/O 阻塞期间未完成，不能判通过 |
| APPCAT-R-023、APPCAT-R-024 | 最小部署两套 Adminer 与 ForwardAuth 网关已启动 | 网关 HTTP 探针超时；浏览器及数据库第二层断言未完成，不判通过 |
| APPCAT-R-030 | 六个纯目录对象 disableSignin=true，grantTypes/redirectUris 为空；使用实际自动生成的客户端字段测试客户端凭据、密码、授权码登录，共 18 个 HTTP 反例均拒绝签发；原生列表密钥掩码检查通过 | SAML 协议反例及其他 Provider 运行检查未完成 |
| APPCAT-R-015、APPCAT-R-029、APPCAT-R-035—APPCAT-R-038 | 保留既有单元/渲染证据与第二阶段范围 | 单改图标实际更新/回滚、完整 API/浏览器、即时双语和两级分组尚未通过 |

原先“纯目录对象客户端字段为空”的测试假设不成立：Casdoor 原生初始化自动补齐字段。
第一次数据库探针因此失败，修正为检查无授权类型/回调、禁用登录及真实签发拒绝后通过。
静态图标首次请求在 Traefik 路由尚未就绪时返回 404；容器内文件为 200，路由就绪后 HTTP 重试通过。
浏览器首次是测试容器 DNS 配置错误；修正并接入本轮 Traefik 网络后，页面标题为 Casdoor，
但正文为空且没有应用 API 响应。随后截图/定位等待超时；前端资源诊断亦被 I/O 阻塞，
目前不能确定页面空白根因，不将其归为已证实源码缺陷或已通过浏览器检查。

MariaDB fixture 中 env 提供的 root 密码未能通过 root@localhost 密码认证；容器内本地无密码
root 连接可用。只通过该测试容器的本地连接创建限定 `catalog_probe` 数据库的受限账号用于
Adminer 测试，不给 Adminer 注入超级用户凭据。该异常根因未验证，不能记作 MariaDB 模块完整验收。

20:08 UTC 主机 load average 达到约 120；kswapd、btrfs-transaction、日志服务及多个 LDAP、
Web、数据库进程处于等待 I/O 的 D 状态。浏览器和网关检查连续超时，停止新增测试负载。
这一记录只证明当时环境资源阻塞，不能判断压力来源或把失败自动豁免。
LLNG/Authentik 测试镜像已准备，尚未执行原生容器/API/浏览器验收。

原始脚本、运行日志和 JSON 在上述远端根目录的 `evidence/` 与 `browser-output/`，
包含 `source-check.json`、`binaries.sha256`、`casdoor-minimal-apply-valid.log`、
`runtime-probe.json`、`native-api.json`、`native-api.log`、`native-api-secret-mask.log`、
`casdoor-apply-complete-images.log`、`native-browser-*.log`、`gateway-api.log`、`withdrawal.log`。
fixture 密码不进入版本库，清理时已删除私有文件与测试数据。证据尚无共享持久归档；
本节保留必要基线与结论，最终验收继续为未完成，不归档计划。
`runtime-probe.json` 的 SHA-256 为 `ebbe2b64085698a0388f6269c978cd16a71d4f61a7ecc64771a863d789b4589b`；
`native-api.json` 为 `d854849ecc7a32d7e86549a63b80b2a832fa60a373a53e6a515e5f30c3a24f05`。
两份脱敏 JSON 已复制到本机 `/tmp/anas-catalog-20261009-5bcb4bf3/remote-evidence.json`，
合并文件 SHA-256 为 `1d0a78b5f9561423f3df30f0cd0d60b582c9fad6fecadb06a4f8447d84f92a2a`；
根据该实际 HTTP 输出复核五个可登录用户的预期目录与掩码，以及禁用账号和 18 项拒绝结果，均通过。
最小目录及七档 PNG 另保存到远端 `evidence/minimal-application-catalog/`，与数据库密码分离。

清理已完成：停止本轮脚本、11 个服务容器和浏览器、专用 Docker/containerd 单元；
移除本轮 namespace、socket、镜像/构建目录、workspace、缓存及私有凭据。
首次使用 rm --one-file-system 跳过了 workspace 的独立 Btrfs data/userdata 子卷；
确认二者属于本轮、没有遗留挂载且创建时间匹配后，使用 btrfs subvolume delete 移除，
最终六个数据/缓存目录均不存在，证据与最小目录/PNG 保留。
清理结果在 `evidence/cleanup-result.json`、`cleanup-owned-data-final.log`。
默认 daemon 测试前后均有 26 个容器，名称、ID、镜像和端口一致，无新增名称，
结果在 `evidence/production-comparison.json`；这不表示测试期间没有共享资源压力。
主机当前 load average 回落到约 2，磁盘可用约 12 GiB。
最初 sudo -E 未保留隔离参数，曾启动一个没有测试应用的默认测试 daemon；
当时已停止该 daemon 和 namespace，后续全部启动使用显式命名参数。
归属不明确的既有默认测试数据目录未删除，其他两个原有测试 namespace 保留。
下一轮在主机资源恢复后补双 Adminer、旧 Cookie 撤组、外部增删/图片回滚和其他 IAM 原生运行检查。

### 3.5 继续验收与修复（2026-10-09—10）

本轮使用独立目录 `/home/whl/anas-catalog-test-20261009-9a14d222`、专用 Docker/socket、
containerd 和 network namespace `anas-catalog-test-9a14d222`，未在默认 daemon 部署测试应用。
源码 HEAD 为 `c397e92e0d17af294877a766770df68198e7b2cb` 加逐文件冻结的工作树；
最后端口修复基线 `source-baseline-port.json` 含 2200 文件，SHA-256 为
`5f74290a1e0803869f9759337fbdfee81b99abf5b19e235b7b052f565d1d3d13`，远端逐文件复核无差异。
该清单包含当时工作树的无关改动，没有重置或提交这些改动；后续文档执行记录不属于运行基线。

本轮已修复实际访问链路中的三个问题：Casdoor JWT-Custom 缺少严格网关要求的 `auth_time`；
Adminer 5.5 拒绝带下划线的数据库 hostname；oauth2-proxy 回跳白名单缺少部署 HTTPS 端口。
Casdoor r12 从成功认证的服务端 session 记录认证时间并绑定原生用户 UUID，快速登录及 refresh
保留原认证时间，不用新 token 的 iat 冒充认证时间。原生两项安全测试及 Linux 构建通过，
实际 OIDC 密码、快速 SSO 和刷新断言通过。Adminer 使用私网别名 postgres/mariadb，
不注入数据库凭据。网关 r7 只信任 bridge loopback 和精确 Traefik peer，外层 bridge 拒绝其他
TCP 调用者，并保留内部 Host 与原 X-Forwarded-Host 的区别；白名单绑定实际部署端口，
不使用任意端口通配符。对应边界单元及 Linux 测试通过。

运行的 Casdoor r12 镜像 ID 为 `d34bbe296524378ec8e16b5bb1c8f32c4e4b1e9bec4e583353f2eac2986034f4`，
patched server SHA-256 为 `2d3ae8a19df159a54a4d55a63a9612012a5d61760ffa7f2c6abac83acd33577a`。
OAuth2 Proxy r7 镜像 ID 为 `2d3f4c95db8443d00843a760016e9d79caf523f6afdfd3598c2395c3d45d6a93`，
运行中 bootstrap 与上传文件均为 `fb4d0486e08273f7a8b71b10eba1224cc50db8bf34020f266259318cc319ec01`。
MariaDB 12.3.2 镜像 ID 为 `ee916be72d31ffed446672e02ca82aa473a268983c3cee3f64aa09e3bdcc8149`。
这些是固定基础镜像加本轮源码的测试 overlay，未执行正式双架构发布构建。

Casdoor r12 原生门户的普通应用组、无组和管理员三类会话经过浏览器/API 复核通过，卡片与原生权限矩阵一致且页面无脚本错误。普通应用组首轮等候列表响应超时；保留失败 JSON，按实际应用响应延长等待后重试通过，不豁免权限断言。

Immich 补齐 user/media 应用声明并移除最后一处旧目录发布；当前 inventory 为 25 个 Module，
运行代码的 APPS_LIST 扫描为零。Authentik 协调器补排除 hidden/禁用条目，避免旧的自管对象
残留；新增 ORM 生命周期反例及共 37 个 Python 检查通过，但尚未作为实际 Authentik 容器验收。

最初 whl 所有的 workspace 无法执行 PostgreSQL Btrfs snapshot 的 root 操作，目录权限也曾导致
apply 后清理失败。递归修改旧 workspace 所有权被自动审批拒绝，未执行；采用新建 root 所有
的 `workspaces/casdoor-root`，保留旧数据及归属，停止本轮旧容器后重新初始化。
新 workspace 的两套 Adminer 部署 `20261009T155313Z-9cbe87f0` apply 与清理通过，
端口修复部署 `20261009T160735Z-0455d57a` 也通过。当前 MariaDB root 密码认证与受限账号创建
通过；没有使用无密码 root 的替代路径，不将历史异常根因标为已解释。

| 需求 ID | 本轮实际结果 | 尚未完成的部分 |
| --- | --- | --- |
| APPCAT-R-010—R-013 | 8 条声明、7 个去重 PNG 的真实 HTTPS、像素/格式/摘要与私有只读挂载通过 | 其他 Provider 实际路径、远程 Docker 投影 |
| APPCAT-R-017、R-020 | 直接组、APP_all、嵌套组、无组、管理员组和禁用账号的原生 API 矩阵及密钥掩码通过；六账号 IsAdmin 均 false | 第二阶段严格过滤、原生管理员绕过/策略失效缺口不豁免 |
| APPCAT-R-022 | 已观测撤组后旧 Cookie 首次与四次交替读取均移除条目，恢复组后 fixture 继续使用 | 其他 Provider、自己的门户缓存判据 |
| APPCAT-R-023、R-024 | 真实 HTTP：普通/无组账号拒绝；管理员自动回 PostgreSQL Adminer，两套 Adminer 空密码、错误密码拒绝及受限账号登录通过 | 其他 Provider 与全部最终判据继续待验收 |
| APPCAT-R-030 | 六个纯目录对象的 18 项 OAuth 与 12 项 SAML 密码/已有 session 请求均拒绝签发 | 其他 Provider 原生容器 |
| APPCAT-R-009、R-015 | 外部中文显示名/权重更新、仅修改图片、删除经真实 CLI apply 及原生数据库检查通过；删除只移除自管 Application/Permission，原 IAM 对象保留 | 其他 Provider 运行生命周期继续待验收 |
| APPCAT-R-026、R-035—R-038 | 原生 Casdoor 功能与限制沿用既定两阶段范围 | 全字段、即时双语、严格过滤及完整两级展示在第二阶段 |

外部更新 deployment `20261009T162225Z-f78f4505`，catalog SHA-256 为
`161be914e94672a93c5e8215f8c2b77607698fe2356fa676c342bd2a5bd623b1`，assets 为
`078e84fd5cd7abf26aa3854e258d6792baa3d0a554e8503cb4b7feaf73b48b41`。
仅修改 PNG 的 deployment `20261009T162421Z-95d1a7e6`，catalog 为
`13c28eddf24c353b4e2c5e85a0396b212b04f30c19a1087af2884e4d1b259916`，assets 为
`c1272eb3c2879d24d927d23fd5551311765f70032c96b168437a6ac576eea181`。
两个输入配置摘要相同，Casdoor 的新图标实际 HTTP 通过，旧 catalog 与全部 PNG 摘要保持不变。
删除 deployment `20261009T162602Z-2e25bd9c`，catalog 为
`21df438efa68a305b9d4d324bfb34652d15ca3625674674d72f01a19b62a751c`，assets 为
`a31b8ed3abfefd228173995ddeea8f27bb7b98f80e2364157bcaee1c7a481928`。
数据库确认被删除外部项的 Application/Permission 均为零，原 directory/oauth2-proxy 对象仍在。
首次配置导入因后续文档同步改变 bundle digest 而被锁拒绝；正常更新锁后导入、apply 通过，
没有直接改写受管 config 或绕过完整性检查。回滚到 `20261009T162225Z-f78f4505` 通过，目录/资产摘要与该历史部署完全一致，实际 14 个 PNG HTTP 与原生显示名/权重通过。图标源文件仍为红色，恢复读取的是旧部署自有蓝色资产。

LLNG 真实密码登录通过后，原生菜单曾缺少全部目录卡片。固定镜像的公开 Menu.pm 支持递归，
但默认 bootstrap/menu.tpl 只渲染顶层 APPSLIST_LOOP；嵌套 category 不会显示卡片。
Hook 改成“用户应用 / 主分类”“后台管理 / 主分类”的单层原生 category，保留精确组 OR、
URL 与稳定排序；完整两级布局继续属于第二阶段，不把原生组合标签计作 R-036 通过。
定向 Hook 测试通过。复测最初仍使用 source bundle 中的旧预编译 Hook，菜单输入仍为嵌套结构；
现已从冻结源码重建 Linux Hook，SHA-256 为
`525c9d4a6ca7cd5cdd8868dd507f5c9687babc55a05b1f5d40d3cc5a77c8d77f`，运行复核仍在进行。

一次 LLNG 更新后，代理根入口为 404、直连为 200，访问日志显示根请求没有路由名。
只重启本轮专用 Traefik 后入口恢复 200；重启后立即探测曾因冷启动返回 000。
容器事件/路由刷新根因尚未确认，保留日志，不将恢复动作当成部署稳定性通过。

最终 revision 门禁按 HEAD 计算为 LLNG r13、Authentik r16、OAuth2 Proxy r6、Immich r2；
对应 manifest/localization/compose 与生成页面已校正。前述 r7 网关和临时 LLNG r14 是本轮
测试标签，测试镜像运行字节没有因标签校正而改变。旧 workspace 的版本锁拒绝 r14/r7
回退到 r13/r6，未改写锁或绕过保护；改用新的 root workspace `workspaces/native-final`，
旧数据和失败记录保留。最终源码增量基线 `source-baseline-native-final.json` 为原 2200 文件
加 31 文件覆盖，SHA-256 为
`422314d395795716ee93e7b352b898a52022b75bc7b32870613d6f523a8b1a27`；逐文件校验通过。
新 workspace 首次 apply 因 Samba 冷启动未健康而执行 candidate_stop；正常重试通过，
deployment 为 `20261009T222813Z-192b9e62`。Samba 数据挂载在该 workspace 自有目录，
没有使用旧 workspace 的目录账号或数据，也没有跳过健康检查。重新生成本轮六账号 fixture，
两套数据库分别创建限定 `catalog_probe` 数据库的受限账号，私有凭据不进入 Git。

LLNG 管理员原生 HTML 显示六个目录入口，分类组合标签与 query/fragment 保持通过；
普通账号登录后曾丢失会话，后来返回原生 `PE_APACHESESSIONERROR`（8）。固定错误计数发现
持久会话表重复键，Cookie 域/HTTPS 标志正确，容器未重启且未 OOM，不能把故障归因于权限规则。
仅重启本轮 LLNG 后，管理员和 APP_all 的矩阵通过，嵌套组读取又遇到网关 504；
完整六账号/API/浏览器尚未通过，恢复动作没有解释存储错误根因，也不计作部署稳定性通过。
诊断分别保存在 `evidence/llng-session-diagnostic.json`、`llng-catalog-before-service-restart.json`
和 `llng-native-restart-ready-probe.log`。浏览器第一次因测试目录镜像副本文件归属不可读而失败；
仅修复新建 catalog.json 测试副本的归属后重试，又在登录等待超时，仍不判通过。

实机还发现上游默认 `1sample`、`2administration`、`3documentation` 分组残留，包含未部署示例
及缺少测试端口的旧地址；启动配置现移除这三个分组，保留 ANAS 管理/文档入口的管理员组规则。
该单文件修复与原生菜单适配分别记录，不改变权限范围。修复后源码清单
`source-baseline-native-cleanup.json` 仍含 2200 文件，SHA-256 为
`272b820148b7c87320966171e93c5a9be71c04de30391a1420fe4bb67edd6f10`，远端逐项复核通过；
llng-config.sh SHA-256 为 `68d64a4134edb47ae26c77b7849c3cee707c75efb4c6d1663ddce93c8c497c73`。
修复后 LLNG r13 测试 image ID 为
`ab52bb50b4b8631d24eae875bd41f11067cc5a0ffff6bbc254e50bd827cb69dd`，正常 lock/apply 通过，
deployment 为 `20261009T230215Z-43edb74f`，catalog SHA-256 仍为
`60f4a8ee0bb277867853da422a61e7808ceb8cc51314dd57451a3f4c8efda2f8`。
构建最初基础镜像解析失败，legacy builder 又拒绝 COPY --chmod；校验本地固定基底后使用
普通 COPY 并保留 0755 文件模式完成构建，失败日志均保留。apply 返回后的首轮探针过早，
容器仍 starting，所有入口和图标为 404；该失败另存 `llng-catalog-starter-cleanup-cold.json`。
随后确认 configured 标志、healthy、无重启/OOM，再执行六账号矩阵和七档 PNG 的真实 HTTP、
尺寸、SHA-256，全部通过；直接/APP_all/嵌套组仅用户入口，无组零目录项，管理员六项，
禁用账号密码认证拒绝。私有挂载只读、原生组合标签与 query/fragment 也通过，结果在
`evidence/llng-catalog.json`。历史会话存储错误仍未解释，不据这次通过删除历史失败或声明根因已修复。
原生注册表另在 `evidence/llng-native-protocol-registries.json` 复核：6 个纯目录条目没有
OIDC/SAML 注册，既有 oauth2_proxy OIDC client 保留，默认三组示例均已移除；该检查不是
OAuth/SAML HTTP 签发反例。浏览器在 password-all 阶段超时，失败时页面没有 input，尚未
完成密码提交或卡片点击；无脚本错误不能代替浏览器矩阵通过，HTTP 导航诊断继续执行。
后续浏览器表单复测在 username-all 阶段超时，DOM 仍有 user/password 输入但未通过可见性等待，
专用容器在失败后未正常退出，已停止。专用 Docker ping 正常但一次容器列表读取超时，
根因仍未确认。主机缺少 Node 和部分 Chromium 动态库，原生进程候选未启动，也未安装组件。
目录浏览器改为在同一浏览器上下文通过原生 HTTP 密码登录、共享服务端真实会话，再验证卡片
和网关跳转，不伪造 Cookie 或授权事实；原生登录表单 UI 失败继续保留。探针增加阶段快照、
整轮超时和只清理自身容器的退出处理。后续响应元数据确认静态 CSS/JS 为 200，
但 `/index.psgi/portal.css` 与 `/index.psgi/psgi.js` 返回 504；仅调整选择器不能解释这一失败。
替代探针首轮没有产生新阶段快照，包装器退出后读到旧 username-all 报告，不能把旧结果归到
新探针，也不能计作实际执行。后续包装器在启动时先写入独立 running 报告，避免混用旧结果。
仅重启本轮专用 Docker 单元后其状态 active；正常 apply 返回 0，但实查 Samba DC 与
Traefik 均 Exited (128)、入口不监听，不能据 apply 返回值宣称环境恢复。
标准 start 返回 1，固定错误统计定位 Traefik 的 `task already exists`；标准 stop 成功移除
本轮容器，再 start 返回 0，Traefik、数据库和 Samba 实查健康，LLNG 仍在启动检查。
Samba 的三个就绪标志及 named 进程均通过，未绕过健康门禁；浏览器补查仍待完成。
记录位于 `llng-own-docker-restart-apply.log`、
`llng-own-docker-recovery-start.log` 与独立停止日志，原始内容不输出到会话。
恢复后的冷探针仍为 404；随后配置标志出现，但健康一度 unhealthy，直连应用内部的根页面
与动态 CSS 在 12 秒内均未收到响应，动态 JS 后续为 200（6.68 秒），不能据此声明 504 根因修复。
固定镜像的 FastCGI 公开源码与进程等待状态已读取，主机 vmstat 同时显示换入换出。
结果在 `llng-resources-after-standard-recovery.log`、`llng-resources-configured-after-recovery.log`。
之后真实健康前置检查通过，新浏览器实际启动，但仍停在 login-all 页面初始化、未完成任何
卡片断言；主机 load average 升到 61.39 后又到 67.89。停止仅本轮浏览器并按标准流程停止
本轮 workspace 释放资源，其他环境未操作；该次不计作浏览器通过，原记录与停止结果继续保留。

后续 load average 达到 82.33，浏览器包装器与标准 workspace stop 分别以 124 超时退出，
不能将其记作正常停止。仅本轮 Docker/containerd 单元停止后均 inactive，本轮浏览器 scope
已不存在，其他两个测试 namespace 保留，load average 后续降到 9.21；该观察不足以确定
504 或运行时异常的根因。通过本轮 run-daemon.sh 重新启动，标准 start 因 Samba 冷启动
健康检查返回 1；随后 UDP/TCP DNS、目录结构标志和实际容器健康通过，没有改健康门禁或手工
补标志。oauth2_proxy 为 dead，执行标准 restart oauth2_proxy 返回 0。

浏览器改在本机 Chrome 154.0.8037.99 的新临时 profile 运行，经只监听 loopback 的 SSH
隧道连接本轮隔离入口，未安装主机浏览器组件。只取回本轮三个虚构用户的测试凭据和公开
目录预期，限制请求到 catalog.test。LLNG 原生密码 HTTP 登录建立真实服务端会话后，
APP_all/无组/管理员的卡片矩阵分别为一项/零项/六项，全部通过；用户实际点击外部入口且
query/fragment 原样保留，无脚本错误。管理员实际点击 PostgreSQL Adminer 后停在
auth-gate 的 callback 500，整轮仍失败。证据位于本机
`/tmp/anas-catalog-20261009-9a14d222/local-browser/output/browser-native-llng.json`
和独立 `browser-native-llng-admin-debug.json`；截图仅为本轮虚构目录，保留本机私有目录。
有限错误分类确认上游拒绝缺少 email 的会话，而三个目录用户均有 AD mail。修复 LLNG
Adapter 将通用 email 来源映射到 AD mail，并在网关通用注册中显式请求 groups；其他来源
和输出 claim 名不变。两个 Hook 完整单元测试通过，修复后的运行回跳尚待复测。
仅四个 Go 文件传输前的 8 文件源码/文档包被自动审批拒绝，未执行；核实公开 GitHub origin、
完整新差异不含敏感值并缩小范围后，四个 Go 文件和 SHA 清单的传输通过审批。远端 2200 文件
清单全部匹配，`source-baseline-native-email-code.json` 摘要为
`5f8ea3005515055c6495311c85d725f66f72ac1fe761c2a8ac9d28cce410fb13`。
重新编译的 LLNG Hook 摘要为 `3b653293845c9e3b94d74edf84b11f526a064a175c5d1fe790f719555b723539`，
oauth2_proxy Hook 为 `eef7cd59c9eb0e26c3b9c9f05915f4b862f4c411690f421ea1c5989f0571473e`。
首次 lock 用 whl 访问 root workspace 而 permission denied；以 root 执行后 lock 通过，apply
仍失败，公开源码模板匹配为 `no usable IPv4 address detected`，原因是测试命令遗漏专属
network namespace。改回本轮 namespace 内执行标准 apply，不改应用逻辑、目录权限或网络。
该次 apply 返回 0；实读公开 OIDC 映射确认 email 来源为 mail、groups 为
`groups;string;always`，`oidcServiceIgnoreScopeForClaims=1` 为既有配置，不需增加 scope
映射或改变全局发布规则。Samba/Traefik healthy，LLNG/网关仍 starting 时不计运行验收通过。
首轮浏览器在 starting 期间读到 404，输入等待超时，独立保留为
`browser-native-llng-admin-fixed.json`。随后实查两者 healthy、登录页 200，再复测卡片通过，
callback 仍 500；该次上游在查 preferred_username 时的 userinfo 补查返回 401，先前缺邮箱
错误计数已为零。独立 OIDC 实验用真实密码、授权码与 token，确认 email 非空、groups 为
包含实际管理员组的数组，nonce/auth_time/sub/exp 存在；preferred_username/email_verified
缺少。两次直接 userinfo 均 200，其中第二次兑换与 userinfo 均无 Cookie；Cookie 依赖假设
被否定，网关这次 401 的根因尚未确认。结果为 `llng-oidc-claims.json` 和
`llng-oidc-claims-no-cookie.json`，不输出实际 token/主体/邮箱值。
网关请求补 preferred_username:sAMAccountName，并固定使用签名 ID token 的声明，关闭
可选 userinfo 补查。三个受管 Adapter 均发布所需 ID token 声明；签名、issuer、nonce、
非空邮箱与明确未验证邮箱的拒绝、bridge 的组/auth_time 校验保持启用，未增加虚构的
email_verified 值或放宽管理组。Hook 单元通过，新的 source-baseline-native-claims.json
摘要为 `a83223309de7947f1b12270b6065e6bab922e150aba9ffbc4037db973460a9af`。
新网关 Hook 为 `81adb60476c7e5649cd598600845644b85fd04154abb33badb3e0b751883b9ba`；
2200 文件一致，标准 lock/apply 及 LLNG/网关实际 healthy 均通过。管理员回跳到 Adminer、
空数据库密码通过，但最初整轮有 TypeError/Failed to fetch；随后复测真正原生登录表单
同样通过回跳，并定位唯一错误为测试主动阻断 `https://www.adminer.org/version/` 的公开 GET。
仅放行该官方版本查询后，Chrome 154.0.8037.99 通过真正原生表单的 APP_all/无组/管理员
三类卡片矩阵、外部用户卡片实际点击与 query/fragment、管理员卡片到 PostgreSQL Adminer
及空密码检查，脚本错误为零，整轮通过。
最终记录在本机 `local-browser/output/browser-native-llng-native-final.json`；先前的 cold、
admin-ready、admin-token、admin-form、admin-network 失败均独立保留，不覆盖。有限截图复核
确认图标/组合分组标题可见；原生仍为一层组合标签，并不计作第二阶段两级门户通过。
网关 bootstrap 单元首轮因沙箱拒绝 loopback bind 失败，允许本地临时监听后整模块通过，
日志为本机 `oauth2-bootstrap-claims-continue.log`。

Authentik 固定上游 2026.5.6 digest 为
`ed120caf710ccf82ef0026f0bc74e51615bc95ebff228a7a2d6fc60c441c3868`，本轮当前 Dockerfile 的
隔离构建 image ID 为 `4a52037ad8c4886228a6cd1d37233f1338852d1a1d1aedb74beee1616bf139c9`；
尚未完成原生会话运行验收。未输出配置/日志中的凭据。
Authentik Hook 重新编译摘要为 `30d41cfa4b2f24cec22aa5379d6be3bd4e131999064eba36ba116255eb5bffdd`。
既有 fake fixture 标准 config import/lock 通过，首轮 apply 在冷数据库迁移中超时 124，
SSH 同时被远端关闭；事后确认没有残留 CLI，server 在约 13 分钟后实际 healthy，
worker/dirwatch 仍 Created，不能将其记作部署成功。主机 load average 快照为
16.48/10.98/9.48；未据此单独推定迁移慢的根因。继续用标准 apply 重试冻结候选。
目录检查脚本中的误写 SourceUserConnection 已按固定源码改为 UserSourceConnection，
只改检查脚本，不改产品实现。
2026-10-10 用户明确“Authentik 不用测试”：停止后续 Authentik 运行验收，保留适配实现、
单元与构建证据，标记为按用户要求未验收，不计通过。第二次标准 apply 同样超时 124；
server/worker 后续均 running/unhealthy，无重启/OOM，server 五次健康检查均超时、worker
五次退出 1；固定错误分类未观察到导入/权限/数据库异常。直连 readiness 在 16.81 秒后
超时。该失败保留，不继续做 LDAP、原生 API/浏览器或生命周期实验。开始停止本轮专属
Authentik 容器以释放资源，其他环境不操作。首次清理误用 auth_* 名称，实际容器名为
authentik/authentik_worker/authentik_dirwatch，未改变应用行为。按实际名称停止的命令又以
124 超时退出；随后使用本轮 run-daemon.sh stop 返回 0，本轮 Docker/containerd 两个专属
单元实查 inactive、专属 namespace 已移除。此处仅为取消测试后的资源清理，不是 Authentik 验收。
再次输出完整远端 gateway-api.py/native-matrix.py 被自动审批拒绝，未执行；后续只提取
函数名和路径，不输出源码、环境值或凭据。读取环境文件路径时曾误写 test-env/environment.sh，
该路径不存在；实际为本轮根目录 environment.sh，修正后未输出其内容。

本地当前 HEAD 为 `efb3fa7c7cb6cd918c6ed15b248731c849ce824e`，与远端冻结的
`c397e92e` 加逐文件清单基线分开记录；没有把其他任务新增提交等同于远端运行通过。
相对当前 HEAD 的 Module revision 检查通过，Incus `7.3.0-r4`、Traefik `3.7.10-r8`
保留本轮目录声明变更的独立 revision；远端旧 Traefik 镜像的通过记录仍沿用其原基线。

停止后按本轮标准包装器恢复 daemon，删除四个专属 Authentik 容器返回 0，未继续其运行测试。
以同一 native-final workspace 切回 Casdoor/双数据库，标准 config import、lock、apply
均返回 0。最终 deployment 为 `20261010T091347Z-e3fe71fc`，六项目录 SHA-256 为
`14e88132213453a1ad8df460e14c5d04883f1731000906701fb15a4555a66848`；实际只读 Source
位于该 deployment 的 shared/application-catalog。Casdoor/网关实查 healthy，镜像 ID 分别为
`d34bbe296524378ec8e16b5bb1c8f32c4e4b1e9bec4e583353f2eac2986034f4` 和
`2d3f4c95db8443d00843a760016e9d79caf523f6afdfd3598c2395c3d45d6a93`。
网关实际启用 skip-claims-from-profile-url 和签名 ID token header，未放宽身份、组或 auth_time 校验。
六类真实密码会话的原生 API 可见性及掩码通过，native-api.json SHA-256 为
`bddccba4927401a805ba310f9cf12dcccad5255279ac36ddb71b9dcd25f57d50`。
旧矩阵脚本要求禁用账号一定已有影子记录而失败；此次初次同步过滤禁用账号，实际五个启用
影子账号均 IsAdmin=false。修正测试假设为全部启用账号必须存在、存在的 fixture 账号均无
IsAdmin，保留禁用登录拒绝及全部权限/掩码断言，复测通过；不修改产品或目录数据。
首次失败在 native-matrix-final-claims.log，修正后在 native-matrix-final-claims-corrected.log
与 native-matrix-final-claims.json，原始历史报告保留。最终实际基线为
evidence/casdoor-final-claims-baseline.json。
Chrome 154.0.8037.99 通过最终配置的原生表单：APP_all/无组直接管理登录拒绝，管理员自动
回原 PostgreSQL Adminer；双 Adminer 的密码为空且默认服务器正确，脚本错误为零。
该轮未重复早已通过的错误密码/受限数据库账号检查。记录为本机
local-browser/output/browser-casdoor-final-gateway.json，SHA-256 为
`6f108baef3700108f81a3b1ee8c7e2d77a57368e7a6399131a1a46a2663b857f`。
本机 loopback 隧道已关闭，两份临时测试凭据/预期文件已删除；浏览器临时 profile 已退出。

HTTP 回跳曾到 auth-gate 的 202 首页；核实运行二进制、上游端口规则及脱敏拒绝计数后修复。
浏览器脚本先后遇到 span/input 选择错误、慢资源导航超时及导航后响应 body 读取竞态，
这些失败与重试均保留；修正探针后，Chromium 151.0.7922.34 的三类用户及双数据库浏览器检查通过。管理员自动回原 PostgreSQL Adminer，两套页面保持空密码，错误密码拒绝、受限账号成功。截图取回因内容敏感性尚未证明被自动审批拒绝，未执行；截图保留远端，未做本机像素复核。原生矩阵探针误写 casdoor/traefik 条目名，
改为实际稳定名称 casdoor-admin/traefik-admin 后通过，未修改应用行为或预期权限范围。
完整配置/环境脚本与配置尾部输出曾被自动审批拒绝，未执行；后续仅投影非敏感字段。

本地目录/配置、Runner 定向、各相关 Hook 与 Linux Runner 目录/inventory/双 Adminer 检查通过。
完整 Linux Runner 尝试因测试 artifact 源路径、umask、PATH、缺少 .git 和 cwd 测试副作用失败；
修正 harness 后定向通过，未据此宣称全套通过或修改无关实现。2026-10-10 另执行当前工作树完整 `go test ./...`：其余包通过，Runner 的 frozen-maintenance retry 与历史 PostgreSQL readdition 用例因 macOS 的 managed temporary storage 要求 local Linux filesystem 而失败；完整门禁仍为失败，日志为本机 `/tmp/anas-catalog-20261009-9a14d222/go-full-continue.log`。随后在隔离 Linux 环境、修正 cwd/PATH/umask 的 harness 下重跑同三项 Runner 测试通过，记录在远端 `evidence/runner-linux-macos-failure-recheck.log`；该结果不改变完整 macOS 门禁的失败状态。
原始日志/JSON、源码清单、测试脚本和截图位于本轮目录的 evidence/ 与 browser-output/，
不进入 Git。Casdoor 外部生命周期与回滚、Casdoor/LLNG 本轮原生范围运行验收已通过；
Authentik 按用户要求停止运行验收，不计通过。最终标准 workspace stop 和专属 daemon stop
均返回 0；剩余一个专属 overlay 挂载正常卸载，删除路径确认属于本轮的六个 Btrfs 子卷，
忽略同文件系统上的另外 347 条盘点记录。只删除本轮 Docker/containerd 数据、workspace、
缓存、临时工具与凭据；源码、编译制品、脚本和证据保留，未递归修改旧 workspace 所有权。
最终复核本轮进程/挂载均为零，两个专属单元 inactive，namespace 和 socket 已移除；
另外两个测试 namespace 及借用的 Go SDK 保留。本轮默认 daemon 基线为 31 个容器，
清理前后名称、ID、镜像和端口完全相同，无增删；§3.4 的 26 容器基线属于此前轮次，不混用。
清理记录为 evidence/cleanup-final-owned-data.json、cleanup-final-verified.json、
default-daemon-final-comparison.json 与标准停止日志。
仍无共享持久证据归档，完整 38 项最终验收及第二阶段门户继续为未完成；计划不归档。

## 4. e2e 待执行清单

下面为实施后必须完成的项目。部分实际检查见 §3.4—3.5，未通过完整判据的项继续保留。

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-001 | 待实现：非 IAM 与外部入口 | 隔离 Casdoor + 本地登录应用 | — | 待执行 |
| R-005 | browser-native-final.mjs：链接 query/fragment 保持 | LLNG 原生浏览器/第二阶段门户 | 2026-10-10 | LLNG 实际点击外部用户卡片原样保留；Casdoor 原生追加 silentSignin 的缺口仍由第二阶段处理 |
| R-010 | runtime-probe.py / llng-catalog-probe.py：七档图标加载与尺寸 | 隔离 Casdoor/LLNG，HTTP | 2026-10-09—10 | 两种 Provider 七档 HTTP/像素/摘要与原生卡片浏览器通过；Authentik 按用户要求停止运行验收 |
| R-013 | runtime-probe.py：制品作用域与远程挂载 | 隔离远端 Unix runtime | 2026-10-09 | 实际只读挂载通过；远程 Docker 投影待实现 |
| R-014 | 第二阶段：未授权目录不泄漏 | 匿名/普通/允许/管理员门户 API | — | 待执行 |
| R-015 | catalog-phase.py / catalog-lifecycle-check.py：图片更新及回滚 | 隔离 Casdoor，两个不可变 deployment | 2026-10-09—10 | 单改图片更新、旧资产不变、实际回滚及 HTTP 通过 |
| R-018 | 第二阶段：hidden/禁用/失效账号/管理员 | 门户 API + Casdoor + 浏览器 | — | 待执行 |
| R-022 | withdrawal.py：撤组与用户缓存隔离 | Samba + Casdoor | 2026-10-09—10 | 旧 Cookie 撤组与交替用户读取通过；其他 Provider/第二阶段未完成 |
| R-023 | gateway-api.py / browser-gateway.mjs / browser-native-final.mjs / browser-casdoor-final-gateway.mjs：直接访问管理门禁 | Casdoor + 双 Adminer、LLNG + PostgreSQL Adminer | 2026-10-09—10 | Casdoor 普通/无组拒绝、管理员回跳，HTTP 与浏览器通过；LLNG 真实表单和管理员卡片回跳通过；最终 Casdoor 网关声明配置原生表单复测通过 |
| R-024 | gateway-api.py / browser-gateway.mjs：Adminer 第二层数据库认证 | 两套 Adminer + 受限 DB 账号 | 2026-10-09—10 | 空密码、错误密码拒绝、受限账号登录，HTTP 与浏览器通过 |
| R-026 | 第二阶段：全部字段完整展示 | 自己的门户 + 固定 Casdoor 镜像 | — | 待执行 |
| R-029 | 待实现：版本绑定的完整验收报告 | 固定源/镜像/API/浏览器 | — | 待执行 |
| R-035 | 待实现：中英文即时切换及 API/缓存一致性 | Casdoor 双用户 + 浏览器 + 单语外部条目 | — | 待执行 |
| R-036 | 待实现：两级分组、同 Module 双入口与固定大组顺序 | Casdoor 普通/管理员会话 + 浏览器 | — | 待执行 |
| R-038 | 待实现：权限及分类筛选后的空组隐藏与全空提示 | Casdoor API + 浏览器 | — | 待执行 |
| R-039 | 待实现：独立门户部署与 IAM 登录后的应用目录 | 独立门户 + 固定 Casdoor + 浏览器 | — | 待执行 |

实验覆盖成员/非成员、APP_all、platform_admin、管理员失去目录准入、空组、未知组、
策略删除/停用、接口错误、撤权前 Cookie、两个用户交替查询；图标验证同源 HTTPS、
七档真实像素、缓存与只改图片。接口和浏览器都检查，不用页面截图代替权限响应断言。

## 5. CI 与文档同步

| 门禁 | 最近记录 |
| --- | --- |
| 固定源码实验 | 2026-10-03，本机 12/12 子场景；非 E2E |
| Adminer 现有单元/依赖解析 | 2026-10-03，TestGate* 与 PostgreSQL/LLNG 解析通过 |
| 需求、状态与站点 | 2026-10-04：中英文规划更新后，35 项需求归属/用例 catalog、两项索引状态、文档状态和 docs:build 通过；构建有 bundle 体积提示 |
| 两级分组文档 | 2026-10-08：基于 aa1944a035c7f96f3afda4cfd1b904852e191b6f 的本地文档改动，38 项需求归属/用例 catalog、两项索引状态、文档状态、docs:build 及 diff 格式检查通过；构建仍有超过 500 kB 的 bundle 提示 |
| 第一阶段代码与渲染 | 2026-10-08：整仓库 `go test ./...`、Casdoor helper 独立模块测试通过；新增排序/身份测试后 Core 定向复核通过；Linux 图标包交叉编译通过；三个原生 Provider 及双 Adminer/外部增删真实 CLI plan/render 通过 |
| 生成物与门禁 | 2026-10-08：Module revision、Module/Contract/Test-case 生成检查、需求与两项索引状态、文档状态、`docs:build`、容器配置和 Shell/Python 语法检查通过；VitePress 仍有既有 bundle 体积提示 |
| 真实主机验收 | 2026-10-09：finance.hlong.wang 隔离 Casdoor 最小 apply、图标 HTTP、原生组过滤与 18 个签发拒绝反例通过；Nextcloud 完整 apply 失败、浏览器/网关/撤组受 I/O 阻塞；整体未完成，见 §3.4 |
| 远端结果与文档同步 | 2026-10-09：Module 文档生成及生成物检查、38 项需求归属/用例 catalog、两项索引状态、文档状态、docs:build 与本次文档 diff 格式检查通过；VitePress 仍有既有 500 kB bundle 提示 |
| 最终声明修复后静态检查 | 2026-10-10：当前 HEAD 加工作树的 Go vet、Module generator check、Module revision check、需求/状态及两项索引状态、docs:build、diff 格式检查通过；LLNG/网关 Hook 与网关 bootstrap 单元通过。完整 macOS Go 测试的三个 Linux 文件系统限制失败及远端定向复测结果仍按 §3.5 保留 |

本次文档校验日志位于本机 `/tmp/anas-app-catalog-groups-{requirements,requirement-status,plan-status,status,build}-20261008.log`
（花括号表示五个独立文件）。实施日志为 `/tmp/anas-app-catalog-full-go-retry-20261008.log`、
`/tmp/anas-app-catalog-final-core-go-20261008.log`、
`/tmp/anas-app-catalog-{requirements,requirement-status,plan-status,status,build,container-config}-20261008.log`，
CLI 各阶段日志为 `/tmp/anas-app-catalog-{provider}-{phase}-20261008.log`，
补充 fixture 为 `/tmp/anas-app-catalog-adminer-external-*-20261008.log`。
完整 Go 首次运行发现既有 VersityGW 用例固定 r3 与统一 revision 更新后的 r4 不符，已更新期望值，
定向及整仓库重跑通过；首次失败日志保留为 `/tmp/anas-app-catalog-full-go-20261008.log`。
所有日志仍为本机临时证据；R-036、R-038 的浏览器验收待执行，R-037 单元已通过。
文档检查不改变运行验收状态。

- [x] 重写架构，补齐与现有协议差异。
- [x] 形成固定源码调研与可复现测试入口。
- [x] 补稳定需求矩阵和里程碑归属。
- [x] 首期中英文、回退和逐语言覆盖纳入协议与验收。
- [x] 用户应用/后台管理两级分组、主分类归属和空组隐藏纳入协议与验收。
- [x] 用户确认第一阶段原生功能、第二阶段自己的门户。
- [x] 更新配置参考、Module generator 输入与中英文生成页面。
- [x] 新协议运行路径 APPS_LIST 零残留，Provider 原生限制公开到 plan。
- [x] 第一阶段代码、单元与真实 CLI 渲染检查。
- [x] 第一阶段 Casdoor/LLNG 原生容器、权限与 Adminer 双层认证运行验收；Authentik 按用户要求不做运行验收，不计通过。
- [ ] 第二阶段自己的门户实现与全部最终判据验收；设计提案已补充，尚待评审。

## 6. Authentik 移除与第二阶段设计（2026-10-10）

用户“移除Authentik，继续设计”替代先前的逐步弃用路线。本轮只实施移除并继续设计，不实现自己的门户。
当前支持集为 Casdoor（主力）与 LLNG；§3 中 Authentik 的制品、失败与清理记录保留原基线。

- [x] 移除 Authentik Module、镜像/Module 发布清单、CI 专属步骤及私有 API 测试脚本。
- [x] 通用静态配置改用 Casdoor；已有 LLNG 探针保留，退役 Authentik 私有流程，不改名伪装验证。
- [x] 保留专属需求/用例的稳定 ID 并明确废弃，旧 Provider 通过不能计入 Casdoor；当前配置不自动改绑。
- [x] 第二阶段部署、认证、权限过滤、资产保护、双语/两级分组与复杂度提案写入设计 §10。
- [x] 执行本轮相关本机检查并记录实际失败/未执行项；完整门禁未通过。
- [x] 用户已明确第二阶段为独立网站；具体账号写入设计见 §7，仍待评审。

本轮删除前源码保全：本机
`/tmp/anas-authentik-removal-20261010-before.tar.gz`、
`/tmp/anas-authentik-removal-immich-harness-before-20261010.tar.gz`。只保全移除的源码和退役测试，
无运行时 Secret；Git 历史继续保留。这些本机文件不是共享持久归档。
当前测试配置/锁变化需独立实机复验，不继承 §3 的不同源码/镜像 baseline。本轮没有登录 SSH 主机、
没有重建已清理的专属环境，也没有再次测试 Authentik。完整需求仍未验收，计划保持 implementing。

### 6.1 本轮基线与保全

源码为 HEAD `efb3fa7c7cb6cd918c6ed15b248731c849ce824e` 加当前工作树；无关 AI Agent、
Incus 等变更保留。本机 Go `go1.26.6`、Darwin/arm64。逐文件基线为
`/tmp/anas-authentik-removal-source-baseline-20261010.json`（1656 个现存文件、45 个已移除路径，
SHA-256 `9e3047c063ec93370ba853ca81f1f7caaea846d7c76acd1afbf1bda070ccfda2`）。
清单覆盖 Core、Module、发布清单、配置及测试；根计划执行记录不纳入，避免自引用摘要。

诊断 CLI 为 `/tmp/anas-authentik-removal-cli-20261010`，
SHA-256 `22a45b62b44b64379fe65205b4626cf981e9d0506026974f062f440c72d27a99`；
逐项渲染显式使用当前 `--module-root`，没有构建或发布运行镜像。
两份新锁经真实 init/lock 生成，摘要分别为：
`test-env/configs/full.lock.yml` =
`ebf69938fac46cddd89e23b7a75ea21bf7e61d6b090d51f3b6a5a3b47433fd1b`，
`test-env/server-identity-app-e2e.lock.yml` =
`437be37bb94235824de75e4c9c367502f131b2577e475b14b8d6c54d2dcf1603`。
锁生成成功不代表包含 NetBird 的配置可渲染或启动。

删除前两份归档的 SHA-256 按上文顺序分别为
`33799a416cefa09e42b12479d73f30fb8b05d86e9708df6825ced1fa78a7e3b4`、
`b24ecef300ffbb912c4cb47f10cf5301b43449b831c94c51d64d328bae8eabc6`。
所有日志、逐文件清单、部署与诊断 JSON 留在本机 /tmp 或 Git 忽略路径，仍不是共享持久证据归档。

### 6.2 检查结果

| 检查 | 实际结果与边界 |
| --- | --- |
| R-027 移除审阅与旧选择反例 | 当前 24 个 Module；模块、发布清单、专属 CI/脚本与当前配置没有 Authentik。registry 反例及 TestRemovedIAMProviderCannotBeSelected 通过，旧显式选择报错，不自动改绑。没有登录主机或删除业务数据。此项本轮审阅通过 |
| 目录与保留适配 | internal/config、internal/computeingress、internal/appcatalog、Casdoor/LLNG/oauth2_proxy Hook、Nextcloud Hook、Casdoor helper 独立模块通过；Go vet 通过。helper 初次受沙箱 loopback bind 限制失败，获准本机端口监听后完整复测通过，未改产品绕过 |
| Runner 全部相关包测试 | 3 项失败：TestCLIApplyRetriesFailedFrozenMaintenanceCandidateAfterHookFailure、TestCLIApplyCannotReaddHistoricalPostgresOverRetainedData、TestCLIApplyAllowsFirstPostgresWithNoPriorPostgresRecords；原因是 macOS 不支持测试要求的 local Linux filesystem。其余用例通过；排除这 3 项的定向复跑通过，不改变完整运行失败状态 |
| 逐配置 init/plan/render | 19 项中修复后 17 项通过；full、matrix-network 因 NetBird 的 DIRKEY-R-013 拒绝失败。原 test-render.sh 在 full 处失败，最终全参数渲染扫描未执行，不能计全套通过 |
| Collabora 管理链接 | 首次 matrix-apps 失败：COLLABORA_ADMIN_URL 到 render_env 才生成。已提前至 calculate；443/9000 两端口 Hook 回归、Go vet 与真实 matrix-apps render 复测通过。保留首次失败和重试，不调整目录 URL 判据 |
| 配置审计 | managed-config CLI 与两个 LLNG 域名分离静态配置通过；test-parameters 当前仍失败，缺少工作树既有 AI Agent/Incus 的 9 项运行效果案例。已对齐已有 json_object、temp_path 及必须解析字段的元数据期望，没有用空案例冒充覆盖 |
| 参数运行效果脚本 | test-parameter-effects 在第一项激活后的临时存储清理处因 local Linux filesystem 要求失败，后续 Casdoor/Nextcloud 等变更用例未执行；未将其计为通过 |
| Immich harness | 36 项单元通过、1 项 Linux /proc/SIGKILL 用例因平台跳过。共用文件直接执行明确失败；受隔离守卫的 shell 入口调用 Casdoor 专属 harness，--help 与语法通过。无当前 revision 组合实机登录/恢复通过结论 |
| 升级 | catalog 静态检查通过：2 个产品、24 个 Module、6 个 suite、22 条 transition；old-compat 脚本单元通过。固定 image-release/46-2 fixture gate 失败：该源码不存在 Casdoor；当前 Casdoor suite 在移除前已存在，不因本轮静态检查就获得升级验收 |
| 构建与容器静态 | shared build 闭包检查、Module revision、容器配置、Shell/Node 语法与脱敏报告器通过；Docker 镜像构建与真实容器升级均未执行 |
| 文档与格式 | Module/Contract/Test-case 生成检查、需求归属、需求/计划索引状态、文档状态及各文档工具测试通过；docs:build 通过（既有 500 kB bundle 提示），diff 格式通过。真实性只限本机文档与静态检查 |
| 第二阶段完整行为 | R-005/R-014/R-018/R-021/R-022/R-026/R-035—R-038 仅继续设计，严格 API、自有页面与原生旁路关闭尚未实现；不更新为运行验收通过 |

日志前缀为 `/tmp/anas-authentik-removal-`，后缀为 `-20261010.log`。
主要名称为 `relevant-go`、`runner-supported`、`casdoor-helper`、`collabora`、
`support-unit`、`vet`、`render`、`matrix-apps-retry`、`parameters-details`、
`parameter-effects`、`managed-config`、`dns-fixtures`、`immich-unit-final`、
`upgrade-fixtures`、`upgrade-catalog`、`module-check`、`contract-check`、
`cases-check-final`、`revisions`、`docs-build-final` 及各 requirement/plan/status 检查。
逐配置初次失败/通过保留在 `/tmp/anas-authentik-removal-fixture-render-summary-20261010.json`，
修复后汇总为 `/tmp/anas-authentik-removal-fixture-render-final-20261010.json`，
逐项日志/只读 deployment 在 `/tmp/anas-authentik-removal-fixture-render-20261010-k6sef7ty/`。

### 6.3 阻塞与下一步

1. 本轮曾提出 Casdoor 同进程页面/API；后续用户明确改为独立网站，推荐位置已被 §7 取代。账号写入方案未实施。
2. NetBird 在保留的两个 IAM 上均违反现有 DIRKEY-R-013，继续保留拒绝与原测试范围。已向用户询问
   是否另开修复范围；尚无答复，本轮未改 NetBird、未删除相关覆盖，也未豁免限制。
3. 固定升级基线缺少 Casdoor、AI Agent/Incus 参数运行效果案例和 Linux 专属检查仍未解决。
   没有将这些失败换算成通过；需按对应既有计划处理并在合适平台复测。
4. 第二阶段实施后按原需求完成 HTTP/浏览器、资产旁路、语言/两级分组、权限撤销及双 Adminer 回归，
   记录固定源码/镜像/制品；当前 38 项完整验收及共享持久证据归档仍未完成，计划不归档。

## 7. 独立网站与账号功能扩展提案（2026-10-10）

用户进一步明确：第二阶段作为单独门户网站，显示当前用户有权限的应用；通过 LDAP 修改本人密码；
有相应权限的管理员能为范围内用户修改分组和重置密码。本轮继续设计，没有实施、部署或写入目录。

应用目录独立部署纳入 APPCAT-R-039，归属 M3；其余 R-001—R-038 判据保持，历史 38 项基线不改写。
旧 Casdoor 同进程页面提案已被独立网站方向取代。新详细设计见
[独立用户门户](../../docs/architecture/user-portal-design.md)，应用目录 §10 改为引用这份统一提案。

### 7.1 设计范围与待确认项

- [x] 区分用户已明确能力与功能建议，按用户自助、委派管理、安全审计和后续便利功能扩展。
- [x] 保留双语、未知语言英文回退、两级分组、严格授权与目录/图标旁路原判据。
- [x] 梳理本人改密与管理员重置、直接/间接组、委派对象/组范围、受保护身份、首次/过期改密闭环。
- [x] 复核当前 Samba svc_ldap 只读、svc_password 有限 reset 权，后者不具备分组管理权限。
- [x] 写明 AD/IAM 状态延迟、会话撤销边界、LDAP 结果未知、审计失败和单组增删的简单交付方式。
- [x] 2026-10-11 用户确认首版功能及显式委派/默认保护方向；实际生产人员与组名单由部署配置给出。
- [ ] 实现 Casdoor 原生本人读取/固定组过滤、会话/审计、LDAP 受限绑定与配置再认证；能力路径与 IAM 联合登出已验证，见门户计划 §7。
- [x] 2026-10-11 新增 PORTAL-R-001—R-040 与配套门户计划，不复用 APPCAT ID。
- [ ] 按原应用目录及新账号矩阵实现和实机验收；LDAP ACL、过期改密与旧会话实验尚未执行。

独立网站方向已经确认，不再重新询问是否拆成网站。整体账号方案复杂度高：
OIDC 门户会话、LDAP 委派 ACL、IAM/目录同步和跨系统审计/结果未知都需要明确接线。
较简单交付为只读目录 + 本人改密，管理员暂跳现有 LAM；它只适合分步交付，
不代替用户提出的门户内分组与重置目标。截至 2026-10-10 仅提供可评审提案，尚未确认新增首版范围；2026-10-11 的确认与独立计划见 §8。

### 7.2 调研和校验边界

2026-10-10 读取当前 structure.sh、IAM 身份/角色契约及目录要求；核对 Microsoft AD unicodePwd
官方文档区分本人 change 与管理员 reset，OIDC 接入参考 RFC 9700。资料链接在架构文档；
Samba 官方委派页面访问失败，未据此宣称其行为已验证。Samba 4.23.6 的 LDAPS 改密/委派 ACL、
Casdoor 首次/过期认证与敏感操作 MFA 接线仍须隔离主机实验。

本轮只修改设计、明确的独立网站需求及计划/索引；不运行产品测试，不做 SSH/LDAP 写入。
本轮文档门禁结果另记下方，不继承 §6 的运行通过记录。

文档复核基线为 HEAD `efb3fa7c7cb6cd918c6ed15b248731c849ce824e` 加本轮提案改动；
新设计文件 SHA-256 为 `2f95ad14a69c6957bad1b7a970d2598fcf5e8f8d4c80d2d01acb61d0cfa12d68`。
独立网站新增判据的 e2e 待执行行初次遗漏导致需求检查失败；已补 R-039 待执行记录，未伪造通过。
需求归属/用例 catalog、两项生成索引状态、文档状态与 diff 格式检查复测通过；docs:build 通过，
仅有既有 500 kB bundle 体积提示。文档校验不证明门户或 LDAP 功能通过。
日志为本机 `/tmp/anas-user-portal-{requirements,requirement-status-check,plan-status-check,status,diff-check,build}-20261010.log`。
旧同进程详细提案保全于 `/tmp/anas-user-portal-superseded-design-20261010.md`；
本机临时证据不是共享持久归档，新增功能仍待设计确认和实机验收。

## 8. 首版确认与独立账号计划（2026-10-11）

用户确认上一轮首版功能与已披露的高复杂度分步方向。实际部署管理名单不由泛化的
“确定”推定为全部人员：采用显式规则，动作/用户/可修改组同规则匹配，空配置关闭管理。
高权限、门户授权边界组和服务身份默认保护，不以平台管理员代替 LDAP 委派权。

新增 [PORTAL 矩阵](../../modules/user_portal/dev-docs/requirements/user-portal.md) 40 条判据及 [门户计划](../../modules/user_portal/dev-docs/plans/user-portal.md)。
首版包括应用搜索、本人资料与改密/首次过期闭环、范围内查询/重置/强改/解锁/分组、
审计和近期认证；其他候选仍不纳入。建议以单个服务、内存会话和私有审计文件保持组件最少。
这里转交账号规划，不重复列出第二份里程碑；M3 与原 R-001—R-039 归属/验收保持。

本轮只更新文档，没有部署或执行 LDAP。具体文档基线/校验结果和能力阻塞见门户计划 §3、§5；
历史原生通过范围及先前失败记录保留，不计为新账号功能通过。
