# Capability 开发标准

> 状态：**当前强制开发标准**。适用于新增、修改、迁移和评审 ANAS Module Capability。
> 更新：2026-08-22。

Capability 表达“Consumer 需要一项可替换的跨 Module 能力，但不应知道由哪个 Module
实现”。本标准规定 Capability 的建模边界、Manifest、Runner 注册、Provider/Consumer
责任、绑定与锁、环境和 Secret、测试、文档及发布门禁。

开始前同时阅读 [Core 实现标准](/architecture/core-implementation-standard)、
[Module 开发规范](/developer/module-development)、
[Module、Contract 与 Resource 设计](/architecture/module-contract-resource-design)和
[Module 设计与发布检查表](/developer/module-design-checklist)。专项要求比本页更严格时，
两者必须同时满足。

## 1. 适用范围与当前边界

本标准所称 Capability，专指以下 Manifest ABI：

```yaml
capabilities:
  provides: []

dependencies:
  requires_capabilities: []
```

当前通用 registry 位于 `internal/runner/capability.go`，只注册：

| Capability | interface | 当前选择策略 |
| --- | --- | --- |
| `iam` | `oidc`、`saml` | 用户显式选择部署级 Provider |
| `forward_auth` | `http` | 单一候选时自动选择 |
| `object_storage` | `s3` | 单一候选自动选择；Runner 投影已注册的统一连接输出 |

`dynamic_dns` 虽然也写入 `capability_bindings`，但它没有 Consumer Module，当前由专用的
部署级 resolver 处理。它不是新增通用 Capability 的实现模板。`relational_database`、
`certificate` 和 `identity` 是 Contract；Linux file capability、
`anas backup capabilities` 的运行环境探测、`features` 中的人类可见功能也不属于本标准。

当前 ABI 有以下硬边界：

1. 一个 Consumer 的一条 Capability requirement 最终只绑定一个 Provider；
2. 每条绑定只选择一个 interface；
3. Consumer 不在 Manifest 中列举或选择 Provider；
4. Capability 没有独立语义版本、request/result schema 或 operation lifecycle；
5. Capability 默认只建立依赖、选择 interface 并记录绑定；只有 registry 显式声明的稳定
   output ABI 可由 Runner 投影 endpoint/连接字段，且仍不代表 readiness 或业务状态；
6. output ABI 不执行 bucket、账号、数据库等持久 Resource 生命周期。

需要 `all_of`、同一 Consumer 同时绑定多个 Provider、按 Consumer 创建持久对象或执行
`ensure/inspect/rotate/delete` 时，必须先设计新 ABI，通常应使用 Contract 与 Resource，
不得把数组或私有约定塞进现有单值字段。

## 2. 先选择正确的抽象

新增跨 Module 关系前，按下表选择模型：

| 需求 | 使用 | 原因 |
| --- | --- | --- |
| 必须依赖某个具体 Module，替换实现没有意义 | `dependencies.requires` | 直接、可审计，不制造虚假抽象 |
| 只要求某项可替换工作或协议，解析时选 Provider 和单一 interface | Capability | Consumer 不感知产品名，Runner 只做拓扑绑定 |
| 有独立 Provider/Consumer、版本化 request/result 或资源 lifecycle | Contract | 需要稳定 schema、版本和 operation 语义 |
| Consumer 申请数据库、证书、账号等持久对象 | Contract + Resource | 需要稳定 identity、幂等、凭据和删除策略 |
| 只传递一个有 owner、consumer 和轮换 lifecycle 的 Secret | `credentials.provides/consumes` | 不应把 Secret 生命周期藏进能力绑定 |
| 两个已选 Module 只需排序 | `dependencies.after` | 排序不能隐式选择 Module |
| 描述供用户或目录展示的产品功能 | `features` 或应用目录声明 | 不参与 Provider 解析 |

只有同时满足以下条件时才新增 Capability：

