# ANAS 运行时、Deployment 制品与状态记录统一方案

本文基于基线提交 `a6d3318` 的实际实现，统一评估并重新设计以下内容：

- 启动、重启、构建、渲染、应用和回滚的命令语义；
- 环境变量、生成密钥和 hook 输入的记录与分发方式；
- 用户配置、已解析配置和已应用状态的职责；
- module 版本锁、能力绑定和 deployment 版本快照；
- 发布失败、进程崩溃、连续回滚和垃圾回收。

项目尚未发布，本方案不保留当前 CLI 和运行目录的兼容语义，优先选择职责
清晰、可验证、可恢复的最终模型。

## 1. 结论

当前实现的三个方向应保留：

1. release 是不可变制品，无配置启动时不重新 calculate/render；
2. 每个 module 的环境按 owner、依赖闭包和 `consumes` 收窄；
3. hook 编译后冻结进 release，制品启动不依赖 Go 工具链。

但当前整体并非最优，主要原因是：

1. `render`、`build`、`start -c` 都可能创建并晋升 release，而只有部分路径
   会提交 lock、capability bindings 和 applied config；
2. `config.yml` 同时充当用户期望配置、release 启动输入和回滚依据，导致
   制品仍依赖解析当前源码中的 manifest；
3. `module.lock.yml` 同时承担解析输入、当前运行版本记录和升级基线，候选构建
   与活动状态会互相污染；
4. `.env` 同时承担 Compose 插值、hook 输入快照和容器运行环境，权限边界仍
   不够细；
5. generated secret 的敏感性依靠值相等推断，且 secret store 只有可变的
   当前值，无法准确表达某个 release 使用的是哪一代 secret；
6. `release`/`release.previous` 只给旧 release 保存 lock snapshot，不能可靠
   连续回滚；
7. 文件目录晋升是原子的，但 Compose 对账、健康检查和 active state 提交不是
   一个可恢复事务；
8. rollback 不检查 change policy：跨 `credential_rotate`/`data_migrate`
   边界的回滚会让制品状态与数据库等外部状态互相矛盾。

推荐目标是：**配置描述期望，lock 固定解析，deployment 封装执行，active state
只指向已验证制品，secret 以版本引用，故障恢复依靠向 active deployment 的
幂等收敛而不是重型事务。**

## 2. 现状职责评估

### 2.1 启动方式

当前命令行为：

| 命令 | 当前行为 | 评价 |
| --- | --- | --- |
| `plan -c` | 解析配置与依赖 | 基本合理，但应保证严格只读 |
| `render -c` | 渲染并直接替换活动 `release` | 不合理；render 不应改变活动制品 |
| `build -c` | 渲染、构建并替换 `release` | 不合理；构建成功不等于部署成功 |
| `start -c` | 渲染、对账、晋升、执行 hook、提交部分状态 | 职责过多，是当前状态分裂的主要来源 |
| `start` | 启动当前 release | 方向正确 |
| `restart` | down/up 当前 release | 语义明确 |
| `rollback` | 交换两个目录并恢复可选 lock snapshot | 只能可靠回退一次，且完全不检查 change policy |

远端实测已经确认：`render -> start` 后服务可以运行，但
`module.lock.yml` 和 `state/config-applied.yml` 都不存在；第一次 rollback
后，新的 `release.previous` 没有 lock snapshot。

### 2.2 环境变量记录

当前每个 module 的 `.env` 已经按作用域过滤，这是明显进步。但一个文件仍有
三种消费者：

1. Docker Compose `${KEY}` 插值；
2. `env_file: .env` 注入容器；
3. runner 在 artifact start 时恢复 hook/Compose 输入。

这三者需要的键集合不同。即使 module 级隔离正确，同一 module 内的辅助容器仍
可能拿到主服务凭据。另一个问题是 owner/sensitive 仍是运行时推断：secret
store 中的键本应天然敏感，不应通过“其值是否也出现在 env”来判断。

### 2.3 配置文件记录

当前把原始 `config.yml` 复制进 release，并在 artifact start 时重新使用
当前 runner 和当前 manifest 解析它。由此产生两个问题：

- release 不是完全自描述；manifest 的依赖、runtime type 或 compose 文件名
  变化后，旧 release 可能无法启动；
