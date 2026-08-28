---
doc_type: requirement
status: current
created: 2026-08-21
updated: 2026-08-22
---

# VersityGW S3 兼容 Module 集成要求

本文规定 ANAS 首个 S3 兼容 Runtime Module 的交付边界与验收标准。产品选型依据见
[开源自部署 S3 兼容文件与对象服务调研](../../docs/research/self-hosted-open-source-s3-compatible-storage-research.md)。
首期固定使用 VersityGW `v1.7.0` 的 POSIX backend，把 workspace 内的专用目录通过 S3 API
提供给客户端；它不是分布式对象存储，也不得把现有 Samba 用户树直接作为 backend。

## 1. 首期范围

首期必须包含固定版本、多架构容器、Traefik HTTPS path-style endpoint、SigV4 根凭据、
专用持久目录、健康检查、只读模式、Hook 单元测试与中英文文档。Module 在真实客户端、备份
恢复和升级验收完成前保持 `developing`。

同时提供两条显式路径：`object_storage/s3` Capability 只投影共享 root 连接；版本化
`object_storage` Contract + Resource 为每个声明的 Consumer 创建独立 bucket 和 AK/SK。Module
可以完全不声明对象存储功能，此时不得创建 bucket、账号、Secret 或投影相关环境变量。

首期不包含 WebUI/Admin API 公网入口、STS、细粒度 bucket policy/配额、virtual-host-style
bucket 域名、实验性 versioning/object lock、bucket notification 或跨节点复制。内部 Admin API
只允许 Provider lifecycle job 经私有容器网络调用。

## 2. 需求矩阵

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `S3-R-001` | Module 名为 `versitygw`、类别为 `storage`、状态为 `developing`，上游与镜像固定为 `1.7.0`，不得使用 `latest` | 静态 |
| `S3-R-002` | 只使用 VersityGW POSIX backend，object root 位于 `${DATA_PATH}/versitygw/objects`，不得挂载 Samba 用户树或 `${USER_DATA_PATH}` | 单元 |
| `S3-R-003` | S3 API 只经 Traefik HTTPS 暴露，不发布宿主明文端口，不改写 Host、path 或 query | 静态 |
| `S3-R-004` | 首期 endpoint 使用 path-style；文档不得宣称 wildcard bucket 域名或完整 AWS S3 等价 | 文档 |
| `S3-R-005` | root access key 有非空静态默认值；root secret 由 Hook 使用加密随机源生成并稳定保存，明文不进入 README、日志或 Compose argv | 单元 |
| `S3-R-006` | region、endpoint、容器名、object 路径和 IAM 路径由 Hook 确定性派生；重复 calculate 不改变现有 Secret | 单元 |
| `S3-R-007` | 服务启用一个不会与合法 bucket 名冲突的匿名健康路径，Compose 健康检查验证 HTTP 服务 | 静态 |
| `S3-R-008` | 业务进程以非 root UID/GID 运行；初始化入口只创建并校正挂载点，然后降权，且启用 no-new-privileges 与最小 capability | 单元 |
| `S3-R-009` | `read_only` 是显式 bool 参数并映射到 VersityGW 全局只读开关；所有配置参数声明类型、变更影响和用途 | 单元 |
| `S3-R-010` | object data、internal IAM、生成 Secret、deployment metadata 必须处于同一 snapshot/backup 恢复点；文档说明 POSIX 直写会绕过 S3 policy/metadata | 文档 |
| `S3-R-011` | WebUI/公网 Admin API、versioning、object lock、外部 IAM/STS、notification 与 replication 未实现时必须明确列为限制 | 文档 |
| `S3-R-012` | Module 必须提供中英文 README、技术文档、版本化本地化元数据，并登记模块与镜像目录 | 静态 |
| `S3-R-013` | Hook 测试覆盖默认派生、显式覆盖、稳定 Secret、错误 module 隔离以及 Compose/入口/Admin 管理面安全边界 | 单元 |
| `S3-R-014` | 真实部署必须用 AWS CLI 或兼容 SDK 验证 bucket/object CRUD、ListObjectsV2、range、multipart、presigned URL、错误凭据拒绝和重启持久性 | e2e |
| `S3-R-015` | 真实恢复必须从空 workspace 恢复 object data、internal IAM、root/Resource Secret 与 deployment metadata，并由 Resource 客户端读取原对象；完成前不得提升为 `release` | e2e |
| `S3-R-016` | Runner 注册中立 `object_storage` Capability 和唯一 `s3` interface；`versitygw` 声明提供该能力，不要求 Consumer 依赖具体 Module | 单元 |
| `S3-R-017` | 单 interface 简写允许 Consumer 仅声明 `dependencies.requires_capabilities: [{name: object_storage}]`，无需 Provider 名、selector 参数或私有环境变量 | 单元 |
| `S3-R-018` | 唯一启用 Provider 自动绑定并进入 Consumer 依赖闭包，Provider 必须排在 Consumer 前，Provider/interface 写入稳定 binding | 单元 |
| `S3-R-019` | VersityGW 必须发布 `ANAS_OBJECT_STORAGE_S3_*` 中立输出；Runner 只向已绑定 Consumer 投影 `ANAS_OBJECT_STORAGE_BINDING__<MODULE>__*` | 单元 |
| `S3-R-020` | S3 binding 必须同时包含 `INTERFACE`、`ENDPOINT`、`REGION`、`ACCESS_KEY_ID`、`SECRET_ACCESS_KEY` 与 `PATH_STYLE`，缺失任何必填输出都在 Consumer Hook 前失败 | 单元 |
| `S3-R-021` | Provider-side S3 输出和某 Consumer 的 binding Secret 不得进入无关 Module；binding key 由 Runner 保留，调用方或 Module 不得预置覆盖 | 单元 |
| `S3-R-022` | `SECRET_ACCESS_KEY` 的 Provider 输出与 Consumer 投影都继承 Secret 敏感性，不得进入 plan、lock、普通错误或其他 Consumer 的渲染环境 | 单元 |
| `S3-R-023` | 文档必须给出只声明能力的 Consumer 示例、统一字段语义，并明确共享 root credential 不是 bucket/租户隔离 | 文档 |
| `S3-R-024` | 新增版本化 `object_storage` Contract 1.0.0 与 `s3` interface，定义 Resource、ensure、inspect、rotate_credential 和 delete schema；VersityGW 实现必需 operation | 静态 |
| `S3-R-025` | 对象 Resource 必须 opt-in：只有同时声明 Contract dependency 与 `resources.requires` 的 Module 才解析和创建资源；未声明 Module 不产生 bucket、账号、Secret 或 Resource 环境变量 | 单元 |
| `S3-R-026` | 每个 Resource 必须拥有唯一 bucket、唯一 access key ID 与独立随机 secret；重复 render 复用同一 Secret，重复 bucket/access key 声明必须 fail closed | 单元 |
| `S3-R-027` | Runner 只向目标 Consumer 发布 `ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__*`，字段包含 interface、endpoint、region、bucket、AK、SK 与 path_style；SK 必须保持敏感 | 单元 |
| `S3-R-028` | VersityGW 启用持久 internal IAM 与独立 7071 Admin listener；IAM 位于 `${DATA_PATH}/versitygw/iam`，Admin API 不得有宿主端口、Traefik route 或 WebUI | 静态 |
| `S3-R-029` | Provider ensure 必须幂等创建/校正 `user` principal、secret 与 bucket owner；既有 bucket 属于其他 principal 时失败，不得自动夺取 ownership | 单元 + e2e |
| `S3-R-030` | Provider 必须实现 inspect 与单 principal credential rotate；移除 Resource 只进入 retained，不得隐式删除 bucket/对象；delete operation 在缺少显式破坏性 Core 入口时保持可选 | 单元 |
| `S3-R-031` | Resource state 与 deployment manifest 只保存 Secret Store 引用，不保存明文；object、IAM、Secret Store 与 deployment metadata 必须处于同一恢复点 | 单元 + 文档 |
| `S3-R-032` | Contract、Runner、Provider 和中英文 Module 文档必须说明 Capability/Resource 边界、可选声明、内部 IAM 明文文件风险和当前无 STS/policy/quota 的限制 | 文档 |

