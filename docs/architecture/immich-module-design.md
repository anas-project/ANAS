---
status: current
created: 2026-10-03
updated: 2026-10-08
---

# Immich Module 接入设计

状态：当前代码模型，`modules/immich` 为 developing。执行结果和基线记录在[配套计划](https://github.com/anas-project/ANAS/blob/master/modules/immich/dev-docs/plans/immich-module.md)；移动端条件仍缺失。
基于固定 Immich `v3.2.4` 和 2026-10-03 的用户决策；只面向新装，不建设旧本地账号迁移框架。

## 1. 已确定的边界

| 项目 | 决定 |
| --- | --- |
| 产品职责 | Immich 承担完整照片/视频备份与管理；ANAS 承担部署、身份接入和服务器灾备 |
| 数据库 | 复用共享 PostgreSQL；Contract 只增加扩展名称列表，版本与升级归 PG Module |
| 队列 | Immich Module 自有 Redis/Valkey |
| 登录与建号 | 从空库开始仅 OIDC；不建立本地密码账号，不增加 LDAP 登录或目录预建号同步框架 |
| 目录身份 | `anasIdentityAnchor = OIDC sub = Immich user.oauthId`；内部 `user.id` 保持上游生成值 |
| 受管媒体 | `${DATA_PATH}/immich/media`，容器内 `/data` |
| 数据库持久目录 | PostgreSQL Provider 自有 `${DATA_PATH}/postgres` |
| 灾备 | 统一使用 ANAS，不另建 Immich 定时数据库备份任务 |
| 资源运行方式 | 通用多档运行方式留到下一版本；本版可显式关闭 ML、配置并发，4GB 资格待整机验证 |

当前选择 `vector`（pgvector 0.8.2）与 `earthdistance`（含 cube 依赖）路线，PG 为 18.4/Alpine，
`DB_VECTOR_EXTENSION=pgvector`。不包含 VectorChord，也不让用户选择扩展版本。
简化后的[Contract 设计](/architecture/relational-database-extension-lifecycle)管理安装及升级，
不增加扩展版本求解或未发布接口的兼容层。

## 2. 从空库开始、始终只有 OIDC 账号，还会误关联吗？

**在所有账号始终具有正确的非空 oauthId、且不能被解绑或替换的前提下，不会触发按邮箱关联未绑定账号的问题。**
Immich 仍会保存应用用户记录；“没有本地账号”在这里表示没有独立密码身份，不是没有用户表。

固定版本的回调顺序是按 sub 查找；未找到才查 email。如果 email 对应用户已经具有非空 oauthId，
会拒绝该次登录；只有 oauthId 为空时才写入新的 sub。因此：

| 场景 | 原生回调行为 |
| --- | --- |
| 同一 anchor 再次登录，即使姓名或邮箱变化 | 按 sub 找到同一应用用户 |
| 新 anchor 使用一个已绑定用户的邮箱 | 拒绝登录，不接管原用户 |
| 首次 OIDC 登录，sub/email 均无冲突 | 启用 autoRegister 后创建带 oauthId 的用户 |
| 同邮箱账号存在，但 oauthId 已被清空 | 存在按邮箱自动关联路径 |

依据：[AuthService 回调与 link/unlink](https://github.com/immich-app/immich/blob/v3.2.4/server/src/services/auth.service.ts)。

上一版把“只关闭密码登录开关”与“从创建到运行始终不存在未绑定账号”合并判断，并默认要求完整身份补丁，
不够准确。后一个部署约束可以消除这条误关联路径，不必为了历史本地账号补一套迁移机制。
但初始化时没有本地账号，只能证明初始状态；上游服务中仍有清空和替换 oauthId 的方法，运行中入口约束仍需验证。

## 3. 收缩后的身份接入方案

### 3.1 原生 OIDC 建号与管理员初始化

- 在开放入口前写入受管 OIDC 配置，关闭密码登录和本地管理员 setup；
  上游提供 `IMMICH_ALLOW_SETUP=false`，不要依赖“先建本地管理员，再按邮箱关联”。
- 复用原生 `oauth.autoRegister`；IAM 的 `sub` 固定为 anchor，并沿用现有应用准入组。
- 首个管理员由受信 IAM 的 `roleClaim` 明确给出 `admin`；普通用户不能自行控制此 claim。
  AuthService 会将其传给 createUser，后者允许首个管理员创建、拒绝首个非管理员创建。
  无需默认引入专用初始化器，也不能把“第一个访问者”自动当管理员。
- 固定受信 issuer，保留上游签名、audience、state/PKCE 等验证。不替换内部 user.id，
  不把 anchor 用作公开 URL、storage label 或文件路径。

Hook 已注册 anchor `sub` 与受信 `anas_role`，由现有 Admins 判据生成 admin/user。
Authentik/LLNG 映射已接线；Casdoor 无此受信条件表达时拒绝该请求。Web/移动端和实际 IAM
组合仍需验收，不能仅据配置与源码宣称初始化通过。
[配置来源](https://github.com/immich-app/immich/blob/v3.2.4/server/src/repositories/config.repository.ts)；
[首个管理员约束](https://github.com/immich-app/immich/blob/v3.2.4/server/src/services/base.service.ts)。

### 3.2 只补维持约束必需的入口限制

运行中保持无本地/未绑定账号、anchor 不可变。验证管理建号、修改身份及 OAuth link/unlink 的服务端路径，
不能只隐藏按钮。优先使用能在服务端生效的现有配置；若无配置，则只为这些已确认可达的路径维护固定版本
最小拒绝补丁。单纯 `passwordLogin.enabled=false` 不能作为所有这些路径已经被禁用的证据。

固定版源码确认原生配置不足，镜像构建采用精确匹配补丁：禁止非 OAuth 建号、写密码、
link/unlink 和管理员批量 unlink-all。保留原生 callback、roleClaim 与内部 user.id。
不增加通用身份模式、邮箱迁移器、初始化 API、并行 LDAP 同步或 Core 直接改应用表。
本轮先测同一主体并发首次登录与软删除后重登，真实 PostgreSQL fixture 复现了同 sub 创建重复行。
据此在原生 migration 锁内、HTTP 启动前增加 oauthId 唯一约束及固定 schema 元数据；
不合并历史数据、不改绑身份。软删除保留绑定，新建失败关闭；8 路并发实测只保留一条。

发布验证至少包括空库首个 OIDC 管理员、普通用户建号、同 sub 改邮箱、不同 sub 复用邮箱被拒绝、
本地建号/解绑/重绑不可达、并发建号及移动端备份。若某入口仍能产生空 oauthId，则“纯 OIDC”约束未交付，
不宣称这一风险已经消失。

### 3.3 登出与目录撤权沿用现有要求

复用既有 IAM 与 Module 双向登出、目录事件要求，不为 Immich 另建通用身份框架。
普通登出与账号停用不同：普通登出撤销对应会话；目录停用/移出准入组需要检查既有会话、
移动端 token、API key 和分享策略。只阻止下一次 OIDC 登录不证明旧凭据已经失效。

缺失的应用适配在 Immich 私有需求中验收；不通过删除照片或重新建号模拟撤权。
这些要求不属于关系数据库 Contract，也不因去掉本地登录而自动完成。
真实签名 fixture 还复现了原生 sid-less logout token 重放撤销新会话的问题；最小固定版补丁在
应用数据库同一事务内消费 jti 和删除会话，重放、并发及跨重启已验证。它不代替目录撤权，
当前 API key 和公开分享仍有效，缺失的目录事件适配继续阻止 release。

## 4. 数据与 ANAS 备份

依照[Module 开发规范](/developer/module-development#持久数据归属)，受管媒体在 `data/immich/media`，
共享 PG 数据在 `data/postgres`；相册、资产归属与路径关系需要匹配的数据库和文件恢复点。
只读外部图库保留源目录所有权，源文件另行核对备份范围。

沿用 ANAS 停写、快照/复制、传输和恢复流程。关闭 Immich 内置自动数据库备份调度以统一责任；
这不是因为它使用 pg_dumpall，`v3.2.4` 实际使用 pg_dump。
[固定版本备份代码](https://github.com/immich-app/immich/blob/v3.2.4/server/src/services/database-backup.service.ts)

首版验证全 workspace 灾备和升级恢复，覆盖共享 PG、完整媒体、配置/Secret、deployment 和可取得的匹配镜像。
共同回退会影响其他共享库及恢复点之后上传的内容，不能只检查 Immich 能启动。路径在 data 下也不自动证明
外部挂载、嵌套子卷或停写时序正确。单应用恢复暂不交付，未来提供时仍纳入 ANAS 框架。

## 5. 实施归属

数据库公共能力按[三阶段计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/archived/relational-database-extensions.md)推进。
Immich 私有需求与计划已建立，覆盖原生 OIDC 初始化、必要入口限制、撤权、移动端、队列及媒体恢复。

Contract、PG 固定扩展与认证、Provider 维护屏障和 Immich Module 代码已落地。
私有需求与计划在 `modules/immich/dev-docs/`；e2e 未完成项继续阻止 release。
通用资源档位仍属下一版本，4GB 整机支持需要 IAM、PG、队列与媒体处理一起测试。
