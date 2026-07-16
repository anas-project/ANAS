# ANAS CLI 配置修改与生效方案

## 目标

CLI 不把“写入配置文件”和“让运行中的服务生效”混为一个动作。修改分成四步：

1. `set` 修改 desired config；
2. `explain` 查看参数的变更类型；
3. `plan` 与最后一次成功启动的配置哈希比较；
4. 根据类型执行热更新、重启、轮换或迁移。

第一阶段已经实现前三步、成功启动后的哈希快照，以及对高风险普通启动的阻断。模块专用的 `apply/rotate/migrate` 执行器按下面的接口逐步补齐。

## 已实现的命令

```text
# 修改模块参数；只写配置，不直接操作运行服务
anas config set -c config.yml samba_dc.user_min_pass_length 10

# core 的非 global 参数会写入顶层 env
anas config set -c config.yml core.share_guest_read_only Yes

# 查看生命周期、是否敏感和应执行的动作
anas config explain samba_dc.user_min_pass_length

# 与最后一次成功 start 的哈希快照比较
anas config plan -c config.yml -b ~/.anas
```

`set` 原子写入 YAML，并尽量保留原有注释和顺序。密码值不会在命令输出和 plan 中显示。成功的 `start/restart` 会把显式配置的 SHA-256 写入 `~/.anas/state/config-applied.yml`，其中不含明文值。

普通 `anas start` 遇到 `credential_rotate`、`data_migrate` 或 `immutable` 变化时会停止，避免仅仅重建容器导致密码失配、空数据目录或身份分裂。

## 变更类型

| 类型 | 含义 | 当前/典型例子 | 执行原则 |
| --- | --- | --- | --- |
| `hot_reload` | 服务在线修改，不重启进程 | Samba 密码长度、历史、锁定策略 | 调用模块 reconciler，验证新值后更新 state |
| `process_restart` | 只重启容器内目标进程 | 将来支持独立守护进程配置时使用 | 先校验配置，再通过 supervisor 重启单个进程 |
| `container_restart` | 配置文件已在持久卷/挂载中更新，只需重启同一容器 | 将来对挂载式静态配置使用 | 不改变镜像、卷和容器参数 |
| `container_recreate` | env、Compose label、端口或服务集合变化 | 时区、DNS、Traefik 端口、Nextcloud 资源限制 | 重新 render，仅 recreate 受影响容器 |
| `reconcile` | 需要应用专用幂等命令，可能无需重启 | Samba share/guest ACL、Nextcloud app、域名关联配置 | 可重复执行；变更前后都读取真实状态 |
| `credential_rotate` | 业务系统中的密码和消费者配置必须事务化更新 | DB、Samba bind、Nextcloud/Keycloak admin 密码 | 保留旧凭据到所有消费者验证成功，失败回滚 |
| `data_migrate` | 数据、数据库或机器身份需要迁移 | `DATA_PATH`、Nextcloud DB、Samba FS hostname | 强制 preflight、备份、维护窗口和回滚点 |
| `immutable` | 初始化身份不能由普通修改改变 | `BASE_DOMAIN`、Samba realm/DC identity | 普通 start 阻断；只能专用迁移或重建恢复 |

未声明的参数保守地归类为 `container_recreate`，不会猜测它可以热更新。

## cask 声明

变更行为由 cask 自己声明，runner 不硬编码具体应用知识：

```yaml
config:
  changes:
    user_min_pass_length:
      effect: hot_reload
      apply: samba-password-policy
      description: samba-tool can update the policy without restarting Samba.
    ldap_bind_password:
      effect: credential_rotate
      apply: rotate-ldap-bind-password
      sensitive: true
```

当前 schema 接受八种 effect。`apply` 是稳定的动作名称，后续由 cask hook 提供同名执行器。

## 第二阶段执行命令

建议在现有命令上增加：

```text
anas config apply -c config.yml --effect hot_reload,reconcile
anas config apply -c config.yml --effect container_recreate
anas secret rotate samba_dc.ldap_bind_password
anas migrate nextcloud.db_type --to postgres
anas config import-state
```

执行协议统一为：

1. `inspect` 返回真实当前值或不可逆 hash；
2. `prepare` 检查权限、备份、容量、依赖和旧凭据；
3. `apply` 执行最小范围变更；
4. `verify` 做应用级验证，而不只看容器健康；
5. `rollback` 在验证失败时恢复；
6. `commit` 最后更新 applied state。

## 各类型的 CLI 行为

### 热更新与 reconcile

可以批量执行，但必须按依赖排序。例如 Samba 密码策略直接调用 `samba-tool domain passwordsettings set`，随后重新读取策略验证，不重启 DC。

### 进程与容器重启

`process_restart` 只能重启声明的进程；`container_restart` 不改变容器创建参数；`container_recreate` 才重新计算 Compose 配置。CLI 应输出受影响服务，避免当前全栈重启。

### 凭据轮换

轮换命令不接受把 `DEFAULT_SERVICE_ROOT_PASSWORD` 当作所有内部服务的通用密码。该值至少 8 位，人工登录管理员可以继承它；数据库、bind account、OIDC client 等使用独立随机 secret。

以 LDAP bind 为例：生成新 secret → 修改 AD → 更新全部消费者 → 验证 LDAP 登录 → 提交 secret version。若消费者支持双凭据，应保留一个短暂重叠窗口。

### 数据迁移与 immutable

迁移必须要求显式确认和最近备份。immutable 参数不提供 `--force` 绕过；只能调用已实现的专用迁移器，或者新建实例并执行备份恢复。

## 状态与安全

- desired config 表达用户意图；applied state 只在验证成功后更新。
- plan 只展示路径、effect 和动作，不展示敏感值。
- `secrets.generated.yml` 继续保存稳定随机 secret；配置状态只保存 secret version/hash。
- 第一次使用新版 runner 时没有 applied snapshot，CLI 会明确提示“initial configuration”。已有生产实例应先执行未来的 `config import-state`，不能仅凭当前 env 猜测真实状态。
- 任何失败都不能提前提交新 state，否则下一次 plan 会误认为配置已经生效。