- 原始配置只包含显式值，不包含默认值、自动 provider 选择、生成 secret
  版本和最终模块顺序，不能完整解释实际运行状态。

`state/config-applied.yml` 只保存显式配置哈希，可防止部分危险修改，但无法
发现 manifest 默认值变化、能力自动绑定变化、镜像 digest 变化或 secret
代次变化。

### 2.4 锁定文件

当前全局 `module.lock.yml` 混合了两种不同职责：

- **解析锁**：下次 resolve 时稳定选择 module 版本和 capability provider；
- **部署记录**：描述当前活动 release 实际用了什么。

解析候选时不应覆盖活动部署记录，活动部署也不应反过来成为唯一的项目解析
输入。二者必须分开。

## 3. 核心模型：五类状态只有一个权威来源

| 状态 | 权威来源 | 是否可变 | 是否含明文 secret |
| --- | --- | --- | --- |
| 用户期望状态 | 用户维护的 `config.yml` | 是 | 可能包含用户 secret |
| 解析锁 | config 同目录 `<config-name>.lock.yml` | 显式更新 | 否，只含版本、digest、绑定和 source |
| deployment 制品 | `deployments/<deployment-id>/` | 否 | module 运行文件可能包含，目录整体按敏感数据保护 |
| 活动运行状态 | `state/active.yml` | 仅 apply/rollback 提交 | 否 |
| 生成密钥 | `secrets/` 的版本化 secret store | 追加式 | 是 |

关键不变量：

1. 只有 `apply` 和 `rollback` 可以修改 `state/active.yml`；
2. `render`、`build`、`plan` 永远不能切换活动 deployment；
3. `start`、`stop`、`restart` 永远不能 calculate、render、修改 lock 或生成
   secret；
4. 每个 deployment 都携带完整、不可变的部署 manifest，不依赖原始 config 才能
   启停；
5. 每个 release 引用准确的 secret generation，不能只引用“当前值”；
6. active state 只指向已经通过 verify 的 deployment；
7. 所有状态文件均使用临时文件、`fsync`、原子 rename 写入；同一 base 使用
   文件锁禁止并发 apply/rollback；
8. deployment 不变性只覆盖制品文件；`data/` 和服务内部状态（数据库里的
   口令、schema、业务数据）不属于 deployment，也不随普通 rollback 回退；
9. 容器只允许 bind mount 自身 deployment 目录内的文件或 `data/`；任何被
   复制或写入到 deployment 目录之外的内容（`docker cp`、外部配置）不得
   包含 deployment 路径，否则 GC 旧 deployment 会留下悬空引用；
10. rollback 与 apply 执行同一套 change-policy 守卫（见 6.1）。

## 4. 运行目录

运行状态位于 workspace 的 `.anas/` 下。workspace 是一次部署拥有的全部内容——配置、
业务数据、快照、运行状态——目的是让"备份一个目录"等价于"备份整套部署"。完整设计见
[workspace-backup-plan.md](../workspace-backup-plan.md)。

```text
<workspace>/
  config.yml                   # 用户期望状态，唯一需要手工维护的文件
  config.lock.yml              # 解析锁，由 config 路径推导
  data/                        # 业务数据；位置固定，不可配置
  snapshots/                   # 时间点副本，与 .anas 平级而非嵌套
  .anas/                       # 以下为运行状态，0700
```

`data/` 没有可配置的位置。可配置就意味着"复制一个目录"不再等价于完整备份，而那正是
本布局要保证的事；需要把数据放在大盘上的用户，把整个 workspace 放过去。

`snapshots/` 与 `.anas/` 平级而不是放在其中：数据恢复会整体替换数据目录，嵌套会让
一次恢复顺手把运行状态也换掉。

```text
<workspace>/.anas/
  state/
    active.yml                 # 唯一活动指针与最后验证结果
    index.yml                  # 可重建的 deployment 状态汇总
    deployments/
      <deployment-id>.yml      # ready/active/previous/failed 等可变状态
    lock                       # apply/rollback 排他锁
    transactions/
      <transaction-id>.yml     # 诊断性事务标记（阶段、候选 ID、宿主副作用补偿）
  deployments/
    <deployment-id>/
      deployment.yml           # 自描述且不可变的部署 manifest
      resolved.redacted.yml    # 最终解析结果，secret 仅保留引用/哈希
      lock.yml                 # 本 deployment 使用的解析锁快照
      modules/
        <name>/
          compose.yml
          compose.env          # 仅 Compose 插值所需，0600
          services/
            <service>.env      # 单容器最小运行环境，0600
          hook
          assets/...
  staging/
    <deployment-id>/           # 只允许 render/build/validate，禁止启动容器
  secrets/
    store.yml                  # key -> generations（值 + 元数据），单文件，0600
```