1. 至少存在一个 Provider 和一个独立 Consumer；
2. Consumer 的业务语义只依赖中立能力和 interface，不依赖 Provider 产品名；
3. Provider 可以通过同一套中立输入/输出 ABI 被替换；
4. 绑定可在 Hook 运行前、无外部副作用地完成；
5. 不需要 Capability 自身管理持久 Resource 或版本化 operation；
6. 名称和 interface 对未知第三方 Module 仍有清楚、可验证的含义。

“以后也许会有第二个实现”不足以建立 Capability。反过来，已经出现多 Provider、持久
Resource、独立协议版本或复杂 lifecycle 时，也不能因为现有 Capability 写起来更短而拒绝
升级为 Contract。

### 2.1 部署级能力

没有 Consumer Module、只代表 deployment 整体需要的能力，无法通过普通依赖边解析。新增
这类能力必须先形成独立架构决策，至少定义：根 Module 注入、用户配置、Provider 选择、锁
identity、与显式 `modules` 的关系及未来第三方扩展方式。不得伪造一个 Consumer，也不得直接
复制 `dynamic_dns` 的 Module 名称列表和专用分支。

## 3. 规范来源与设计交付物

Capability 的机器事实来源是 Runner registry 和 Module manifests；设计文档不能声明代码
不认识的名称或 interface。实现前必须有一份专项架构或要求文档，至少固定：

- 中立名称、业务语义、适用范围和非目标；
- interface 集合及每个 interface 的可互操作语义；
- Provider 准入条件和 `RequireAll`；
- Provider 选择策略、候选基数和用户配置；
- Consumer interface 选择优先级；
- 中立数据 ABI、字段 ownership、Secret 和信任边界；
- 绑定变更、锁、失败、降级、弃用和迁移语义；
- synthetic resolver 测试和真实 Provider × interface × Consumer E2E 矩阵。

功能实施必须按仓库工作流建立 `dev-docs/requirements/<topic>.md` 与配对的
`dev-docs/plans/<topic>.md`，使用稳定 requirement ID 分配验收范围。只新增或澄清本开发标准、
不改变运行行为时，不必为了文档改动虚构功能计划。

专项文档不得复制易漂移的 Provider/Consumer 清单；清单以当前 manifests 为准。需要面向人
展示时必须说明采集来源和实现状态。

## 4. 命名与 interface 设计

Capability 名称必须：

- 使用小写 `lower_snake_case`；
- 表达中立工作或协议角色，例如 `iam`、`forward_auth`；
- 不包含产品、Module、镜像或厂商名称；
- 不复用 Contract、配置字段或产品功能名来表达不同语义；
- 一经发布保持稳定，不能只靠改名改变原有绑定含义。

interface 是 Provider 与 Consumer 真正互操作的协议标识，不是实现标签。它必须：

- 使用简短、稳定的小写标识；
- 对双方都有可测试的协议或交换语义；
- 不用 `v2`、`advanced`、`native` 等含糊词掩盖未定义差异；
- 不把认证方式、传输、数据模型和商业版本等互不等价的维度压成一个枚举；
- 在 registry 的已知集合中注册后才能进入 Manifest。

当前 Capability 没有独立 SemVer。增加已知 interface 可以是兼容扩展，但把它加入
`RequireAll` 会立即使旧 Provider 失去准入资格；重命名、删除、收紧语义、改变选择策略或
绑定 cardinality 都是破坏性变更。此类变更必须采用新名称/interface、分阶段迁移或正式的
Module ABI 变更，不能静默解释旧 Manifest。

## 5. Provider Manifest 与责任

Provider 在 `module.yml` 中声明：

```yaml
capabilities:
  provides:
    - name: example_gate
      interfaces:
        - http
```

Provider 必须满足：

1. 同一 Capability 只声明一次，`interfaces` 非空、无重复且全部为 registry 已知值；
2. 包含 registry `RequireAll` 指定的所有 interface；
3. 资格不依赖用户配置、运行时探测或某个 Consumer 是否启用；
4. 固定 Module 版本确实实现所声明的每个 interface；
5. 通过 Provider-neutral ABI 接收 Consumer 声明并发布结果，不要求 Consumer 读取私有变量；
6. 重复 calculate/render/apply 收敛到同一结果，切换 interface 时清理旧投影；
7. 明确健康检查、启动时序和故障行为；只有依赖排序时，Consumer 仍须保留有界重试；
8. 未完成真实 E2E 时保持 `developing`，不能用 Manifest 声明替代运行证据。

