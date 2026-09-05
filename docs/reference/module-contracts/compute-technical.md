> 本页由 Contract 技术文档生成，请勿直接编辑。

# compute Contract 技术说明

## 交付的是围栏，不是实例

`compute` 1.x 在 `anas apply` 时交付一份**隔离沙箱租约**：一个受限 project、一组由 Provider 侧
强制的配额、一份固定镜像 allowlist，以及一张只绑定该 project 的受限客户端证书。实例的
`create`/`start`/`exec`/`delete` **不是** Contract operation，而是消费者在租约边界内自行驱动的
运行时行为。

这条边界不是省事，是模型决定的。Contract operation 的唯一 runtime 是 `compose_run`，由 Runner
在 apply 时以 `docker compose run --rm` 执行一次；ANAS 在运行时没有「模块容器 → Core」的调用
通道。而一次性实例是 per-job 热路径，发起方是常驻消费者容器。把 per-job 的实例生命周期塞进
per-apply 的冷路径，只会得到每个操作一次容器冷启动、且没有 stdin 流可用于注入 Secret。

因此职责这样切分：

| 层 | 机制 | 时机 | 发起方 |
| --- | --- | --- | --- |
| 围栏供给 | 本 Contract 的 `ensure` | apply，一次 | Runner |
| 围栏内使用 | 消费者直连 Provider daemon | 每个 job | 消费者容器 |
| 围栏运维 | Module Command | 按需 | 管理员 |

一个受限 project 之于 `compute`，等于一个 database + role 之于 `relational_database`：Provider 在
apply 时把它建好并交出凭据，之后不再位于数据路径上。

## 声明模型

`compute` 1.x 提供 `incus_vm` 与 `incus_container` 两个 interface。二者的 schema 与操作语义完全
一致，区别只有隔离强度：系统容器与宿主共享内核，VM 提供独立 guest kernel。选哪一档是部署
决策，由消费者的 `binding` 参数选择，Provider 不自动降级。

```yaml
dependencies:
  contracts:
    - name: compute
      version: ">=1.0.0 <2.0.0"
      selected_by: actions_isolation
      interfaces: [incus_container, incus_vm]
      default: incus_container
resources:
  requires:
    - id: runners
      contract: compute
      binding: actions_isolation
      spec:
        sandbox: anas-forgejo-runners
        instance_prefix: anas-fj-
        quota: {max_instances: 8, cpu: 4, memory_mib: 8192, disk_gib: 40}
        image_allowlist: ["<64 位 SHA-256 fingerprint>"]
        credential: {policy: generated}
        deletion_policy: retain
```

删除这两段声明的 Module 不参与 Resource 解析，也不会收到任何沙箱凭据。

## 生命周期

Runner 为每个 Resource 生成一对稳定的客户端证书与私钥，作为单条 Secret 保存，然后在 Consumer
启动前调用 Provider 的幂等 `ensure`。Provider 必须：

1. 校验 endpoint 可达，且 server certificate 与固定 fingerprint 一致，失败即 fail closed；
2. 确保 `sandbox` 指定的 project 存在且 `restricted=true`；
3. 把 `quota` 写到 project 自身的限制上，而不是依赖调用方自觉；
4. 在 project 内建立受管 network 与租约 profile，并把 profile **读回**校验：它必须恰好只有一块
   根磁盘（在受管存储池上、无 host source）和一块接到该受管 network 的 NIC；
5. 把 Runner 传入的客户端证书登记为**只绑该 project** 的受限证书，不使用全局管理凭据；
6. 重复调用收敛到同一结果，不产生第二个 project、第二个 network 或第二条 trust 条目。

## Provider 拥有 profile 与 network

实例的数值上限来自消费者，**其余一切来自 profile**：根磁盘落在哪个存储池、插在哪张网卡上。
profile 名字由 Contract 固定为 `anas-lease`，消费者只能引用、不能编写——能命名一个 profile 的
调用方，也就能指向别人写的 profile，而那正是「Provider 拥有它」要防的事。