快照不在 `.anas/` 内，见上方 workspace 布局与
[contracts/snapshot.md](contracts/snapshot.md)。

不再通过 rename 交换 `release` 和 `release.previous`。deployment ID 是
"时间戳 + 随机后缀"的唯一标识符，明确不做内容寻址：内容 hash 在物化时
无法确定（image digest 要到 build/pull 之后才固定），`deployment.yml` 又
包含 ID 自身，自引用无法闭合；去重在这个规模下也没有价值。制品完整性
由 `deployment.yml` 中逐文件的 artifact digest 负责，而不是由 ID 负责。

`staging` 不是一个可运行的 deployment 类型，只是同一文件系统内的临时物化
目录。render/build/validate 完成后，先原子 rename 到最终
`deployments/<id>`，再从最终目录执行 Compose `up`。因此 Docker Compose
记录的 working directory/compose file 永远是最终路径，不会因移动 compose
文件而失效。失败的 staging 可以整体删除，不能直接启动其中的容器。

`state/active.yml` 原子切换引用。它只保存 release ID 和验证结果，不
重复 release manifest 里已有的指纹，避免两处漂移：

```yaml
api_version: anas.state/v2
active_deployment: 20260720T081530Z-8f3c12ab
runtime_status: running
previous_deployments:
  - 20260719T163200Z-31aa920d
activated_at: 2026-07-20T08:16:02Z
verified_at: 2026-07-20T08:16:18Z
transaction: apply-20260720T081530Z
```

这样可以连续回滚任意仍在保留期内的 deployment，不再需要
`.module.lock.snapshot`。`state/`、`staging/`、`deployments/` 必须位于同
一文件系统，晋升和状态提交才能用原子 rename 完成；`data/` 可以单独
挂载。

每 release 独立目录意味着 module 工作目录路径每次 apply 都会变化，这是
上文第 9 条不变量存在的原因：hook 派生的路径、compose bind mount 和
`docker_copies` 的目标都不得把 release 路径固化到 release 目录与
`data/` 之外。迁移时需要审计现有 module（samba_dc 证书路径、postgres
init 脚本等）是否违反这一条。

## 5. Deployment manifest

`deployment.yml` 是 artifact start/stop/restart 的唯一结构化输入：

```yaml
api_version: anas.deployment/v1
id: 20260720T081530Z-8f3c12ab
created_at: 2026-07-20T08:15:30Z
config_fingerprint: sha256:...
lock_fingerprint: sha256:...
module_order: [lego, traefik, postgres, nextcloud]
capability_bindings:
  nextcloud:
    relational_database: postgres
modules:
  traefik:
    module_version: 1.2.0
    app_version: 3.2.0
    runtime: compose
    compose_file: modules/traefik/compose.yml
    compose_env: modules/traefik/compose.env
    hook: modules/traefik/hook
    artifact_digest: sha256:...
    image_digests:
      - traefik@sha256:...
    secret_refs:
      TRAEFIK_ADMIN_PASSWORD: TRAEFIK_ADMIN_PASSWORD@2
```

启动旧 deployment 时不再调用 `loadRegistry` 重新解释当前 module manifest。
runner 只校验 deployment ABI、artifact digest 和本机依赖，然后按
`module_order` 执行。

release ABI 需要明确的兼容政策：runner 声明支持的 ABI 区间，区间外的
release 拒绝 start/rollback 并提示 re-apply，而不是尝试猜测。否则
"旧 release 永远能启停"只是空头承诺。另外，冻结的 hook 二进制是平台
相关的，release 不可跨机器或跨架构迁移；这与"备份单元不含 release"
（第 13 节）一致。

## 6. 命令语义

项目未发布，推荐直接收敛命令，不保留 `start -c`：

