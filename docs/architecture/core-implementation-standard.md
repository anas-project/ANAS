# ANAS Core 实现标准

> 状态：当前强制架构标准

ANAS Core 是通用编排器和契约执行器，不是内置 Module 业务逻辑的集中实现。Module 拥有
自身参数的业务语义、跨参数不变量、派生值、外部系统适配和持久状态协调；Core 只提供声明式
Schema、ABI、调度、安全边界和事务框架。

## 1. 参数所有权边界

`modules.<name>.config.*`、对应环境变量及 Module 导出值的业务含义归该 Module 所有。
Core 可以根据 manifest **机械地**完成以下工作：

- 解析结构化地址，应用 Module 声明的 literal default，并执行类型、enum、format 和通用
  constraint 规范化；
- 解析依赖、Capability、Contract、Resource 和 effective topology；
- 按声明的 phase 调度 Hook/lifecycle operation，并交付 scoped env/Secret；
- 校验 Hook patch 的 Schema、敏感性、ownership 和只读/可变边界；
- 生成 plan、lock、deployment manifest，管理事务、回滚和稳定错误契约；
- 发布明确属于 Runner 的 global/runtime facts 和通用 Contract binding。

这些动作不得把 Core 变成参数语义的第二个所有者。Core 不得为了让配置通过而猜测、纠正或
覆盖 Module 参数；非法组合应由所属 Module 拒绝。

## 2. 禁止的实现

Core 生产代码不得：

1. 按 Module 名称、环境变量前缀或服务产品名写 `switch`/`if` 业务分支；
2. 新增 `validateSambaDomainDNSConfig` 一类名称或行为都绑定单个 Module 的校验、派生或
   修复函数；
3. 直接合成、覆盖或静默迁移 `SAMBA_DC_*`、`NEXTCLOUD_*` 等 Module 私有参数；
4. 在 `config set/import/plan/render/apply` 分别复制同一 Module 规则；
5. 为单个 Module 特判其数据库、DNS、证书或持久目录状态；
6. 收到非法 Module 值后改写为 fallback、自动降级或“最接近”的合法值。

测试 fixture 可以使用具体 Module 验证集成结果，但通用 Core 行为应优先用 synthetic Module
测试，避免测试反向固化生产代码中的 Module 特判。

## 3. 正确的扩展方式

当需求涉及一个 Module 的参数语义时：

1. 在 `module.yml` 声明类型、默认来源、敏感性、变更 effect 和所需 Hook phase；
2. 在 Module `validate` Hook 实现无副作用的跨参数校验；
3. 在 Module `calculate` Hook 产生派生值；
4. 需要读取或改变外部/持久状态时，使用 Module lifecycle preflight/reconciler/operation；
5. Core 只负责按 effective dependency order 调度、限制输入、验证输出和传播结构化错误。

如果多个 Module 共享同一概念，应先抽象成中立的 Schema、Capability 或 Contract。Core 可以
认识 `oidc`、`saml` 这类通用协议词汇并解析 Provider/Consumer 兼容性，但各 Module 支持哪些
协议、参数组合是否合法，仍由 manifest 与 Module 实现拥有。不得以“复用”为理由把某个
Module 的规则搬进 Core。

## 4. 默认可用，高级可替换

ANAS 是 NAS 服务启动器，不是给专家用的装配套件。**用户想要一个服务，就应该能直接把它装好，
不需要先理解这个服务依赖什么、更不需要先手工把依赖准备好。** 技术实现对用户透明是产品要求，
不是可选的打磨。

由此得到两条对每个 Module 和每个 Contract 都成立的规则：

1. **默认路径必须开箱即用。** 一个 Module 声明的依赖——数据库、对象存储、隔离沙箱、镜像——
   必须能由 ANAS 自动满足。不得把「先在别处准备好 X，再把它的地址和凭据填进配置」当作默认
   安装路径。要求用户手工准备的东西，等于这个服务在默认情况下装不上。
2. **高级路径必须存在且等价。** 用户已经自建了同类服务时，必须能通过配置把 Module 指向它，
   并获得与默认路径相同的能力。默认自动化不得以「只能用我们建的那套」为代价。

判断一条设计是否满足本节，用这个问题：**一个只知道自己想要什么服务、不知道它依赖什么的
用户，能不能装上并用起来？** 答案是否，就说明还差自动化，而不是差文档。

这条与 §1 的参数所有权不冲突：自动满足依赖是 Core 按 manifest 声明去调度 Provider 的结果，
不是 Core 替 Module 猜测语义。它也不豁免安全边界——自动生成的凭据仍然是凭据，自动安装的组件
仍然要有明确的特权边界与卸载路径。

需要提权才能自动满足的依赖是本节与「anas 从不自己提权」之间的真实张力点。这类依赖必须单独
记录：提权发生在哪一步、产生什么用户事后要面对的产物、以及不提权时的降级路径是什么。不得
以本节为由把隐式 `sudo` 塞进 Module Hook。

## 5. Samba 域分离的规范示例

Core 可以调度通用 `validateModules`，但不能实现
`validateSambaDomainDNSConfig`，也不能认识 `samba_dc`、父子域关系或
`application_dns_mode` 的业务分支。

Samba DC Module 自己负责：

- 校验 `BASE_DOMAIN` 与 `SAMBA_DC_DOMAIN` 的关系；
- 解析 `auto/ad_zone/separate_zone`；
- 派生 Realm、Base DN、DC FQDN 和 DNS zone；
- 检查 Samba/DNS observed state 并执行受管迁移。

Core 只负责通用 Schema 规范化、Hook 调度、Secret 隔离、mutation/ownership 校验、plan
metadata 持久化和失败事务边界。这一分层适用于所有当前及未来 Module。

## 6. 评审门禁

Core 变更评审必须回答：

- 该逻辑是否适用于未知的第三方 Module？如果不是，应移到 Module；
- 是否只依赖 manifest/Contract 声明，而不是 Module 名称或私有环境变量？
- 是否会修改 Module requested value，而不是拒绝或把它交给 Module 解析？
- 新能力能否通过通用 ABI 表达，并由 synthetic Module 测试？
- 如果是跨 Module 概念，是否已有中立 Schema/Capability/Contract，而非隐式产品耦合？
- 新增的依赖是否有自动满足的默认路径（§4）？如果它要求用户先手工准备外部服务，这个功能
  在默认安装下就是装不上的。

只有 Runner 自身拥有的全局事实和真正通用的跨 Module 契约可以留在 Core。任何例外都必须先
形成独立架构决策和通用接口，不得直接合入 Module 专用分支。
