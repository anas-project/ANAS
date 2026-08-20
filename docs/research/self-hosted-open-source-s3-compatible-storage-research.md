---
doc_type: research
created: 2026-08-15
updated: 2026-08-15
evidence_as_of: 2026-08-15
---

# 开源自部署 S3 兼容文件与对象服务调研

本报告按[应用研究文档规范](/developer/research-document-standard)研究能够自行部署、对外提供 S3 API 的开源文件网关与对象存储，为 ANAS 后续 Runtime Module 或外部存储资源选型提供依据。动态版本、维护状态和许可证边界采集于 2026-08-15；报告是研究快照，不是当前部署说明。

## 1. 结论先行

1. **不存在一个适合所有场景的单一替代品**。如果需求是“把单机文件目录开放成 S3”，首个 PoC 应选 **VersityGW**；如果需求是“对象、文件、FUSE/NFS 统一在分布式存储上”，应选 **SeaweedFS**；如果只需要核心 S3、跨地点小集群和低资源占用，可选 **Garage**；已经具备专职存储运维和多节点硬件时才考虑 **Ceph RGW**。
2. **ANAS 首个 Runtime Module 候选是 VersityGW `v1.7.0`**。它是 Apache-2.0、单进程、提供官方 Docker/Helm 和 Linux amd64/arm64 制品，POSIX backend 直接把 bucket/key 映射为目录/文件，并有可选 Web UI、bucket policy、CORS、版本和对象锁 API。代价是高可用和耐久性完全继承底层文件系统；POSIX 版本控制仍被上游标为实验性，不能在 PoC 前承诺不可变备份或完整 AWS S3 等价。
3. **VersityGW 只能服务专用目录，不应直接覆盖现有 Samba 用户树**。S3 允许 `a` 与 `a/b` 这样的键同时存在，POSIX 不允许文件同时充当目录；直接文件写入还可能绕开 S3 policy、版本、retention 和元数据维护。若确需 S3 与 POSIX 共用命名空间，必须接受这组语义限制，并把它当成数据模型迁移，而不是多开一个端口。
4. **SeaweedFS `4.29` 是功能最完整的开源多协议候选**。其 S3 网关支持核心对象操作、multipart、policy、versioning、object lock、SSE、presigned URL、IAM/STS 等，并能通过 filer、FUSE 和 NFS 访问同一数据。生产拓扑却包含 master、volume server、filer、filer metadata DB、S3 gateway 和运维 worker；官方完整备份流程还要求协调卷数据与 filer 元数据。它适合独立存储产品或后续实验性 Module，不适合以“一个轻量容器”包装。
5. **Garage `v2.3.0` 的轻量和跨地点复制很有吸引力，但它不是广义 drop-in S3**。它支持常用 CRUD、multipart、presigned URL、静态网站与 SSE-C，却不支持 bucket policy、versioning、object lock、notification、S3 replication API 或 SSE-S3/KMS，lifecycle 也只覆盖过期与未完成 multipart 清理。只有依赖清单明确落在其支持子集时才应采用。
6. **Ceph RGW `20.2.2` 是成熟度和高级 S3 覆盖最强的严格开源候选之一，但不是家用 NAS 默认项**。Cephadm 本身要求多类系统依赖，典型生产集群使用 3 或 5 个 monitor，并需要独立 OSD、容量规划、故障域和恢复演练。已有 Ceph 集群时启用 RGW 很合理；为 1–20 人的单机服务专门引入 Ceph，运维成本通常超过收益。
7. **RustFS 已推进到 `1.0.0-rc.2`，仍只进入观察/隔离 PoC**。它采用 Apache-2.0、提供单机 Compose、控制台、OIDC 和较宽的 S3 功能面，但 GitHub release 仍明确标为 Pre-release，上游功能表仍把 distributed mode、lifecycle 和 KMS 标为 under testing；最近版本也持续修补节点间认证与存储恢复边界。等 GA、升级契约和恢复演练稳定后再重新排名。
8. **MinIO 不再作为新开源部署的默认答案**。历史 `minio/minio` server 代码仍是 AGPL-3.0，但仓库已在 2026-04-25 归档且只读，最后 release 是 `RELEASE.2025-10-15T17-29-55Z`。当前厂商发布的 MinIO Software 使用另一份许可证：没有 Enterprise Agreement 时只允许一个非生产内部评估实例。报告将“归档的 AGPL server”和“当前厂商软件”分开处理，不把二者混成一次许可证变更。
9. **复制、纠删码、RAID 和 Btrfs snapshot 都不是异地备份**。对象服务的恢复点必须同时包含 payload、对象元数据、bucket policy、版本/删除标记、IAM 凭据映射、加密密钥和部署版本；跨服务或跨节点备份还要证明这些数据处于一致时点。必须实际执行“空机恢复 + 应用读取”，不能以文件数量或 API `200` 代替恢复验证。
10. **实施顺序建议为 `VersityGW` 单机 PoC → 按真实消费者跑兼容测试 → 再判断是否需要 SeaweedFS/Garage/Ceph**。不要先部署多套存储再让应用试用，也不要仅凭“支持 S3”替换生产 endpoint。首轮必须覆盖普通附件、备份工具、multipart、presigned URL、virtual-host/path-style、versioning、object lock 和恢复演练。

## 2. 范围与研究方法

### 2.1 主题卡

```yaml
topic: self-hosted-open-source-s3-compatible-storage
title: 开源自部署 S3 兼容文件与对象服务
snapshot_date: 2026-08-15
decision_for: ANAS Runtime Module 或外部存储资源候选
must_be:
  - 服务端源代码使用通常意义的开源许可证
  - 可在自有 Linux 主机或集群部署
  - 对外提供 S3-compatible API endpoint
core_categories:
  - POSIX 文件系统到 S3 的网关
  - 原生对象存储
  - 同时提供对象与文件协议的分布式存储
adjacent_categories:
  - 多云或既有存储到 S3 的协议网关
  - 面向开发测试的 S3 emulator
  - 依赖 OpenStack、Kubernetes 或 Hadoop 的平台组件
excluded:
  - 只有 S3 客户端、FUSE 客户端或同步工具
  - 只有 Web 文件管理界面而不提供 S3 服务端 API
  - SaaS-only 或生产自部署需要非开源许可证
deployment_target:
  os: Linux
  runtime: Docker Engine + Docker Compose v2
  ingress: Traefik HTTPS
  architectures: [amd64, arm64]
target_scale:
  users: 1-20
  default_topology: 单主机 NAS
  optional_topology: 3 个以上故障域的独立存储集群
questions:
  - 哪些项目值得进入 ANAS Module PoC？
  - 实际支持到哪一层 S3 API，而不只是 CRUD？
  - 文件与对象能否安全共用命名空间？
  - 备份、恢复、升级和身份边界是否可管理？
```

