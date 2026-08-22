# VersityGW S3 gateway

使用专用 POSIX 目录提供 S3 兼容 API。该 Module 是单机文件系统网关，不是分布式对象存储。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `versitygw` |
| 版本 / revision | `1.7.0-r3` |
| 状态 | `developing` |
| 类别 | `storage` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | HTTPS reverse proxy |
| `object_storage` | 提供的 Capability | `s3` |
| `object_storage` | 提供的 Contract | `1.0.0` / `s3` |

Capability 统一发布共享 root S3 连接信息；Contract + Resource 则按声明为 Consumer 创建独立
bucket、独立 AK/SK 和持久状态。两者互不隐式转换。

## 最简配置

```yaml
modules:
  versitygw: {}
```

默认 endpoint 为 `https://s3.<base_domain>:<traefik_port>`，region 为 `us-east-1`，access key
为 `ANASROOT`。客户端应使用 path-style addressing。

## 供其他 Module 使用

Consumer 不依赖 `versitygw`，只在 manifest 声明：

```yaml
dependencies:
  requires_capabilities:
    - name: object_storage
```

Runner 自动绑定唯一 Provider，并向该 Consumer 发布：

```text
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__INTERFACE=s3
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__ENDPOINT=...
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__REGION=...
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__ACCESS_KEY_ID=...
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__SECRET_ACCESS_KEY=...
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__PATH_STYLE=true
```

Consumer Hook 把自己的 binding 翻译成应用配置，不读取 `VERSITYGW_*` 或 Provider-side
`ANAS_OBJECT_STORAGE_S3_*`。当前所有 Consumer 共用 root credential；binding 不提供 bucket
隔离或最小权限。

需要独立 bucket/凭据的 Consumer 显式声明 `object_storage` Contract 与 Resource：

```yaml
dependencies:
  contracts:
    - name: object_storage
      version: ">=1.0.0 <2.0.0"
      selected_by: object_storage_type
      interfaces: [s3]
      default: s3
resources:
  requires:
    - id: objects
      contract: object_storage
      binding: object_storage_type
      spec_from:
        bucket: object_bucket
      spec:
        credential: {policy: generated}
        deletion_policy: retain
config:
  defaults:
    object_storage_type: auto
    object_bucket: example-objects
  types:
    object_storage_type: {enum: [auto, s3]}
    object_bucket: string
```

Runner 为 Resource 派生唯一 access key、生成独立 secret，并发布
`ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__OBJECTS__*`。完全不声明这些段落的 Module 不会创建
bucket/用户，也不会收到相应环境变量。

## 身份、用户与 Group

S3 请求使用 AWS Signature Version 4 的 access key/secret key，不使用网页登录、OIDC、LDAP
或 Samba 身份。Capability Consumer 使用共享 root credential；Resource Consumer 使用
VersityGW internal IAM 中的独立 `user` principal，只能看到分配给自己的 bucket。Admin API
仅监听私有容器网络的 7071，不经 Traefik 或宿主端口开放；WebUI、bucket policy 自动编排和
STS 临时凭据仍未实现。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM / OIDC / SAML | 不支持/不适用 |
| S3 authentication | SigV4 root AK/SK 或 per-Resource AK/SK |
| Group / 密码回写 | 不支持/不适用 |

本服务没有浏览器 session，因此也没有 Module 发起或 IAM 发起的登出语义。撤销访问需要替换
root credential 并重建容器；首期未实现无中断轮换。

## 管理员登录与 IAM 故障恢复

没有 Web 管理控制台或 `anas admin local` 账号。私有 Admin API 只供 Provider operation 使用，
不是操作者入口。root access key 可从配置清单读取；省略
`root_secret_key` 时，Hook 生成的 secret 只能通过显式 Secret Store 命令取得：

```bash
anas config list versitygw -w /srv/anas
anas config secret list -w /srv/anas
anas config secret get VERSITYGW_ROOT_SECRET_KEY -w /srv/anas
```

