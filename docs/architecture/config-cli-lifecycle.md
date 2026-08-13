# CLI 配置修改与生效生命周期

> [!IMPORTANT]
> 本页同时记录当前实现与后续设计。受控的 `config import`、`config migrate` 与管理员专用轮换已实现；通用 `config apply`、任意 `secret rotate` 和 `config import-state` 仍是设计提案。

## 当前模型

ANAS 将 CLI 管理的规范化 desired config、解析后的 lock 和不可变 deployment 制品分开：

1. `config set` 只修改 `<workspace>/config.yml`；
2. `config explain` 查看参数类型、敏感性和变更 effect；
3. `config plan` 分类配置差异，不修改运行状态；
4. `anas apply` 渲染并激活新的 deployment。

当前 deployment 激活会比较现用制品与目标制品的 guarded settings。遇到 `credential_rotate`、`data_migrate` 或 `immutable` 变化时，普通 `apply` 会阻止激活。`--allow-risky` 只解除这道门禁，不会替用户执行凭据轮换、数据迁移或应用级验证。

## 当前命令

```bash
# 只写配置文件
anas config set -c /srv/anas/config.yml samba_dc.user_min_pass_length 10

# 查看参数的 effect、类型和敏感性
anas config explain samba_dc.user_min_pass_length

# 预览配置差异
anas config plan -c /srv/anas/config.yml -w /srv/anas

# 生成并激活 deployment
anas apply -w /srv/anas
```

`config set` 尽量保留 YAML 注释和顺序。敏感值不会出现在普通列表或 plan 输出中。早期 workspace 可能没有 applied 配置指纹，此时 `config plan` 会将其视为 initial configuration；实际激活门禁仍以 deployment 中冻结的设置为准。

## 变更类型

| Effect | 含义 | 当前处理 |
| --- | --- | --- |
| `hot_reload` | 服务可在线调整 | 记录建议动作；专用 reconciler 尚未统一实现 |
| `process_restart` | 只需重启目标进程 | 记录建议动作 |
| `container_restart` | 配置已更新，只需重启容器 | 记录建议动作 |
| `container_recreate` | env、label、端口或服务集合变化 | 重新 render，并由新 deployment 重建受影响容器 |
| `reconcile` | 需要应用专用的幂等操作 | 记录 module 声明的动作 |
| `credential_rotate` | 凭据及其消费者必须协同更新 | 激活门禁，除非显式 `--allow-risky` |
| `data_migrate` | 数据、数据库或机器身份需要迁移 | 激活门禁，除非显式 `--allow-risky` |
| `immutable` | 普通变更不应修改的初始化身份 | 激活门禁，除非显式 `--allow-risky` |

未声明 effect 的参数保守地按 `container_recreate` 处理。

## Module 声明

变更行为由 Module 在 `module.yml` 中声明，Runner 不硬编码应用知识：

```yaml
config:
  changes:
    user_min_pass_length:
      effect: hot_reload
      apply: samba-password-policy
      description: Update and verify the Samba password policy.
    ldap_bind_password:
      effect: credential_rotate
      apply: rotate-ldap-bind-password
      sensitive: true
```

`apply` 是动作标识，不代表当前 Runner 已实现同名执行器。新增或修改 effect 时，必须同时考虑快照、回滚阻断和应用级验证。

## 尚未实现的执行器设计

以下命令仅是目标接口，当前不可执行：

```text
anas config apply -c config.yml --effect hot_reload,reconcile
anas secret rotate samba_dc.ldap_bind_password
anas migrate nextcloud.db_type --to postgres
anas config import-state
```

未来的专用执行器应统一遵循：`inspect` → `prepare` → `apply` → `verify` → `rollback` → `commit`。其中：

- 凭据轮换必须更新所有消费者并在验证通过后提交新版本；
- 数据迁移必须要求 preflight、最近备份、维护窗口和可验证的回滚点；
- immutable 参数不能靠一个通用 `--force` 假装完成迁移；
- applied state 只能在应用级验证成功后提交。

在这些执行器落地前，操作者必须在外部完成并验证相应迁移，再决定是否使用 `anas apply --allow-risky`。
