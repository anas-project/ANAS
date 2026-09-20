# Forgejo 技术实现

本文记录 `forgejo` 的容器适配、Hook、安全边界与验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `15.0.7-r1` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Compose 拓扑

Actions 的预检和 controller 经各自 compute Resource 投影连接独立 `compute-control` 网络；
本机供给时该网络引用宿主已创建的 external 控制桥，Compose 不负责创建/删除它。Forgejo Web
不因此加入宿主控制网络。原 `actions-control` 以 `gw_priority: 1` 保持默认业务出口；该声明要求
Compose 2.33.1+。这是连接配置，不是 mTLS、隔离或 one-job 实机验收记录。

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_forgejo` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-forgejo:15.0.7-r1` | `actions-control, db, traefik` | 2 |
| `anas_forgejo_actions_controller` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-forgejo-actions-controller:15.0.7-r1` | `actions-control, compute-control` | 1 |
| `anas_forgejo_actions_preflight` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-forgejo-actions-controller:15.0.7-r1` | `actions-control, compute-control` | 0 |
<!-- generated:compose-topology:end -->

Web/API 仅在 Traefik network 暴露 `3000/tcp`；内置 SSH server 的容器端口 `2222/tcp` 直接发布为
`FORGEJO_SSH_PORT`。`db` 是数据库 Resource 绑定的 PostgreSQL 或 MariaDB external network。rootfs read-only，
`/tmp` 是 tmpfs，完整 `/var/lib/gitea` 是唯一应用数据 bind mount。

自定义 image 基于 `codeberg.org/forgejo/forgejo:15.0.7-rootless`。静态入口仅在 mount 根目录不是
`1000:1000` 时用不跟随 symlink 的 `WalkDir`/`Lchown` 修正树，再不可逆降权并 `exec` 上游
entrypoint。健康检查同样降权后请求 `/-/healthcheck`。

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `forgejo.actions_allowed_scopes` | string | — | `""` | `static` | `FORGEJO_ACTIONS_ALLOWED_SCOPES` | 否 | 否 | 否 | 是 | `container_recreate` | 可使用 ANAS Runner 的组织或仓库 scope，逗号分隔 |
| `forgejo.actions_enabled` | bool | — | `false` | `static` | `FORGEJO_ACTIONS_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | Actions 服务端与 one-job Runner controller 的唯一共同开关 |
| `forgejo.actions_isolation` | enum (`auto`, `incus_vm`, `incus_container`) | — | `auto` | `static` | `FORGEJO_ACTIONS_ISOLATION` | 否 | 否 | 否 | 是 | `container_recreate` | 向 compute Provider 申请的隔离档 |
| `forgejo.actions_runner_image` | string | `format: json_object` | `""` | `static` | `FORGEJO_ACTIONS_RUNNER_IMAGE` | 否 | 否 | 否 | 是 | `container_recreate` | 结构化镜像引用；Core 在渲染前冻结摘要 |
| `forgejo.custom_git_hooks_enabled` | bool | — | `false` | `static` | `FORGEJO_CUSTOM_GIT_HOOKS_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 是否允许仓库自定义 Git Hooks；Hook 会以 Forgejo 用户身份执行服务端代码 |
| `forgejo.db_name` | string | — | `forgejo` | `static` | `FORGEJO_DB_NAME` | 否 | 否 | 否 | 否：`migrate-forgejo-database` | `data_migrate` | 应用数据库名 |
| `forgejo.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `FORGEJO_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-forgejo-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `forgejo.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `git` | `static` | `FORGEJO_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `forgejo.iam_protocol` | enum (`auto`, `oidc`) | — | `auto` | `static` | `FORGEJO_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | IAM 登录协议；仅支持 OIDC |
| `forgejo.language` | string | — | — | `inherited` | `FORGEJO_LANGUAGE` | 否 | 是 | 否 | 是 | `reconcile` | 默认 UI 语言；浏览器和用户偏好优先 |
| `forgejo.local_path_import_enabled` | bool | — | `false` | `static` | `FORGEJO_LOCAL_PATH_IMPORT_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 是否允许从 Forgejo 容器内已可见的本地路径导入；不会增加宿主路径挂载 |
| `forgejo.ssh_port` | int | `1..65535` | `2222` | `static` | `FORGEJO_SSH_PORT` | 否 | 否 | 否 | 是 | `container_recreate` | 对外 SSH Git 端口 |

