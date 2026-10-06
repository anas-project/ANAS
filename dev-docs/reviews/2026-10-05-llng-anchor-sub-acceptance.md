# LLNG anchor sub 与 Nextcloud 隔离实机验收

日期：2026-10-05。服务器：`whl@finance.hlong.wang`。
测试根目录：`/home/whl/anas-casdoor-test-20261003/llng-anchor`。
Docker：`unix:///run/anas-casdoor-test-20261003.sock`，与业务 Docker 独立。
LLNG 容器：`anas_llng_anchor_llng`；目录、数据库与 Nextcloud 复用该测试守护进程中的
`anas_cd261003_` 容器。没有迁移既有业务账号，也没有修改业务部署。

## 结论与范围

LLNG `2.23.2-r12` 已将 OIDC `sub` 设置为 `anasIdentityAnchor`，无需修改上游 Perl 源码。
正式 Dockerfile 的 Linux/amd64 镜像构建与启动已通过；签名、刷新、注销、改名和回收标签的
完整六项实机回归均通过，最终脚本退出码为 0。
Module 保持 `developing`。

本轮只验收 LLNG OIDC 身份键及 Nextcloud r11 / 官方原样 `user_oidc 8.11.0` 的兼容性。
LLNG 注册配置使用生产启动脚本，RP 和 LDAP/数据库连接由隔离夹具接线；不是一次完整的
新工作区 `anas apply`、历史版本升级或全 Consumer 矩阵验收。

## 实现

| 边界 | 最终行为 |
| --- | --- |
| OIDC 主体 | 每个 RP 的 `oidcRPMetaDataOptionsUserIDAttr=anasIdentityAnchor` |
| 缺 anchor | RP Rule 要求 `defined($anasIdentityAnchor) and $anasIdentityAnchor ne ""`，与原组规则取交集；无组限制时仍保留此条件 |
| 登录与显示 | Portal 保持 `sAMAccountName` 登录；日志/查询标签 `whatToTrace` 保持小写登录名；姓名使用目录属性 |
| Nextcloud UID | LDAP 内部 UID 与 UUID 同为 anchor，OIDC `mapping-uid=sub`、`unique-uid=0` |
| 配置刷新 | Nginx 私有 `/reload` 仅监听 `127.0.0.1:8089`，转交现有 FastCGI `LLTYPE=reload`；最终刷新成功才写配置就绪标记 |
| Netbird | calculate/render_env 均拒绝组合，避免未经批准的 anchor URL 投影（`DIRKEY-R-013`） |
| SAML | NameID 尚未切换；列为待实现，不作为本轮主要支持协议 |

固定镜像包为 `lemonldap-ng 2.23.2-1`。源码核对显示：
`Lib/OpenIDConnect.pm::getUserIDForRP` 在选项配置为空时才回退 `whatToTrace`；配置为 anchor 后
直接读取会话变量。ID Token、UserInfo、刷新后的 ID Token 与 Logout Token 使用该方法。
在线 refresh session 保存 `_oidc_logout_sub`，避免终止 refresh session 时丢失主体。
不透明 access/refresh token 本身不是 JWT，无需声称它们含有 `sub`。

