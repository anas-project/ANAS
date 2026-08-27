# Casdoor IAM 运维 Runbook

本文适用于 ANAS 管理的 Casdoor Provider。示例 workspace 为 `/srv/anas`；执行写操作前先确认
`anas status -w /srv/anas` 指向预期部署，并在同一维护窗口内保留可用的 `admin_casdoor` 凭据。

## 日常检查与 IAM 故障恢复

先检查 ANAS 状态、Casdoor、PostgreSQL、Samba DC 和目录订阅器的健康状态，再检查 OIDC discovery
或 SAML metadata。Samba AD 是业务用户与组的事实来源；不要在 Casdoor 中直接修复业务用户、组、
禁用状态或密码。

IAM 登录不可用时，使用本地恢复入口（Module 导出的 `CASDOOR_LOCAL_RECOVERY_URL`，默认是 Casdoor
HTTPS 地址的 `/login`）和账号 `admin_casdoor`。密码只在受控终端读取，不写入命令参数、工单或日志：

```bash
anas admin local credential casdoor break_glass -w /srv/anas
anas admin local rotate casdoor break_glass -w /srv/anas
```

轮换成功后立即用新密码登录并确认旧密码被拒绝。命令失败时 ANAS 会恢复旧 bcrypt 值并保持 Secret
Store 不变；先验证旧密码仍可登录，再调查 Hook、容器和 PostgreSQL，不要连续盲目轮换。

## 签名密钥与 Client Secret 轮换

先做只读库存和 dry-run：

```bash
anas credential list -w /srv/anas
anas credential rotate casdoor.signing_key -w /srv/anas --dry-run --json
anas credential rotate casdoor.portal_client_secret -w /srv/anas --dry-run --json
```

确认计划只包含预期 Module 后，在维护窗口执行：

```bash
anas credential rotate casdoor.signing_key -w /srv/anas -y --json
anas credential rotate casdoor.portal_client_secret -w /srv/anas -y --json
```

- `casdoor.signing_key` 使用新 RSA/X.509 keypair 创建不可变候选部署。当前证书以指纹命名，旧证书在
  一小时信任重叠期内继续发布到 JWKS；新 token 必须使用新 `kid`，重叠期内的旧 token 仍应通过
  签名验证。私钥只存在于 Secret Store 和 Casdoor 的受管证书行，不进入 deployment manifest。
- `casdoor.portal_client_secret` 原子更新 built-in Portal Application。新 Secret 验证成功后才提交
  Secret Store，旧 Secret 不设重叠期并必须立即失效。
- 任一步骤失败时，ANAS 停止候选、恢复上一部署和应用侧凭据，并保持 Secret Store 的原 generation。
  `anas credential list` 显示 `recovery_required` 时不要再次轮换；保留事务 journal 和部署制品，先完成
  恢复诊断。

轮换后至少验证真实 OIDC 登录、JWKS 签名、SAML assertion 签名、Portal 登录和一个目录用户的永久
锚点。不要只以健康检查或 discovery 返回 200 作为完成证据。

## 一致备份与空 workspace 恢复

恢复点必须同时包含 PostgreSQL 数据、Casdoor 签名材料、Consumer Secret、
`${DATA_PATH}/casdoor/dirwatch` 游标、`.anas/secrets.yml`、`.anas/local-admins.yml`、active deployment
及其 deployment metadata。不要只备份 PostgreSQL。

```bash
anas backup create -w /srv/anas --to /backup/anas --mode snapshot -y --json
anas backup verify --to /backup/anas --backup-id BACKUP_ID --json
anas init /srv/anas-restore -y
anas backup restore --from /backup/anas -w /srv/anas-restore \
  --backup-id BACKUP_ID --dry-run --json
anas backup restore --from /backup/anas -w /srv/anas-restore \
  --backup-id BACKUP_ID -y --json
```

同一容器前缀不能同时启动源和恢复 workspace。验证备份后停止源 workspace，再启动恢复目标；回切时
按相反顺序操作。恢复后必须确认 issuer/域名未改变、原 OIDC/SAML client 与签名仍有效、订阅游标
未倒退、`admin_casdoor` 可登录，并以恢复前的 Samba 锚点和同一个 Casdoor `sub` 完成真实登录。

## 固定版本升级、重启与制品回滚

升级前先创建并验证一致备份，再使用 `anas lock`、`anas plan`、`anas apply` 和 `anas status`
完成固定 revision 升级。生产维护不得使用测试专用的 `--no-snapshot`。升级或重启后必须复验真实登录、
永久身份锚点、Consumer client、JWKS/SAML 签名和目录订阅游标；常规重启优先通过 ANAS 执行，
直接操作 Docker 只用于已经验证过的故障恢复步骤。

使用 `anas deployments -w /srv/anas` 选择已验证的旧 deployment，再执行：

```bash
anas rollback DEPLOYMENT_ID -w /srv/anas -y
```

安全制品回滚要求目标 deployment 标记 `data_touched=false`。它只切换运行制品，不回退数据库、Secret、
游标或其他持久数据，也不会自动降低当前期望的 Module revision；不要通过强制降低 lock 来冒充数据回滚。
需要回退数据时，必须单独执行上一节的显式 snapshot restore，并重新完成恢复验收。

## Provider 切换与弃用迁移

Provider 切换不是账号或会话迁移。先在 Consumer 侧保留 Samba 永久锚点作为关联键，为新 Provider
生成独立 client，验证 redirect/logout URI、`ALLOW_GROUPS`、管理员降权和真实登录，再逐个切换
Consumer。切换会使旧 Provider 的 session、refresh token 和 client credential 失去用途；不要尝试
复制 Casdoor 本地 User ID、session 或密码。

所有 Consumer 完成验证并经过回退观察期后，撤销 Casdoor client、停止 Module，最后按数据保留策略
处理 PostgreSQL 和备份。只删除配置声明不能替代 Secret 撤销和备份保留决策。

## 明确不支持的能力

- 固定 Casdoor `3.143.0` 不发布 SAML SLO；SAML Consumer 只能本地登出。
- 不启用 LDAP/AD 密码写回，Casdoor 也不是业务目录权威。
- 不支持静默改变数据库类型或名称；只能使用 PostgreSQL，迁移必须显式执行。
- 不保证跨 issuer/域名恢复旧 token、Cookie 或 Passkey；本 Runbook 的恢复验收要求同一 issuer。
- 不把 Casdoor 本地 User ID 当作 Samba 永久身份锚点，也不自动迁移 Provider session。
