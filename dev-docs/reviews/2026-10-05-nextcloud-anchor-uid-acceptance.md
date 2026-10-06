# Nextcloud anchor UID 隔离实机验收

日期：2026-10-05。服务器：`whl@finance.hlong.wang`。测试仅使用 `/home/whl/anas-casdoor-test-20261003` 下既有独立 Docker 环境，容器前缀为 `anas_cd261003_`。

## 结论

Nextcloud 34.0.2 + 官方原样 user_oidc 8.11.0 已完成正式 Dockerfile 构建、隔离 Web/cron 重建和真实登录回归。统一 anchor UID 后，无需本地插件补丁；改名保留账号和文件，显示名称保持人类可读，目录组撤权终止原会话。以下按顺序保留 8.10.1 配置实验、8.11.0 制品验证和正式镜像验收的证据边界。

## 配置与诊断

最终通过的配置为：

| 设置 | 值 |
|---|---|
| LDAP UUID 属性 | `anasIdentityAnchor`，保持原配置 |
| `ldapExpertUsernameAttr` | `anasIdentityAnchor` |
| OIDC UID mapping | `sub` |
| OIDC unique-uid | `0`，保持原配置 |
| `ldapAttributesForUserSearch` | 原有 `sAMAccountName;userPrincipalName;mail` 加上 `anasIdentityAnchor` |
| LDAP 登录过滤器 | 原有用户名、登录名和邮箱匹配分支加上 `(anasIdentityAnchor=%uid)` |

仅修改内部 UID 来源与 OIDC mapping 的首次尝试返回 HTTP 400。只补充搜索属性的第二次尝试仍返回 HTTP 400。补充 LDAP 登录过滤器后，首次导入和登录通过。因此不能只实施前两项配置后就认定问题解决。

最初的 8.10.1 配置实验移除了本地 `anchor → LDAP UID` 查询代码，但保留已有 back-channel logout 补丁；这一阶段自身不能证明整个插件无需补丁。后续原样 8.11.0 结果见下节。

## 实机结果

测试走真实 HTTP OIDC 授权、Nextcloud cookie 与 OCS 用户接口，直接读取 LDAP 映射，并通过 WebDAV 操作真实文件：

- 首次登录：OCS 返回 UID 等于 Casdoor 目录 anchor，LDAP `owncloud_name` 与 `directory_uuid` 同为该 anchor。
- 文件创建：使用该 UID 的 WebDAV 路径创建成功。
- 目录改名：Casdoor 身份与 anchor 保持不变，旧会话撤销；新登录回到相同 Nextcloud UID，原文件内容一致。
- 无关用户：改名和组权限撤销均未撤销另一用户的有效会话。
- 移出允许组：原 Nextcloud cookie 失效。
- 测试退出码 `0`；测试账号和文件清理，LDAP/OIDC 配置与控制器恢复到测试前状态。

远程证据：`nextcloud-anchor-uid-e2e.log`、`nextcloud-anchor-uid-e2e.exit`。执行入口为同目录的 `nextcloud-anchor-uid-run.sh`，测试脚本为 `repo/test-env/scripts/server-nextcloud-anchor-uid-e2e.py`。这些是本轮隔离验收文件，尚未作为正式模块实现发布。

## 模块实现

统一 UID 配置及 LDAP 搜索/登录过滤器变更已落入模块。模块固定官方 8.11.0，移除两项本地补丁和系统 `patch` 依赖，在 OIDC 初始化中执行 `occ integrity:check-app user_oidc`，失败时不进入就绪状态。对应需求 R-010/R-013 明确允许 Nextcloud 新部署在内部 UID、技术路径和内部存储目录使用 anchor；不扩大到其他 Consumer。

## 官方 8.11.0 原样制品验收

同日第二轮实机使用 Nextcloud 官方应用商店指向的 GitHub 发布资产：

- 制品：`https://github.com/nextcloud-releases/user_oidc/releases/download/v8.11.0/user_oidc-v8.11.0.tar.gz`。
- 制品 SHA256：`887920504c2f0e0d055abd66225ee7b66d756f50fe5a3739779d6b2ce108633f`。
- 原样控制器 SHA256：`9504a13d1a6206b5cf6ec52e220608d67c7aa434876161f0959ade9923568cdf`，与官方压缩包内文件一致。
- 实际安装版本 `8.11.0`；`occ integrity:check-app user_oidc` 退出码 `0`，没有关闭完整性检查。
- 不含任何本地控制器补丁；首次登录、anchor UID、改名后同账号和原文件、组撤权后原 Cookie 失效、无关用户会话保留全部通过；执行退出码 `0`。

