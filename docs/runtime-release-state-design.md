# ANAS 运行时、发布制品与状态记录统一方案

本文基于基线提交 `a6d3318` 的实际实现，统一评估并重新设计以下内容：

- 启动、重启、构建、渲染、应用和回滚的命令语义；
- 环境变量、生成密钥和 hook 输入的记录与分发方式；
- 用户配置、已解析配置和已应用状态的职责；
- cask 版本锁、能力绑定和 release 版本快照；
- 发布失败、进程崩溃、连续回滚和垃圾回收。

项目尚未发布，本方案不保留当前 CLI 和运行目录的兼容语义，优先选择职责
清晰、可验证、可恢复的最终模型。

## 1. 结论

当前实现的三个方向应保留：

1. release 是不可变制品，无配置启动时不重新 calculate/render；
2. 每个 cask 的环境按 owner、依赖闭包和 `consumes` 收窄；
3. hook 编译后冻结进 release，制品启动不依赖 Go 工具链。

但当前整体并非最优，主要原因是：

1. `render`、`build`、`start -c` 都可能创建并晋升 release，而只有部分路径
   会提交 lock、capability bindings 和 applied config；
2. `config.yml` 同时充当用户期望配置、release 启动输入和回滚依据，导致
   制品仍依赖解析当前源码中的 manifest；
3. `cask.lock.yml` 同时承担解析输入、当前运行版本记录和升级基线，候选构建
   与活动状态会互相污染；
4. `.env` 同时承担 Compose 插值、hook 输入快照和容器运行环境，权限边界仍
   不够细；
5. generated secret 的敏感性依靠值相等推断，且 secret store 只有可变的
   当前值，无法准确表达某个 release 使用的是哪一代 secret；
6. `release`/`release.previous` 只给旧 release 保存 lock snapshot，不能可靠
   连续回滚；
7. 文件目录晋升是原子的，但 Compose 对账、健康检查和 active state 提交不是
   一个可恢复事务。

推荐目标是：**配置描述期望，lock 固定解析，release 封装执行，active state
只指向已验证制品，secret 以版本引用，transaction journal 负责故障恢复。**

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
| `rollback` | 交换两个目录并恢复可选 lock snapshot | 只能可靠回退一次 |

远端实测已经确认：`render -> start` 后服务可以运行，但
`cask.lock.yml` 和 `state/config-applied.yml` 都不存在；第一次 rollback
后，新的 `release.previous` 没有 lock snapshot。

### 2.2 环境变量记录

当前每个 cask 的 `.env` 已经按作用域过滤，这是明显进步。但一个文件仍有
三种消费者：

1. Docker Compose `${KEY}` 插值；
2. `env_file: .env` 注入容器；
3. runner 在 artifact start 时恢复 hook/Compose 输入。

这三者需要的键集合不同。即使 cask 级隔离正确，同一 cask 内的辅助容器仍
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

当前全局 `cask.lock.yml` 混合了两种不同职责：

- **解析锁**：下次 resolve 时稳定选择 cask 版本和 capability provider；
- **部署记录**：描述当前活动 release 实际用了什么。

解析候选时不应覆盖活动部署记录，活动部署也不应反过来成为唯一的项目解析
输入。二者必须分开。

## 3. 核心模型：五类状态只有一个权威来源

| 状态 | 权威来源 | 是否可变 | 是否含明文 secret |
| --- | --- | --- | --- |
| 用户期望状态 | 用户维护的 `config.yml` | 是 | 可能包含用户 secret |
| 解析锁 | 项目侧 `anas.lock.yml` | 显式更新 | 否，只含版本、digest、绑定和 source |
| release 制品 | `releases/<release-id>/` | 否 | cask 运行文件可能包含，目录整体按敏感数据保护 |
| 活动运行状态 | `state/active.yml` | 仅 apply/rollback 提交 | 否 |
| 生成密钥 | `secrets/` 的版本化 secret store | 追加式 | 是 |

关键不变量：

1. 只有 `apply` 和 `rollback` 可以修改 `state/active.yml`；
2. `render`、`build`、`plan` 永远不能切换活动 release；
3. `start`、`stop`、`restart` 永远不能 calculate、render、修改 lock 或生成
   secret；
4. 每个 release 都携带完整、不可变的部署 manifest，不依赖原始 config 才能
   启停；
5. 每个 release 引用准确的 secret generation，不能只引用“当前值”；
6. active state 只指向已经通过 verify 的 release；
7. 所有状态文件均使用临时文件、`fsync`、原子 rename 写入；同一 base 使用
   文件锁禁止并发 apply/rollback。