`secret get` 会输出明文，应只在受控终端使用。用户显式提供的 secret 保留在受保护配置中，
不会被 `config secret get` 回显。Resource secret 由 Runner 按 Consumer 独立保存在 Secret Store；
不要直接编辑 `${DATA_PATH}/versitygw/iam` 的明文 JSON 文件。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。对象 payload 直接保存在专用 POSIX backend。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；
不要把它当成首选配置接口。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `versitygw.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `s3` | `static` | `VERSITYGW_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | S3 HTTPS endpoint 的域名前缀 |
| `versitygw.read_only` | bool | — | `false` | `static` | `VERSITYGW_READ_ONLY` | 否 | 否 | 否 | 是 | `container_recreate` | 启用上游全局只读保护 |
| `versitygw.region` | string | `length: 1..64`; `pattern: ^[A-Za-z0-9][A-Za-z0-9._-]*$` | `us-east-1` | `static` | `VERSITYGW_REGION` | 否 | 否 | 否 | 是 | `container_recreate` | SigV4 签名 region |
| `versitygw.root_access_key` | string | `length: 3..64`; `pattern: ^[A-Za-z0-9._-]+$` | `ANASROOT` | `static` | `VERSITYGW_ROOT_ACCESS_KEY` | 否 | 否 | 否 | 是 | `container_recreate` | root S3 credential 的 access key 标识 |
| `versitygw.root_secret_key` | string | `length: 16..128` | — | `generated` | `VERSITYGW_ROOT_SECRET_KEY` | 否 | 是 | 是 | 是 | `container_recreate` | root S3 credential 的 secret key |

### 查询和修改

```bash
anas config list versitygw -w /srv/anas
anas config explain versitygw.read_only
anas config set versitygw.read_only true -w /srv/anas
anas config plan -w /srv/anas
```

root credential 变更会使旧签名立即失效，当前按容器重建应用，不是事务化在线轮换。不要在
shell argv 中直接写 secret；省略它以使用生成值，或通过受保护的配置导入流程设置。

## S3 客户端验证

以下命令不会把 secret 明文写进 shell history，但会把它放入当前进程环境；用完应 `unset`：

```bash
export AWS_ACCESS_KEY_ID=ANASROOT
export AWS_SECRET_ACCESS_KEY="$(anas config secret get VERSITYGW_ROOT_SECRET_KEY -w /srv/anas)"
export AWS_DEFAULT_REGION=us-east-1
aws --endpoint-url https://s3.example.com s3api create-bucket --bucket smoke-test
aws --endpoint-url https://s3.example.com s3api put-object --bucket smoke-test --key hello.txt --body ./hello.txt
aws --endpoint-url https://s3.example.com s3api get-object --bucket smoke-test --key hello.txt ./restored.txt
unset AWS_SECRET_ACCESS_KEY
```

把 endpoint 替换为 `VERSITYGW_ENDPOINT` 的实际值。目标客户端必须支持自定义 endpoint 与
path-style addressing；virtual-host style 需要 `*.s3.<base_domain>` DNS/证书，首期未提供。

## 存储、备份与验证

对象目录固定为 `${DATA_PATH}/versitygw/objects`，internal IAM 位于
`${DATA_PATH}/versitygw/iam`，两者随 workspace 数据进入 snapshot/backup。恢复点必须同时包含
对象目录、IAM 目录、`.anas/secrets.yml`、配置、lock 与 deployment metadata，随后
用签名客户端读取原对象验证。只检查文件数量或 health `200` 不能证明可恢复。

该目录是专用 S3 命名空间，不挂载 `${USER_DATA_PATH}` 或 Samba 共享。直接从宿主/SMB 修改
文件会绕过 S3 鉴权、metadata、policy 及未来版本语义，不属于支持的写入方式。

## 当前限制

- 所有 Capability Consumer 共享 root credential；任一 Consumer 泄漏都可能影响全部 bucket；
- Resource Consumer 已隔离 bucket 与长期 AK/SK，但尚无 STS、quota 或细粒度 policy；
- Admin API 仅供内部 lifecycle job 使用，不提供公网或 WebUI 管理入口；
- 未启用实验性 versioning/object lock，不承诺 WORM 或不可变备份；
- 未提供外部 IAM 集成、STS、notification、replication 或跨节点高可用；
- 未验证完整 AWS S3 API 等价、virtual-host style、真实恢复和版本升级；
- 完成真实客户端与恢复验收前状态保持 `developing`。

## 技术文档

镜像降权、Secret、SigV4 代理边界、Hook 和测试见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`1.7.0-r3`（reviewed 2026-08-22）
- Timezone / 时区：`container` — The gateway receives TZ through the Module .env for process and access-log timestamps.
- Language scope / 语言范围：S3 HTTP protocol service
- Selection / 选择方式：`none`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：none
- Fallback / 回退：S3 responses are protocol-defined and the Module exposes no WebUI.
- Supported languages / 支持语言：not applicable / 不适用
- Notes / 说明：Client tools choose their own display language; it is not a gateway setting.

Evidence / 证据：

- [1.7.0 — cmd/versitygw CLI and S3 API server; WebUI is not enabled by this Module](https://github.com/versity/versitygw/tree/v1.7.0)
<!-- generated:localization:end -->
