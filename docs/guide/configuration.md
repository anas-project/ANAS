# 配置

## 配置文件的职责

`<workspace>/config.yml` 是用户维护的期望状态；`config.lock.yml` 是 ANAS 解析并固化的 Module 版本、能力提供方和宿主机策略。不要手工编辑 `.anas/` 中的运行状态。

配置只支持结构化 YAML。主要区域为：

- `modules`：选择参与部署的 Module；
- `global`：域名、邮箱、时区等共享设置；
- `administration`：引导管理员和 Module 本地管理员默认策略；
- `identity`：目录与 IAM Provider 选择；
- `dynamic_dns`：负责 ANAS 声明记录的 DDNS Module 和 DNS 厂商；
- `rollback`：本地快照后端与保留策略；
- `modules.<name>`：单个 Module 的启用状态、身份协议和 `config` 参数；
- `secrets`：需要显式提供的敏感值；
- `env`：无法用结构化字段表示的原始环境变量。

完整字段清单见[配置结构参考](/reference/configuration)。

## 修改和预览

可以直接编辑 YAML，也可以使用 CLI：

```bash
anas config explain nextcloud.domain_prefix
anas config set global.timezone Asia/Singapore -w /srv/anas
anas config plan -w /srv/anas
anas apply -w /srv/anas
```

`config plan` 用于查看待应用变更。某些会改变服务内部持久状态的配置需要迁移步骤，ANAS 会拒绝普通应用；只有在已经完成对应迁移准备时才可显式使用 `anas apply --allow-risky`。这个标志只解除门禁，不会代替数据库迁移或凭据轮换。

## Secret

不要把真实 Secret 写进示例文件或提交到版本库。`config secret list` 只列键名；只有明确的 `config secret get` 操作会输出明文。生成的 Secret 位于受保护的 workspace 运行目录中，并由 ANAS 备份流程处理。