| 命令 | 新语义 | 是否改变 active state |
| `anas plan -c config.yml` | 解析和校验；严格不读写 runtime base | 否 |
| `anas lock -c config.yml` | 显式生成/更新同目录 `<config-name>.lock.yml` | 否 |
| `anas render -c config.yml` | 生成 ready deployment，输出 deployment ID | 否 |
| `anas build -c config.yml` | render 并构建 ready deployment | 否 |
| `anas apply -c config.yml [--build]` | 完整部署事务，验证后提交 active | 是 |
| `anas apply --deployment <id>` | 激活一个已存在的 ready deployment | 是 |
| `anas status` | 显示 active deployment 和验证结果 | 否 |
| `anas start` | 幂等启动 active deployment | 否 |
| `anas restart` | 明确 down/up active deployment | 否 |
| `anas stop` | 停止 active deployment | 否 |
| `anas rollback [deployment-id]` | 经配置与 module 版本门禁后回到目标 deployment | 是 |
| `anas deployments list/inspect` | 查看 deployment 状态索引与不可变 manifest | 否 |

`render` 和 `build` 不再“顺便部署”。如果用户希望一步完成，唯一入口是
`apply`；`apply --deployment` 让 render/build 产出的 ready deployment
可以单独激活，apply 因此是可组合的终点而不是另一条平行管线。

### 6.1 Rollback 安全边界

rollback 不是纯粹的指针切换。secret generation 只能回退文件状态，回退
不了外部系统状态：如果 generation 2 的口令已经通过 `credential_rotate`
写入数据库，回滚到引用 `@1` 的 release 后，容器拿到旧口令而库内存的是
新口令，连接直接失败；跨越 `data_migrate` 边界回滚则让旧版应用面对新
版数据。

因此 rollback 必须执行与 apply 相同的变更计划比较（目标 deployment 的
resolved state vs 当前 active deployment），命中 `credential_rotate`、
`data_migrate`、`immutable` 策略时默认拒绝。即使配置完全相同，只要 module
或 app 版本发生反向变化，也按“数据兼容性未知”拒绝。操作者必须选择：

1. 已完成外部迁移/验证后使用 `--allow-risky`，只回退制品；
2. 存在匹配快照时使用 `--restore-data --yes`，同时恢复数据。

`rollback.snapshot.backend: btrfs` 可把 `global.data_path`（或显式 `source`）
作为 Btrfs 子卷，在 apply 前停旧 deployment 并创建只读快照。快照只允许
用于它记录的 `from_deployment -> to_deployment` 边界，不能错配恢复。恢复
前先停止当前 deployment；当前数据子卷不会直接删除，而是重命名为
`.rollback-recovery-<snapshot-id>`，再从只读快照建立新子卷。

```yaml
rollback:
  snapshot:
    backend: btrfs
    source: /srv/anas/data       # 省略则使用 global.data_path
    root: /srv/anas/.snapshots   # 必须与 source 位于同一 Btrfs 文件系统
```

Btrfs 是数据回退后端，不是兼容性证明：快照创建失败时 apply 失败并重新启动
旧 deployment；没有快照时门禁仍然生效。后续可在同一接口下增加 ZFS/LVM，
但普通目录复制不应伪装成原子、一致的在线快照。

## 7. Apply 事务

`apply` 使用显式状态机：

```text
resolve
  -> plan（含 change-policy 守卫）
  -> allocate secret generations
  -> materialize staging
  -> validate artifact and compose
  -> build/pull images and pin digest
  -> finalize deployment（先原子移动到最终路径）
  -> preflight host resources
  -> reconcile deployment runtime
  -> verify services
  -> commit active state
  -> garbage-collect by retention policy
```

image digest 在 materialize 之后才固定，所以最终 manifest 的写入和封印
必须排在 pin 之后；staging 内容允许变化，进入 deployments 后不允许。

单机 Docker 无法参与原子事务。本方案不建可重放的多阶段 journal 状态
机——那套机制的复杂度会超过业务本身——而是依靠两条结构性保证做恢复：

1. `state/active.yml` 只在 verify 通过后原子提交，任何时刻都指向最后
   一个验证过的 deployment；
2. staging 目录和未被任何 manifest 引用的 secret generation 随时可以
   整体丢弃。