`capabilities.provides` 只是准入声明，不会自动生成 endpoint、配置 Provider 或证明服务可用。
登记了 `Outputs` 时 Runner 只投影 Provider Hook 已发布的字段；字段生成和外部系统配置仍由
Provider Hook/lifecycle 实现，并遵守自身 ownership、幂等、失败补偿和回滚边界。

## 6. Consumer Manifest 与责任

Consumer 只声明自己支持的 interface 和 `auto` 偏好，不得出现 Provider 名称：

```yaml
dependencies:
  requires_capabilities:
    - name: example_gate
      interface_selected_by: gate_interface
      interfaces:
        any_of:
          - http
        prefer:
          - http

config:
  defaults:
    gate_interface: auto
  types:
    gate_interface:
      enum: [auto, http]
```

registry 为单 interface 能力显式登记 `ImplicitInterface` 时，Consumer 可以使用 name-only
简写，例如：

```yaml
dependencies:
  requires_capabilities:
    - name: object_storage
```

简写固定使用登记的 interface，不创建 selector 参数；未登记简写的 Capability 仍必须声明
`interface_selected_by` 和非空 `any_of`。新增第二个 interface 不得改变既有简写的含义。

规则如下：

- 除 name-only 简写外，`interface_selected_by` 必须是当前 Module 拥有的普通配置参数，不得来自 Secret；
- 除 name-only 简写外，`any_of` 非空、无重复，表示固定 Module 版本实际能够使用的完整集合；
- `prefer` 无重复且必须是 `any_of` 的子集，顺序就是 `auto` fallback 顺序；
- 参数 schema 至少认识 `auto` 和 Core 共享的 interface 词汇；实际支持子集仍以 `any_of`
  为准，显式选择超出子集必须由 resolver 拒绝；
- Consumer 不声明 `selected_by`、`provider_selected_by`、`providers` 或其他 Provider 列表；
- Consumer 只读取自己的 binding 和中立 Provider 输出，不读取其他 Consumer binding；
- interface 或 Provider 改变会影响容器、外部注册或数据时，必须声明准确的 change effect 与
  lifecycle，不能让普通 `apply` 静默越过迁移门禁。

Capability 是硬依赖。没有可用 Provider、Provider 被禁用或没有兼容 interface 时，Consumer
必须在 plan/lock 阶段失败；不得退回未认证、本地共享密码或功能残缺的隐式模式。确需可选
能力时，应把可选性建模为显式 Module 配置和拓扑规则，而不是吞掉解析错误。

### 6.1 条件依赖：`enabled_by`

Consumer 的**可选服务**需要某个 Capability 时，用 `enabled_by` 声明条件：

```yaml
dependencies:
  requires_capabilities:
    - name: forward_auth
      enabled_by: adminer_enabled
      interface_selected_by: forward_auth_interface
      interfaces:
        any_of: [http]

config:
  defaults:
    adminer_enabled: "false"
  types:
    adminer_enabled: bool
```

**条件只决定这条依赖是否存在，不改变它存在之后的任何语义。** 条件成立时它就是一条普通的硬
依赖：照常参与拓扑排序、Provider 缺失照常失败、绑定照常写入 lock。**不存在「弱条件依赖」这种
中间态**——一个只在运气好时才被满足的安全网关不是安全网关。

`enabled_by` 必须命名**本 Module 自己的一个 `bool` 参数**，且该参数必须在 `config.types` 中声明。
三条限制各有原因，不是风格约束：

- **别的 Module 的参数或裸环境变量**在读取这个条件的时刻还不存在——依赖图先于其他 Module 的值
  建立；
- **未在 `config.types` 声明的参数名**没有任何东西校验，而这里拼错名字不会响亮地失败，它会静默
  求值为 false；对这个字段的第一个使用者来说，那意味着一道认证网关无声消失；