## 3. 安全与兼容边界

S3 root key 是部署级最高权限机器凭据，不是网页登录账号。操作者只能通过受保护的
`anas config secret get VERSITYGW_ROOT_SECRET_KEY` 显式取出生成值；普通配置清单与 plan 必须
保持脱敏。Runner 只把它投影给明确绑定 `object_storage` Capability 的 Consumer，且各
Consumer 看到自己的 `ANAS_OBJECT_STORAGE_BINDING__<MODULE>__SECRET_ACCESS_KEY`；无关 Module
和 Provider 私有命名空间不得可见。由于当前所有绑定共享 root credential，任一 Consumer
被攻陷都会影响全部 bucket，不能宣称最小权限或租户隔离。

Resource 路径不复用 root credential。Runner 为每个 `<consumer>.<resource_id>` 生成独立 Secret，
VersityGW internal IAM 保存 `user` principal，bucket owner 指向该 principal。Consumer 只获得自己
的 Resource namespace；这提供 bucket 所有权隔离，但不等价于 STS、条件 policy 或动态短期授权。
internal IAM 文件包含明文 JSON 字段，必须依靠 0700 目录、workspace 边界和一致恢复点保护。

Traefik 只终止 TLS 和转发原始请求。SigV4 会覆盖 Host、canonical URI、query 和签名 headers，
因此不得添加 strip-prefix、path rewrite 或 ForwardAuth。健康路径使用 `/_anas_health`；下划线
不属于合法 S3 bucket 名字符，避免永久占用一个可用 bucket。

POSIX backend 目录属于 VersityGW 的专用命名空间。即使文件在宿主可见，也不得把直接文件
访问描述为与 S3 完全一致：直接修改会绕过 S3 鉴权、元数据与未来版本语义。