### 2.2 发现与证据口径

候选从 [AlternativeTo 的 MinIO 开源替代项](https://alternativeto.net/software/minio-io/?license=opensource)、[Open Source Builders 的 Amazon S3 替代项](https://opensource.builders/alternatives/amazon-s3)、GitHub 的 `s3-storage`/`object-storage` topic，以及 Ceph、MinIO、SeaweedFS 等项目的相互比较和兼容性测试引用中发现。目录只用于发现；许可证、版本、S3 API、拓扑和限制均回到官方仓库、release 或当前版本文档核验。

本轮深入核验 17 个系统或网关：VersityGW、SeaweedFS、Garage、Ceph RGW、RustFS、Apache Ozone、CubeFS、OpenStack Swift、NooBaa、Zenko CloudServer、S3Proxy、MinIO、UltiHash、LeoFS、CORTX、Riak CS 与 rclone `serve s3`。只提供客户端能力的 s3fs、rclone mount、goofys、Mountpoint for S3、AWS CLI 和 SDK 不进入服务端排名。

### 2.3 “S3 兼容”的分层

“S3 compatible”不是认证标章，也不表示实现 AWS S3 的全部 API、错误码和边界行为。本报告用三个层级描述：

| 层级 | 最低能力 | 典型用途 |
| --- | --- | --- |
| L1 核心对象 | bucket/object CRUD、ListObjectsV2、range、multipart、SigV4、presigned URL | 附件、镜像、普通备份目标、SDK 开发 |
| L2 应用运维 | policy/ACL、CORS、versioning、lifecycle、tag、SSE、conditional request | 多租户应用、浏览器直传、保留与成本管理 |
| L3 平台语义 | IAM/STS、object lock、notification、replication、KMS、审计 | 不可变备份、事件驱动、跨站点、企业权限 |

任何项目都必须按实际消费者验证。例如 restic 主要依赖核心 API，而要求 S3 Object Lock 的备份产品、Velero 插件、Terraform provider 或依赖 bucket notification 的应用会触发完全不同的接口集合。

## 3. 先分清对象存储与文件系统

S3 key 是不透明字符串，“目录”通常只是 `/` 分隔的前缀；POSIX 文件系统则有 inode、目录、权限、rename、link、锁和大小写规则。把对象存储挂成 FUSE 不会自动获得完整 POSIX 语义，把文件树映射成 S3 也不会自动获得完整对象语义。

### 3.1 四种产品形态

| 形态 | 数据真实形态 | 代表 | 主要风险 |
| --- | --- | --- | --- |
| POSIX → S3 网关 | 普通目录和文件 | VersityGW、S3Proxy filesystem | S3 key 冲突、xattr/sidecar、一致性与直接访问绕过 |
| 原生对象存储 | 项目私有块/对象布局 | Garage、RustFS、MinIO、Ceph RGW | 不能直接浏览或修改后端文件；必须经 API/工具恢复 |
| 统一文件/对象平台 | 共享的分布式元数据与数据层 | SeaweedFS、CubeFS、Ozone、LeoFS | 组件多，协议语义仍存在交集限制 |
| 现有后端协议网关 | S3 请求翻译到云或其他对象存储 | NooBaa、Zenko、S3Proxy、Swift s3api | 最终一致性、ETag/metadata/ACL 差异随后端变化 |

### 3.2 共用目录的硬限制

- 在 S3 中，key `a` 和 `a/b` 可以同时存在；在 POSIX 中，`a` 不能同时是文件与目录。VersityGW 明确会让这类 PUT 失败；SeaweedFS 和 CubeFS 也记录了同类映射限制。
- 对象 metadata、tag、retention、owner 和 version 不能只靠文件内容表达。VersityGW 默认把 metadata 放在 xattr，非当前对象版本另存 shadow namespace；拷贝工具若不保留 xattr 或漏掉 versioning directory，恢复出来的文件内容可能在，但对象语义已经损坏。
- 从透明双入口架构可推断，直接通过 shell/SMB/NFS 修改底层文件通常不会生成 S3 version、事件或审核记录，也可能绕过 policy 和 object lock。对象锁若能被另一路径删除，就不能作为合规 WORM 证明。
- 原生对象存储的后端目录是内部格式，不能通过 Samba 共享给用户。要提供文件访问，应使用项目声明支持的 filer/FUSE/NFS 层，而不是暴露 volume/OSD 数据目录。

因此，ANAS 中的 S3 数据必须使用专用命名空间。即使选择 VersityGW，也应创建新的空目录，通过 S3 导入对象；只有明确需要透明文件访问的 bucket 才允许受控的 POSIX 访问。

## 4. 核心候选总览

符号：✅ 当前官方文档明确支持；◐ 部分、实验性或依后端；❌ 当前矩阵明确不支持；`PoC` 表示文档不足，必须以目标版本实测。

### 4.1 版本、许可证与定位

| 项目 | 研究版本/状态 | 许可证 | 主要定位 | ANAS 结论 |
| --- | --- | --- | --- | --- |
| [VersityGW](https://github.com/versity/versitygw) | [`v1.7.0`](https://github.com/versity/versitygw/releases)；2026-07-15 | Apache-2.0 | POSIX/ScoutFS/云后端到 S3 的无状态网关 | **单机 Module 首选 PoC** |
| [SeaweedFS](https://github.com/seaweedfs/seaweedfs) | [`4.29`](https://github.com/seaweedfs/seaweedfs/releases)；2026-05-26 | Apache-2.0 | 对象、文件、FUSE/NFS、Iceberg 的分布式存储 | **多协议/分布式首选 PoC** |
| [Garage](https://github.com/deuxfleurs-org/garage) | [`v2.3.0` 文档线](https://garagehq.deuxfleurs.fr/documentation/) | AGPL-3.0 | 小中型、跨地点、低资源对象存储 | 核心 S3 子集备选 |
| [Ceph RGW](https://docs.ceph.com/en/latest/radosgw/s3/) | [Tentacle `20.2.2`](https://docs.ceph.com/en/latest/releases/)；2026-06-16 | 主体 LGPL-2.1/3.0，另有 BSD 等 | 统一对象/块/文件的大型分布式平台 | 已有 Ceph 时推荐 |
| [RustFS](https://github.com/rustfs/rustfs) | [`1.0.0-rc.2`](https://github.com/rustfs/rustfs/releases)；Pre-release | Apache-2.0 | MinIO 风格的 Rust 对象存储 | 观察/隔离 PoC |
| [Apache Ozone](https://ozone.apache.org/) | [`2.1.0`](https://github.com/apache/ozone/releases)；2026-01-05 | Apache-2.0 | Hadoop 生态的数据湖对象存储 | Hadoop 专项 |
| [CubeFS](https://www.cubefs.io/docs/master/overview/introduction.html) | release 文档 `3.5.3`；版本需部署前重查 | Apache-2.0 | POSIX/S3/HDFS 多协议分布式文件系统 | K8s/大集群专项 |
| [OpenStack Swift](https://docs.openstack.org/swift/latest/) | 持续维护；发行线随 OpenStack | Apache-2.0 | OpenStack 对象存储，s3api middleware | 既有 OpenStack 专项 |

### 4.2 S3 能力比较

| 项目 | L1 核心/multipart | Policy/IAM | Versioning | Lifecycle | Object Lock | SSE/KMS | Event/Replication API | 文件协议/透明文件 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| VersityGW POSIX | ✅ | bucket policy；多种 IAM 后端 | ◐ 上游标实验性 | PoC | ◐ API 已列，需连同版本实测 | 未形成清晰 POSIX 矩阵 | event 文档存在；逐消费者 PoC | ✅ 透明 POSIX，有限制 |
| SeaweedFS | ✅ | ✅ policy、IAM、STS | ✅ | ◐ 无 storage transition | ✅ | ✅ SSE-S3/C/KMS | ❌ S3 notification/replication API | ✅ filer/FUSE/NFS |
| Garage | ✅ | ❌ AWS policy；自有 key/bucket 权限 | ❌ | ◐ expiration/abort MPU | ❌ | ◐ SSE-C | ❌ | ❌ 原生文件协议 |
| Ceph RGW | ✅ | ✅ 广泛 policy、IAM/STS 子集 | ✅ | ✅ | ✅，逐版本验证 | ✅ | ◐ notification/multisite 较强，部分 API 有差异 | CephFS 是同集群另一接口，不是同一对象命名空间 |
| RustFS RC | ✅ | ✅ policy/OIDC，仍需验证 | ✅ | ◐ under testing | 文档/矩阵 PoC | ◐ KMS under testing | replication/notification 宣称可用 | ❌ 原生 POSIX |
| Ozone 2.1 | ✅ 常用子集 | ◐ Kerberos/Ranger 路线 | ❌ S3 bucket versioning | ❌/roadmap | ❌ | ❌ S3 SSE；可加密 bucket | ❌/roadmap | ✅ OFS/Hadoop，同 bucket layout 有条件 |
| CubeFS | ✅ 基础集合 | ◐ bucket policy/ACL 文档分散 | PoC | PoC | 版本文档提及 WORM，PoC | PoC | PoC | ✅ POSIX/S3/HDFS，同名冲突 |

这张表不是兼容性证明。SeaweedFS 的[当前 S3 API 页面](https://github.com/seaweedfs/seaweedfs/wiki/Amazon-S3-API)、Garage 的[兼容矩阵](https://garagehq.deuxfleurs.fr/documentation/reference-manual/s3-compatibility/)、Ceph RGW 的[S3 API 实现清单](https://docs.ceph.com/en/latest/radosgw/s3/)、Ozone 的[当前 S3 API 表](https://ozone.apache.org/docs/user-guide/client-interfaces/s3/s3-api/)与 VersityGW 的[POSIX backend 操作表](https://github.com/versity/versitygw/wiki/POSIX-Backend)采用不同粒度；最终必须使用相同消费者测试集比较。

### 4.3 部署和运维比较

| 项目 | 最小形态 | 生产扩展形态 | 外部依赖 | 备份/恢复难度 | 默认适配度 |
| --- | --- | --- | --- | --- | --- |
| VersityGW | 1 进程 + 1 文件系统 | 多 gateway 需要真正共享且一致的 RWX backend；底层仍可能单点 | 无 DB；xattr 或实验性 sidecar | 低到中；数据、xattr、IAM、versions 同点保护 | 高 |
| SeaweedFS | `weed mini`/单机组合 | 3 master、N volume、3 filer、HA metadata DB、N S3 | filer DB；生产组件较多 | 高；卷与元数据必须一致 | 中 |
| Garage | 单 daemon，无冗余 | 通常 3 副本、跨至少 3 节点/地点 | 无外部 DB | 中；需保留 metadata snapshot 并演练 repair | 中高，前提是 API 子集足够 |
| Ceph RGW | 可单机实验 | 多 monitor/mgr/OSD/RGW，按故障域部署 | cephadm、容器运行时、LVM2、时钟同步等 | 高；依赖 Ceph 原生灾备与集群恢复 | 低，已有 Ceph 则高 |
| RustFS RC | 单容器/Compose | 分布式模式仍在测试 | 简单模式无外部 DB | 中高；升级/恢复契约未 GA | 低 |
| Ozone | 多 Java daemon | SCM/OM/DataNode HA 与 Hadoop 安全体系 | Java、Kerberos/Ranger 可选/常见 | 高 | 低 |
| CubeFS | 多角色集群 | 至少 3、建议 4 个 K8s 节点及多类 node | Kubernetes/主机卷 | 高 | 低 |

### 4.4 社区版与商业边界

| 项目 | 严格开源自部署边界 | 商业层影响 |
| --- | --- | --- |
| VersityGW | 仓库与 gateway 使用 Apache-2.0，当前部署流程未要求 license key | 厂商另有存储产品和支持；不影响本报告的 POSIX gateway 功能，但部署前仍需核验镜像条款 |
| SeaweedFS | community server 使用 Apache-2.0，可自行构建和部署 | 官方仓库同时宣传 enterprise edition 的 self-healing storage format 与更强数据保护；这使社区版/企业版耐久性边界成为 PoC 必查项 |
| Garage | 项目整体以 AGPL-3.0 提供，无功能 license key | 可购买生态服务不改变开源功能面；对外提供修改版服务时应评估 AGPL 义务 |
| Ceph/Ozone/CubeFS/Swift | 基金会或社区项目的开源发行可完整自部署 | 厂商发行、托管和支持是可选采购，不作为 S3 核心能力解锁条件 |
| RustFS | 当前仓库、镜像和功能使用 Apache-2.0，未见生产 license key | 风险来自 Pre-GA 成熟度而非付费功能墙；GA 时仍需重查发行条款 |

本表不把“厂商提供商业支持”视为开放核心。真正影响严格开源排名的是：生产运行需要非开源许可、关键身份/HA/恢复功能只在商业代码中，或免费自部署仅限评估；MinIO 与 UltiHash 因此在第 7 节单列。

## 5. 值得进入 PoC 的候选

### 5.1 VersityGW：最贴近“文件服务 + S3 接口”

VersityGW 是网关，不是自带分布式数据层的对象存储。它的优势恰好与 ANAS 单机 NAS 场景对齐：一个 Go 服务即可把专用 POSIX 根目录变成 S3 endpoint，官方提供 [Docker/Helm](https://github.com/versity/versitygw/wiki/Docker)、[amd64/arm64 制品](https://github.com/versity/versitygw/wiki/Linux-Install)、可选[对象浏览 Web UI](https://github.com/versity/versitygw/wiki/WebGUI)，最新 `v1.7.0` 还修复了 LDAP filter injection 与 path validation 绕过问题。

官方 [POSIX backend](https://github.com/versity/versitygw/wiki/POSIX-Backend)列出了 bucket policy、CORS、bucket ACL、tag、versioning、object lock、legal hold、retention、multipart、range 和 list versions；不支持 object ACL、restore object 与 S3 Select。默认元数据保存在 xattr，sidecar metadata 是替代选项；版本则放在独立 shadow directory，并被明确标为实验性。

身份层可使用内部 JSON、S3 object、Vault、LDAP 或 FreeIPA 等后端，详见[多租户/IAM 文档](https://github.com/versity/versitygw/wiki/Multi-Tenant)。但这不是 AWS IAM 的完整控制面，也不是 OIDC SSO：Web UI 直接使用 access key/secret key 登录；内部 JSON 的机密性主要依赖文件权限。ANAS 首版应只生成最小权限 AK/SK，LDAP 集成作为后续 PoC，不应把 oauth2-proxy 套在 S3 API 前面。

主要门槛：

- 数据耐久度、校验、RAID、磁盘故障恢复和跨机复制都来自底层文件系统/存储，不由 gateway 自动提供；
- 多个 gateway 副本只有在 IAM、versioning 和对象根都使用一致共享后端时才构成无状态扩展；单机 bind mount 上多开容器不会消除存储单点；
- xattr、版本目录和对象目录必须一起备份；用不保留 xattr 的复制工具会静默损失 metadata；
- 透明 POSIX 访问会绕过 S3 层的策略、版本和锁，不适合宣称合规不可变存储；
- bucket/key 到路径的映射意味着目录冲突、大小写、权限、symlink 与 rename 行为都要测试。

**结论：**进入 ANAS 第一顺位 PoC，但产品定位应写“为专用文件树提供 S3 API”，不写“分布式对象存储”。不可变备份场景在实验性版本控制、直接文件旁路和恢复演练全部通过前不得上线。

### 5.2 SeaweedFS：最完整的分布式多协议候选

SeaweedFS 的 S3 服务是无状态桥，bucket 默认映射到 filer 的 `/buckets/<bucket>`。它把对象层建立在文件元数据和 volume 数据层上，因此能用 S3、filer API、FUSE 和 NFS 访问相关数据；官方仓库同时覆盖 Linux amd64/arm64 release 资产。

其[当前 S3 API 页面](https://github.com/seaweedfs/seaweedfs/wiki/Amazon-S3-API)显示：核心 bucket/object/multipart、SigV4/presigned URL、policy、versioning、object lock、SSE-S3/SSE-C/SSE-KMS、IAM API，以及 AssumeRole、WebIdentity、LDAPIdentity 等 STS 路径已实现；lifecycle 不做 storage transition，bucket notification、S3 replication、website、Select/Restore 尚未实现或是 stub。

复杂度来自生产拓扑。官方[生产部署说明](https://github.com/seaweedfs/seaweedfs/blob/master/note/slides/seaweedfs-production-setup.md)示例包含 3 master、多个 volume server、3 filer、filer 的 HA metadata DB、多个 S3 server 以及 admin/worker。纠删码不是单机魔法：例如 5+3 shard 本身就需要足够节点和故障域。

备份尤其需要谨慎。官方[数据备份页面](https://github.com/seaweedfs/seaweedfs/wiki/Data-Backup)说明 `weed backup` 可增量拉取 volume，但构建完整镜像时必须同时移动 volume 与 filer metadata，并建议暂停写入以避免两套数据不匹配；页面还明确把分布式一致恢复、历史时间点与验证策略列为待完善问题。异步 filer metadata backup 或 replication 可降低 RPO，但不能替代完整、可回放的基线。

**结论：**如果目标是三节点以上的长期存储平台、需要文件与对象协议或处理大量小文件，SeaweedFS 是第二阶段首选。ANAS 单 workspace 无法覆盖跨主机 volume 和 metadata DB，生产集群更适合作为外部 Resource，由专用 provider 负责生命周期；单机 `weed mini` 只适合功能 PoC。

### 5.3 Garage：轻量跨地点核心对象存储

Garage 专门面向小中型自部署、跨地点节点和不均匀硬件，采用 AGPL-3.0，一个 daemon 自带 metadata 与数据管理，不依赖外部数据库。官方[快速开始](https://garagehq.deuxfleurs.fr/documentation/quick-start/)明确指出单节点没有冗余，不适合生产；生产应按故障域配置复制，常见 replication factor 为 3。

其优点是部署面小、静态网站、presigned、multipart、SSE-C、管理 API、Prometheus/OpenTelemetry 和后台 scrub/repair；官方还提供 metadata snapshot 与[耐久性/修复操作](https://garagehq.deuxfleurs.fr/documentation/operations/durability-repairs/)。

缺口同样明确：官方[兼容矩阵](https://garagehq.deuxfleurs.fr/documentation/reference-manual/s3-compatibility/)不支持 AWS bucket policy/ACL 模型、versioning、object lock、notification、replication API、SSE-S3/KMS；lifecycle 只覆盖 expiration 和中止未完成 multipart。Garage 使用自己的 access key 对 bucket 的权限模型，因此依赖 Terraform AWS provider、细粒度 IAM policy 或 immutable bucket 的系统可能直接不兼容。

**结论：**只在“核心 CRUD + multipart + presigned + 跨地点三副本”能覆盖全部消费者时进入 PoC。它可能比 SeaweedFS/Ceph 更适合低功耗多节点，但不能因轻量而忽略缺失 API。

### 5.4 Ceph RGW：成熟但重量级

Ceph RGW 的[实现矩阵](https://docs.ceph.com/en/latest/radosgw/s3/)覆盖广泛的 bucket/object、multipart、policy、versioning、lifecycle、tag、notification、storage class、object ownership、public access、encryption 与 multisite 能力；[IAM API](https://docs.ceph.com/en/latest/radosgw/iam/)和 STS 是 AWS 子集，必须按目标版本核验。当前稳定 Tentacle `20.2.2` 的预计维护期到 2027-11。

部署代价是整个 Ceph，而不只是 RGW 容器。[Cephadm 安装文档](https://docs.ceph.com/en/tentacle/cephadm/install/)列出 Python、systemd、Podman/Docker、时间同步和 LVM2 等前置条件，典型 monitor 数量为 3 或 5，单主机通常不适合生产。CephFS 与 RGW 可以共享一个 RADOS 集群，但它们不是让同一个对象同时以 POSIX 路径读写的透明双协议视图。

**结论：**已有 Ceph 平台或明确需要块、文件、对象统一基础设施时优先；ANAS 可以管理 endpoint 与 credential resource，但不应把完整 Ceph 生命周期塞进普通应用 Module。

### 5.5 RustFS：快速成熟中的 Pre-GA 候选

RustFS `1.0.0-rc.2` 提供单节点 Docker/Compose、Web 控制台、Linux amd64/arm64 构建、versioning、replication、notification、policy 和 OIDC claims 映射，Apache-2.0 对嵌入与商业使用友好。它是当前最接近“现代 MinIO 式体验”的严格开源新项目之一。

但[项目当前功能表](https://github.com/rustfs/rustfs)仍把 distributed mode、lifecycle 和 KMS 标成 under testing，GitHub release 仍标 Pre-release。2026 年 beta/RC 迭代中还出现过默认 secret 导致节点间 RPC 签名可预测、绕过 S3 IAM 的问题，后续版本已改为 fail closed；这说明项目在认真加固，也说明安全和数据恢复边界仍快速变化。上游性能数字是项目自测，不作为本报告排名依据。

**结论：**可在隔离数据上运行与 VersityGW/SeaweedFS 相同的 PoC，用于观察 API 覆盖和迁移体验；在 GA、两次跨版本升级、磁盘损坏恢复与安全基线验证前不承载唯一数据副本。

## 6. 平台型与特定生态候选

### 6.1 Apache Ozone

Ozone 面向 Hadoop/数据湖，提供 Ozone Shell、Hadoop-compatible filesystem 与 S3 gateway。稳定 `2.1.0` 支持常用 bucket/object/multipart 和 presigned 操作，但当前[S3 API 文档](https://ozone.apache.org/docs/user-guide/client-interfaces/s3/s3-api/)仍列出 bucket versioning、object lock、S3 SSE、Select、完整 policy/ACL、notification 和 cross-region replication 等缺口或 roadmap。安全部署通常走 Kerberos 生成 S3 secret、Ranger/KMS 管理加密 bucket。

它不是轻量 S3 appliance；只有 ANAS 未来服务 Hadoop/Iceberg 数据湖或已有 Ozone 时才进入专项验证。还要注意当前网站可能展示 2.2 开发线能力，不能把尚未进入稳定 `2.1.0` 的 conditional request 等功能提前计入。

### 6.2 CubeFS

CubeFS 是 CNCF graduated 的 Apache-2.0 分布式文件系统，ObjectNode 可让同一 CubeFS 数据通过 [POSIX 与 S3](https://www.cubefs.io/docs/master/design/objectnode.html)访问。其[主 S3 API 表](https://www.cubefs.io/docs/master/user-guide/objectnode.html)主要覆盖基础 bucket/object/multipart；版本特定文档还提及 policy、ACL 和 WORM，但缺少像 Ceph/Garage 那样统一、逐 API 的当前矩阵，应全部进入 PoC。

官方 Kubernetes 部署建议[至少 3、最好 4 个节点](https://www.cubefs.io/docs/master/deploy/k8s.html)，并包含 master、meta node、data node、object node 等角色。它适合已有 Kubernetes 和多节点文件平台需求，不适合作为单机 S3 Module。

### 6.3 OpenStack Swift

Swift 是成熟的 Apache-2.0 分布式对象存储，`s3api` middleware 在 Swift 语义上模拟 S3。它的[架构](https://docs.openstack.org/swift/latest/admin/objectstorage-arch.html)包含 proxy、account、container 和 object server；[S3 compatibility 文档](https://docs.openstack.org/swift/rocky/s3_compat.html)记录了 API 与响应语义差异。当前安装和身份文档围绕 Keystone/OpenStack 生态。

**结论：**既有 OpenStack 集群可以复用；为 ANAS 单独搭 Swift 再加 s3api 没有优势。旧兼容矩阵还必须以当前 release 做回归，不能直接当 2026 状态表。

### 6.4 LeoFS

LeoFS 是 Apache-2.0、Erlang 实现的最终一致分布式对象存储，由 LeoGateway、LeoStorage 和 LeoManager 组成，支持 S3、REST、多数据中心复制和 NFS。上游仓库 2026 年仍有活动，但主文档仍引用 Ubuntu 14.04/16.04、OTP 19.3，version 2 的 encryption、expiration 和 object versioning 仍列在 WIP/后续 roadmap。

**结论：**保留为有既有运维经验时的专项候选，不进入新 ANAS Module 排名；先要求当前稳定 release、支持 OS/OTP、S3 矩阵与恢复 runbook 四项证据。

## 7. 网关、开发工具、开放核心与停止项目

| 项目 | 分类 | 处理理由 |
| --- | --- | --- |
| [NooBaa](https://github.com/noobaa/noobaa-core) | Kubernetes 多云网关 | Apache-2.0，可把 S3/GCS/Azure/文件系统组织为 tier/mirror/spread；[operator](https://github.com/noobaa/noobaa-operator)依赖 Kubernetes，默认含 PostgreSQL，适合混合云 bucket class，不是单机存储引擎 |
| [Zenko CloudServer](https://github.com/scality/cloudserver) | 开发/嵌入式 S3 服务 | Apache-2.0，`9.4.1` 仍活跃，可接文件、内存和多种后端；README 主要定位开发、CI 和抽象层，用户管理 Vault 为 proprietary，不作为默认生产数据层 |
| [S3Proxy](https://github.com/gaul/s3proxy) | 协议代理/emulator | Apache-2.0，Java 17，适合把 B2/Azure/GCS/Swift/本地文件翻译成 S3 或测试；官方限制明确包含无 lifecycle、policy、versioning、object lock、SSE、notification 等 |
| [rclone serve s3](https://rclone.org/commands/rclone_serve_s3/) | 工具型网关 | 适合临时把 rclone backend 暴露为 S3；生命周期、HA、IAM 与恢复契约不足，不作为权威存储服务 |
| [UltiHash Core](https://github.com/UltiHash/core) | 许可证过渡/源码可见 | core 仓库是 Apache-2.0，但当前[自部署页面](https://www.ultihash.io/self-hosted)把免费档限定为 10 TiB test/PoC、生产付费，[服务条款](https://www.ultihash.io/terms-of-service)仍把 self-hosted software 写为 proprietary；待无 license key 的完整开源发行后重评 |
| [MinIO server](https://github.com/minio/minio) | **停止维护的历史开源项目** | AGPL-3.0 server 仓库已于 2026-04-25 归档；最后 release 为 2025-10-15，不用于新部署 |
| [当前 MinIO Software](https://docs.min.io/license/) | 非开源生产许可 | 无 Enterprise Agreement 时只允许一个非生产内部评估实例；不计入严格开源候选 |
| [CORTX](https://github.com/Seagate/cortx) | 停止/研究项目 | 仓库 2024-05-03 归档并明确“不再维护”“不用于生产” |
| [Riak CS](https://github.com/basho/riak_cs) | 历史/遗留 | Apache-2.0、基于 Riak 的旧 S3 服务，当前缺少现代发行、安全和部署维护证据；仅为存量系统保留 |

Zenko、NooBaa 和 S3Proxy 可以成为“把现有后端统一成 S3”的解法，但翻译层不能消除后端差异。ETag、metadata、ACL、conditional request、multipart 和一致性语义仍可能随 backend 改变；它们不应被评价为与自带数据耐久层的 Garage/Ceph 等价。

## 8. 安全、身份和网络边界

### 8.1 S3 API 身份不等于网页登录 SSO

S3 客户端通常使用长期 AK/SK 或 STS 临时凭据签署 SigV4。给控制台接 OIDC、在 Traefik 前加 ForwardAuth，并不会让 AWS SDK 自动获得 S3 凭据。正确集成顺序是：

1. 首选服务原生 IAM/STS，使用 OIDC WebIdentity、LDAPIdentity 或明确的 service account；
2. 每个应用独立 credential，按 bucket/prefix 最小授权，禁止共用 root key；
3. 长期 secret 进入 ANAS Secret Store，配置、Compose labels、日志和研究文档不得保存明文；
4. 轮换必须同时验证新 key 生效、旧 key 失效和正在进行的 multipart 行为；
5. root/break-glass credential 离线保存，默认控制台和管理 API 不暴露公网。

VersityGW 的 LDAP 是 credential/identity backend，不是 OIDC 控制台 SSO；SeaweedFS 和 RustFS 的 OIDC/STS 更接近临时 credential 流程，但仍需做 issuer、audience、claim、policy 和过期回归。Garage 使用自己的 key/bucket 权限，不能把 AWS IAM policy 文件原样迁入。

### 8.2 Traefik、域名与 SigV4

- 首版优先 `https://s3.example.com/bucket/key` 的 path-style endpoint，避免每个 bucket 参与 DNS；必须确认目标 SDK 允许强制 path style。
- virtual-host style `https://bucket.s3.example.com/key` 需要 `*.s3.example.com` 的 DNS 与证书。现有 `*.example.com` 通配符只覆盖一层标签，**不覆盖** `bucket.s3.example.com`。
- Traefik 必须保留 `Host`、原始 path/query、签名相关 headers，不能在认证后重写 canonical URI；代理缓冲、请求体大小、空闲超时和 multipart 长连接必须按最大对象验证。
- 时间偏差会导致 SigV4 拒绝；主机与客户端都需可靠 NTP。
- 浏览器直传还依赖 bucket CORS 与 presigned URL，控制台可用不能证明业务前端直传可用。

### 8.3 主机与数据安全

- 禁止默认 AK/SK 和 anonymous/public bucket；若产品没有 S3 Public Access Block，Module 需要用自己的默认配置和验收测试补足。
- 服务以固定非 root UID/GID 运行，只授予专用数据目录；不要把整个 workspace、Docker socket 或任意宿主目录挂入网关。
- SSE 只保护存储层读取，不替代 TLS、主机磁盘加密和密钥备份。KMS 不可用时不能静默降级到明文。
- 自部署 object lock 主要防应用凭据误删或勒索。宿主 root、直接 POSIX 路径或具有管理密钥的运维者仍可能绕过；合规 WORM 需要独立威胁模型、审计和硬件/组织控制。

## 9. 数据、备份、恢复与迁移

### 9.1 必须保护的恢复面

| 数据面 | 示例 | 漏备结果 |
| --- | --- | --- |
| 对象 payload | 文件、volume、OSD/block | 直接丢数据 |
| 对象 metadata | content-type、checksum、ETag、tag、xattr/sidecar | SDK 行为改变或对象不可读 |
| bucket 控制面 | policy、CORS、versioning、lifecycle、lock | 权限放大、旧版本/保留失效 |
| 版本数据 | noncurrent version、delete marker、shadow directory | 无法时间点恢复 |
| 身份 | user/service account、AK/SK hash、LDAP mapping、STS config | 应用无法登录或越权 |
| 加密 | KMS key、SSE master key、TLS key | payload 存在但永久不可解密 |
| 集群元数据 | filer DB、Garage metadata、Ceph maps、Ozone metadata | 数据块存在但不能定位 |
| 运行版本 | image digest、配置 schema、拓扑 | 无法用兼容版本重建 |

### 9.2 备份策略

- **单机 VersityGW：**把 object root、xattr、versioning directory 和内部 IAM 放入一个一致性域。建议位于 workspace 的 `userdata/versitygw/` 下不同子目录，运行配置与可重建凭据物化放 `data/`；备份 hook 先阻止写入，再让 ANAS backup 包含 `userdata/`。不能只做普通 pre-apply snapshot，因为 ANAS 默认不把 `userdata/` 放入普通 snapshot。
- **单机原生对象存储：**若上游明确支持冷文件系统快照，先停写/停服务再对 metadata 与 data 的所有路径做同点 Btrfs snapshot；否则用项目原生导出或 S3 API 复制。快照仍需发送到异盘/异机。
- **多节点集群：**不能对每台机器各拍一个“差不多同时”的快照就声称一致。优先项目原生 backup、replication/export 或经协调的 quiesce；记录 generation/epoch，并在隔离集群恢复。
- **便携副本：**可以用 AWS CLI/rclone 等按 S3 API 复制当前对象，但普通 sync 通常不会完整迁移 version、delete marker、policy、retention、IAM、event 和 lifecycle。它是内容副本，不是控制面灾备。
- **复制不是备份：**错误删除、被盗管理员 key、软件 bug 和加密勒索会快速复制到各副本。至少保留一个独立 credential、不可被源端删除的异地恢复点。

### 9.3 恢复验收

恢复测试必须从空机器或空集群开始，并验证：

1. 用锁定的旧镜像恢复 metadata 和 payload，服务可以启动；
2. 原应用凭据的最小权限仍正确，root key 未意外暴露；
3. 普通对象、0-byte、UTF-8 key、大对象、multipart、range、checksum 与 custom metadata 可读；
4. version list、delete marker、retention/legal hold 和 lifecycle 状态符合预期；
5. presigned URL、path-style/virtual-host style、CORS 和反向代理签名不变；
6. 抽样或全量 checksum 对比通过；
7. 写入新对象后再升级到目标版本，证明恢复介质不是“只能看不能继续运行”。

### 9.4 迁移原则

- 新旧 endpoint 并行，先做基线复制，再做增量/短暂停写切换；不要直接把原服务的数据目录挂给另一个实现。
- 对当前对象与历史版本分别制定迁移目标。若目标不支持 versioning/object lock，应明确接受丢弃哪些语义，而不是只比较对象数。
- presigned URL 包含 endpoint 和有效期，切换后旧 URL 通常失效；应用缓存与数据库中的 URL 需要清查。
- ETag 不总是 MD5，multipart 和加密对象尤其如此；完整性应使用明确 checksum 或下载后哈希。
- 切换前冻结 bucket policy、CORS、lifecycle、notification 和 credential 变更，切换后逐项对账。

## 10. ANAS 适配设计

### 10.1 Runtime Module 与外部 Resource 分界

| 形态 | ANAS 管理建议 | 原因 |
| --- | --- | --- |
| 单机 VersityGW + workspace 文件系统 | Runtime Module | 生命周期、数据路径、Traefik、secret、backup 可落在一个 workspace |
| 单机 SeaweedFS/RustFS 功能实验 | experimental Module | 只验证 API；不能把单机结果外推为分布式可靠性 |
| Garage/SeaweedFS/Ceph 多节点集群 | 外部 storage Resource/provider | 单 workspace 无法原子管理跨主机数据、故障域和恢复 |
| 既有 Ceph/Swift/NooBaa | endpoint + credential Resource | ANAS 消费服务，不接管底层平台 |

### 10.2 VersityGW Module 最小设计

- **版本：**固定 `v1.7.0` tag，发行前解析并锁定 image digest；禁止 `latest`。
- **架构：**验证官方 linux/amd64 与 linux/arm64 镜像/二进制，不自行声称其他平台。
- **数据：**在 workspace 内使用 `userdata/versitygw/objects`、`versions`、`iam` 这样的专用子目录；不允许用户传入 workspace 外绝对路径。保留 xattr，并验证 ANAS backup/restore 实际保存它。
- **网络：**S3 API 与 Web UI 分开 router；首版只公开 path-style S3 endpoint，Web UI 默认仅局域网或管理员访问。virtual-host style 作为显式能力，要求 `*.s3.<domain>` DNS/证书。
- **身份：**root AK/SK 由 Secret Store 生成；每个消费者创建独立 service account。首版不声明 OIDC；LDAP 只有完成 account disable、group/UID/GID mapping 和 credential rotation E2E 后才声明 capability。
- **健康：**进程健康不够；readiness 应使用受限 credential 完成 Head/List 或独立 health endpoint，另有定时 put/get/delete canary。
- **备份：**hook 进入只读或停服务，创建包含 `userdata` 的 backup；恢复后自动运行对象、metadata、version 和 policy smoke test。普通升级 snapshot 不包含 userdata，因此升级前仍需应用级兼容检查和必要的显式 backup。
- **升级：**只允许相邻已验证版本；先在克隆 workspace/恢复副本上升级，检查 xattr、IAM schema 与 versioning namespace，再切生产。binary 回滚不能假定 data schema 可回滚。

### 10.3 不应在首版声明的能力

- 不声明“100% S3 compatible”或 AWS 认证；只公布已通过的消费者与 API case；
- 不声明多节点 HA，除非共享 backend、IAM、versioning、load balancer 和故障切换全部实测；
- 不声明 immutable/compliance backup，直到实验性 versioning、object lock、host bypass 和恢复审计有完整证据；
- 不把 Web UI 的 AK/SK 登录写成 SSO；
- 不允许 S3 与 Samba 默认指向同一目录；
- 不把 Btrfs snapshot、RAID 或副本数写成 backup。

## 11. PoC 验收矩阵

### 11.1 三条工作负载线

| 工作负载 | 必测能力 | 淘汰条件 |
| --- | --- | --- |
| 应用附件/制品 | CRUD、ListV2 分页、multipart、range、presigned、CORS、SDK retry | 目标应用任何核心操作失败或需不安全全局权限 |
| 备份目标 | 大/小对象、并发、versioning、delete marker、object lock、retention、恢复 | 所需 immutability API 缺失，或空机恢复不能重现版本/锁 |
| 文件与 S3 共用 | UTF-8、case、文件/目录冲突、rename、xattr、direct write、权限 | 任一路径可绕过必须的 policy/retention，或数据模型无法约束 |

### 11.2 通用测试清单

- [ ] 锁定版本、镜像 digest、许可证文本与支持架构；记录测试日期。
- [ ] AWS CLI 与实际 SDK 使用 SigV4 完成 bucket/object CRUD、pagination、range、conditional request。
- [ ] 5 MiB 边界、100+ parts、abort/resume、UploadPartCopy 和失败重试均不留孤儿数据。
- [ ] presigned GET/PUT 在代理后成立；header、query、UTF-8、空格、`+`、`%2F` 与超时行为正确。
- [ ] path-style 与目标应用实际 addressing style 一致；virtual-host 测试 wildcard DNS/cert。
- [ ] bucket policy/ACL、CORS、anonymous/public、service account 最小权限和 deny 优先级正确。
- [ ] versioning enable/suspend、delete marker、list versions、恢复旧版本与并发覆盖正确。
- [ ] object lock Governance/Compliance、legal hold、默认 retention 和管理员旁路符合威胁模型。
- [ ] SSE-C/SSE-S3/KMS 仅在项目支持时启用；key 丢失、轮换、恢复与错误 key 均测试。
- [ ] 主机重启、进程 kill、磁盘满、只读文件系统、单节点/单盘故障与网络分区按拓扑注入。
- [ ] 备份在全新环境恢复，应用真实读取成功，checksum、metadata、policy、version 与 IAM 对账。
- [ ] 从当前版本升级到下一版并回归；失败时证明可从 backup 恢复，而不只是回滚镜像。
- [ ] Prometheus/日志不包含 AK/SK、Authorization header、presigned query 或对象敏感名称。

### 11.3 候选的停止线

| 候选 | 进入 PoC 前提 | 停止线 |
| --- | --- | --- |
| VersityGW | 专用 POSIX 目录、可接受单机底层耐久性 | version/lock 或 xattr 恢复不可靠；消费者依赖缺失 API |
| SeaweedFS | 能运维完整拓扑和 filer DB | 无法形成可重复的 metadata+volume 一致恢复流程 |
| Garage | 所有消费者 API 都落在官方支持矩阵 | 任何应用要求 policy、versioning、object lock、notification 或 KMS |
| Ceph RGW | 已有或明确投资多节点 Ceph | 只为一个轻量 S3 endpoint；无专人做容量与恢复演练 |
| RustFS | 只用隔离/可再生数据 | 仍是 Pre-GA，或分布式/升级/恢复测试任一不稳定 |

## 12. 决策与后续动作

### 12.1 当前决策

1. 建立 `versitygw` 实验性 Runtime Module，固定 `v1.7.0`，默认 path-style endpoint、专用 `userdata` 命名空间和独立 Web UI router。
2. 用一个真实附件应用、一个备份工具和 AWS CLI/SDK 建立消费者驱动测试集；测试集成为以后替换 endpoint 的兼容契约。
3. VersityGW 只通过普通对象线时，可以作为应用附件/制品存储；只有 versioning、object lock、旁路威胁和空机恢复全部通过后，才允许“不可变备份目标”标签。
4. 若出现多节点、跨地点或文件/FUSE/NFS 需求，分别启动 Garage 与 SeaweedFS 对照 PoC；不是在 VersityGW 上叠加多个容器伪装分布式。
5. 已有 Ceph/OpenStack/Kubernetes 存储时优先接入外部 endpoint，ANAS 只管理 consumer credential、health 和备份依赖，不接管集群。
6. RustFS 在 GA 后重新核验 release、S3 matrix、distributed/lifecycle/KMS 状态、安全公告和两个相邻版本升级，再决定是否取代 VersityGW 单机候选。

### 12.2 90 天后必须重查

- MinIO 归档仓库或许可证是否出现官方继任/社区维护分支；
- RustFS 是否 GA，以及 distributed、lifecycle、KMS 是否离开 under testing；
- VersityGW POSIX versioning 是否结束 experimental，S3 API 与 event/encryption 文档是否补全；
- SeaweedFS 4.29 之后的 release、社区/enterprise 数据保护边界与备份 runbook；
- Garage S3 compatibility 页面是否新增 versioning、policy、lock 或 notification；
- Ceph 当前稳定线与 EOL、Ozone 稳定版是否吸收 2.2 开发能力；
- 所有官方镜像的 amd64/arm64 manifest、签名/SBOM 和已知 CVE。

## 13. 主要官方来源

- VersityGW：[仓库](https://github.com/versity/versitygw)、[releases](https://github.com/versity/versitygw/releases)、[POSIX backend](https://github.com/versity/versitygw/wiki/POSIX-Backend)、[Multi-Tenant/IAM](https://github.com/versity/versitygw/wiki/Multi-Tenant)、[Docker/Helm](https://github.com/versity/versitygw/wiki/Docker)
- SeaweedFS：[仓库与许可证](https://github.com/seaweedfs/seaweedfs)、[releases](https://github.com/seaweedfs/seaweedfs/releases)、[S3 API](https://github.com/seaweedfs/seaweedfs/wiki/Amazon-S3-API)、[生产拓扑](https://github.com/seaweedfs/seaweedfs/blob/master/note/slides/seaweedfs-production-setup.md)、[数据备份](https://github.com/seaweedfs/seaweedfs/wiki/Data-Backup)
- Garage：[文档与 quick start](https://garagehq.deuxfleurs.fr/documentation/)、[features](https://garagehq.deuxfleurs.fr/documentation/reference-manual/features/)、[S3 compatibility](https://garagehq.deuxfleurs.fr/documentation/reference-manual/s3-compatibility/)、[durability/repairs](https://garagehq.deuxfleurs.fr/documentation/operations/durability-repairs/)
- Ceph：[releases](https://docs.ceph.com/en/latest/releases/)、[RGW S3 API](https://docs.ceph.com/en/latest/radosgw/s3/)、[IAM](https://docs.ceph.com/en/latest/radosgw/iam/)、[cephadm install](https://docs.ceph.com/en/tentacle/cephadm/install/)、[multisite](https://docs.ceph.com/en/tentacle/radosgw/multisite/)
- RustFS：[仓库/功能状态](https://github.com/rustfs/rustfs)、[releases](https://github.com/rustfs/rustfs/releases)、[架构文档](https://docs.rustfs.com/concepts/architecture)
- Apache Ozone：[releases](https://github.com/apache/ozone/releases)、[S3 API](https://ozone.apache.org/docs/user-guide/client-interfaces/s3/s3-api/)、[多接口读写](https://ozone.apache.org/docs/quick-start/reading-writing-data/)
- CubeFS：[介绍](https://www.cubefs.io/docs/master/overview/introduction.html)、[ObjectNode 设计](https://www.cubefs.io/docs/master/design/objectnode.html)、[S3 API](https://www.cubefs.io/docs/master/user-guide/objectnode.html)、[Kubernetes 部署](https://www.cubefs.io/docs/master/deploy/k8s.html)
- OpenStack Swift：[架构](https://docs.openstack.org/swift/latest/admin/objectstorage-arch.html)、[S3 compatibility](https://docs.openstack.org/swift/rocky/s3_compat.html)
- 历史与许可证：[MinIO archive/releases](https://github.com/minio/minio/releases)、[当前 MinIO Software License](https://docs.min.io/license/)、[CORTX archive](https://github.com/Seagate/cortx)、[UltiHash self-hosted](https://www.ultihash.io/self-hosted)

---

**重验证规则：**动态版本、维护状态、许可证、自部署免费边界、S3 API 和商业功能边界在 90 天后视为过期；任何进入 Module 的候选还必须在锁定 tag/image digest 上复核 Compose、镜像架构、数据路径、healthcheck、身份、备份和升级说明。