- **非布尔参数**需要一套「真值」规则，而任何真值规则都会把某些合法取值意外变成 false，后果同上。

省略 `enabled_by` 即无条件，既有声明的行为不变。

**与另外两个「可选」机制的区别**——三者不是同一件事的三种写法：

| 机制 | 引用什么 | 求值时机 |
| --- | --- | --- |
| `requires_capabilities[].enabled_by` | 本 Module 的 bool 配置参数 | 解析依赖顺序时，早于一切 Hook |
| `services.optional[].enabled_by` | 本 Module 的配置参数 | 声明式；实际禁用由 Hook 的 `disable_services` 执行 |
| `dependencies.requires[].optional` | 无条件字段，修饰的是 Module 依赖 | **语义相反**：optional 依赖被排除出依赖图，不产生排序边，只在该 Module 恰好也被部署时检查版本 |

`requires_capabilities[].enabled_by` 与 `services.optional[].enabled_by` 同名同义是有意的：同一个
粒度（本 Module 的一个开关），同一个求值时机。**`dependencies.requires[].optional` 不是它们的
简写**——它表达「碰巧在场就顺带检查」，条件依赖表达「条件成立就必须在场且必须排在前面」，两者不
可互换。

应用目录 schema 里的 `enabled_if` 引用的是**变量**而不是参数，在渲染期求值，可以依赖 Hook 的输出。
因此它**不能**用作依赖条件——依赖顺序必须在任何 Hook 运行之前就确定。

以上是使用这套机制需要知道的全部约束。规范来源是[条件 Capability 依赖要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/conditional-capability-dependency.md)的需求矩阵（仓库 `dev-docs/`，不在本站发布），冲突时以矩阵为准。

### 6.2 无序依赖：`ordering`

Provider 必须在部署里、但**不需要排在 Consumer 前面**时，用 `ordering: any`：

```yaml
dependencies:
  requires_capabilities:
    - name: forward_auth
      ordering: any
      interface_selected_by: forward_auth_interface
      interfaces:
        any_of: [http]

config:
  consumes:
    - ANAS_FORWARD_AUTH_MIDDLEWARE
```

它存在的理由是**守门人依赖被守卫者**这类环：网关要守卫数据库，网关自己又需要 IAM，而 IAM 需要那个
数据库。强依赖在这里必然成环，而这不是接线接错了，是这类关系的固有形状。

**放弃的只有顺序，不是要求。** Provider 缺失、被禁用或没有兼容 interface 时照样在 `plan` 阶段失败，
绑定照样记录、照样进 lock。字段命名的是被放弃的东西——**不要读成「这条依赖可有可无」**，那是
`dependencies.requires[].optional` 的语义，见下表。

| 机制 | 存在性 | 排序边 |
| --- | --- | --- |
| `dependencies.requires[].optional` | **不强制** | 无 |
| `requires_capabilities`（缺省） | 强制 | 有 |
| `requires_capabilities[].ordering: any` | 强制 | **无** |

使用它要接受两条约束：

- **Consumer 的 calculate Hook 读不到该 Provider 拥有的任何键。** Runner 会主动把它们从 calculate
  环境中移除，不是靠自觉。理由是 Provider 可能还没跑，那些键的有无取决于解析顺序；一个稳定的缺失
  比一个时有时无的值好得多——错误会在作者第一次运行时暴露，而不是在别人的拓扑上。
  **渲染期不受此限**：`calculate` 与 `renderAll` 是两趟，渲染读的是所有 Hook 跑完后的完整环境，
  所以 Compose 变量、模板照常取值。
- **必须显式声明 `consumes`。** 去掉排序边同时也把 Provider 移出了 Consumer 的依赖闭包，闭包与前缀
  可见性都不再覆盖它，显式声明是唯一剩下的通路。

两处限制：Contract 依赖与 `resources.requires` **不得**使用（Resource 是 Provider 的 Hook 真实创建
的持久对象，顺序是语义的一部分）；注册了 output ABI 的 Capability 也不得使用——那些键投影在 Provider
的 calculate 之后、且归 Consumer 所有，上面的过滤覆盖不到，会留下一个静默的洞。两种情况都在 Manifest
加载期失败。