Hook 把固定版本支持的 31 个 locale 与 `DEFAULT_LANGUAGE` 做 BCP 47 匹配，并把匹配项移到完整
`[i18n] LANGS/NAMES` 列表首位。无法匹配时 warning 后回退 `en-US`；浏览器和用户保存偏好优先。
所有 `FORGEJO__[SECTION]__[KEY]` 上游配置键统一输出为 ANAS Hook ABI 接受的大写形式；双下划线
分隔语义不变，避免小写 section 键在 Runner 边界被拒绝。

## 数据库与持久状态

Module 是 `relational_database` Consumer，申请 retained `primary_database` Resource。PostgreSQL 映射
上游 `postgres`，MariaDB 映射 `mysql`。连接使用 provider internal network，`SSL_MODE=disable`。
数据库保存用户、组织、仓库元数据、Issue、权限和 session；数据目录保存 repository、LFS、Package、
附件、SSH state、索引及应用配置。`db_type`/`db_name` 为显式数据迁移边界。

## OIDC 数据流与会话边界

`calculate` 发布 provider-neutral registration：client id `forgejo`，redirect URI
`<domain>/user/oauth2/anas/callback`，scopes `openid,profile,email,groups`，以及 name、username、email、
groups claim。应用过滤打开时准入组为 `APP_forgejo,APP_all,<admin group>`。

`render_env` 只消费自己的 OIDC binding discovery/issuer 信息。`after_start` 通过 stdin 把 registration
送入容器 helper；helper 使用固定版本 `forgejo admin auth add-oauth/update-oauth` 幂等维护名为 `anas`
的 `openidConnect` source，并把管理员组映射为 site admin。CLI 没有 secret-stdin，所以 secret 会在
容器内部短暂成为 helper 子进程 argv，但不会进入宿主 `docker exec` argv、Hook 输出或错误文本。
这一上游限制也是 OIDC secret 使用 `rotation_mode: migrate` 而非统一事务轮换的原因。

自动外部注册开启，开放注册关闭，账号自动 linking 关闭。外部用户不能删除自己或管理应用密码，
仍可管理 SSH/GPG keys。session 使用数据库 provider。固定版本只提供本地 `/user/logout`，不登记
post-logout URI 或 IAM 主动 logout receiver。

当前 Module 不消费目录 Capability，不配置 LDAP source，不发布或消费 `anasIdentityAnchor`，也不支持
SAML。固定版本没有"按不可变 ID 把 OIDC 身份关联到预配 LDAP 用户"的接口——OIDC source 只能按用户名或
邮箱回退绑定——因此不实现 LDAP + OIDC 双链路；用户由 OIDC JIT 创建，Organization/Team 仍由 Forgejo
管理。决策与升级复核触发点见[Forgejo Module 设计](/architecture/forgejo-module-design) §2.2。

## 单链路身份的边界

只有 OIDC 一条链路意味着 Module 不保存目录副本，因此不落入[目录事件订阅要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/directory-event-subscription.md)
的适用范围，没有目录事件 watcher 进程；OIDC 一侧按 `LOGOUT-R-007`/`R-008`，固定版本既不能结束 IAM 中央会话，
也没有 IAM 主动 logout receiver。两个方向都没有自动路径，按 `DIRSYNC-R-014` 在此声明缺失方向与兜底：