## 4. 推荐运行目录

```text
~/.anas/
  state/
    active.yml                 # 唯一活动指针与最后验证结果
    transactions/
      <transaction-id>.yml     # 可恢复的 apply/rollback journal
  releases/
    <release-id>/
      release.yml              # 自描述部署 manifest
      resolved.redacted.yml    # 最终解析结果，secret 仅保留引用/哈希
      lock.yml                 # 本 release 使用的解析锁快照
      casks/
        <name>/
          compose.yml
          compose.env          # 仅 Compose 插值所需，0600
          services/
            <service>.env      # 单容器最小运行环境，0600
          hook
          assets/...
  candidates/
    <release-id>/              # 尚未激活的完整候选制品
  secrets/
    values.yml                 # key + generation -> value，0600
    metadata.yml               # 创建时间、来源、状态、非明文摘要
  data/                         # 可选默认业务数据根；永不放进 release
```

不再通过 rename 交换 `release` 和 `release.previous`。release 目录以内容 hash
为 ID 永久不变，`state/active.yml` 原子切换引用：

```yaml
api_version: anas.state/v2
active_release: 20260720T081530Z-8f3c12ab
previous_releases:
  - 20260719T163200Z-31aa920d
config_fingerprint: sha256:...
activated_at: 2026-07-20T08:16:02Z
verified_at: 2026-07-20T08:16:18Z
transaction: apply-20260720T081530Z
```

这样可以连续回滚任意仍在保留期内的 release，不再需要
`.cask.lock.snapshot`。

## 5. Release manifest

`release.yml` 是 artifact start/stop/restart 的唯一结构化输入：

```yaml
api_version: anas.release/v2
id: 20260720T081530Z-8f3c12ab
created_at: 2026-07-20T08:15:30Z
config_fingerprint: sha256:...
lock_fingerprint: sha256:...
module_order: [core, lego, traefik, postgres, nextcloud]
capability_bindings:
  nextcloud:
    relational_database: postgres
casks:
  traefik:
    cask_version: 1.2.0
    app_version: 3.2.0
    runtime: compose
    compose_file: casks/traefik/compose.yml
    compose_env: casks/traefik/compose.env
    hook: casks/traefik/hook
    artifact_digest: sha256:...
    image_digests:
      - traefik@sha256:...
    secret_refs:
      TRAEFIK_ADMIN_PASSWORD: TRAEFIK_ADMIN_PASSWORD@2
```

启动旧 release 时不再调用 `loadRegistry` 重新解释当前 cask manifest。runner
只校验 release ABI、artifact digest 和本机依赖，然后按 `module_order` 执行。

## 6. 命令语义

项目未发布，推荐直接收敛命令，不保留 `start -c`：

| 命令 | 新语义 | 是否改变 active state |
| --- | --- | --- |
| `anas plan -c config.yml` | 解析、校验并与 active release 比较；新 secret 只显示“将生成” | 否 |
| `anas lock -c config.yml` | 显式生成/更新项目侧 `anas.lock.yml` | 否 |
| `anas render -c config.yml` | 生成 candidate release，输出 release ID | 否 |
| `anas build -c config.yml` | render candidate 并构建/固定镜像 digest | 否 |
| `anas apply -c config.yml [--build]` | 完整部署事务，验证后提交 active | 是 |
| `anas start` | 幂等启动 active release | 否 |
| `anas restart` | 明确 down/up active release | 否 |
| `anas stop` | 停止 active release | 否 |
| `anas rollback [release-id]` | 将 previous 或指定保留 release 重新对账并验证 | 是 |
| `anas releases list/inspect/gc` | 查看、诊断、清理非活动 release | 仅 gc 删除未引用制品 |

`render` 和 `build` 不再“顺便部署”。如果用户希望一步完成，唯一入口是
`apply`。

## 7. Apply 事务

`apply` 使用显式状态机：

```text
resolve
  -> plan
  -> allocate secret generations
  -> materialize candidate
  -> validate artifact and compose
  -> build/pull images and pin digest
  -> preflight host resources
  -> reconcile candidate runtime
  -> verify services
  -> commit active state
  -> finalize secret generations
  -> garbage-collect by retention policy
```

每完成一步就原子更新 transaction journal。journal 至少记录：旧 release、
候选 release、已执行的 cask、已创建的 project/network、secret generations
以及下一恢复动作。

如果 reconcile 或 verify 失败：