以上是使用这套机制需要知道的全部约束。规范来源是[无序 Capability 依赖要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/weak-capability-dependency.md)的需求矩阵（仓库 `dev-docs/`，不在本站发布），冲突时以矩阵为准。

## 7. Runner registry 与解析

新增通用 Capability 必须在中立 registry 中增加 `capabilityDefinition`。定义至少包含：

```go
type capabilityDefinition struct {
    Interfaces         []string
    RequireAll         []string
    Selection          string
    ConfiguredProvider func(*app) string
    DefaultInterface   func(*app) string
    ConfigKey          string
    ImplicitInterface  string
    Outputs            map[string]capabilityOutputDefinition
}
```

### 7.1 Registry 规则

- `Interfaces` 是 Manifest 可使用的完整词汇表；未知名称或 interface 在加载阶段失败；
- `RequireAll` 是 Provider 准入下限，必须与用户配置无关；
- `Selection` 只使用已实现、已测试的通用策略；
- `ConfigKey` 必须指向真实、公开、文档化的规范配置路径，并能用于可执行错误提示；
- `ConfiguredProvider` 和 `DefaultInterface` 只能机械读取通用 deployment 配置，不能按 Module
  名称、环境变量前缀或产品类型分支；
- `ImplicitInterface` 只用于稳定单 interface 简写；值必须属于 `Interfaces`，且不能因新增
  interface 静默改变；
- `Outputs` 按 interface 登记 Provider source prefix、Consumer binding prefix、必填字段和
  敏感字段；未登记 output 的 Capability 不得由 Runner 猜测或复制环境变量；
- Capability 专属业务规则留在 Module；只有跨所有未知 Provider/Consumer 都成立的不变量
  才能进入 Runner。

如果新 Capability 使用 `DefaultInterface`，诊断也必须是 Capability-neutral 的；不得复用
包含 `iam.*` 等其他能力名称的错误路径。新增定义前应先把任何阻碍未知第三方 Capability 的
硬编码诊断抽象为通用实现，并用 synthetic Module 测试。

### 7.2 Provider 选择

`explicit` 适用于选择错误会产生高迁移成本、唯一事实来源或安全域变化的能力：

- 存在 Consumer 时配置必须显式给出 Provider；
- 所选 Module 必须存在、启用并声明该 Capability；
- 配置 schema、导入、plan、lock 和参考文档必须在同一变更中实现；
- 不能提供默认产品名，也不能根据目录顺序猜测。

`auto` 只适用于候选可安全互换且单 Provider 是常态的能力：

- 已有有效 lock binding 时保持原 Provider；
- 没有 lock 且只有一个启用候选时自动绑定；
- 没有候选时失败并列出可启用 Provider；
- 多个候选时拒绝猜测。

在为 `auto` Capability 发布第二个 Provider 前，必须提供真实可用的消歧配置或经过架构评审的
通用选择规则。错误信息不得要求用户设置一个 schema 中不存在的键。固定产品优先级只适用于
有独立部署级设计和兼容性推导的特殊 resolver，不能加入通用 Capability 默认行为。

### 7.3 Interface 选择

解析顺序固定为：

1. Consumer 参数的显式非 `auto` 值；
2. Capability 定义声明的 deployment-wide default，且该值在 Consumer `any_of` 中；
3. Consumer `prefer` 中第一个可用值；
4. `any_of` 第一项。

最终值必须同时属于 Consumer `any_of` 和 Provider `interfaces`。失败信息必须包含 Consumer、
Capability、Provider、请求值、双方支持集合和可执行修复动作，敏感来源只显示 `<redacted>`。

### 7.4 调度边界

Capability 解析必须在任何 Module Hook 运行前完成，并且自身无外部副作用。成功绑定后：