崩溃后的恢复动作因此是幂等收敛：`anas start` 把 Docker 状态收敛回
active deployment，未来的 `anas deployments gc` 清理残留 staging 和无引用
generation。journal 降级为诊断性标记文件，记录 deployment ID、当前阶段
和时间，供 `status` 展示与排错，不承载 resume/abort 状态机。唯一需要
显式补偿记录的是宿主机网络（macvlan）这类 Compose project 之外的副作
用。

如果 reconcile 或 verify 失败：

1. 对已更新的稳定 Compose project 用旧 release 再执行 `up -d`；
2. down 仅存在于候选 release 的新增 project；
3. 恢复旧 release 的主机网络配置；
4. 将候选标为 failed，不修改 active state；
5. 新分配但未被任何 release 引用的 secret generation 留待 gc 清理。

存在未完成标记时，`start/stop/restart` 仍然允许运行——`start` 本身就是
向 active deployment 收敛的恢复动作；被阻塞的只有新的 apply/rollback/gc，
它们要求先收敛或显式清理标记。

## 8. 环境变量与 secret 图

### 8.1 用显式值图替代启发式 owner

resolve 阶段的每个值都应携带元数据：

```go
type ResolvedValue struct {
    Key            string
    Producer       string       // global、user 或 module name
    Source         ValueSource  // config/default/hook/capability/secret
    Classification Classification // public/internal/sensitive
    Consumers      []string
    SecretRef      string       // 非 secret 为空
    Value          string       // 仅在受控内存和物化阶段存在
}
```

规则：

- secret store 中的任何键天然是 `sensitive`；
- `config.secrets` 天然是 `sensitive`；
- manifest `changes.sensitive` 是额外分类信号，而不是 secret 判断的唯一来源；
- 禁止使用“值与某个 secret 相等”推断敏感性；
- `Consumers` 必须来自声明（manifest `consumes` 加上 8.2 的
  per-service env 声明），不允许退回 closure/prefix 推导——否则值图只是
  把现有启发式换了个数据结构，没有解决过度广播；
- calculate 阶段的输入保持特权（可读全量 env 与 secrets），约束落在
  输出侧；只有 render/services/after_start 的输入收窄到 contract。否则
  samba_dc 这类在派生阶段读取大量跨 module 键的 hook 会全部断掉；
- hook calculate 的输出必须声明 producer、classification 和 export contract；
- capability 输出使用类型化 capability 名称，不依赖任意全局 env 键覆盖。

### 8.2 分开三类环境

1. `compose.env`：只包含 compose 文件 `${KEY}` 插值需要的键；
2. `services/<service>.env`：只包含该容器进程真正需要的键；
3. hook request：运行时按 manifest contract 临时构造，不保存全量 secrets
   快照。

容器级拆分需要新的 manifest 契约：module.yml 为每个 service 声明它需要
的 env 键集合（例如 `modules.<name>.config: [KEY...]`），初值可以从
compose 文件中的 `${KEY}` 插值和 `environment:` 引用自动推导，再由声明
显式补充。没有这个 schema，per-service env 无从生成，阶段 3 无法开工。

逐步移除 compose 中的 `env_file: .env`，改为每个服务显式引用自己的 env
文件或 `environment:` 映射。例如数据库密码只进入数据库主容器和明确声明
的消费者，adminer、redis、imaginary 等辅助容器不自动继承。

`internal_env` 可继续存在，但应变成值图的 classification/consumer 属性，
不再由 hook 事后返回一组要删除的键。

### 8.3 Secret generation

secret store 采用追加式版本，单文件（`secrets/store.yml`）同时保存值与
元数据，避免值文件与 metadata 文件漂移：

```yaml
POSTGRES_PASSWORD:
  generations:
    1:
      value: ...
      created_at: ...
    2:
      value: ...
      created_at: ...
```

需要认清 generation 的真实收益边界：release 的 env 文件本来就物化了它
使用的 secret 值（release 目录整体按敏感数据保护），rollback 拿到正确
旧值并不依赖 generation store。generation 的价值在于三点：
`anas secret get` 与 active deployment 一致、旧 deployment 被 GC 后值不丢失、
以及变更计划中的 secret 代次检测。按这个边界实现，不要扩展。

