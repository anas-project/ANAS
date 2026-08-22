# VersityGW S3 gateway 技术实现

本文记录 `versitygw` 首期 POSIX backend 实现。用户配置与客户端操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `1.7.0-r3` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | HTTPS reverse proxy |
| `object_storage` | 提供的 Capability | `s3` |
| `object_storage` | 提供的 Contract | `1.0.0` / `s3` |

Capability 发布统一服务级连接信息；Contract Provider 管理 per-Resource IAM user、bucket 与凭据。

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_versitygw` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-versitygw:1.7.0-r3` | `default` | 2 |
| `anas_versitygw_provision` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-versitygw:1.7.0-r3` | `default` | 1 |
<!-- generated:compose-topology:end -->

常驻 `anas_versitygw` 与一次性 `anas_versitygw_provision` 连接 Traefik external network，没有
宿主 `ports`。Traefik 只按 Host 路由到 S3 7070，
不使用 path rewrite 或 ForwardAuth，避免破坏 SigV4 canonical request。对象 bind mount 是
`${DATA_PATH}/versitygw/objects:/data/objects`；IAM bind mount 是
`${DATA_PATH}/versitygw/iam:/data/iam`。Admin listener 7071 没有 Traefik label，只能被同一
container network 上的 lifecycle job 使用。`/_anas_health` 使用 S3 bucket 名不允许的下划线，
不会占用合法 bucket 名。

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `versitygw.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `s3` | `static` | `VERSITYGW_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | S3 HTTPS endpoint 的域名前缀 |
| `versitygw.read_only` | bool | — | `false` | `static` | `VERSITYGW_READ_ONLY` | 否 | 否 | 否 | 是 | `container_recreate` | 启用上游全局只读保护 |
| `versitygw.region` | string | `length: 1..64`; `pattern: ^[A-Za-z0-9][A-Za-z0-9._-]*$` | `us-east-1` | `static` | `VERSITYGW_REGION` | 否 | 否 | 否 | 是 | `container_recreate` | SigV4 签名 region |
| `versitygw.root_access_key` | string | `length: 3..64`; `pattern: ^[A-Za-z0-9._-]+$` | `ANASROOT` | `static` | `VERSITYGW_ROOT_ACCESS_KEY` | 否 | 否 | 否 | 是 | `container_recreate` | root S3 credential 的 access key 标识 |
| `versitygw.root_secret_key` | string | `length: 16..128` | — | `generated` | `VERSITYGW_ROOT_SECRET_KEY` | 否 | 是 | 是 | 是 | `container_recreate` | root S3 credential 的 secret key |

参数库存以 `module.yml` 为准。`read_only` 直接投影为 `VGW_READ_ONLY`；region 必须与客户端
SigV4 签名一致。endpoint、container name 和 object path 是 Hook 私有派生值，不是用户参数。

## Capability 输出 ABI

Hook 以 `ANAS_OBJECT_STORAGE_S3_` 为前缀发布 endpoint、region、access key、secret access key
和 path-style。Runner 在 Provider calculate 后校验完整性，并为每个绑定 Consumer 生成
`ANAS_OBJECT_STORAGE_BINDING__<MODULE>__*`。Consumer 只声明 `name: object_storage`，不声明
Provider、selector 或 `config.consumes`，也看不到 Provider-side namespace。

binding 的 `SECRET_ACCESS_KEY` 属于目标 Consumer 且保持敏感；无关 Module 和其他 Consumer
不可见。当前值仍是共享 root credential，不等价于 per-bucket principal 或权限隔离。

## Contract Resource 生命周期

`object_storage/s3` Contract Provider 通过 `compose run --rm --no-deps --no-TTY
anas_versitygw_provision` 执行。`ensure` 等待私有 Admin API，然后：

1. 创建 `user` principal，或把既有 principal 的 secret 对账为 Runner 期望值；
2. 创建 bucket 并直接指定 owner；
3. 若 bucket 已属于其他 principal，fail closed，不自动夺取 ownership；
4. 写入只含 Secret Store 引用的 `anas.resource-state/v1` 状态。

`inspect` 验证 user role 和 bucket owner；`rotate_credential` 更新该 principal 的 secret。Contract
的 `delete` 为可选且当前未提供：移除声明只进入 `retained`，不隐式清空或删除对象。Runner 只向
声明 Resource 的 Consumer 发布 `ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__*`，
未声明的 Module 不触发 operation，也不获得凭据。

## 运行身份与文件边界

派生镜像固定基于 `ghcr.io/versity/versitygw:v1.7.0`，只增加 `su-exec` 和 ANAS entrypoint。
`su-exec` 是 Alpine 的小型 exec/setuid 工具，用于直接替换 PID 1 并降权，避免自写用户切换器
或让 shell 继续作为 root 父进程。
容器以 root 进入最小初始化阶段，且只保留 `CHOWN`、`FOWNER`、`SETUID`、`SETGID` capability。entrypoint
创建/校正 object 与 IAM mount root，不递归 chown 对象树，然后以 `1000:1000` exec 上游 entrypoint；业务
进程不保留 root。root filesystem 为只读，`/tmp` 是限定大小的 tmpfs，启用
`no-new-privileges`。

`chmod 0700` 和 `umask 0077` 把专用 backend 限制给 gateway runtime identity。它不是与 Samba
共用的透明文件树；宿主直接修改即绕过 S3 authentication 与 metadata，超出支持边界。

## 身份、管理面与会话

S3 数据面使用 SigV4 root 或 per-Resource AK/SK。VersityGW internal IAM 与私有 Admin API 只
服务 Contract Provider；不开放 WebUI、公网管理、OIDC、LDAP、Group、STS、密码回写或浏览器
session。flat-file IAM 含明文 JSON 字段，依赖 0700 目录权限与 workspace 备份边界保护，不能
手工编辑来绕过 Resource state。

## Secret 生命周期

`calculate` 只在 `VERSITYGW_ROOT_SECRET_KEY` 既没有显式值、Secret Store 也没有历史值时，
用 `crypto/rand` 生成 32 byte 并 hex 编码。Secret Store key 与运行环境 key 相同；重复 render
复用现值。Hook response 经 Runner 敏感 patch 边界写入 `.anas/secrets.yml`（0600），普通 list、
plan、lock、deployment manifest 和 Hook 错误不得出现明文。

Compose 通过 environment mapping 把 Module 私有键投影为上游 `ROOT_SECRET_KEY`，secret 不在
镜像、README 或 argv。显式配置值不复制进生成 Secret Store。当前 root credential 变更是
container recreation，不宣称事务化在线轮换；对象内容本身不随凭据变化。相同值的
Provider-neutral output 和 per-Consumer binding 继承敏感 provenance，不进入 plan/lock。
每个 Resource secret 使用 `RESOURCE_<CONSUMER>_<ID>_SECRET_ACCESS_KEY` 稳定键，Secret Store
metadata owner 是 Consumer；deployment manifest 和 Resource state 只保存引用。

## 数据库、持久化与恢复

没有数据库 Contract。权威数据包括 object directory、IAM directory、Secret Store、config、lock 和 active
deployment metadata，必须保持同一恢复点。首期未启用 `VGW_VERSIONING_DIR`；不能把普通
workspace snapshot 描述为 S3 object versioning 或 object lock。

## 环境变量所有权

显式消费：

- `TRAEFIK_BASE_PORT`

Module 自有前缀为 `VERSITYGW_`，并精确导出五个 `ANAS_OBJECT_STORAGE_S3_*` Capability
字段。Compose 内的 `ROOT_*` 与 `VGW_*` 仍是同一 service 的上游适配。Runner-owned
`BASE_DOMAIN`、`DATA_PATH`、`CONTAINER_PREFIX` 参与 Hook 派生。

## Hook、变更与回滚

Hook 只实现无外部副作用的 `calculate`：生成/复用 Secret，派生 hostname、domain、endpoint、
object path 与 IAM path，并发布统一 S3 输出。Runner 随后完成 Consumer binding/Resource 投影。其他 phase 返回
空响应。五个用户参数均通过 container recreation 生效；
rollback 只能回到带匹配 Secret Store 与数据快照的 deployment，不能单独回滚凭据文件。

## 测试与实现位置

- [`main_test.go`](../hook/main_test.go)：派生、Secret 稳定性、显式值与 Module 隔离；
- [`docker-compose.yml`](../docker-compose.yml)：固定镜像、Traefik、health 与 mount；
- [`anas-entrypoint.sh`](../versitygw/anas-entrypoint.sh)：挂载点初始化和降权；
- [`provision.sh`](../providers/object_storage/provision.sh)：user、credential 与 bucket owner 对账；
- `go test ./modules/versitygw/hook`：Module 单元测试；
- `go test ./internal/runner -run ObjectStorage`：name-only 绑定、per-Resource 隔离、状态与 Secret 边界；
- M2 真实 S3/恢复脚本尚未交付，见实施计划。

## 当前限制

兼容范围只声明核心 S3 PoC，不声明 AWS 全集。Capability Consumer 仍共享 root credential；
Resource Consumer 使用独立 bucket/长期凭据，但无 STS、quota 或细粒度 policy。WebUI、公网 Admin API、versioning、
object lock、notification、replication、virtual-host style 和真实 HA 均不在首期；真实客户端、
恢复与升级未完成前保持 `developing`。