每份租约有自己的受管 bridge，因此一个消费者的实例出网路径与另一个消费者不共享。网络名不是
sandbox 名：Linux bridge 接口名上限 15 字符，而 `anas-forgejo-runners` 已经 20，所以它由 sandbox
名哈希派生——短、跨 apply 稳定、租约之间不碰撞。

`ensure` 每次都**整体替换** profile 的设备而不是合并。一块被人手工挂上去的 host 路径，正是
最不该在一次 ensure 之后还留在那里的东西。

`inspect` 是只读的，必须能分别报告 `exists`、`ready`、`restricted` 与 `quota_enforced`——一个存在
但未受限或未设配额的 project 是没有围栏的围栏，必须能被单独看见。`revoke` 是 1.x 可选 operation。

就绪租约通过 Consumer 私有命名空间发布：

```text
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__INTERFACE
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__ENDPOINT
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__SANDBOX
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__INSTANCE_PREFIX
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__PROFILE
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__SERVER_CERT_FINGERPRINT
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__CLIENT_CERT
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__CLIENT_KEY
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__IMAGE_ALLOWLIST
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__MAX_INSTANCES
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__CPU
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__MEMORY_MIB
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__DISK_GIB
```

`CLIENT_KEY` 标为敏感值，只属于目标 Consumer。部署清单与 Resource state 只保存 Secret Store
引用，不保存明文。

## 多消费者隔离

一份租约恰好拥有一个 project 与一张证书；两个消费者不共用 project，也不共用证书。**跨消费者
隔离完全由 project 承担**：受限证书越不出自己的 project，两份租约即使落在同一 Provider 上也互相
看不见对方的实例。

实例名前缀解决的是另一个问题，不要与上面混为一谈：它区分的是**同一个 project 内部**哪些实例由
ANAS 托管。运维可能在这个 project 里手工建过实例，janitor 必须能认出哪些不该由它回收。前缀是
消费者侧的过滤，不是安全边界。

真正重要的是：跨 project 这道边界由 **Provider daemon 自己**执行，不是由消费者代码自觉遵守。
受限证书的作用域写在 daemon 的信任库里，配额写在 project 上。消费者容器被完全攻破，越界请求
依然会被 daemon 拒绝。

## 两种 fingerprint

Incus 把「SHA-256 十六进制摘要」统称 fingerprint，本 Contract 里它出现在两个**互不相关**的位置，
读文档时不要混淆：

| 字段 | 是什么的 SHA-256 | 作用 |
| --- | --- | --- |
| `image_allowlist[]` | **镜像内容**（`incus image list` 的 FINGERPRINT 列） | 钉死这份租约能启动哪些镜像 |
| `server_certificate_fingerprint` | **daemon 证书的 DER** | 让消费者钉死它连的是同一台 daemon |

下文提到 fingerprint 时一律写明是哪一种。

## 安全边界

镜像 fingerprint 是镜像**内容**的标识，而 alias（例如 `images:debian/13`）只是一个指针，远端明天
可以把它指向新内容。因此固定镜像 fingerprint 的 allowlist 意味着一份租约能启动的东西在 apply 时
就已封闭：tag、alias 与远程 URL 一律不接受，否则一份审过的租约会随远端更新而漂移。Provider 拥有 profile、network 与 storage pool，不接受调用方传入 device、raw config、
挂载或宿主 socket。

Contract 不负责安装、配置或托管 Provider daemon 本身，也不要求 ANAS 宿主具备虚拟化能力——它
只是这台 daemon 的客户端控制面。`deletion_policy: delete` 仅表达显式删除意图；在 Core 提供
破坏性 Resource delete 入口前仍按 retain 处理。

一次性 Secret（例如 runner token）如何进入 guest 不属于本 Contract：那发生在租约交付之后，由
消费者经 Provider 的 exec stdin 通道注入，Secret 不得出现在命令参数、环境变量、cloud-init、
镜像、磁盘状态或日志中。