| 目录侧变更 | Forgejo 侧的收敛路径 |
| --- | --- |
| 账号停用、删除、移出 `APP_forgejo` | **无自动路径**：管理员必须在 Forgejo 停用或删除该账号 |
| 组成员变更 | 该用户下次 OIDC 登录时随 groups 声明到达 team |
| 改名、邮箱变更 | **同一个账号**：按 `sub` 匹配，用户名与邮箱只在建号时写一次，改名后用户名冻结在旧值（`推断`，待探针） |

access token 与 SSH key 不经过登录，所以"下次登录才收敛"对它们不成立。撤权动作必须落到账号本身。

**尚未复核**：固定版本的 `prohibit_login` 是否同时关闭 access token 与 Git over SSH。在有探针结论之前，
撤权流程按"停用账号并显式吊销其 token 与 SSH key"执行，见[互操作基线](/developer/forgejo-interop) §4。

## 本地恢复与 Secret 边界

`break_glass` 使用 ANAS `generated_per_module` 密码，默认用户 `admin_forgejo`。`local_account_apply`
通过 docker stdin 把 JSON 交给 helper。首次创建时 helper 要求 Forgejo CLI 生成临时随机密码，再用
loopback Basic-auth admin API 把密码改为托管值并重新认证；托管密码从不进入 CLI 或 docker argv。
Forgejo 固定用 bcrypt 保存本地账号 hash，与 Manifest 的 `container_format` 一致。若同名账号已存在
但密码漂移，apply fail closed，不会覆盖未知账号。

Module 未声明 rotate：Forgejo CLI 的 change-password 只接收 argv，且当前没有满足 Runner
rotate/verify/rollback 合约的无泄漏原语。`FORGEJO_SECRET_KEY` 与 OIDC secret 是稳定随机 32-byte hex，
数据库密码来自 Resource。明文仅保存在 `.anas/secrets.yml` 并投影到获权渲染制品。

## Actions controller 与 compute 边界

`forgejo.actions_enabled` 是唯一功能开关。Hook 把同一值写入 Forgejo `[actions].ENABLED` 和 controller
环境；开启时 `validateActionsConfig` 校验三项 Forgejo 自己拥有的输入：`FORGEJO_ACTIONS_ALLOWED_SCOPES`
的形状（只接受 `{owner}` 或 `{owner}/{repo}`）、`FORGEJO_ACTIONS_CONTROLLER_PASSWORD`、以及 64 位
image fingerprint，缺失即失败。**Incus endpoint 与客户端证书不再是 Forgejo 配置项**，它们由 compute
contract 供给并投影到 controller，因此 Hook 阶段不可能校验它们；project/quota/profile 由下面的
preflight 在运行时验证。Forgejo 应用 service 使用显式环境白名单，不读取 module-wide `.env`，因此
Incus credential 与 controller 口令不会进入应用容器。

### 隔离档

`forgejo.actions_isolation` 选择 compute contract 的 interface，`module.yml` 的 contract 默认值把 `auto`
解析为 `incus_container`。`incus_container` 是非特权 Incus 系统容器，与宿主共享内核；`incus_vm` 是
QEMU/KVM，有独立 guest kernel 但要求宿主具备 KVM。默认取容器档的理由是目标硬件不保证提供 KVM
（`INCUS-R-052`、`FORGEJO-R-024`）；不按宿主能力自动升降级。

两档在 Provider 侧受同一组约束：restricted project、四类配额、受限 egress、唯一 managed NIC、无 host
disk/physical NIC/任意 device；容器档另由 project 强制 `restricted.containers.privilege=unprivileged`。
差别只在内核边界，因此跨信任域或执行不受信输入的 scope 应显式选 `incus_vm`（`FORGEJO-R-025`）。

**当前缺口**：`modules/forgejo` 下没有任何测试断言隔离档的解析与不自动升降级（`R-024`），也没有校验
固定 fingerprint 与所选档、目标架构一致（`R-026`）——`runner-image/` 要求按 amd64/arm64 × 容器/VM 各出
一份镜像，而 `actions_runner_image` 只有一个值且只校验 hex 形状。

### 控制面账号