- Provider 被加入 Consumer 的有效依赖闭包；
- Provider 的 calculate/render 排在 Consumer 之前；
- 同一解析器用于 import、plan、lock、render 和 apply，不能各写一套规则；
- `plan` 可以报告绑定问题，但不能创建 Secret、改外部 Provider 或写运行目录；
- 依赖边不等于 readiness barrier，运行时连接失败仍由双方按协议处理。

## 8. Binding、lock 与变更语义

普通 Consumer 绑定以以下稳定形状进入 lock/deployment/plan：

```yaml
capability_bindings:
  example_consumer:
    example_gate: example_provider
    example_gate.interface: http
```

要求：

1. Provider 和 interface 都必须记录，不能只锁其中一个；
2. 普通 start/apply 不得在 lock 背后切换 Provider 或 interface；
3. locked Provider 缺失、禁用或不再提供该能力时必须失败，不得静默重绑；
4. 更新绑定必须走显式 lock/update 流程，并在 plan 中展示影响；
5. 错误、JSON、YAML 和 API 使用相同的 Consumer/Capability identity；
6. 新增字段或改变 key 形状必须评估旧 lock、deployment manifest、rollback 和 API 兼容性。

若 Provider/interface 切换需要改写外部客户端、撤销旧凭据、迁移状态或协调多个服务，必须先
定义专用 lifecycle 和失败补偿。当前 Capability binding 本身不是事务执行器；无法仅靠 Module
局部 Hook 安全完成时，应升级为 Contract operation 或独立迁移方案。

## 9. 数据、环境变量与 Secret ABI

Capability 声明不会搬运业务数据。Provider 与 Consumer 的交换必须使用明确的中立 ABI；
registry 未声明 `Outputs` 时仍不自动传递任何字段。已声明 output ABI 时，Runner 只能把
必填 Provider-neutral 字段复制到目标 Consumer 自己的 binding namespace：

- Runner 解析事实使用 Runner-owned key，例如每 Consumer 的 binding interface；
- Provider 产物使用 Capability-neutral 的 `ANAS_<CAPABILITY>_*` 命名空间；
- per-Consumer 注册或 endpoint 使用稳定 Module ID 分区，不能依赖显示名或域名前缀；
- Provider 通过 `config.exports` 声明跨前缀输出；普通共享字段由 Consumer 通过
  `config.consumes` 精确声明，registry output 则由 Runner 直接归属目标 Consumer；
- 不得依赖全量环境注入、Provider 私有前缀、Compose service 名或共享文件路径；
- interface 切换、Consumer 删除和重复 apply 必须清理不再属于目标状态的旧字段；
- 字段、ownership、空值/缺失、排序、编码和兼容语义必须在专项文档中逐项定义；
- output 缺失或与已有值冲突必须在 Consumer Hook 前失败；调用方不得通过 raw env/secrets
  预置 Runner-owned binding key；
- 敏感 output 必须继承 Secret provenance，只对目标 Consumer 可见，不进入其他 Module、
  plan、lock、deployment manifest 或普通错误。

Secret 明文不进入 `capabilityDefinitions`、binding metadata、plan、lock 或普通错误。需要跨 Module Secret 时，
使用 `credentials.provides/consumes`、Resource credential 或受 scoped env 约束的既有 Secret
投影，并分别定义 owner、authority、轮换、验证和失败回滚。Provider/interface selector 是部署
结构，禁止来自 `secrets` 或 lifecycle Secret Store。

Provider 扫描 Consumer 声明时，只能读取 Runner 发布的中立聚合事实或显式允许的命名空间，
不得枚举所有环境变量后推断 Consumer。Consumer 也不得把“能读取某个 key”当成能力存在证明；
绑定才是拓扑事实。

## 10. 安全与故障语义

每项 Capability 必须在专项要求中给出威胁模型，至少覆盖：

- Provider、Consumer、Runner 和外部系统分别信任什么；
- endpoint、证书、代理 Header、回调 URI 和来源地址如何验证；
- 身份、认证、授权和应用会话由哪一层执行，不能互相投影能力；
- 最小权限、网络暴露、Secret 可见范围和日志脱敏；
- Provider unavailable、部分配置、旧投影残留和协议不兼容时是否 fail closed；
- 降级是否会绕过 IAM、组门禁、TLS、数据隔离或审计；
- 删除 Consumer、切换 Provider 和 rollback 后如何撤销旧注册与凭据。

