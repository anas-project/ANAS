---
doc_type: research
created: 2026-10-22
updated: 2026-10-23
evidence_as_of: 2026-10-23
---

# LLNG 与 Samba AD 原生强制改密

## 结论

LLNG `2.23.2` 已原生支持 Samba AD 的“下次登录必须修改密码”和“密码已到期”：
用户输入原密码后显示改密表单，通过 LDAPS 写回新密码。2026-10-22 的隔离实测确认了这两条路径，
不需要修改 LLNG 源码，也不需要为这项能力升级版本。

需要开启 `ldapChangePasswordAsUser` 和 `ldapAllowResetExpiredPassword`。2026-10-23 已将两项
接入 ANAS Module 的初始配置与每次启动配置，并验证保留配置卷重启后的收敛。未切换现用 IAM。

**改密成功后需要用新密码再登录。** 固定版本返回成功提示 `35` 和登录表单，不直接创建 SSO
会话；重新登录后可以访问 LLNG 保护的应用。最初“改密提交后立即得到 SSO 会话”的断言未通过，
该限制保留，不将它算作已实现。

本报告是单项集成能力的 PoC，覆盖 LLNG 原生 AD 后端、Samba 密码状态与 HTTP 表单。
2026-10-23 追加了候选 Module 镜像和真实 Vikunja OIDC 浏览器验证：两种改密后重新登录
均保留原始 state、只回调一次，应用用户接口返回 200。详细验收见[配套计划](https://github.com/anas-project/ANAS/blob/master/modules/llng/dev-docs/plans/llng-password-change.md)。
完整 runner 部署、SAML、其他 OIDC 应用、二次认证及完整细粒度策略矩阵仍未在本轮验证。

## 原生机制与配置

[LLNG AD 文档](https://lemonldap-ng.org/documentation/2.0/authad.html)明确列出下次登录改密流程，
并要求启用上述两个参数。测试使用以下配置核心：

```json
{
  "authentication": "AD",
  "userDB": "AD",
  "passwordDB": "AD",
  "ldapPpolicyControl": 0,
  "ldapChangePasswordAsUser": 1,
  "ldapAllowResetExpiredPassword": 1,
  "ldapVerify": "require"
}
```

这不是完整部署配置；还需要 LDAPS 地址、可信 CA、查询账号、用户搜索范围及过滤器。
参数含义见[官方参数清单](https://lemonldap-ng.org/documentation/2.0/parameterlist.html)。

固定镜像中原生 Perl 模块的调用路径为：

1. `Auth/AD.pm::authenticate` 先识别明确的错误凭据 `52e`，再读取 `pwdLastSet` 和
   `msDS-User-Account-Control-Computed`。`pwdLastSet=0` 进入提示 `25`；自然到期进入提示 `22`。
2. `Password/AD.pm::modifyPassword` 调用 `Lib/Net/LDAP.pm::userModifyPassword`。
   开启 `ldapChangePasswordAsUser` 后，以 UTF-16LE 编码的带引号密码提交同一次 LDAP Modify：
   删除旧 `unicodePwd` 值、添加新值。Samba 在该操作中验证旧密码和密码策略。
3. 此 AD 路径使用查询连接执行上述修改，不要求过期用户先完成成功的普通 LDAP Bind。
   测试查询账号只是普通域用户；另行尝试管理员式 `replace unicodePwd` 返回 LDAP `50`，确认没有
   管理员重置密码权限，而携旧密码的原生修改仍成功。
4. `Auth/LDAP.pm::authenticate` 在密码修改分支明确不创建会话，因此提交成功后返回登录页。

官方 AD 文档仍有“密码已过期需要联系管理员”的表述。这里以固定 `2.23.2-1` 包源码和实际
过期用户结果为准：开启 `ldapAllowResetExpiredPassword` 后，已到期用户也完成了改密。

## 2026-10-22 原生 PoC 结果

在用户明确指定的 `finance.hlong.wang` 上新建独立 Docker、containerd、网络命名空间和
`LLNGTEST.INVALID` Samba 域。现用 Docker 仅用于读取并导出已有 Samba 镜像，未向现用目录
创建用户或修改密码策略。LLNG 前端 HTTP 仅用于隔离网段测试；LLNG 到 Samba 使用校验证书的 LDAPS。

| 检查 | 下次登录必须改密 | 密码自然到期 |
| --- | --- | --- |
| Samba 初始状态 | `pwdLastSet=0` | `pwdLastSet>0`，计算属性带 `0x800000` 到期标志 |
| 错误原密码 | 提示 `5`，无会话，密码状态不变 | 提示 `5`，无会话，密码状态不变 |
| 正确原密码 | 提示 `25`，出现新密码与确认字段，无会话 | 提示 `22`，出现相同改密字段，无会话 |
| 提交全新有效密码 | 提示 `35`，`pwdLastSet` 更新，到期标志清除 | 提示 `35`，`pwdLastSet` 更新，到期标志清除 |
| 自动建立会话 | 未建立；返回登录表单 | 未建立；返回登录表单 |
| 用新密码重新登录 | `302`，获得 LLNG 会话 | `302`，获得 LLNG 会话 |
| 受保护应用 | 原生示例应用 HTTP `200`，显示对应用户名 | 原生示例应用 HTTP `200`，显示对应用户名 |
| 旧密码立即拒绝 | Samba 宽限期设为 `0` 后提示 `5` | Samba 宽限期设为 `0` 后提示 `5` |

最终记录为首次改密 10 项、自然到期 9 项检查通过，另有查询账号无管理员重置权限的检查通过。
这里的检查数量包含“成功后返回登录页”的实际行为断言，**不代表自动登录断言通过**。

自然到期通过仅对独立域设置 `maxPwdAge=10` 秒、等待 12 秒触发，并读取计算属性确认；没有把
`pwdLastSet=0` 当作自然到期。改密后恢复测试域最大密码年龄，再验证新会话与应用访问。

### 保留的差异与重试

- Samba 默认 `old password allowed period=60`，首次检查发现旧密码仍能登录。只在测试域将
  该参数设为 `0` 后，旧密码即时拒绝通过。默认行为不是 LLNG 未写回密码。
  [Samba 参数文档](https://www.samba.org/samba/docs/current/man-html/smb.conf.5.html#OLDPASSWORDALLOWEDPERIOD)
  说明宽限期；[Samba 4.16 发布说明](https://www.samba.org/samba/history/samba-4.16.0.html)记录了
  LDAP Simple Bind 遵循该参数的修复。当前 ANAS 模板没有显式设置该参数，本轮没有改变现用策略。
- 一次重跑复用了密码历史中的新密码，修改被拒绝、`pwdLastSet` 保持 `0`，页面仍显示提示 `25`。
  后续使用全新随机密码通过。未将这次拒绝当作成功，也未据此宣称所有密码策略错误都有清晰提示。
- 临时环境曾因无关插件设置、旧配置缓存和缺少文件会话锁目录失败；修正测试启动配置后完成
  验证。这些修正未修改 LLNG 认证、改密源码或 ANAS Module。

## 2026-10-22 PoC 版本与证据

| 项目 | 实测基线 |
| --- | --- |
| LLNG | `lemonldapng/lemonldap-ng:2.23.2`，经仓库配置的 `docker.1ms.run` 加速源取得；包 `liblemonldap-ng-portal-perl 2.23.2-1` |
| LLNG 镜像摘要 | `sha256:a43d6110e6754a10d5f19986d8125cbdc4e4247a7cd1da1c62d8bffa4f7997b9` |
| Samba | `ghcr.io/anas-project/anas-samba-dc:4.23.6-r13`，二进制 `4.23.6-Ubuntu-4.23.6+dfsg-1ubuntu2.2`；使用原生 provision 创建测试域 |
| Samba 镜像摘要 | `sha256:1d110032df763dd0ba7d1dbe233f4b4f40468322eb49f779677bbfa89a62333e` |
| 当时本地源码核验 | 仓库 HEAD `058d5c2ce87253897e1619b762f42cef9c3e71ee`；LLNG 文件未由本轮修改 |
| 当时未修改的配置模板摘要 | `lmConf.json` SHA-256 `060749c963b0d3071bbe662b7ff924b24e349ccbbb8f2ffc2af2474834cdeff1` |
| 原生 AD 认证源码摘要 | `Auth/AD.pm` SHA-256 `e3205142074763bf34f1d48b3d6c966aa0beecf51a2755ec293e2869d22a8d7d` |
| 原生 LDAP 改密源码摘要 | `Lib/Net/LDAP.pm` SHA-256 `bc1e212680271c9f2453e9614c82194a55d53fdb755ce61965a43df8b5ee9de9` |
| 24 文件证据清单摘要 | `manifest.json` SHA-256 `d474b0dac2f8e389d8fdc68f432a4862932d001b599186b7e609a6c39dda1d4c` |

脱敏 JSON、原生源码副本和测试脚本保留在本机 Git 忽略目录
`test-env/reports/llng-samba-password-change-20261022/`，远端副本位于
`/home/whl/anas-llng-test-20261022/evidence/`。原始失败断言也在清单内。
这些路径是复核副本，不是已发布的共享持久证据归档；清理结果另记 `cleanup.json`。

ANAS `2.23.2-r13` 和历史 `r11` 镜像拉取均返回 404，Docker Hub 直连超时，最终使用上述官方
上游镜像完成实验。因此 2026-10-22 的 PoC 不代表 ANAS `r13` 完整镜像验收。次日使用本地 Module Dockerfile
重新构建候选镜像并执行实际启动脚本，补充了 PostgreSQL、目录锚点与 OIDC RP 配置验证；
该候选未作为正式 revision 发布。

## 后续边界

两个原生开关与 Vikunja 回跳已接入、验证，执行结果以配套计划为准。Vikunja 使用上游 2.4.0
和隔离 SQLite，LLNG 使用当前 Module 候选镜像与 PostgreSQL；不能扩展成全栈部署、所有
Consumer 或生产升级通过。现用 Samba 旧密码宽限期没有调整。

如果进一步评估替换 Casdoor、UI 定制或免密码登录，见
[LLNG 与 Casdoor 集成比较](./llng-casdoor-integration-comparison.md)。这些仍是后续方案，
不属于本次两个开关的已实现行为。