`after_start` 只在 Actions 开启时调用 `reconcileActionsAccount`，经 stdin 把固定账号
`anas_actions_controller` 交给容器 helper 的 `local-admin` 子命令；口令来自 Secret Store，不进入宿主
`docker exec` argv。该子命令固定带 `--admin`，因此这个账号是**站点管理员**。

controller 的调用集合只有三个端点，全部限定在获批 scope：`GET .../actions/runners/jobs`、
`POST .../actions/runners`、`DELETE .../actions/runners/{id}`（`orgs/{owner}` 或
`repos/{owner}/{repo}`），使用 basic auth。这三个端点要的是组织 owner 或仓库 admin，不是全站权限；
站点管理员是"没有按 scope 授权的调和路径"的后果，属于已登记偏差（`FORGEJO-R-068`、`R-069`）。

`reconcileActionsAccount` 在 Actions 关闭时直接返回，因此**关闭开关不会撤销该账号**：账号与
Secret Store 中的有效口令都会留下，需要管理员手工处理。收敛要求见 `FORGEJO-R-070`。

Compose 先运行同一 controller image 的一次性 `preflight`。Actions 开启时，它实际连接 Incus 并验证
project、quota 与 profile；只有成功退出后 Forgejo 和长驻 controller 才能启动。Actions 关闭时
preflight 不访问 Incus 并直接成功。该 service 没有独立开关或状态，不构成第二个 Runner 功能。

controller 通过 provider-neutral `ComputeProvider` 和 `compute` Contract 目录表达 create/inspect/start/
exec-stdin/stop/delete/list-managed 生命周期。首个适配器用固定 remote/project/profile 调用 Incus CLI；
变更前验证 project `restricted=true`、instance/CPU/memory/disk quota、受限 egress 标记、唯一 managed
NIC，以及不存在 cloud-init secret、host disk、physical NIC 或任意 device。调用方不能传 Incus raw
config、device、mount 或 socket。

每 15 秒按获批 scope 查询 waiting jobs，默认空队列为零 registration/实例。每个 job 创建 ephemeral
registration 和一个固定指纹实例，token 只通过 Incus exec stdin 进入 guest tmpfs，再以 `--handle`、
`--wait` 运行 `one-job`。全局并发上限 4、每 scope 上限 2、waiting TTL 10 分钟、job timeout 1 小时。
state 只持久化 handle、scope、registration/实例 identity 和时间，不含 token；正常结束、取消、超时、
关闭和重启残留均走同一 cleanup/janitor。state 存在 named volume `forgejo_actions_state`，不在 `R-003` 的备份
一致点内，但**也不能随手丢弃**：`ListManaged` 只在 `CleanupAll`（关闭开关）里调用，周期性
`Reconcile` 完全依据 state；孤立的 Forgejo runner registration 更是没有兜底路径——注销依赖 state 里
的 `RunnerID`，而 `ForgejoAPI` 没有列举 runner 的方法。丢失 state 的后果：Actions 仍开着时孤立实例
要等到下次关闭才回收，崩在创建 registration 与作业开始之间的 registration 则永远留着。收敛要求见
`FORGEJO-R-046`。

guest image 资产位于 `runner-image/`：`runner-agent` 运行 Runner，`runner-engine` 运行 rootless Podman；
capacity=1、`privileged=false`、`valid_volumes=[]`，并设置 CPU/memory/PID/no-new-privileges。镜像不启动
daemon Runner，也不提供 `host` label。独立 Incus 宿主、真实防火墙/egress 和两档各一遍的 one-job E2E
仍是发布门禁。

## 安全默认与运维边界

- Actions 默认关闭且只有一个开关；controller 不共享 host Docker socket，空队列不创建 Runner/实例。
- 默认隔离档 `incus_container` 与宿主共享内核；需要独立 guest kernel 的 scope 必须显式选 `incus_vm`。
- Actions 开启会留下一个站点管理员账号 `anas_actions_controller`，关闭开关不会撤销它。
- Git hooks 与 local-path import 默认关闭且可独立开启；Hook 将以 Forgejo 用户身份执行服务端代码，
  local import 只能读取容器内本来已可见的路径，Compose 不为它增加宿主挂载；LFS 与内置 SSH 开启。