私有刷新入口采用[上游的 Nginx 配置刷新机制](https://www.lemonldap-ng.org/documentation/latest/configlocation.html)。
它没有暴露 Manager API，也没有发布额外端口。实际检查本机返回 200；同网络其他容器连接 8089 被拒绝。

## 实机断言

执行入口：`test-env/scripts/server-llng-nextcloud-identity-e2e.py`。
使用测试主机已有 `cryptography` 验证 RS256，不引入产品运行时依赖，不在日志输出 token 或密码。

| 用例 | 断言与证据 |
| --- | --- |
| 首次 Nextcloud 登录 | 真实授权码回调、原 Cookie 的 OCS UID 与 LDAP `owncloud_name/directory_uuid` 均等于 anchor；页面显示人类可读姓名；真实 DAV 文件创建 |
| OIDC 主体链路 | 使用 JWKS 验证 ID Token 签名、issuer、audience、expiry、nonce 与 anchor `sub`；UserInfo 同一 `sub`；refresh 后签名 ID Token 的 `sub/sid` 保持不变 |
| Portal 登出 | 原 Nextcloud Cookie 失效，另一用户仍在线；协议 RP 收到签名 Logout Token，anchor `sub`、对应 `sid`、logout event 与无 nonce 均验证 |
| 改名 | 仅先修改 `sAMAccountName`，anchor、Nextcloud UID 与 OIDC `sub` 不变；原 DAV 文件内容不变 |
| 回收标签 | 再释放旧对象的 UPN/CN 后创建新目录对象；新 anchor、UID、`sub` 不同；不能访问原账号文件 |
| 缺 anchor | 先证明控制用户可正常取得代码和 token；只修改其一条持久 SSO 会话，删除 anchor 并定向清除相同 SID 的本地缓存；再次授权被拒绝，不能把 HTTP 5xx 算作通过 |

协议 RP 的 refresh 开关仅为验证固定版本刷新链路而开启，不表示所有生产 RP 都默认启用 refresh。
Nextcloud Provider 仅在本隔离实例中临时指向 LLNG；原 discovery/end-session 配置已恢复并读回核对；测试目录账号已清理。

## 失败与修正记录

- 原镜像没有有效的 `reload.local` 虚拟主机，CLI apply 命中默认路由，出现本机 403/read timeout。
  首轮配置未完成；手动重跑后可以登录，但测试 RP 的新 refresh 开关未及时生效。
  补齐私有 reload 路由后，新配置签发 refresh token，正式镜像启动不再出现该默认路由错误或 PIPE signal。
- 首轮协议夹具没有取得 refresh token，不计作刷新通过；后续配置刷新修正后的签名 refresh 断言通过。
- 一次在容器仍处于 `starting` 时取 discovery，响应不是 JSON；后续先等待健康，并明确断言 discovery/JWKS HTTP 200。
- 首次回收标签夹具在重新创建旧用户名时命令失败；修正为先释放旧 UPN/CN，再创建新对象，随后隔离断言通过。
- 首次缺 anchor 夹具只修改 DB，未清除 `Cache::FileCache`，工作进程仍读取有效 anchor，因此取得授权码。
  固定源码 `Common/Apache/Session/Store.pm` 确认缓存优先于 DB；修正为定向删除测试 SID 缓存后，负例独立通过。
  这不是目录变更即时撤销测试，也不能因此声称 LLNG 会自动刷新已建立会话的目录属性。
- 镜像站拒绝固定基础镜像，隔离网络直接 Docker Hub 获取 metadata 也出现 TLS timeout。
  从测试主机取回同一官方固定基础镜像并导入隔离守护进程后，正式构建成功；主机侧只写镜像缓存，未改变运行部署。

## 制品与证据

- 官方基础镜像：`lemonldapng/lemonldap-ng:2.23.2@sha256:a43d6110e6754a10d5f19986d8125cbdc4e4247a7cd1da1c62d8bffa4f7997b9`。
- 正式候选：`ghcr.io/anas-project/anas-llng:2.23.2-r12`，Linux/amd64，
  `sha256:2df0357521b47b31a9df85bfb218c17fe71225e1deee05fea73cd93643b9d950`。
- 本地证据目录：[脱敏验收证据](assets/2026-10-05-llng-anchor-sub/evidence.json)，包含最终六项结果、退出码、正式运行镜像、包版本、夹具 hash、配置恢复及目录账号清理结果。
- 远程保留：`formal-build.log/.exit`、`formal-startup.log`、历次 `identity*.log`、
  `missing-anchor.log/.exit`、`final-identity.log/.exit`。包含运行凭据的 compose/provider 文件留在服务器，权限限制为 0600，不复制到仓库。

## 验证限制与下一步

本地验证通过：LLNG / Nextcloud hook 单元测试、LLNG hook `go vet`、相关 shell 语法检查、
Python 编译检查、公共配置与内置清单定向测试、需求覆盖与测试用例目录、需求/计划索引、
文档状态、Module 文档生成一致性、升级测试目录、文档站构建以及 `git diff --check`。
文档构建有 bundle 大小提示，但未阻断构建。
远程夹具 SHA-256 与当前本地脚本一致。

本轮不覆盖 ARM64、桌面/移动客户端、独立 LDAP 登录、全部 Consumer、新工作区完整部署或旧版数据升级。
MeshCentral 新版登录矩阵没有在本轮重跑；原 OIDC 测试的注销断言已从用户名改为 anchor，避免错误地把有效 anchor UID 会话判成已注销。

LLNG 仍没有 Samba AD 事件监听与实时会话撤销：已有会话可能保留旧目录属性，停用、删除或撤组
仍需 Manager 删除全部相关 SSO session 并执行 Consumer 撤权。SAML NameID、应用会话与 SLO
列为待实现。身份键局部通过不等于整个 Module 已达到发布状态。
