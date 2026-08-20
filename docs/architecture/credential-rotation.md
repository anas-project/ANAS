---
doc_type: architecture
status: proposed
created: 2026-08-19
updated: 2026-08-20
---

# ANAS 凭据库存与 deployment 驱动轮换设计

> 状态：目标方案；阶段 A 基础切片已开始实施（2026-08-20）
>
> 适用范围：ANAS Core、Module、resource provider、Secret Store 与 deployment 生命周期
>
> 当前实现与目标方案的差异见[实施基线与迁移顺序](#实施基线与迁移顺序)。

本文整理 ANAS 管理凭据的统一模型和最终轮换方案。全文只记录逻辑键、来源、用途和生命周期，
不记录、复制或散列任何真实凭据值。

## 1. 决策摘要

1. **Deployment 是凭据期望状态和回滚单元。** 轮换不再直接修改 active deployment；Runner
   从它创建 candidate deployment，previous deployment 始终保持不可变。
2. **不使用 `*_SET` 触发变量。** Module 启动时检查 deployment 声明的 ANAS-owned 凭据是否
   已经等于期望值；匹配则不修改，不匹配才协调并验证。
3. **全量轮换是有计划的全部署停机操作。** 停止旧 deployment 后，按依赖正序逐个激活
   candidate Module；当前 Module 完成凭据协调并达到 ready 后，才允许下游 Module 启动。
4. **单项轮换使用同一个执行器。** 它只停止并重新激活凭据影响的依赖闭包；候选生成、协调、
   验证、提交和回滚语义与 `--all` 相同。
5. **Core 只负责通用编排。** SQL、`samba-tool`、`occ`、应用 API 和容器内脚本均由所属 Module
   或 resource provider 实现。
6. **密码修改必须幂等。** 每项支持 `probe → reconcile → verify`；服务未就绪、网络错误和存储
   损坏不得误判为密码不一致。
7. **凭据修改顺序和 Module 启停顺序是两张图。** Module 按 provider 到 consumer 激活；一个
   provider 内部先协调普通账号，最后协调 root、superuser、Administrator 等控制账号。
8. **Previous deployment 只能恢复投影，不能单独恢复持久密码。** 回滚通过启动 previous
   deployment、重新运行同一幂等协调器，把数据库、AD 和应用内部状态恢复到旧期望值。

## 2. 范围与术语

### 2.1 三个不同的管理边界

不能因为 ANAS 保存了一个值，就推断它可以自动轮换：

| 能力 | 含义 |
| --- | --- |
| 保存 | ANAS 能安全保存和投影该值 |
| 签发 | ANAS 能生成符合目标系统要求的新值 |
| 协调 | ANAS 能更新权威系统和全部消费者、验证新值并恢复旧值 |

只有活动、`authority=anas`，并同时具备签发与协调能力的逻辑凭据，才能进入自动轮换。

### 2.2 Authority 与来源

- `anas`：当前值由 ANAS 生成，或者操作者通过 `--force` 明确交由 ANAS 接管；允许幂等协调。
- `external`：值由用户或外部系统提供；默认只能投影或验证，ANAS 不得自行重置或重新签发。
- 来源必须冻结进 deployment manifest，不能从参数名或是否存在配置入口推断。
- 没有 ANAS 生成策略的 DNS/API token 即使使用 `--force` 也保持 `external/manual`。

### 2.3 本方案覆盖的轮换模式

| 模式 | 例子 | 本方案处理方式 |
| --- | --- | --- |
| `reconcile` | 数据库账号、Samba 服务账号、本地管理员、TURN shared secret | deployment 启动时幂等协调 |
| `overlap` | OIDC client secret、SAML/签名密钥 | 需要新旧值并存和退役窗口，后续单独设计 |
| `migrate` | 数据加密主钥 | 需要数据重加密，不进入普通 rotate |
| `external` | DNS/API token | 人工操作或厂商 provider 集成 |

文中的“全部轮换”只指活动 deployment 内可执行的 `reconcile` 目标，不包含后三类。

## 3. 当前库存与能力边界

### 3.1 存储面

| 存储面 | 内容 | 约束 |
| --- | --- | --- |
| `<workspace>/config.yml` | 外部 Secret、普通敏感参数 | `0600`；值可被投影，但不代表 ANAS 有轮换权 |
| `<workspace>/.anas/secrets.yml` | generated、lifecycle-managed、local-admin、resource credential | `0600` 原子写入；当前元数据不足以单独表达完整轮换计划 |
| `<workspace>/.anas/local-admins.yml` | 本地账号 ID、用户名、Secret key | 只做账号索引，不保存密码 |
| deployment-scoped `.env`/Secret 文件 | Module 的期望凭据投影 | previous 与 candidate 各自持有封印后的独立投影 |
| 应用内部状态 | 数据库角色、AD 账号、bcrypt、应用数据库配置 | 真正的协调目标；只改 Store 或 `.env` 不构成轮换 |
| Module 数据目录 | ACME 私钥、应用自行生成的 token/密钥 | 没有结构化声明时不能纳入统一轮换 |

`anas config secret list` 只列 Secret Store 键和 kind，不是全量库存。`anas credential list` 现已
把 active deployment 中冻结的 `credentials.provides/consumes` 记录规范化为无明文库存；resource
credential、本地管理员与未结构化外部 token 尚未并入这一统一视图，仍是后续迁移范围。

### 3.2 当前阶段 A/B 实施能力

旧的未提交实现曾直接修改 active deployment `.env`，逐目标在线调用
apply/verify/rollback，并逐项提交 Secret Store；该实现已删除，不再作为迁移基线。当前代码只落地
由显式 Module 协调合同证明安全的能力，不会把未迁移凭据假装成可轮换目标：

| 能力 | 当前实现边界 |
| --- | --- |
| Module 静态声明 | `module.yml` 已支持 `credentials.provides/consumes`；校验 namespace、Secret key/projection、生成策略、handler、显式 Hook phase、唯一性、引用完整性和 control edge |
| Frozen credential record | materialization 在 calculate 后从 Secret Store provenance 解析 authority/generation，自动冻结无明文的 ID、Secret key、owner、consumer、mode、projection、generator、lifecycle 与 control edge |
| 共用 planner | 单项与全量计划共用纯内存 planner；合并 Module dependency 与 credential owner→consumer edge，计算控制顺序、停机逆序、激活正序、单项下游闭包和 blockers |
| Candidate deployment | 从 active deployment 的**有效**冻结制品复制 candidate；候选值只写 candidate 的 Module `.env`，previous、active pointer 和 Store 不变；candidate 再次封印为只读 |
| Store 提交原语 | Secret Store v2 record 新增可选 `generation`/`rotation_id`；批量提交先完整校验，再只执行一次原子 Save |
| 持久 journal | `.anas/state/transactions/credential-*.yml` 记录 deployment、ID、代次、phase、Module 进度和恢复状态；不记录值、hash 或 verifier；新的排他操作先自动恢复，恢复失败才阻断 |
| Module ready barrier | Module 逐个执行 `up → credential probe/reconcile/verify → after_start/local-account reconciliation`；当前 Module 成功后才启动下一个 Module |
| 结构化 Hook ABI | 新增三个显式 opt-in phase；期望值只通过 stdin JSON 的 `secrets.ANAS_CREDENTIAL_DESIRED` 传递，stderr 不进入 Core 错误；普通 mutation/未知 JSON/多 JSON/无效状态均 fail closed |
| 首个 provider | `eturnal.secret` 已实现实际运行配置 probe、ANAS-only restart reconcile 与 verify；Nextcloud/NetBird 已声明 consumer，owner verify 前不会启动 |
| 启动失败补偿 | Candidate 启动失败时先停止 candidate，再重新激活 previous；错误会明确区分 previous 已恢复或恢复失败 |

这些能力现已由同一个生产执行器串接到 `anas credential list/rotate`：planner preflight、candidate
激活、ready barrier、一次性 Store commit、promotion 与排他锁下的 crash recovery 使用同一冻结
deployment 合同。当前可实际轮换的首个 provider 是 `eturnal.secret`；`anas admin local rotate` 的
既有逐账号实现仍保留兼容行为，还没有委托新执行器，以免重新引入“只改投影或 Store 就算轮换
成功”的旧语义。

### 3.3 已有值但尚未接入最终协调器

| 类别 | 逻辑键或模式 | 需要补齐的能力 |
| --- | --- | --- |
| MariaDB root | `MARIADB_ROOT_PASSWORD` | 本地管理通道、probe、幂等重置和候选认证 |
| Samba DC 五个账号 | `SAMBA_DC_{ADMIN,ADMINISTRATOR,LDAP_BIND,PASSWORD_BIND,ANCHOR_BIND}_PASSWORD` | AD 协调、认证验证和内部控制顺序 |
| 关系数据库资源账号 | `RESOURCE_<CONSUMER>_<RESOURCE_ID>_PASSWORD` | provider 级 probe/reconcile/verify；消费者重新激活 |
| Module 私有管理员 | `COLLABORA_ADMIN_PASSWORD`、`LAM_ADMIN_PASSWORD` 等 | 显式逻辑凭据声明和本地验证 |
| 内部 shared secret | Talk/Signaling/Imaginary/Relay 等 | `eturnal.secret` 已接入；其余仍需 owner/consumer 声明和启动时协调 |

PostgreSQL 与 MariaDB provider 的现有 `ensure` 已能创建或 `ALTER` 普通资源账号，但没有独立的
候选认证、authority 检查和回滚合同；统一库存接入后必须把这些资源凭据报告为 unsupported，
不能仅凭现有 `ensure` 宣称可轮换。

四个本地管理员仍可通过兼容命令逐账号轮换，但它们尚未声明新的 probe/reconcile/verify 合同，
也尚未进入 candidate deployment 事务。PostgreSQL 的旧版在线 credential Hook 未保留；其当前
`POSTGRES_HOST_AUTH_METHOD=trust` 基线不足以证明密码认证，不能冒充完成。Eturnal 已按新的启动时
协调 ABI 重新接入；Nextcloud 与 NetBird 已成为其显式 consumer。

## 4. 目标数据模型

### 4.1 Module 静态声明

Module 只声明它能够管理什么以及如何管理，不记录某个 workspace 的明文值或动态 authority。
以下 YAML 是当前已冻结的结构：

```yaml
credentials:
  provides:
    - id: postgres.superuser
      secret_key: POSTGRES_PASSWORD
      type: password
      rotation_mode: reconcile
      generation:
        kind: password
        length: 32
      lifecycle:
        probe: probe-postgres-superuser
        reconcile: reconcile-postgres-superuser
        verify: verify-postgres-superuser
      controls:
        - postgres.nextcloud

  consumes:
    - credential: eturnal.secret
      projection: TURN_SECRET
```

禁止按 `PASSWORD`、`TOKEN`、`KEY` 名称猜测凭据。未显式声明的值只能进入未知/人工库存，不能
被自动修改。

### 4.2 Frozen deployment 记录

每个 deployment 必须冻结足以独立启动和回滚的无歧义记录：

```yaml
credentials:
  - id: nextcloud.primary_database
    secret_key: RESOURCE_NEXTCLOUD_PRIMARY_DATABASE_PASSWORD
    owner: postgres
    consumers: [nextcloud]
    authority: anas
    rotation_mode: reconcile
    generation: 4
    desired_projection: deployment-secret://nextcloud.primary_database
    lifecycle:
      probe: probe-resource-password
      reconcile: reconcile-resource-password
      verify: verify-resource-password
```

要求：

- 明文只进入 deployment-scoped 的 `0600` 投影，不进入 manifest、日志或审计事件；
- previous 与 candidate deployment 的投影彼此独立，不能共享一个会被覆盖的 runtime Secret 文件；
- `authority`、消费者、handler、代次和影响范围全部冻结，运行时不得重新读取变化后的 Module 源码；
- Secret Store 是 workspace 的当前权威索引，但 Store 更新必须发生在 candidate 验证成功之后。

### 4.3 两张依赖图

1. **Module 运行图**决定停止和启动顺序：停止时 consumer 到 provider，启动时 provider 到
   consumer。`credentials.consumes` 若不在普通依赖闭包中，必须生成显式 activation edge。
2. **凭据控制图**决定同一阶段内的修改顺序：被控制的普通账号先修改，提供修改权限的账号
   最后修改；回滚时顺序相反。

典型控制关系：

```text
MariaDB/PostgreSQL resource accounts → root/superuser
Samba svc_ldap/svc_password/svc_anchor/admin → Administrator
```

Eturnal 在运行上依赖 Traefik，但 `eturnal.secret` 不依赖 Traefik Dashboard 密码；二者没有
凭据控制 edge，启动顺序由 Module 运行图决定。Planner 必须合并两张图并在任何副作用前拒绝环。

## 5. 幂等凭据协调合同

### 5.1 不使用 `_SET`

ANAS-owned 凭据在每次 Module 激活时都执行以下状态机：

| Probe 结果 | 行为 |
| --- | --- |
| `match` | 已是 deployment 期望值；不修改，继续 verify |
| `missing` | 使用本地管理通道创建或设置，再 verify |
| `mismatch` | 使用本地管理通道重置为期望值，再 verify |
| `unavailable` | 服务未就绪、网络或存储故障；失败退出，禁止重置 |
| `unsupported` | 生命周期不完整；在 preflight 阶段阻断 |

`authority=external` 只能 probe/verify。出现 mismatch 时返回 manual action，不允许调用 reconcile。
`--force` 的含义是创建一个 authority 已变为 `anas` 的 candidate deployment，而不是绕过 Module
能力检查。

### 5.2 Module 激活屏障

每个 Module 的启动必须成为一个有结果的阶段：

```text
start authority services
→ wait ready
→ probe/reconcile/verify owned credentials
→ start remaining in-module services
→ module health verification
→ module ready barrier
```

Runner 只有在当前 Module 通过 ready barrier 后才启动下游。Module 内部必须决定最小服务集，
例如 MariaDB 先启动数据库、完成账号协调后再启动 Adminer；Samba DC 先完成 AD 账号协调，再启动
Anchor Worker。

### 5.3 本地管理通道是硬条件

回滚时实际系统可能使用 candidate 密码，而 previous deployment 只携带旧期望值。协调器必须
拥有不依赖被协调密码的本地权限：

- PostgreSQL：容器内受限的本地 socket 管理连接；
- Samba：本地 SamDB/Samba 管理权限；
- MariaDB：必须明确提供可靠的本地 socket/恢复管理路径，不能假设旧 root 密码仍可登录；
- 普通数据库账号：由 provider 的本地管理通道修改；
- 应用本地账号：使用应用自身管理 API/CLI 或可验证的受限数据接口。

无法建立独立管理通道的凭据不得宣称可以通过 deployment 自动回滚。

### 5.4 安全要求

- 候选值通过 Hook stdin、deployment-scoped Secret 文件或等价受限通道传递；不得放入宿主机
  argv、普通日志或审计字段；
- 容器内脚本应随 Module 镜像版本管理；`docker exec -i` 可以调用脚本，但 Core 不硬编码脚本名；
- 先等待服务 ready，再区分认证失败与连接故障；不能因超时而重置密码；
- probe 必须控制失败次数，避免 Samba/LDAP 锁定；能比较 verifier/hash 时优先比较；
- reconcile 重复执行必须得到相同逻辑结果，不得因密码历史、随机 hash salt 或重复建号而失败；
- verify 必须验证真实认证或有效配置，不能只检查环境变量是否存在。

## 6. 执行流程

### 6.1 Preflight

单项和全量命令共用同一个 planner，并在零副作用阶段完成：

1. 获取 workspace 锁，读取 active deployment、Store 和库存；
2. 选择目标，解析 authority、生成策略、owner、consumer 和影响闭包；
3. 校验所有 probe/reconcile/verify handler、本地管理通道声明和 Hook ABI；
4. 合并 Module 运行图与凭据控制图并检查无环；
5. 估算停机范围，列出 manual、unsupported 和 blockers；
6. `--dry-run` 到此结束，不生成候选、不创建 deployment、不调用 Docker/Hook。

活动 ANAS-owned 目标有任一 blocker 时，全量执行必须 fail closed，不提供 `--allow-partial`。

### 6.2 Candidate deployment

实际执行在第一个副作用前完成：

1. 为全部选中目标生成候选值，但尚不持久化；
2. 先写入不含明文的 planned journal 和 candidate deployment ID；
3. 复制 active deployment 的非凭据制品，只在 candidate 的 deployment-scoped Secret 投影中写入候选值；
4. 封印 candidate；previous deployment、active pointer 和 Secret Store 保持不变。

### 6.3 `credential rotate --all`

```text
stop previous deployment once, in reverse dependency order
→ activate candidate modules one by one, in dependency order
→ each module reconciles owned credentials before releasing downstream
→ verify deployment-wide authentication and health
→ atomically commit candidate values and generations to Secret Store
→ promote candidate deployment
→ mark transaction journal complete and clear active transaction marker
```

所有容器先停止；需要服务在线才能修改持久凭据时，由该 Module 的激活阶段先启动最小 authority
service。不会在每个凭据修改后恢复整个依赖链，因此一个 Module 不会因为上游随后轮换而被重复
正式启动。

### 6.4 `credential rotate ID`

单项命令创建同样的 candidate deployment，但只改变一个逻辑凭据，并只停止受影响闭包：

```text
stop affected consumers and owner
→ activate owner with candidate projection
→ reconcile and verify the selected credential
→ activate affected consumers
→ verify and commit
```

本地管理员命令最终应委托同一执行器；兼容命令 `anas admin local rotate MODULE [ACCOUNT]` 只负责
把 Module/account 映射为 credential ID 和处理 TTY 输入。

### 6.5 普通 start/restart/rollback

- `start`/`restart` 先要求当前 deployment 与 Store presence、authority provenance 和 generation
  一致，再运行 probe/reconcile/verify，因此应用侧漂移会被恢复到当前期望值，Store 分裂则 fail closed；
- 运维人员在应用内部手工修改 ANAS-owned 密码，下一次启动会被 deployment 覆盖；此行为必须
  在 CLI plan 和运维文档中明确；
- 当前普通 `rollback DEPLOYMENT` 遇到目标 deployment 与 Store 代次/authority 不一致时返回
  `credential_store_mismatch`，`--allow-risky` 也不能绕过；应恢复包含匹配 Store、deployment 和数据的
  snapshot。未来显式 credential rollback 才会使用同一事务执行器恢复旧投影并提交 Store，不能维护
  第二套 rollback 密码脚本。

## 7. 事务、回滚与崩溃恢复

### 7.1 回滚流程

Candidate 激活或验证失败时：

```text
stop candidate modules
→ activate previous deployment in dependency order
→ previous modules reconcile persistent systems back to old desired values
→ verify previous deployment
→ keep previous active and Secret Store unchanged
```

凭据控制图在回滚时反向执行：先恢复 root/superuser/Administrator，再恢复由它们控制的普通账号。
环境型 Secret 只需 previous deployment 重新启动；数据库、AD 和应用内部持久配置必须通过
Module 协调器实际恢复。

### 7.2 事务日志

`.anas/state/transactions/<rotation-id>.yml` 至少记录：

- previous/candidate deployment ID；
- 目标 credential ID 和代次，不记录值或 hash；
- 当前 phase、已完成 Module、失败 Module；
- Store 是否已提交、candidate 是否已 promoted；
- 回滚和人工恢复状态、时间和执行 uid。

审计日志同样只记录逻辑身份和结果。Hook 错误在持久化前必须按 previous/candidate 全部 Secret
统一脱敏。

### 7.3 崩溃窗口

| 崩溃点 | 恢复决策 |
| --- | --- |
| candidate 创建后、停止前 | 删除/保留未激活 candidate；previous 不变 |
| candidate 部分激活、Store 未提交 | 停止 candidate，激活 previous 并协调旧值 |
| 全部验证后、Store 提交前 | 默认回滚 previous；也可按 journal 的明确 phase 安全续交 |
| Store 已提交、promotion 前 | 根据 Store `rotation_id/generation` 完成 candidate promotion |
| promotion 后、journal 清理前 | 核对 active、Store 与 candidate generation 后清理 journal |

存在未完成或 recovery-required 事务时，新的写操作必须阻断，直到自动恢复成功或操作者显式处理。

## 8. 各类凭据的具体落地

### 8.1 PostgreSQL 与 MariaDB

- 每个 `relational_database` resource 的普通账号都是独立逻辑凭据，必须纳入全量轮换；不能让
  Consumer 使用 root/superuser；
- provider 启动后先协调所有活动 resource account，再协调 root/superuser；
- probe 应尝试目标数据库的真实认证，或在不触发锁定的前提下比较 verifier；
- `ensure` 继续负责创建数据库和账号，新增的 reconcile 合同负责 authority、候选验证和幂等
  密码变化；二者不能仅因都执行 `ALTER USER/ROLE` 就混为同一个语义；
- MariaDB 必须先解决 root 的独立本地管理通道和多个 root principal/auth plugin 的识别；
- Provider 验证成功后，使用 candidate 投影启动 Nextcloud、LLNG、MeshCentral、Authentik 等消费者。

### 8.2 Samba DC

Samba Module 在下游停止时协调五个独立账号：

| 逻辑凭据 | 主要消费者 |
| --- | --- |
| `samba_dc.admin_password` | Samba FS 等日常域管理任务 |
| `samba_dc.ldap_bind_password` | LAM、MeshCentral |
| `samba_dc.password_bind_password` | Nextcloud、LLNG、Authentik |
| `samba_dc.anchor_bind_password` | Anchor Worker |
| `samba_dc.administrator_password` | DC 恢复、结构和控制操作 |

前四项先协调，Administrator 最后协调；回滚顺序相反。Probe 使用 LDAP/Kerberos 或安全 verifier，
reconcile 使用容器内本地 Samba 管理能力。Samba Module 通过 ready barrier 后，下游按 candidate
投影启动并各自验证目录连接。密码历史、最短期限和锁定策略必须有真实容器测试。

### 8.3 Eturnal shared secret

`eturnal.secret` 是环境/配置型 shared secret，不存在数据库账号重置：

1. Traefik 等上游先 ready；
2. Eturnal 使用 candidate `TURN_SECRET` 启动并验证实际运行配置；
3. Nextcloud 启动时幂等检查 Talk TURN 配置，不一致才用 `occ` 更新；
4. NetBird 启动时重新生成并验证 Management 配置。

消费者启动协调替代当前逐个在线 apply Hook，但保留真实配置和认证验证，不能只检查容器 Env。

### 8.4 本地管理员

- bcrypt/file-provider 型账号先比较期望明文与现有 hash，匹配时不重新生成 hash；
- 应用数据库型账号使用受限管理 API/CLI probe 和 reconcile；
- bootstrap-only 投影必须改为 deployment-scoped，previous/candidate 不能共享可变密码文件；
- TTY 用户输入仍不得进入 argv；用户输入值默认 external，只有明确 takeover 才变为 ANAS-owned。

### 8.5 不适用普通协调的材料

- 外部 DNS/API token：只有接入厂商 create/verify/revoke API 后才能自动轮换；
- OIDC/SAML client secret、签名证书：需要 overlap 期和旧值退役；
- session/cookie secret：必须声明强制登出影响；
- 数据加密主钥：需要迁移和重加密；
- 应用数据目录内未声明的密钥：先纳入结构化库存，不能按文件名猜测。

## 9. CLI 合同（首批已实现）

```text
anas credential list [-w WORKSPACE] [--json]
anas credential rotate CREDENTIAL_ID [-w WORKSPACE] [--force] [--dry-run] [-y] [--json]
anas credential rotate --all [-w WORKSPACE] [--force] [--dry-run] [-y] [--json]
```

- `list` 永不返回值、hash 或可用于离线猜测的摘要；至少返回 ID、owner、kind、authority、代次、
  consumers、rotation mode、状态和原因；
- `--dry-run` 使用真实 planner，但不生成随机数、不创建 candidate、不调用 Hook/Docker；
- `--force` 只改变有 ANAS generator 和完整协调器的用户覆盖值之 authority；
- 非交互实际执行必须 `-y`；
- `--all` 是全部署停机操作，输出必须包含预计停止 Module、激活顺序和 blockers；
- 不提供明文密码参数、环境变量输入或 `--allow-partial`。

建议状态：`rotatable`、`manual`、`unsupported`、`orphaned`、`recovery_required`。失败结果必须区分
candidate failed、previous restored、previous restore failed 和 not started，不能把补偿失败报告为成功。

## 10. 实施基线与迁移顺序

### 10.0 2026-08-20 实施状态

| 项目 | 状态 | 说明 |
| --- | --- | --- |
| Frozen credential deployment schema | 已接入 materialization | 从 Module 静态声明生成；authority/generation 来自 Secret Store provenance，manifest 无明文 |
| 共用无副作用 planner | 已接入 CLI | `--dry-run` 使用真实 planner，并检查 runtime 与 Store presence/generation；零随机、零写入、零 Hook/Docker |
| Candidate 独立投影与封印 | 已接入生产 executor | previous/Store/active pointer 在验证前不变；只重写 candidate 的 owner/consumer 投影 |
| Journal、代次与一次性 Store commit | 已接入生产 executor | Store 一次 Save 提交全部候选；排他操作自动按 commit 前/后语义恢复或完成 promotion |
| 顺序 Module ready barrier | 已接入现有 start/apply/rollback | 屏障顺序为 container→credential lifecycle→after-start→local-admin；owner verify 失败不启动 consumer |
| 结构化 credential Hook ABI | 已实现 | 显式 phase、专用 stdin Secret、严格 response、external 禁止 reconcile；Eturnal 为首个 provider |
| Previous 启动失败补偿 | 已强化 | candidate 先停止，随后 previous 重新激活；Eturnal 可由同一幂等协调器恢复，其他持久凭据仍未迁移 |
| `anas credential list/rotate` | 已实现首批 | 无明文 list、共享 planner dry-run、单项/全量执行与结构化失败状态；当前生产 provider 为 Eturnal |

因此当前完成了阶段 A 的 candidate/journal/executor/recovery 闭环和阶段 B 的静态
schema/ABI/ready-barrier 主干。Eturnal 已获得 deployment 驱动的启动时协调与主动轮换能力；
数据库、Samba、本地管理员、resource credential 和 overlap/migrate 类型仍未获得统一轮换能力。

### 阶段 A：事务与 deployment 基础

1. Candidate deployment 与 deployment-scoped Secret 投影；
2. 持久事务 journal、rotation ID 和 Secret generation；
3. 顺序 Module activation 与 ready barrier；
4. previous deployment 自动恢复；
5. 单项 ID 和 `--all` 共用 planner/executor。

### 阶段 B：统一幂等 ABI

1. **已完成**：Module 静态 inventory、frozen record 生成、probe/reconcile/verify 请求与结果；
2. **已完成首批**：Eturnal 与 Nextcloud/NetBird consumer 迁移为启动时协调器；
3. **已完成基线清理**：旧 active deployment `.env` 原地修改和逐目标 Store 提交实现未保留；
4. **待完成**：PostgreSQL 与四个本地管理员 handler 迁移；PostgreSQL 必须先退出无法验证密码的
   `trust` 基线并建立独立本地管理通道；
5. **待完成**：`admin local rotate` 改为统一 executor 的兼容适配器。

### 阶段 C：数据库全覆盖

1. MariaDB root 本地管理与验证；
2. PostgreSQL/MariaDB resource credential 协调；
3. resource consumer 顺序激活；
4. 普通账号先、控制账号最后的正向与回滚测试。

### 阶段 D：Samba 全覆盖

1. 五个账号的独立声明和控制图；
2. Samba 本地 probe/reconcile/verify；
3. LAM、MeshCentral、Nextcloud、LLNG、Authentik、Samba FS、Anchor Worker 启动验证；
4. 密码历史、锁定、进程崩溃和 previous deployment 恢复测试。

### 阶段 E：其余凭据类型

按风险分别实现 Module 私有管理员、内部 shared secret、overlap、migrate 和外部 provider；不得用
普通 password reconcile 代替双钥匙或数据迁移流程。

## 11. 验收标准

- Inventory 无明文覆盖 config、Store、local admin、resource 和 deployment 声明；
- dry-run 零随机、零写入、零 Hook/Docker 调用；
- 全体轮换只停止旧 deployment 一次，candidate Module 各正式激活一次；
- 下游在 owner credential verify 前绝不启动；
- 重复启动同一 deployment 不修改已匹配密码；
- candidate 任意阶段失败后，previous deployment 能恢复真实数据库/AD/应用凭据；
- kill -9 覆盖每个 journal phase，恢复结果确定且不泄密；
- Store、active pointer、deployment generation 和实际认证状态最终一致；
- 日志、JSON、Hook stderr、Docker 命令行和审计记录均不包含 Secret；
- 真实容器 E2E 覆盖 PostgreSQL、MariaDB、Samba、Eturnal 及至少一个实际消费者。

这套设计把“轮换”定义为 deployment 期望状态的可验证切换，而不是 Secret Store 的批量重写。
只有 Module 能证明自身凭据可探测、可协调、可验证且可由 previous deployment 恢复时，Core 才
允许它进入自动轮换。