- Web port 只在 Compose network，且覆盖 v15 image 的 `REVERSE_PROXY_TRUSTED_PROXIES=*` 默认值，
  仅信任 loopback 与 RFC 1918 container source。
- 本地恢复需要 internal sign-in 和 Basic API authentication；开放注册仍关闭。
- SMTP、S3 与外部搜索不在当前自动配置范围；Actions 的真实 Incus 宿主 E2E 尚未完成。
- 备份必须一致覆盖数据目录、数据库、Secret Store 与部署元数据。

## Hook 与测试位置

- [`hook/main.go`](../hook/main.go)：域名/语言、OIDC registration、数据库与上游配置、after-start 和本地账号 apply。
- [`hook/main_test.go`](../hook/main_test.go)：Secret 稳定性、数据库映射、安全默认、语言、Group 与 stdin 边界。
- [`forgejo/entrypoint.go`](../forgejo/entrypoint.go)：降权、健康检查、OIDC auth source 与恢复管理员适配。
- [`forgejo/entrypoint_test.go`](../forgejo/entrypoint_test.go)：symlink 安全、REST 凭据 bootstrap、OIDC CLI 参数。
- [`actions-controller/`](../actions-controller/)：scope queue、ephemeral registration、compute adapter、state 与 janitor。
- [`runner-image/`](../runner-image/)：one-job 启动器、rootless Podman unit 与固定 guest 配置。
- [`module.yml`](../module.yml) 与 [`docker-compose.yml`](../docker-compose.yml)。

设计决策见[Forgejo Module 设计](/architecture/forgejo-module-design)，剩余工作与明确排除项见
[Forgejo Module 实施计划](../dev-docs/plans/forgejo-module.md)。

提升 `release` 前还需完成 PostgreSQL/MariaDB、amd64/arm64、LLNG/Authentik 浏览器登录、IAM-down、
HTTP/SSH clone/push、LFS、Package、备份恢复和前一 LTS patch/minor 升级回滚 E2E。

## compute 镜像配置冻结

镜像配置改为结构化对象：Forgejo 为单对象，AI Agent 为 runtime→对象映射。Core 通过显式
`spec_from` 投影，在 Hook calculate 前解析。运行时容器只接收冻结摘要：Forgejo 读取租约
allowlist，AI Agent 读取 JSON 镜像绑定。Agent Hook 使用与 manifest 参数一致的
`AI_AGENT_AGENT_RUNTIMES`，Compose 向 orchestrator 映射为 `AI_AGENT_RUNTIMES`。本轮未引入新依赖。

Incus 的 `image_architecture` 必须显式描述目标 daemon。受信 bundle 目录当前为空；ensure 在
登记信任前检查租约 project 中现有镜像的 fingerprint、架构与类型，缺失直接失败，不查询 alias
或重建。自动导入/烘焙仍待实现。快照及回滚语义见 [compute 契约](../../../contracts/compute/docs/technical.md)。

HTTP 网络原型 `cmd/incus-network-prototype` 只生成实验产物：指定源地址的 guest /32 路由、
绑定 veth 的入站过滤、限时地址/端口集合，以及既有 Traefik 路由环境字段。它不安装规则，也不开启
生产 ingress。Docker/Incus 规则顺序、来源冒用、IP 复用和长连接撤销仍需真实 Linux 证据；
TCP/UDP 发布未实施。

## 租约命名密钥生命周期

Core 已接入独立的 32 字节 compute `LEASE_SECRET`，与客户端证书分开生成和复用。Deployment 与
resource state 只保存引用；消费者接收敏感 base64 投影，备份恢复保留同一密钥，不参与凭据轮换。
详见 [compute 生命周期契约](../../../contracts/compute/docs/technical.md#独立租约命名密钥)。
专属轮换命令和生产 HTTP 发布仍待实现。