release 引用 `POSTGRES_PASSWORD@1`，新候选可引用 `@2`。rollback 后
`anas secret get POSTGRES_PASSWORD` 默认解析 active deployment 的 generation，
不会返回另一个 release 的密码。GC 不维护引用计数或 orphan 标记：一个
generation 可删除，当且仅当扫描所有保留 deployment 与 staging 的
manifest 后，没有任何 `secret_refs` 引用它。

## 9. 配置状态

### 9.1 用户配置

用户的 `config.yml` 只表示期望状态，是唯一需要人工维护的配置。runner 不
应修改它，除非用户显式执行 `config set`。

release 不再保存原始配置作为启动输入。为了审计，保存
`resolved.redacted.yml`：

- 包含所有默认值、自动绑定、最终模块顺序和值来源；
- 生成 secret 以 `secretRef`（`KEY@generation`）表示；用户提供的
  secret 使用 HMAC 指纹，HMAC key 保存在 `secrets/` 内——这个文件的
  定位是可安全 inspect 的审计文件，普通 sha256 摘要会给低熵用户口令
  提供离线爆破材料，不可用；
- 保留完整 config fingerprint；
- 可用 `anas deployments inspect` 解释任意活动或历史 deployment。

如果用户希望完整灾备，应备份原始 config 和 secret store；不要把“复制一份
明文 config 到每个 release”当成配置备份机制。

### 9.2 Applied state

删除独立的 `state/config-applied.yml`。其职责并入 active deployment manifest
和 `state/active.yml`。变更计划比较：

```text
new resolved state
vs
active deployment resolved.redacted.yml + deployment.yml
```

比较范围必须包括：

- 用户显式配置；
- manifest 默认值；
- module/app 版本和 image digest；
- capability bindings；
- secret generation；
- 模块集合、顺序和服务集合；
- hook 和模板 artifact digest。

这样 `credential_rotate`、`data_migrate`、`immutable` 守卫不会因为
`AppliedAt` 缺失而失效，也能发现默认值或 provider 漂移。

应用内部 observed state 仍与 desired/applied state 分开。后续 module
reconciler 可把探测摘要写入 transaction verification result，但不能把容器
当前状态反写为用户期望配置。

## 10. Lock 文件

拆成两层：

### 10.1 项目解析锁 `<config-name>.lock.yml`

定义为 config 文件的 sibling（例如 `prod.yml` 对应 `prod.lock.yml`）：`-c`
指向哪个目录，lock 就在哪个目录，
而不是模糊的"项目侧"。由用户提交或备份，包含：

- config schema 和 module ABI；
- module packaging version、source 和 source digest；
- capability provider bindings；
- image tag 解析后的 digest；
- hook/template bundle digest。

只有 `anas lock` 或带明确 `--update-lock` 的命令能更新。普通 plan/apply 在
lock 与 config 不一致时给出 diff 并失败，不能静默改锁。

### 10.2 Deployment lock snapshot

每个 deployment 内保存其使用的 `lock.yml`，并由 `deployment.yml` 记录
checksum。这是部署证据，不可修改。rollback 直接使用目标 deployment 的
snapshot，不修改
项目侧 lock，也不存在全局 `.module.lock.snapshot`。

### 10.3 Runner 与 module bundle 分离

runner 可执行文件不再假设 module 必须和源码仓库同目录。解析/物化命令按以下
顺序寻找独立 bundle 根：`--module-root`、`ANAS_MODULE_ROOT`、约定安装目录；
项目 lock 同时记录 module version、source 标识和整目录 SHA-256。相同版本但
内容被改动时，普通 render/apply 拒绝，必须显式 `anas lock` 或
`--update-lock`。

分离边界是：plan/lock/render/build/apply 需要 module bundle；已经物化的
start/stop/restart/rollback 只读取 `deployment.yml`、冻结的 compose/env/hook，
既不需要 module 源目录，也不需要 Go 工具链。后续 module store/下载器只需把
校验后的 bundle 放进 `ANAS_MODULE_ROOT`，无需改变 deployment ABI。

## 11. 构建和镜像

当前 build 只记录 module version，无法保证同一 tag 下镜像内容不变。目标方案：