证据见 [official-8.11.0-e2e.log](assets/2026-10-05-nextcloud-anchor-uid/official-8.11.0-e2e.log)。远程入口为 `nextcloud-official-8.11.0-run.sh`。测试配置已恢复，测试 AD 对象及 Nextcloud 账号清理；隔离环境保留官方插件升级结果。

官方制品直连下载曾发生 TLS 超时，随后通过本机 GitHub API 下载并校验后传入测试服务器。第一次安装启动早于下载完成，误移除了隔离插件目录，已从本轮备份恢复后重试；最终记录属于完整制品安装后的成功验收。Nextcloud 升级例程也检查了其他应用，本轮不将该操作计为模块完整生命周期验收。

此制品阶段运行在既有隔离镜像中，不单独计作正式 Dockerfile 启动通过；后续完整构建与启动结果见下节。

## 正式镜像构建、启动与显示名称验收

使用仓库当前完整 `modules/nextcloud/nextcloud/` 构建上下文和正式 Dockerfile，不使用旧模块镜像作为 carrier。Docker Hub 直连 TLS 超时后，使用仓库已有的 `m.daocloud.io/docker.io` 镜像代理及阿里云 APT 镜像构建成功，退出码 `0`。

- 平台：Linux amd64；基础镜像 `nextcloud:34.0.2-apache`，所用 manifest digest 为 `sha256:3323e178371b1b0d03f9b3fdbe1831ff78335f07f25116d0d598048ce459e329`。
- 候选镜像 `ghcr.io/anas-project/anas-nextcloud:34.0.2-r11`，实际容器记录的 image digest 为 `sha256:89bb0c1f8f896444cedd386998971ed4ba952504e3ea3b568263583b9d3e4593`。
- Dockerfile SHA256：`abeb852461dbbbce7ecfd5d3639ee8e48e73cfd45902b8e697bcf0eab4d152f4`。
- `task.sh` SHA256：`ac733a932603079413617d756f7e8f37c82bd52bf74dc13499aabbba86c65d66`，容器内文件与本地源相同。
- 仅重建隔离 Compose 的 Web/cron；临时 override 提供新 Hook 对应的 anchor 登录过滤器，没有修改冻结 `.env` 或业务部署。
- Web 达到 healthy、cron 启动、`nextcloud-tasks.ready` 存在。初始化自动写入 anchor 内部 UID、anchor 搜索属性、`mappingUid=sub` 和关闭 unique UID 的设置；未手工修改 LDAP/OIDC 数据库来伪造通过。
- 官方控制器摘要仍为 `9504a13d…568cdf`，完整性检查通过，镜像没有本地补丁脚本。证据见 [formal-image-startup.log](assets/2026-10-05-nextcloud-anchor-uid/formal-image-startup.log)。

正式镜像启动后，使用仓库 `server-casdoor-nextcloud-identity-e2e.py` 建立真实会话并验证：

- LDAP UID 与目录 anchor 相同；通过 WebDAV 创建文件。
- 测试用户以目录登录名认证；登录页面 HTML 的显示名称和 OCS `display-name` 均为设置的姓名，区别于 anchor UID。
- 改名后同 UID、原文件内容保留；组撤权后旧会话失效，无关用户会话有效。
- 退出码 `0`，测试 AD 对象和 Nextcloud 账号清理，anchor UID 测试映射剩余数为 `0`。证据见 [formal-image-identity.log](assets/2026-10-05-nextcloud-anchor-uid/formal-image-identity.log)。

本轮构建上下文、成功运行的测试脚本摘要、镜像、平台、退出码和清理结果记录在 [evidence.json](assets/2026-10-05-nextcloud-anchor-uid/evidence.json)。之后只补充了脚本的 HTTP 响应读取超时诊断，未改变成功路径的断言；这项诊断更新通过 Python 编译检查，没有据此重复整个实机矩阵。

验收脚本首次启动检查把接口返回的布尔值 `false` 当作字符串 `0` 比较，造成断言误报；修正类型断言后复核通过。首次显示名回归发生 HTTP 读取超时；相同 45 秒请求限制下重跑通过，未确认该超时的根因，不将它归因成身份绑定缺陷。脚本已补充无令牌的请求阶段诊断，并在登录失败时清理已知 anchor UID。

Hook 测试、Go vet、Shell/Python 语法、文档生成/需求/状态门禁、文档构建和升级用例静态目录检查通过。旧 Authentik/LLNG 登录脚本已调整 UID 断言，但没有在本轮执行；LLNG 现有用户名 `sub` 与新配置不兼容，需先切换其主体标识符。桌面/移动客户端、其他 UI、第三方应用、ARM64 镜像和完整版本升级矩阵未验收。

下一步是其他 Provider 的主体标识符整改与回归，以及桌面/移动客户端验收；模块保持 developing。本项目无需为旧 UID 转换提交 user_oidc 功能请求。