“服务返回 2xx”“存在 discovery URL”“配置字段已渲染”只能证明局部连通性，不能证明授权、
会话撤销、持久状态更新或最小权限成立。安全能力必须使用真实身份和负向用例验证。

## 11. 测试与验收矩阵

Capability 变更至少覆盖以下层次。

### 11.1 Registry 与 Manifest 单元测试

- 未知 Capability、未知/空/重复 interface；
- Provider 重复声明、缺失 `RequireAll`；
- 非简写 Consumer 缺失 `interface_selected_by`、空 `any_of`、非法 `prefer`；
- 登记简写的单 interface Capability 能从 name-only manifest 稳定解析；
- `prefer` 不是 `any_of` 子集；
- 严格 YAML 拒绝 Consumer Provider 列表等未知字段；
- 名称、大小写、去空格和确定性排序。

### 11.2 Synthetic resolver 测试

通用 Runner 行为优先使用 synthetic Module，不得只靠内置产品 fixture：

- `explicit` 的未设置、未知、未提供、禁用和成功路径；
- `auto` 的零、一个、多个候选及已有 lock 路径；
- 显式 interface、deployment default、`prefer` 和 `any_of` fallback；
- Consumer/Provider interface 不兼容；
- Provider 自动进入依赖闭包并排在 Consumer 前；
- binding 的 provider/interface 被写入 plan、lock 和 deployment；
- 普通 apply 拒绝偏离 lock；
- plan 失败不运行 Hook、不生成 Secret、不写运行状态；
- 错误信息可执行、稳定且不泄露 Secret。

### 11.3 环境与安全测试

- Consumer 只获得自己的 binding 和声明消费的中立输出；
- Provider/Consumer 私有变量不跨边界泄露；
- Secret selector 被拒绝，plan/lock/log 不含明文；
- interface/Provider 切换和 Consumer 删除清除旧投影；
- Provider 故障、超时、TLS/Header/签名错误和未授权主体 fail closed；
- 重复 apply、重启和中断重试收敛。

### 11.4 真实 E2E

发布前必须对每个声称支持的 Provider × interface 至少选择一个真实 Consumer，验证：

1. 全新安装、重复 apply、重启和恢复；
2. 正向业务行为，而不只是容器健康；
3. 拒绝、撤权、错误凭据或不兼容协议等负向行为；
4. Provider outage 和恢复；
5. 绑定切换或明确记录为不支持；
6. 声称支持的 Secret 轮换、会话撤销、删除或 rollback 语义。

只有声明、Hook 单测或模拟 HTTP 不能把组合状态提升为 `release`。无法自动化的真实环境证据
必须记录固定版本、平台、配置组合、日期、脚本和结果。

## 12. 文档与发布门禁

引入或扩展 Capability 的同一变更必须同步：

- 专项 requirement、architecture 和实施 plan；
- `docs/architecture/index.md`、`docs/developer/index.md` 及站点导航；
- 每个 Provider/Consumer 的中英文 README 与技术文档依赖表；
- Module 目录、支持矩阵、配置和环境变量参考；
- 当前限制、未实现 interface、Provider/Consumer 组合和真实 E2E 缺口；
- registry/Manifest/Runner 测试和验证命令。

专项文档必须分别说明“Provider 声明了什么”“Consumer 声明了什么”“哪些组合真实验证过”。
不能从 capability 名称、环境变量或上游宣传推断实现状态。经网关、代理或 Adapter 间接获得
的能力，必须拆开各层会话、权限和故障边界，不能把外层能力投影给后端应用。

新 Capability 在以下条件全部满足前不得作为 `release` Module 的硬依赖：

1. registry、配置 schema、plan/lock/deployment 形状稳定；
2. 至少一个 Provider 和一个 Consumer 完成真实 E2E；
3. 无 Provider、错误 Provider、错误 interface 和故障恢复路径均有测试；
4. Secret、授权和 fail-closed 边界已评审；
5. 文档、支持矩阵和当前状态一致；
6. 相关自动门禁与 `npm run docs:build` 通过。