1. build/pull 后解析并记录 image content digest；
2. release compose 使用 digest，而不是可变 tag；
3. 自建镜像使用 release ID 或 artifact hash 作 tag；
4. hook 二进制、compose、模板和生成文件全部记录 SHA-256；
5. `start` 前快速校验 artifact digest，发现 release 被人工修改就拒绝启动。

## 12. Verify 与健康提交

Compose `up -d` 成功不等于部署成功。module manifest 应允许声明：

```yaml
verify:
  timeout: 180s
  checks:
    - type: container_health
    - type: tcp
      address: postgres:5432
    - type: command
      service: postgres
      command: [pg_isready, -U, postgres]
```

每类 check 必须定义执行上下文：`container_health` 读取 Docker 健康状
态；`command` 通过 `docker compose exec` 在容器内执行；`tcp` 中的容器
名（如 `postgres:5432`）在宿主机上不可解析，所以 tcp 检查要么限定为宿
主可达地址，要么退化为在某个容器内执行的 command 检查。不定义执行上下
文，示例就是误导。

只有所有 required module 验证通过后才提交 active state。`after_start` 中实际
属于验证的逻辑迁到 verify；必须修改外部状态的 reconcile hook 要幂等，并在
journal 中记录补偿动作。

## 13. 并发、权限与备份

- `.anas/` 根目录、`snapshots/` 和所有 secret/env 文件保持 `0700/0600`；
- `state/lock` 上使用 flock：apply/rollback/gc 取排他锁，
  start/stop/restart 取共享锁，"start 不能与 apply 并发"由锁模式自动
  成立，不需要额外机制；
- release 普通资产在封印后改为只读（`sealDeployment`，rename 进
  `deployments/<id>/` 之前执行）。封印按位清除写权限而非赋固定模式，因此可执行文件
  仍可执行（0755 → 0555）、owner-only 的敏感文件仍是 owner-only（0600 → 0400），其余
  资产落在 0444 —— 容器内的服务用户仍读得到 bind mount 进去的配置。目录保持 0700：
  只读目录会连 unlink 一起挡住，而 unlink-and-replace 分配的是新 inode，恰恰是硬链接
  本来就安全的那种改动，封目录换不来额外保证，却会让 deployment 无法回收。
  该封印是快照复用 deployment 制品时启用硬链接的前提，
  见 [contracts/snapshot.md](contracts/snapshot.md)；
- `deployments/<id>/config.source.yml`（0600）保存该 deployment 构建时所用配置的原文。
  这不违反 §9.1 —— 那条禁止的是把配置当**启动输入**，此处是只读的**恢复用**副本，
  没有它，快照拿不到与自身匹配的配置；
- **备份单元就是整个 `<workspace>/`**。这正是 workspace 布局存在的理由：把它拆成
  一份需要人工核对的路径清单，就等于把"漏掉一项"变成常态。缓存目录
  （`.anas/go-build-cache/`、`.anas/hook-bin/`、`.anas/staging/`）可以排除，
  其余不行；详见 [contracts/backup.md](contracts/backup.md)；
- GC 默认保留 active + 最近两个 verified release + 所有未过期 failed
  staging；被保留 manifest 引用的 deployment 和 secret generation 不能
  删除；
- 镜像是宿主机共享资源，anas 无法区分自己 pull 的镜像和用户或其他工具
  的镜像，因此 gc 不删除镜像，清理交给用户显式 `docker image prune`。

`state/index.yml` 回答“每个 deployment 当前是什么情况”，记录 ID、创建时间、
`ready/active/previous/failed`、激活/停用/验证时间、predecessor、失败原因和
数据快照 ID。它由 `state/deployments/<id>.yml` 重建，不是第二个权威来源；
不可变的构建事实仍只在 `deployments/<id>/deployment.yml`。因此根目录需要
状态索引，但不应把全部历史塞进一个只能追加、损坏后无法恢复的大文件。

## 14. 实施顺序

### 阶段 0：修复当前基线的确定性问题

阶段 0 只保留不会被后续阶段整体删除的修复：

1. secret store 中的所有键默认 sensitive，修复 artifact start hook 泄漏；
2. 修复 `config secret list -b ...` 参数解析；
3. 为以上问题增加远端已复现场景的自动测试。

原先考虑的"统一提交 bindings/applied state"和"每个 release 自带 lock
snapshot"，修的正是阶段 1-2 要删除的机制；项目未发布、无兼容包袱，
这些是纯沉没成本，直接进入阶段 1。