1. 对已更新的稳定 Compose project 用旧 release 再执行 `up -d`；
2. down 仅存在于候选 release 的新增 project；
3. 恢复旧 release 的主机网络配置；
4. 将候选标为 failed，不修改 active state；
5. 新分配但未被任何 release 引用的 secret generation 标为 orphan，稍后 GC。

如果 runner 崩溃，下次任何有写操作的命令必须先读取未完成 journal，执行
`recover` 或要求用户明确 `transaction resume|abort`，不能直接开始新 apply。

严格意义上 Docker 运行时无法和文件 rename 组成单个原子事务；journal +
幂等补偿才是正确模型。

## 8. 环境变量与 secret 图

### 8.1 用显式值图替代启发式 owner

resolve 阶段的每个值都应携带元数据：

```go
type ResolvedValue struct {
    Key            string
    Producer       string       // global、user、core 或 cask name
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
- hook calculate 的输出必须声明 producer、classification 和 export contract；
- capability 输出使用类型化 capability 名称，不依赖任意全局 env 键覆盖。

### 8.2 分开三类环境

1. `compose.env`：只包含 compose 文件 `${KEY}` 插值需要的键；
2. `services/<service>.env`：只包含该容器进程真正需要的键；
3. hook request：运行时按 manifest contract 临时构造，不保存全量 secrets
   快照。

逐步移除 compose 中的 `env_file: .env`，改为每个服务显式引用自己的 env
文件或 `environment:` 映射。例如数据库密码只进入数据库主容器和明确声明
的消费者，adminer、redis、imaginary 等辅助容器不自动继承。

`internal_env` 可继续存在，但应变成值图的 classification/consumer 属性，
不再由 hook 事后返回一组要删除的键。

### 8.3 Secret generation

secret store 采用追加式版本：

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

release 引用 `POSTGRES_PASSWORD@1`，新候选可引用 `@2`。rollback 后
`anas secret get POSTGRES_PASSWORD` 默认解析 active release 的 generation，
不会返回另一个 release 的密码。只有没有 release、candidate 或备份引用的
generation 才允许 GC。

## 9. 配置状态

### 9.1 用户配置

用户的 `config.yml` 只表示期望状态，是唯一需要人工维护的配置。runner 不
应修改它，除非用户显式执行 `config set`。

release 不再保存原始配置作为启动输入。为了审计，保存
`resolved.redacted.yml`：

- 包含所有默认值、自动绑定、最终模块顺序和值来源；
- secret 值替换为 `secretRef` 或不可逆摘要；
- 保留完整 config fingerprint；
- 可用 `anas release inspect` 解释任意活动或历史 release。

如果用户希望完整灾备，应备份原始 config 和 secret store；不要把“复制一份
明文 config 到每个 release”当成配置备份机制。

### 9.2 Applied state

删除独立的 `state/config-applied.yml`。其职责并入 active release manifest
和 `state/active.yml`。变更计划比较：

```text
new resolved state
vs
active release resolved.redacted.yml + release.yml
```

比较范围必须包括：

- 用户显式配置；
- manifest 默认值；
- cask/app 版本和 image digest；
- capability bindings；
- secret generation；
- 模块集合、顺序和服务集合；
- hook 和模板 artifact digest。

这样 `credential_rotate`、`data_migrate`、`immutable` 守卫不会因为
`AppliedAt` 缺失而失效，也能发现默认值或 provider 漂移。

应用内部 observed state 仍与 desired/applied state 分开。后续 cask
reconciler 可把探测摘要写入 transaction verification result，但不能把容器
当前状态反写为用户期望配置。

## 10. Lock 文件

拆成两层：

### 10.1 项目解析锁 `anas.lock.yml`

与 `config.yml` 放在一起，由用户提交或备份，包含：

- config schema 和 cask ABI；
- cask packaging version、source 和 source digest；
- capability provider bindings；
- image tag 解析后的 digest；
- hook/template bundle digest。

只有 `anas lock` 或带明确 `--update-lock` 的命令能更新。普通 plan/apply 在
lock 与 config 不一致时给出 diff 并失败，不能静默改锁。

### 10.2 Release lock snapshot

每个 release 内保存其使用的 `lock.yml`，并由 `release.yml` 记录 checksum。
这是部署证据，不可修改。rollback 直接使用目标 release 的 snapshot，不修改
项目侧 lock，也不存在全局 `.cask.lock.snapshot`。

## 11. 构建和镜像

当前 build 只记录 cask version，无法保证同一 tag 下镜像内容不变。目标方案：

1. build/pull 后解析并记录 image content digest；
2. release compose 使用 digest，而不是可变 tag；
3. 自建镜像使用 release ID 或 artifact hash 作 tag；
4. hook 二进制、compose、模板和生成文件全部记录 SHA-256；
5. `start` 前快速校验 artifact digest，发现 release 被人工修改就拒绝启动。

## 12. Verify 与健康提交

Compose `up -d` 成功不等于部署成功。cask manifest 应允许声明：

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

只有所有 required cask 验证通过后才提交 active state。`after_start` 中实际
属于验证的逻辑迁到 verify；必须修改外部状态的 reconcile hook 要幂等，并在
journal 中记录补偿动作。

## 13. 并发、权限与备份

- base 根目录和所有 secret/env 文件保持 `0700/0600`；
- `apply/rollback/gc` 获取 `state/lock` 排他文件锁；start/stop 与 apply 也
  不能并发操作同一 Compose project；
- release 普通资产在物化完成后改为只读，敏感 env 仍为 `0600`；
- 备份单元是：用户 config、项目 lock、`secrets/`、业务 data 和
  `state/active.yml`；release 可选备份，但不是业务数据替代品；
- GC 默认保留 active + 最近两个 verified release + 所有未过期 failed
  candidate；引用中的 secret generation、镜像和 release 不能删除。

## 14. 实施顺序

### 阶段 0：修复当前基线的确定性问题

1. artifact start 成功后统一提交 bindings/applied state；
2. secret store 中的所有键默认 sensitive，修复 artifact start hook 泄漏；
3. 每个 release 自带 lock snapshot，修复连续 rollback；
4. 修复 `config secret list -b ...` 参数解析；
5. 为以上问题增加远端已复现场景的自动测试。

这一阶段只为当前基线止血，随后由新模型删除临时状态路径。

### 阶段 1：命令与 release store

1. 新增 `apply`，移除 `start -c`；
2. `render/build` 改为只创建 candidate；
3. 引入 `releases/<id>/release.yml` 和 `state/active.yml`；
4. artifact start 完全由 release manifest 驱动；
5. rollback 按 release ID 工作。

### 阶段 2：配置与 lock 分层

1. 引入项目侧 `anas.lock.yml`；
2. 保存 `resolved.redacted.yml`；
3. 删除 release 中作为启动输入的原始 `config.yml`；
4. 删除全局 mutable `cask.lock.yml` 和独立 `config-applied.yml`。

### 阶段 3：值图和容器级环境隔离

1. 用 `ResolvedValue` 图替换 owner/value-equality 推断；
2. secret generation 版本化；
3. 拆分 `compose.env` 与 service env；
4. 逐 cask 移除全量 `env_file`；
5. capability contract 类型化。

### 阶段 4：事务、验证与恢复

1. transaction journal 和 crash recovery；
2. cask verify contract；
3. reconcile 失败自动补偿旧 release；
4. release/secret/image 引用计数和 GC；
5. fault injection 测试覆盖每个事务阶段。

## 15. 必须具备的测试

1. `render`、`build` 后 active release 和 active state 完全不变；
2. `start/stop/restart` 不修改 config、lock、secret store 或 release 内容；
3. `apply` 在 render/build/reconcile/verify/commit 任一点失败均可恢复；
4. 第一次、第二次及指定 release rollback 都恢复正确 lock、env 和 secret；
5. 新增、移除、改名 cask 不遗留 Compose project；
6. manifest 默认值改变能出现在 plan 中；
7. auto provider 不会在 lock 未更新时漂移；
8. 每个 service 只拿到声明的 secret，secret store 任意键默认不跨边界；
9. release 在当前源码 cask 已变化或不存在时仍能 start/stop；
10. 两个并发 apply 中只有一个能进入事务；
11. 进程在每个 journal phase 被强制终止后，下次运行能确定性恢复；
12. GC 不删除 active/previous/candidate 引用的 release、image 或 secret
    generation。

## 16. 最终判断

当前方案可以作为从“每次启动都重算”迁向不可变制品的中间版本，但不应作为
最终运行时模型。最优方案不是继续给 `runner.execute` 增加条件分支，而是：

- 用命令边界分离 candidate creation 和 activation；
- 用 release manifest 取代原始 config 作为制品启动输入；
- 用 active pointer 取代可变 release 目录；
- 用项目解析锁 + release lock snapshot 取代全局混合 lock；
- 用版本化 secret 引用和容器级 env 取代值相等推断与 cask 全量 env；
- 用 journal + verify + compensation 取代“目录 rename 即部署成功”。

完成阶段 1 和阶段 2 后，当前两个最严重的状态一致性问题会从结构上消失；
阶段 3 和阶段 4 则把安全边界与生产可恢复性补齐。