## 13. 兼容、弃用与迁移

Capability 缺少独立 SemVer，因此兼容处理必须保守：

- 新增可选 interface 时先更新 registry 和测试，再发布 Provider，最后发布 Consumer；
- 不得在同一发布中让旧 Provider 因新增 `RequireAll` 突然无法加载；
- interface 或字段弃用要先停止新增 Consumer、给出替代项和 lock 迁移，再删除；
- Provider 下线前必须检测所有 lock/deployment binding，并给出显式迁移路径；
- 选择策略或规范配置路径改变时必须迁移旧配置和锁，不能依赖新默认值；
- Capability 演进出版本化 schema、Resource 或 operation 后，迁移到 Contract，不继续扩充
  私有环境协议。

`dependencies.requires_one` 是保留的旧 Provider 列举模型。新 Capability 不得使用它维护
静态 Provider 名单；迁移旧关系时，先建立中立 registry 和 Consumer interface，再分阶段移除
产品列表，保证旧 lock 有明确迁移语义。

## 14. 推荐实施顺序

1. 用 §2 判定 Capability、Contract 或其他模型；
2. 写 requirement matrix、架构设计和实施 plan；
3. 定义名称、interface、选择、绑定、数据 ABI 和安全边界；
4. 抽象 Runner 中任何阻碍未知 Capability 的产品特判；
5. 注册 `capabilityDefinition`，补 Manifest 与 synthetic resolver 测试；
6. 实现第一个 Provider，验证幂等、输出、Secret 和 fail-closed；
7. 实现第一个 Consumer，验证只依赖中立 ABI；
8. 验证 plan、lock、普通 apply 拒绝漂移及显式切换；
9. 完成真实 Provider × interface × Consumer E2E；
10. 同步双语 Module 文档、全局矩阵和导航后再提升状态。

不得先在 Consumer Hook 中按产品名接通，再把 Capability 声明补作装饰；这样会让 Manifest
看似解耦，而真实 ABI 仍然绑定实现。

## 15. 评审清单

- [ ] `[M]` 已证明 Capability 比硬依赖、Contract/Resource、Credential 或排序关系更合适。
- [ ] `[M]` 名称和 interface 中立、稳定、可由未知第三方实现。
- [ ] `[A]` Registry、Provider 和 Consumer Manifest 的未知值、重复、空值及准入校验完整。
- [ ] `[M]` Provider 选择、候选基数、interface fallback 和用户配置没有隐式猜测。
- [ ] `[A]` Provider 自动进入依赖闭包，binding provider/interface 进入 plan、lock 和 deployment。
- [ ] `[A/M]` 普通 apply 不改变 lock，显式切换的 effect、迁移和失败补偿准确。
- [ ] `[A/M]` 中立环境 ABI 有明确 owner，Provider/Consumer 不读取对方私有变量。
- [ ] `[A/M]` selector 不来自 Secret，Secret 不进入 binding、plan、lock、日志或错误。
- [ ] `[M/E]` 安全、授权、故障和降级 fail closed，没有把代理能力投影成后端能力。
- [ ] `[A]` 通用解析使用 synthetic Module 覆盖零/一/多 Provider 和 interface 组合。
- [ ] `[E]` 每个发布组合有固定版本真实 E2E，包含正向、拒绝、故障与恢复。
- [ ] `[A/M]` requirement、plan、架构、Module 双语文档、全局矩阵和导航已同步。

## 16. 验证命令

按实际影响选择并记录命令；新增或修改通用 Capability 至少运行：

```bash
go test ./internal/runner
go test ./internal/modulepackage
go run ./cmd/gen-module-docs --check
npm run docs:check-requirements
npm run docs:build
git diff --check
```

再运行受影响 Provider/Consumer 的 Hook 单测、Compose 校验和专项 E2E。静态命令全部通过只
证明机器契约和文档一致，不能替代真实协议、安全和故障证据。