### 阶段 1：命令与 release store

1. 新增 `apply`（含 `--deployment <id>`），移除 `start -c`；
2. `render/build` 只物化 ready deployment，不切换 active；
3. 引入 `deployments/<id>/deployment.yml` 和 `state/active.yml`；
4. artifact start 完全由 release manifest 驱动；
5. rollback 按 release ID 工作，并接入 change-policy 守卫；
6. 新增 `status`；
7. 审计现有 module 对 release 路径的外部固化（不变量 9）。

### 阶段 2：配置与 lock 分层

1. 引入 config 同目录 `<config-name>.lock.yml`；
2. 保存 `resolved.redacted.yml`；
3. 删除 release 中作为启动输入的原始 `config.yml`；
4. 删除全局 mutable `module.lock.yml` 和独立 `config-applied.yml`。

### 阶段 3：值图和容器级环境隔离

1. 定义 per-service env 的 manifest schema（8.2），这是本阶段的前置；
2. 用 `ResolvedValue` 图替换 owner/value-equality 推断；
3. secret generation 版本化；
4. 拆分 `compose.env` 与 service env；
5. 逐 module 移除全量 `env_file`；
6. capability contract 类型化。

### 阶段 4：事务、验证与恢复

1. 诊断性 journal 标记与收敛式 crash recovery（`start` + `gc`）；
2. module verify contract（含每类 check 的执行上下文）；
3. reconcile 失败自动补偿旧 release，macvlan 等宿主副作用记录补偿动作；
4. 基于 manifest 扫描的 release/secret GC；
5. fault injection 测试覆盖 seal、reconcile、commit 与网络补偿等关键点。

## 15. 必须具备的测试

1. `render`、`build` 后 active deployment 和 active state 完全不变；
2. `start/stop/restart` 不修改 config、lock、secret store 或 release 内容；
3. `apply` 在 render/build/reconcile/verify/commit 任一点失败均可恢复；
4. 第一次、第二次及指定 release rollback 都恢复正确 lock、env 和 secret；
5. 新增、移除、改名 module 不遗留 Compose project；
6. manifest 默认值改变能出现在 plan 中；
7. auto provider 不会在 lock 未更新时漂移；
8. 每个 service 只拿到声明的 secret，secret store 任意键默认不跨边界；
9. release 在当前源码 module 已变化或不存在时仍能 start/stop；ABI 区间外
   的 release 被明确拒绝并提示 re-apply；
10. 两个并发 apply 中只有一个能进入事务；apply 期间 start 被共享锁阻塞；
11. 进程在 seal、reconcile、commit 前后被强制终止后，`start` + `gc`
    能收敛回 active deployment，随后可开始新 apply；
12. GC 不删除 active/previous/staging 引用的 deployment 或 secret
    generation；
13. 跨 `credential_rotate`/`data_migrate`/`immutable` 边界的 rollback
    默认被拒绝，`--force` 或显式操作命令才能通过。

## 16. 最终判断

当前方案可以作为从“每次启动都重算”迁向不可变制品的中间版本，但不应作为
最终运行时模型。最优方案不是继续给 `runner.execute` 增加条件分支，而是：

- 用命令边界分离 staging materialization 和 activation；
- 用 release manifest 取代原始 config 作为制品启动输入；
- 用 active pointer 取代可变 release 目录；
- 用项目解析锁 + release lock snapshot 取代全局混合 lock；
- 用版本化 secret 引用和容器级 env 取代值相等推断与 module 全量 env；
- 用 verify + 幂等收敛恢复取代“目录 rename 即部署成功”。

同时要守住两条克制的边界：第一，release 模型保证的是**制品**可回退，
数据和外部系统状态不在承诺范围内，rollback 的 change-policy 守卫就是
这条边界的执行者；第二，这是单机、单用户、Compose 之上薄薄一层的工
具，事务机制的规模不应超过业务本身——active 指针 + 可丢弃 staging +
幂等收敛已经覆盖了重型 journal 的绝大部分价值。

完成阶段 1 和阶段 2 后，当前两个最严重的状态一致性问题会从结构上消失；
阶段 3 和阶段 4 则把安全边界与生产可恢复性补齐。
