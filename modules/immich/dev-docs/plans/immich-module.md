---
doc_type: plan
status: implementing
created: 2026-10-03
updated: 2026-10-09
---

# Immich Module 实施计划

验收依据是[需求矩阵](../requirements/immich-module.md)，实现见[技术文档](../../docs/technical.md)。
固定 Immich 3.2.4、PG 18.4/pgvector 0.8.2、Valkey 9.1.1；版本变化须重审补丁锚点和上游行为。
公共扩展三阶段计划中的主机维护/恢复验收与本计划共用证据，不重复定义要求。

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：Module、配置与入口保护 | R-006、R-013—R-014 | 已完成 |
| M2：原生 OIDC、唯一绑定与退出 | R-001—R-005、R-008—R-009 | 实施中；Casdoor 全新服务器完整套件及两角色网页已通过；最新 master 服务器复验通过；生产管理员缺邮箱、移动端待验收 |
| M3：媒体、撤权、队列与 ANAS 恢复 | R-007、R-010—R-012 | 实施中；Casdoor 全新上传/升级/失败与 SIGKILL/完整恢复及恢复后撤权已通过；移动端待验收 |

## 检查表

- [x] 共享普通 PG Resource、专属 Valkey、显式 ML/并发、媒体 data 路径、双语文档和生成输入。
- [x] 原生 autoRegister/roleClaim 与 anchor sub；无本地初始管理员。
- [x] 固定版本限制本地建号、密码写入和 link/unlink；唯一 oauthId、软删绑定与 jti 重放保护。
- [x] Authentik 2026.5.6-r15 的 canonical sub 与受信 Admins role；协商 CAEP 能力，缺少能力的 Provider 明确拒绝。
- [x] 本机真实普通 PG、签名 IdP 与 HTTP：首普通拒绝、首管理员/普通、同 sub 改邮箱、冲突与并发建号。
- [x] 本机 Cookie/Bearer、sid-less backchannel、错误 token、并发重放与重启后重放；真实user锁等待后短exp拒绝，jti不消费且旧session保留。
- [x] Authentik 原生 LDAP 同步/删除/准入丧失及 Admins 降权向现有持久 Task 入队；保持原事件时间重试。
- [x] Immich 持久撤权界线、会话/API key/分享撤销、旧回调拒绝、凭据创建并发锁和服务端 WebSocket 断连。
- [x] PNG/H.264 原件 hash、缩略图、相册、重启；真实 Valkey AOF 队列恢复和 4MiB tmpfs ENOSPC 反例。
- [x] ANAS CLI 全 workspace 验收脚本：真实0.8.1→0.8.2、停写事件、维护失败/进程中断与冻结候选重试、匹配snapshot/backup恢复、新装/repeat/restart/目录撤权及邮箱/冲突/并发/软删驱动，严格隔离检查。
- [x] 真实 Samba/Authentik grant、首管理员/普通、冲突/并发/软删、停用/删除/准入和管理员组丧失功能场景（分轮证据）。
- [x] 不手工触发同步的自动 journal/subscriber/native Task 到应用撤权路径及300秒主机验收时限（r18：71.206秒）。
- [ ] 真实浏览器与移动端上传、后台备份、普通及 IAM 退出和实时连接终止。
- [x] Linux/Btrfs 主机重复 apply、重启、扩展维护/失败与SIGKILL冻结重试、匹配全 workspace 恢复；核对其他共享 PG Consumer。

- [x] Casdoor r11 正式固定 Dockerfile 构建及候选二进制一致；真实原生首管理员/普通用户、权限入口与五项自动撤权。
- [x] Casdoor 固定组合完整新装复跑、0.8.1→0.8.2、维护失败/SIGKILL 和全 workspace 恢复（9fc699c 基线 b12 与 aa1944a 基线 b13 均为 17 组通过）。
- [x] Casdoor 原生桌面浏览器管理员和普通用户验收（第五轮通过）；手机条件明确缺失，移动端要求保留。

## 已执行证据

Linux 非 root 隔离复制树中的 Runner、PostgreSQL/Immich/Authentik/LLNG/Casdoor Hook 全相关包
`go test -count=1` 与 `go vet ./...` 通过。固定补丁测试和镜像内原生编译方法验证通过；
这些方法验证包含仓储替身，不能代替真实 IAM。

`modules/immich/tests/container-e2e.sh` 使用新命名网络/卷与真实普通 PG，签名测试 IdP 驱动原生 OAuth callback。
最新已记录完整通过 run `f0961da23d`（镜像 ID 见 JSON）：身份、绑定、媒体、AOF、ENOSPC 和退出均通过。
原生 8 路首次 callback 的重复 oauthId、sid-less logout 重放、新增 asset 回滚顺序、仅通知客户端未断开
WebSocket 均有修复前失败对照，见[实施核对记录](#执行记录归并)。

目录事件本机验证：旧 session/key/share（包含等待事件期间按旧 grant 创建的凭据）失效，旧的首次/已存在用户
callback 被拒绝；新 grant 保持同内部 id。按原事件时间重试不会误撤新凭据，另一用户与媒体保留。
服务端断开旧 session/key 的 WebSocket，无需客户端配合。普通退出保留独立 API key/分享，并使父/子/孙委派会话 HTTP401、对应 WS 全部断开，其他用户保留。
独立普通 PG 子进程争锁测试覆盖 JIT/session/delegated/key/share/rotate 的事务并发窗口，包括旧管理员回调角色保护和同ID轮换认证版本核对。

Valkey 使用真实写入健康检查；满盘退出 1 且 Docker unhealthy。暂停的 metadataExtraction job 在 AOF 重启后
保持原 ID，由原生 bootstrap 恢复处理。满盘上传 500 不留下新 asset，恢复后可重新上传并生成缩略图；
无法入队清理的未引用临时文件可能残留，不宣称已实现垃圾回收。

Authentik 固定源码方法和持久 Task/事务边界已测试，包括 canonical sub、无 AccessToken 的目录撤权、
partial sync、原时间重试、受信 Admins 角色丧失与 HTTP 非成功响应重试；真实管理员/普通用户浏览器链已通过；原生 LDAP 停用、准入丧失、删除和旧事件重试已通过，logout-first和管理员降权仍在接续。

`test-env/scripts/server-immich-workspace-e2e.sh` 调用本次源码构建的真实 ANAS CLI；
要求专用 Linux/Btrfs、新 workspace、指定 network namespace 和空隔离 Docker daemon。
harness 定向测试和实际Hook编译通过；测试副本来自受管SCRAM Provider与审定0.8.1源码，不是历史发行版本，也没有修改SQL extversion模拟升级。实际生成的旧PG镜像构建、ordinary TCP/HNSW/错口令/preload检查通过，固定Immich编译版本范围确实支持0.8.1。主机前置检查按设计退出 2且workspace_mutated:false；不是ANAS主机e2e通过记录。

收尾审计补齐真实 LDAP 邮箱变更/身份冲突、独立授权码并发 callback、原生 HTTP 软删除驱动，
以及维护后真实 SIGKILL/同冻结候选重试。CLI 非零或超时先保留0600阶段输出，失败清理前保留有限生命周期状态。
实际当前源码 `anas init/plan` 本机检查列出两个共享消费者和完整停启范围，config/Secret未改变；
没有执行本机 apply/start，也不把这项计划输出核对算作真实维护验收。对应定向测试进入既有 CI，
不在 CI 连接 Docker daemon、真实 IAM 或远程目标。主机扩展维护、失败恢复、SIGKILL和冻结重试现已通过，见后续主机记录；客户端验收仍待接续，M2/M3状态不变。

## e2e 执行记录

| 需求 ID | 场景/脚本 | 环境 | 日期 | 结果 | 尚待验收 |
| --- | --- | --- | --- | --- | --- |
| IMMI-R-001 | container-e2e.sh；server-immich-workspace-e2e.sh | arm64 本机；finance 专用 Linux/Btrfs/真实 AD+Authentik | 2026-10-03 | 本机及主机对应身份场景通过（c8e14a68） | 完整 workspace suite 仍待通过 |
| IMMI-R-002 | container-e2e.sh；server-immich-workspace-e2e.sh | arm64 本机；finance 专用 Linux/Btrfs/真实 AD+Authentik | 2026-10-03 | 本机及主机对应身份场景通过（c8e14a68） | 完整 workspace suite 仍待通过 |
| IMMI-R-003 | container-e2e.sh；server-immich-workspace-e2e.sh | arm64 本机；finance 专用 Linux/Btrfs/真实 AD+Authentik | 2026-10-03 | 本机及主机对应身份场景通过（c8e14a68） | 完整 workspace suite 仍待通过 |
| IMMI-R-004 | container-e2e.sh；verify_entry_restrictions | arm64本机；finance真实ANAS/普通PG/OIDC角色 | 2026-10-03 | 8个真实HTTP入口拒绝、无未绑定账号/密码写入/身份角色改变通过 | 无；固定补丁锚点反例已通过 |
| IMMI-R-005 | container-e2e.sh；server-immich-workspace-e2e.sh | arm64 本机；finance 专用 Linux/Btrfs/真实 AD+Authentik | 2026-10-03 | 本机及主机对应身份场景通过（c8e14a68） | 完整 workspace suite 仍待通过 |
| IMMI-R-007 | container-e2e.sh；主机workspace照片/视频hash、缩略图、相册 | 本机；finance真实ANAS/AD/Authentik | 2026-10-03 | 本机及主机API/媒体通过 | 真实浏览器已通过；移动端上传/后台备份 |
| IMMI-R-008 | 同上，普通/单次/并发/重放/重启退出；真实浏览器 | 本机；finance原生IAM/浏览器 | 2026-10-03 | 本机及真实浏览器两方向退出通过 | 手机 |
| IMMI-R-009 | 同上，普通/单次/并发/重放/重启退出；真实浏览器 | 本机；finance原生IAM/浏览器 | 2026-10-03 | 本机及真实浏览器两方向退出通过 | 手机 |
| IMMI-R-010 | 同上，目录事件接收、凭据事务竞态与 WS；原生LDAP/Task/签名HTTP | 本机；finance真实IAM | 2026-10-03 | 五种功能场景和恢复准入后旧事件重试通过 |自动事件路径71.206秒通过；容量/故障传播保证不在本次结论 |
| IMMI-R-011 | container-e2e.sh restart/AOF/ENOSPC；主机workspace repeat/restart | 本机；finance真实ANAS | 2026-10-03 | 本机队列/满盘及主机repeat/restart通过 | 完整suite后续场景 |
| IMMI-R-012 | server-immich-workspace-e2e.sh；c8e14a69接续r5 | finance专用 Linux/Btrfs/隔离 daemon | 2026-10-03 | 真实全workspace backup/restore及业务核对通过 | 公共扩展维护失败/中断与匹配旧/新恢复已通过；浏览器/手机另验 |

## 阻塞和未交付

- 真实 Android/iOS 客户端的照片/视频上传、后台备份、退出与目录撤权会话仍缺验收条件。桌面浏览器与服务器 API 的通过结果不替代此项，M2/M3保持实施中、Module保持 developing。
- 内部测试CA的浏览器夹具未通过ServiceWorker/PWA注册；未声明PWA安装/离线可用，也没有整机容量测试或4GB支持结论。
- 新装、重复apply、重启、原生身份、扩展维护/失败/SIGKILL精确重试、匹配旧/新及当前CA组合全workspace恢复、两角色浏览器和五种目录功能均有真实主机分轮通过证据。自动事件不手工同步时71.206秒完成旧四类凭据撤销，满足本次健康固定组合300秒验收上限；不声明故障/容量普遍保证。
- 最新 master aa1944a 的 b13 单轮全新安装完整 suite 已通过，17 组检查及恢复后五种自动撤权均通过。历史失败报告仍保留，不能将历史进行中记录作为当前结论。
- finance 已正式部署 Immich；生产目录管理员缺少 mail，原生 OAuth 回调 HTTP400（OAuth profile does not have an email address），应用仍为零用户。等待用户指定邮箱或选择现有服务联系邮箱后，仅补齐现有目录账号资料并由原生同步接入；生产首次建号和上传尚未通过。
- 最新 Linux/Btrfs 完整 Runner 包通过（120.683秒），相关 Hook/helper、补丁及文档门禁通过。本地 macOS 完整 Runner 仍受 rsync/Linux 文件系统条件限制而失败；未执行最新全仓库 go test ./...，不得声称全仓库 Go 测试通过。
- 本轮按用户授权临时暂停限定的 28 个 finance/旧测试容器，测试后全部按原 ID/镜像恢复健康（restored=true、restore_errors=[]）。随后正式 apply 更新 finance，现有 16 个常驻服务健康，目录初始化任务正常退出 0。隔离工作区、备份、报告和任务代理仍保留；未操作无关部署，未提交或推送。

## 补充证据（按轮次）

主机浏览器补充：c8e14a69 browser-r4两角色passed（管理员111124ms、普通108090ms）。真实OIDC、PNG/H.264上传、原件hash/缩略图、相册创建改名、RP退出和IAM主动退出、旧Bearer/Cookie401及再次IAM认证通过。失败r1/r2/r3均保留；仍缺真实手机/后台备份，不能将R-007/R-009完整放行。原生目录r14停用/准入丧失/删除和旧事件重试通过，r16当前HTTP CA镜像恢复也通过；退出后撤权与管理员降权接续中。


最新r14：原生disable/remove-app-group/delete及重新准入后旧事件重试均通过；logout-first响应解析TypeError导致该轮failed并清理成功。r15先刷新当前受管HTTP CA镜像的全workspace一致备份恢复，再补验logout-first和管理员降权；默认suite五场景不变，分轮证据不作为单轮新装全通过。Linux驱动当前42/42零skip，真实手机和PWA未放行。


r17接续passed：原生空302正确处理且IAM cookie/AccessToken实际失效，logout-first随后撤独立key/share；管理员失去Admins但保留APP准入后，同身份新OIDC普通用户与新凭据有效、旧凭据不复活。五种原生目录功能按分轮证据全部通过，媒体和绑定保持。自动事件触发和传播时限另由r18验证：仅准备阶段手工同步，测试成员移除后禁止手动sync；300秒验收上限不表示4GB或普遍容量SLA。手机/后台备份仍缺，M2/M3继续实施中。


r18自动事件传播passed：不手工sync，匹配新事件seq99与持久cursor99、原生LDAP/签名任务DONE、接收端已验签截止及HTTP旧四类凭据401，71.206秒达到本次300秒主机验收上限；无关用户、媒体/hash/绑定和基础恢复状态保留。健康固定组合的该时限不是4GB、整机容量或故障保证。手机仍未验收、PWA测试CA限制未放行，M2/M3维持实施中；原生Task耗尽后仍经现有任务入口处理。


## 2026-10-07：合入线上 master 与 Casdoor 接续

- 已合入 `origin/master` `9fc699c`（原基线 `fd734b8`，9 个提交），保留原已跟踪/未跟踪工作及 stash 备份；冲突逐项合并，生成文件重新生成。
- 复用 master 的 Casdoor r10 canonical subject、原生 refresh/UserInfo 与持久定向会话撤权；r11 增加可选受信 `anasRole` 映射和协商 CAEP 策略事件，无新服务或调度器。
- Casdoor 的新增真实 OIDC/媒体/目录撤权/恢复验收进行中，尚不算已通过。
- 用户确认目前没有真实手机验收条件。IMMI-R-007、R-008、R-009 的手机要求没有删除或以 API 测试替代；Module 在满足这些强制项前保持 `developing`。
- 本机完整 Runner 测试失败，包括 macOS rsync 不支持 `-A`；最新 Linux 验证与新组合门禁需继续执行，旧基线失败不能替代新检查。

- 最新 Linux Runner 全包通过（10.026 秒）；Hook/helper、固定源码七项补丁测试、文档生成与构建通过。新 Casdoor 主机测试等待三份源码同步授权，不能计为通过。

- 三份新增/修改源码获准并上传；固定镜像成功后自动启动隔离主机验收。共享驱动两处停写断言已本地改为实际 Provider，第四份脚本同步另需明确授权。

- 第四份共享脚本获准并上传，候选镜像 `f606c9081978` 构建成功；补齐公开 pgvector 升级夹具后，在 finance 的新隔离 workspace `c8e14b08` 开始主机新装验收。正式 BuildKit 配方与应用验收尚未计为通过。

- 实机确认 Casdoor dirwatch 403 来自内部服务请求误走构建代理；helper 已改为直接连接，真实 HTTP 回归通过。候选 `00b78c312d45` 在新 workspace `c8e14b09` 开始完整重跑；前一续跑在原生授权码阶段失败，尚未计 OIDC/上传通过。

- 2026-10-07：Casdoor 隔离新装 `c8e14b09` 服务与目录同步健康；首次登录
  因 CAEP 标记误用原生登录 tags 被拒绝。已修正为受管 Application 布尔字段，
  正在重建和复验；OIDC、上传与完整恢复尚不能据此标为通过。

- 2026-10-07：固定源最小补丁的 Go 模块校验与测试镜像编译完成；新的空工作区
  `c8e14b10` 初始化成功，继续验证登录、原生权限入口、上传、升级与一致恢复。
  镜像使用临时同版本缓存层配方，正式构建配方验收仍单列未完成。

- 2026-10-07：实际回调发现 Casdoor 自定义 token 的空 nonce 不符合严格客户端
  检查；最小固定源修复后，普通用户首次建号拒绝、空库保持为空、可信目录
  管理员原生建号均在主机通过。继续完整新工作区验证，不能将诊断计作完整
  release 或手机客户端验收。

- 2026-10-07：`c8e14b11` 全新 apply 和原生管理员/普通用户建号、Casdoor
  普通 profile 权限、Immich 八项入口限制实机通过。完整套件因并发回调
  “全部成功”断言失败，保留原报告；按 IMMI-R-005 记录竞争状态、验证一个
  有效绑定后，在 `continuation-r1` 继续身份、上传、撤权及恢复。
  续测不计新装完整验收；正式 Dockerfile 构建同步执行，仍待结果。

- 2026-10-07：`continuation-r2` 实机照片/H.264 上传、缩略图/相册、应用角色
  扩展与 HNSW、重复 apply、重启通过；五项 Casdoor 自动目录撤权均使旧
  bearer/cookie/API key/share 401，管理员降权后同内部 id 以普通角色重登。
  仍是保留工作区续测，软删除重登上游返回 500；完整新装复跑、扩展升级、
  一致恢复、正式构建与手机验收仍待完成。证据见同日 master 整合评审。

- 2026-10-07：正式 Casdoor Dockerfile 串行构建通过，server/helper 摘要与
  实机候选一致，临时公开 Go 缓存服务已停止。`recovery-continuation-r3`
  继续常规 apply 的扩展升级和完整恢复；历史新装失败报告仍保留。

- 2026-10-07：`recovery-continuation-r8` 在正确隔离网络中完成真实 vector
  0.8.1→0.8.2 协调升级，验证全工作区停写、旧实际镜像恢复点、消费者
  启动前维护、普通角色权限与扩展/preload、HNSW、原件/相册和共享数据。
  直接回退被拒绝。完整备份恢复、维护失败/SIGKILL、修正后完整新装、
  Casdoor 网页及手机客户端验收仍未完成，M2/M3 和 developing 状态不变。
  恢复后目录入口改为 Casdoor 原生五种自动撤权检查；本机运行 43 项
  （41 项通过、2 项 Linux 条件跳过），不替代实机结果。证据见同日整合评审。

- 2026-10-07：r8 一致工作区备份 create/verify 及内容/镜像归档核对通过。
  重启后应用 unhealthy、实际 ping 404；主机 memory/I/O PSI some avg10
  约 95%/97%。为减少共享主机争用，中断本任务 runner 并确认隔离容器
  全部停止，工作区和备份保留。报告仍 failed，完整恢复未执行；已询问
  更空闲 Linux/Btrfs 或更多内存条件，生产部署仍未改变。

## 2026-10-08：暂停负载后的全新 Casdoor 实机验收

- 用户授权临时暂停 finance 任务并测试后恢复。原运行 28 个容器的 ID、镜像、健康状态和 socket 保存在远端私有恢复清单；仅临时停止 ANAS 两套任务，保留网络服务。
- 全新工作区 `c8e14b12` 的服务器完整套件退出 0、status=passed，17 组检查通过；不是 b11 接续结果。新装、原生首管理员/普通用户、角色与入口限制、邮箱/冲突/四独立并发/软删、PNG/H.264、repeat/restart、实际普通角色/扩展/preload/HNSW、五项自动撤权通过。
- 真实 vector 0.8.1→0.8.2 受控维护及旧实际镜像恢复点、当前完整 workspace 备份恢复、维护后失败的四项屏障与旧/新匹配恢复通过。SIGKILL exit=-9，精确冻结候选重试保留原 Hook/manifest/恢复点，屏障清除及正常 start 通过；重试后匹配新备份恢复也通过。
- 恢复后重新跑五项 Casdoor 自动撤权：disable 64.350s、remove-app-group 66.825s、delete 67.395s、logout-first 59.172s、admin-role-loss 69.028s，旧四类凭据 401，绑定/媒体保留及无关用户可用。
- 套件清理确认隔离容器停止、工作区与历史证据保留。维护清单 restored=true、errors=[]，原 28 个容器 ID/镜像不变并恢复健康。Casdoor 浏览器待验，恢复账号原生准入负例已确认现有密码有效、回调拒绝且不新增账号（第二维护窗口）；手机条件仍缺失，M2/M3 与 Module developing 不变。详见[本轮实机记录](#执行记录归并)。

- 浏览器第二维护窗口：修正独立网络命名空间下的测试域名地址（ANAS_TEST_HOST_ADDRESS），管理员流程到达 Casdoor，但原生表单尚未出现，上传未开始。保留失败记录；原 28 个容器按原 ID/镜像恢复健康，无恢复错误。第三轮继续采集原生匿名页面，不能将尚未完成的 Casdoor 浏览器验收计为通过。

- 第五维护窗口 Casdoor 两角色网页 passed：管理员 38.385s、普通 36.804s，原生 OIDC/引导/PNG 与 H.264 上传、hash/缩略图、相册和双向登出/旧凭据 401/重登录均通过。原生恢复管理员准入负例通过；原 28 个容器恢复健康且 ID/镜像不变。固定 v3.2.4 更新提示仅用原生 Acknowledge 关闭，未升级；仍不计手机、PWA 或容量验收，状态保持 developing。生产公开候选保留 Nextcloud r10、Samba r12，生产部署接续中。

## 执行记录归并

依照最新 AGENTS.md，验收结论归入配套计划。以下历史记录保留原测试基线与失败；较晚的代码合并不自动继承实机通过结论。原始日志、截图禁用策略、执行 JSON、备份及重试均保留在 finance 私有测试路径，未放入 Git。

### 2026-10-03 早期实施与验证基线


> 2026-10-03；HEAD `fd734b83` 加初始与本任务未提交工作树。
> 已实施代码；finance专用主机新装/重复apply/重启、真实AD/Authentik身份与扩展升级通过。
> 原生PG、整体backup restore、维护失败两方向恢复与真实SIGKILL/冻结重试通过；原生目录五种功能场景分轮通过。
> 真实管理员/普通用户浏览器验收通过；原生目录五种功能、自动事件传播与当前CA镜像恢复通过，真实移动端尚缺，不能发布放行。
> 无提交或推送，未操作无关部署；初始差异保存在 `/private/tmp/anas-immich-initial.diff`。

## 当前验收状态

以下为当前结论；后面的分轮记录保留当时结果，不能把早期“待验收”当作最新状态。

| 项目 | 当前结果 |
| --- | --- |
| 新装、重复 apply、重启、原生管理员/普通用户、身份冲突与并发 | 主机对应场景通过；失败分轮报告保留，没有单轮完整新装 suite 通过记录 |
| 8条建号、密码及身份绑定入口 | 真实 OIDC 角色请求拒绝，身份与角色不变，无未绑定账号，通过 |
| 管理员和普通用户浏览器 | 原生登录、照片/视频、hash、缩略图、相册、RP/IAM 两方向登出通过 |
| PostgreSQL 扩展生命周期 | 普通角色 SCRAM、实际升级、停写、维护失败、匹配旧/新恢复、SIGKILL和精确冻结重试通过 |
| 原生目录撤权 | 停用、移出应用组、删除和恢复准入后旧事件重试保护通过；r14/r17五种功能场景分轮通过；r18无手工同步自动传播71.206秒，300秒主机验收上限通过 |
| 当前 Authentik CA 修正组合恢复 | r16当前CA组合匹配备份恢复及数据、配置、Secret、精确images核对通过；分轮失败报告保留 |
| 手机、PWA、容量 | 手机上传/后台备份未验；内部测试 CA 的 Service Worker 失败未解决；未声明4GB支持 |
| 最新仓库门禁 | Linux驱动42/42零skip、需求646项覆盖、状态索引、文档构建与diff格式检查通过；全Go测试仍有干净HEAD复现的基线失败 |

## 实施范围

按 AGENTS、需求/计划索引、两份设计、矩阵/计划、Module 规范与
[前次评审修正结论](../../../../dev-docs/reviews/2026-10-02-immich-integration-plan-review.md) §6 实施。
[公共扩展计划](../../../../dev-docs/plans/archived/relational-database-extensions.md) 和
[Immich 私有计划](#执行记录归并)采用同一证据；公共扩展M1/M2/M3完成并归档，Immich M1完成、M2/M3实施中。

| 落点 | 已实现行为 |
| --- | --- |
| relational_database Contract / Runner | 仅增加可选 postgres.extensions 名称列表；严格校验并在生成 Secret 前拒绝非法请求，投影既有 Resource env |
| PostgreSQL Provider | PG18.4/pgvector0.8.2/cube1.5/earthdistance1.2；Provider 管理依赖/版本/preload/初始化升级，应用普通角色 TCP 实测 |
| trust 认证修复 | 新装 SCRAM；旧 trust 在 TCP 关闭和私有 socket 中修复管理员口令/HBA，再开放正式服务；未赋应用超级用户或角色成员 |
| 扩展维护 | 列出并停止全部共享消费者，必需 ANAS Btrfs 恢复点及镜像归档验证通过后授权维护；inspect 只读，未知来源写前拒绝 |
| 失败与恢复 | 既有 deployment/backup 生命周期持久保护，失败拒绝旧镜像打开变化或未知数据；同一冻结候选有条件重试，匹配恢复才解除保护 |
| PG 暂停补偿 / 历史重加 | 补偿启动 PG 后执行 readonly after_start，再启动消费者；历史数据重加普通 Apply 在 up/ensure 前拒绝 |
| Immich Module | 共享 PG、专属 Valkey、OIDC-only、原生首管理员/普通建号、显式 ML/并发、data/immich/media、无应用备份调度器 |
| 固定身份保护 | 最小固定源码入口 guard、oauthId 唯一约束、持久 jti、旧 grant 撤权界线及创建事务锁；内部 user.id/媒体路径不改为 anchor |
| Authentik r15 / 中立 IAM 字段 | canonical sub 与受信 roleClaim；协商 OIDC_CAEP_EVENTS，复用 LDAP 同步和 PostgreSQL Task 发送准入/角色丧失事件 |
| 目录撤权与实时连接 | 旧 grant 的 session/key/share 失效，延迟旧 callback 与并发创建拒绝；服务端断开对应 WS，原时间重试保留新授权 |
| 队列故障 | Valkey 写健康检查；AOF 原任务恢复；满盘上传回滚新 asset，健康后可重试 |

没有旧 Contract/lock 转换、多版本适配、版本求解器、扩展目录协议、新请求 ABI、独立维护状态机、
身份框架、迁移器或调度服务。复用 Resource/Compose/Secret/plan/apply/锁/原生 migration 与 ANAS 备份，
没有新增项目 Go/npm 依赖。LLNG 可映射管理员角色但缺少本版所需策略通知能力，Casdoor 缺少所需角色来源；
当前 Immich 组合只允许具备全部能力的 Authentik r15，不按上游支持推断已验收。

## 固定组合与实测补丁

固定 Immich3.2.4/ML、Valkey9.1.1 和 PG18.4/pgvector0.8.2 的摘要或源码校验；preload 明确为空。
PG 源 revision 遵循发布工具，本机临时 tag `anas-immich-test/anas-postgres:18.4.0-r5` 不作为发布版本。
PG 与 Immich 保持 developing。Authentik 固定2026.5.6 多架构 index，并升到 r15 包含安全补丁。

空库首普通用户拒绝、首管理员使用原生 autoRegister/roleClaim。
`anasIdentityAnchor = OIDC sub = user.oauthId`，保留上游内部 UUID，storageLabel 为空。
固定编译文件精确锚点变化/缺失/重复或重复应用均构建失败。

| 已复现缺陷 | 修复与证据 |
| --- | --- |
| 同 sub 的8路 callback 建立8条 oauthId（run39a8c685a6） | 原生 migration 锁内、监听前唯一约束；并发和软删保护通过 |
| sid-less logout 重放撤掉新 session（runaae1be3c35） | jti 消费与删除同事务；数据库年龄[-5,126)秒及可选exp重检，5分钟保留，8路仅1次成功，跨重启重放拒绝 |
| 500上传仍新增asset（runa04fe5fdb6） | 将既有新增asset回滚提前至清理任务入队；修复后满盘无新asset、健康重试成功 |
| HTTP session401但WS仍连接（run3ba8fd38a2） | 凭据房间加入后再认证，撤销时服务端精确断连；不依赖客户端logout消息 |
| 延迟old admin callback返回403却把fresh普通用户isAdmin设true（run875f2c4d1a） | callback角色写入使用同anchor/user锁与已验签grant截止，真实普通PG编译方法/争锁及整体HTTP通过 |
| 同ID API key轮换后旧认证请求借用新iat | 内部不可枚举Symbol保留认证hash，锁内核对版本；最终镜像真实普通PG12组运行断言与子进程旧来源拒绝、新来源有效 |
| 同ID轮换旧secret HTTP401但WS仍在房间（runeb55aa16e0） | 返回新secret前按canonical UUID断开旧凭据房间，固定原生方法及真实HTTP/WS通过 |
| 短exp退出token等待真实user锁10秒后仍200（run8af18f6c80） | DB接收时复核可选exp与JOSE容差，整个事务拒绝，定向及真实10秒user行锁等待HTTP回归通过，jti未消费且session有效 |
| Authentik IDToken内部uuid与映射sub表示不同 | canonical sub贯穿原生IDToken→AccessToken→session-delete→signed backchannel；实际固定方法验证 |
| 普通Apply历史PG重加启动旧候选 | 五组公开负例修复前均成功，修复后postgres_restore_required；未宣称实际数据损坏 |

策略撤权使用原事件时间及已验证 ID Token iat，而非凭据创建时间；JIT/session/delegated/key/share/rotate
共享 anchor advisory lock→用户行锁，unknown subject 也持久记录，拒绝延迟首次 callback。
原事件重试保留新凭据。同秒 iat 保守拒绝，可下一秒重新 grant。
旧管理员callback的全局角色写入也经过同锁与cutoff；API key认证内部保留认证时hash，不能借同ID新版本来源。成功轮换在返回新secret前断开旧ID房间。普通 logout 仍保留独立 API key/分享，策略事件明确撤它们；不通过删除用户/媒体或改变绑定实现。
无效 Logout Token 使用固定日志，不泄露 JOSE payload/cause。故障期间未引用临时文件可能残留，未实现垃圾回收；
被拒绝的首次 callback 可能已创建原生空 cluster_group，未创建无绑定 user/凭据，不声称无任何写入。

## 维护与灾备边界

PG 维护前全 workspace 停写，Btrfs 覆盖检查拒绝 data 外部挂载、嵌套子卷、失效/逃逸链接。
所有 Compose service 捕获实际不可变 Image ID、归档和校验，经现有 metadata 通道随 snapshot/copy/send/send-file。
恢复只固定恢复制品，不改其他全局 tag。变更数据前设置保护，最后在数据、配置、Secret、状态和 active 提交后清除。
失败/中断普通 start/apply/rollback/备份补偿拒绝；仅同冻结失败候选核对原恢复点及归档并重停后重试。
大版本、降级、未知来源要求匹配 ANAS 恢复，不能用旧镜像打开新数据。

CLI/HTTP snapshot 与 backup 共用 PG 停写规则；实时 PG 备份拒绝 --no-stop。
补偿启动 PG 后 readonly after_start 验证实际 TCP/SCRAM、版本和 preload，失败保留事务、不启动消费者，
检查不会继承维护授权。PG 历史重加检查既有 Resource 与 PreviousDeployments，无法核对也拒绝；首次空库允许。
媒体和 Valkey AOF 在 data/immich；PG data 由 Provider 管理。只读外部图库源文件备份范围单独核对。

## 已执行验证

| 检查 | 结果与限界 |
| --- | --- |
| PG arm64完整隔离脚本 | 新装/repeat/restart、正确/错/空口令、管理员冒用、库隔离、权限/角色成员、扩展/preload漂移、inspect对象hash、retain通过 |
| 真实vector0.8.1→0.8.2与旧trust | 旧HNSW和其他库保留、SCRAM修复、未知来源写前拒绝、部分状态重试通过；不替代ANAS主机恢复 |
| PG amd64模拟构建/基础新装 | 通过；未宣称原生amd64整机验收 |
| Immich普通PG/signed IdP/HTTP/WS | 最新完整runf0961da23d的36项检查通过：身份/绑定/入口/媒体/队列/退出、旧key/share/旧callback撤权、新凭据保护及服务端断连 |
| Valkey AOF/ENOSPC | 原metadataExtraction ID从AOF恢复且原生bootstrap处理；4MiB tmpfs满盘写探针退出1/Docker unhealthy，上传500无新asset，健康重试原件/缩略图通过 |
| 普通PG多进程凭据竞态 | 最终镜像12组运行断言通过：JIT/session/delegated/key/share/rotate实际advisory等待后撤权提交，旧授权创建均拒绝；不是完整app/IAM验收 |
| Authentik固定原生方法 | canonical sub、nativeLDAP更新/组删除/受信Admins丧失、partial-sync、持久事务/Task参数、原时间/HTTP失败重试验证；无真实LDAP主机链 |
| Runner+PG/Immich/Authentik/LLNG/Casdoor Hook | Linux非root隔离复制树Go1.26.6，相关全包-count=1通过 |
| go vet ./... | 同环境通过；日志/private/tmp/anas-immich-final-target-output/ |
| CLI Contract | 当前源码非root Linux隔离复制树通过26断言；初次临时环境noexec/只读缓存失败已修环境并复查 |
| 补丁/驱动及Hook编译 | Immich19项、Authentik35项、workspace33项通过；workspace全family在Linux非root执行、零skip，含实际/proc归属和自有SIGKILL保护。7个选定Hook与3份PG测试副本实际Go编译通过 |
| 实际Docker镜像归档round-trip | 旧tag移动/旧缓存删除后加载归档、恢复制品ImageID固定、原tag不变通过；不是整机数据恢复 |
| PG补偿/历史重加 | 新非rootLinux定向回归+vet通过，各有修复前负例对照 |
| ANAS全workspace脚本 | 定向测试、7个Hook实际编译和隔离前置检查通过；真实0.8.1→0.8.2/失败guard/matching恢复脚本已补齐。本机非Linux/Btrfs退出2、workspace_mutated:false，流程待运行 |
| 文档/生成/索引门禁 | 本轮需求覆盖/状态/索引与对应测试、Module/Contract生成检查、21条升级目录和CNB目录通过；npm run docs:build通过（仅既有大chunk提示） |
| 全仓库go test ./... | 未通过；干净HEAD也复现下列环境/时序/文件系统失败，未跳过失败包 |

runf0961da23d JSON：`anas-immich-it-f0961da23d-4kywv_3y/report.json`（任务临时目录）。
PG ImageID `sha256:ea0c71dffe191396f81a4b1d3504f00244d13ad486b4de1b13a21f72619f27fb`；
Immich ImageID `sha256:68d7b411b339b2b856aa672b38ea42254ce18afd2cce07d71eb195f377e48d08`。
最新run同时通过普通logout父/子/孙会话的HTTP401和服务端WS断连，其他用户连接保持；独立普通PG锁等待/FK冲突验证委派先/退出先两种顺序。Authentik任务镜像ID `sha256:f488ddedffe9ad0d034486f927b968e6f656f370329970e99ba81d0b747ff4c3`。原生默认五次重试耗尽后需经现有任务入口重试，未宣称无限自动投递。

Linux全仓库完整对照使用注册非root tasktest、Go1.26.6、独立Git复制树、私有临时目录和新GOCACHE。
当前与干净HEAD完整执行，日志`/private/tmp/anas-linux-review-output/README.md`：

- computeingress/jobexecutor测试目录umask022下0755，正式API要求0700；HEAD复现。全局umask077会破坏权限负例，未采用。
- consolejobs在tmpfs立即同大小改写后size/mtime/ctime未变，HEAD也漏检journal损坏。
- processgroup/jobexecutor并行套件即时回收时序失败；HEAD独立10次可过，不推断全门禁通过。
- backup PlanID含目标剩余空间；HEAD同文件系统写无关1MiB即可复现确认失效。

未为本任务修改无关失败代码或关闭保护以制造全绿。

## 收尾验收驱动与诊断证据

已补齐真实 workspace 的邮箱变更、同邮箱不同anchor冲突、两个独立PKCE grant并发callback及原生管理员
HTTP软删除后换邮箱仍拒绝重新建号驱动；全部使用独立AD用户，不改主恢复账号和绑定。
固定Immich镜像的实际编译API探针核对soft delete DTO/status/保留行与原生lookup过滤；
此探针不是LDAP或完整应用流程验收。现有用户保持原应用email是v3.2.4原生行为，此处只断言同内部id。

维护中断驱动在测试副本的原生维护成功锚点后暂停。真实Linux PID/start ticks/session/group/父链、
精确冻结 `.hook.bin`/source/cwd/Workdir 核对后仅SIGKILL此次CLI及其独立Hook组；
不能用ready任意pid授权杀进程。普通操作必须仍被持久保护阻断，再以 `apply --deployment` 重试同一
冻结候选并核对原point/hash不变、重停候选、共享数据和当前guard。之后恢复匹配新版backup。
测试副本移除PG prebuilt Hook，生产源码/ABI不变；该完整ANAS流程仍待真实主机。

所有CLI非零/超时先保存0600 stdout/stderr，异常信息不包含argv/凭据。
失败清理前有限保存active/deployment状态和保护记录，不复制Secret/env/media/artifact。
本机实际当前源码 `anas init/plan` 检查通过，列出authentik/immich及
lego/samba_dc/traefik/postgres/authentik/immich完整停启范围，config/Secret未变；
证据 `/private/tmp/anas-immich-plan-proof-317ymszs/report.json`，没有执行apply/start或Docker。
固定补丁、Authentik Python与workspace驱动测试加入现有无daemon CI。
最终workspace family在当前源码只读挂载、无网络、Linux arm64非root/Go1.26.6环境执行33/33通过
（24core+3plan+6crash，零skip），包含真实/proc归属正负例、自有CLI/独立child组SIGKILL且无关进程存活。
三份PG副本实际编译与维护成功锚点后的pause/release/原ready重用通过；维护命令替身只验证驱动，
不代替真实PG/ANAS维护。日志 `/private/tmp/anas-immich-workspace-family-linux-nonroot-20261003.log`。
CI YAML使用仓库已有yaml.v3校验；未增加依赖。文档构建和本轮需求/计划状态门禁通过。

实际远程注册路径为 `test-env/remote/*.local.yml`（默认targets.local.yml），当前目录只有example文件，
不存在已授权配置。当前主机Darwin；未猜测历史SSH部署。此环境阻塞不影响已交付实现，但阻止M2/M3验收完成。

## 主机授权前的未完成与接续条件（历史状态）

以下保留授权前状态；现有授权和实际主机证据见本文顶部与后续接续记录。

1. 需要明确授权的专用Linux/Btrfs远程SSH目标、network namespace/空隔离daemon；不能猜测历史部署。
   `server-immich-workspace-e2e.sh` 已准备真实ANAS CLI的新装/repeat/restart、身份边界、维护失败/中断及backup/copy/verify/restore，尚未执行；测试副本不是历史发行版本，未修改SQL extversion模拟升级。harness定向测试、Hook编译及实际受管0.8.1镜像/SCRAM/普通role/HNSW/preload基线通过。
2. 真实Samba事件→Authentik持久Task→Immich链：停用/删除/准入组/Admins降权、真实grant角色/sub、队列重试及最大传播时间待验收。
   此实现仅覆盖协商能力的Authentik→Immich；LLNG、Casdoor及其他DIRSYNC消费者未被推断完成。
3. 真实浏览器与移动端上传/后台备份/普通及IAM退出未验收；本机signed fixture不替代客户端。
4. 扩展维护失败/中断与全workspace一致恢复需验证DB、媒体、配置、Secret、匹配镜像及其他PG消费者。
5. 全仓库已有失败归独立主题；无整机内存测试，不声明4GB或自动运行档位。

当时下一步：提供上述专用环境并运行已交付主机脚本，依据真实结果完成M2/M3与发布评估。

## 2026-10-03 SSH 主机接续检查

用户已指定 `whl@finance.hlong.wang`，重新连接时严格known-host检查与BatchMode成功。
只读检查确认Linux x86_64、Btrfs、passwordless sudo、Docker Compose5.5.0、Python/PyYAML、
rsync及Btrfs工具；Go不在当前PATH，尚需私有测试工具链。默认Docker有24个运行容器，
约3306MiB RAM/8GiB swap、106GiB可用空间；不复用或清理这些部署，不作4GB支持声明。

已仅创建本次私有空staging目录 `/home/whl/anas-immich-e2e-c8e14a62`。
本机当前工作树2104个文件打包为5813529字节，SHA256
`f20cc9482e35ad662df3fa7ad5f52e7c4f53fc17770eae22a57d028854db2469`，
同源码Linux amd64 ANAS/helper已构建。准备上传时自动审批拒绝：用户授权远程e2e，
但尚未明确授权该私有源码payload上传。未绕过拒绝，未上传源码/二进制，未创建netns/daemon/workspace，
未启动测试或修改现有部署。服务器e2e仍待明确上传授权后接续，不计验收通过。

用户随后明确允许上传：上述源码包与同源码二进制已上传并校验，公开Go1.26.6 Linux amd64工具链
按官方SHA256校验后仅安装到私有stage。已用仓库脚本创建独立
`anas-immich-test-c8e14a62` netns、专用Docker/containerd及私有root，默认daemon的24个容器未改动。
因主机GitHub直连失败，使用任务专用SSH转发及仅允许公开构建域名的TLS出站通道；没有修改原代理或SSH配置。
严格workspace preflight返回 `preflight_passed/workspace_mutated:false`。
完整suite已由专用systemd unit启动，stdout/stderr在私有stage/suite.log，当前仍在冷构建，
尚无业务/升级/恢复通过结论。最终结果以 `/data/anas-immich-workspace-c8e14a62.reports/report.json` 为准。

首轮真实主机运行成功构建PG18.4/pgvector0.8.1与0.8.2镜像，但new-apply因私有模块fixture遗漏
共享contracts/目录而失败（contract_root_invalid），尚未启动业务应用。已修复fixture复制与重复
创建的目录一致性校验，并增加回归测试；当前完整workspace family在Linux非root环境34/34通过、零skip。
修正后的脚本已同步到同一私有stage，在新空workspace `/data/anas-immich-workspace-c8e14a63`
重跑，复用专用daemon的已构建镜像。首轮失败证据保留，第二轮结果以该workspace旁reports/report.json为准。

第二轮Contract校验已通过，new-apply停在Samba镜像APT步骤：临时出站代理仅支持CONNECT，
Ubuntu HTTP软件源GET返回501。已仅补齐临时代理对公开ubuntu.com/debian.org软件源的GET，
上游使用TLS，APT签名验证保持启用；隔离netns实际InRelease下载返回200。第三轮使用新空workspace
`/data/anas-immich-workspace-c8e14a64`，继续同一独立daemon，第二轮证据保留。

第三轮完成固定PG、Samba、Authentik和Immich镜像构建，但new-apply在Samba AD健康检查失败，
业务应用尚未启动。只重启本次诊断容器确认 `/etc/localtime` 已指向Etc/UTC，初始化 `cp` 同文件
在set-e下退出；无OOM。已在现有初始化脚本增加同文件检查，并同步中英文技术文档。
同一实际Samba镜像中修复前UTC命令退出1、修复后的初始化片段退出0且timezone内容正确。
诊断容器已移除；host-network服务没有默认Compose网络，额外网络清理返回not found，无遗留网络。
第四轮新空workspace `/data/anas-immich-workspace-c8e14a65` 继续完整验收，前述失败证据保留。

第四轮确认UTC修复生效：AD完成域、identity schema和DNS初始化、无重启；目录structure脚本
因合法缺省的ANAS_IDENTITY_CAPABILITY_GROUPS在set-u下读取而退出，缺少structure就绪标记。
该列表由声明能力码的Module追加，本组合没有这类Module；现有脚本改为缺省空列表，未引入新配置。
同步中英文技术文档并重新生成，bash语法检查通过。第四轮失败与清理现场保留；
第五轮新空workspace `/data/anas-immich-workspace-c8e14a66` 继续完整验收。

第五轮AD、PG、Authentik均启动；中途client blueprint先error，随后原生重试成功，最终apply首因
为未发布的 `anas-mirror-immich-valkey:9.1.1` registry denied。未将诊断重试视为新装通过。
已在suite首次apply前按现有mirrors.json准备固定Immich upstream摘要，仅在专用daemon标记目标名，
不推送，并记录source/target/ImageID。客户端blueprint另补固定内置flow/scope原生metaapplyblueprint依赖；
实际2026.5.6事务中缺失默认logout stage旧blueprint返回False，带依赖返回True，回滚后原stage保留。
这验证依赖处理，不宣称它就是第五轮最终失败原因。相关Hook测试通过；诊断PG及两张标签归属网络已清理。
另修正文档/注释：生成client blueprint包含客户端凭据及签名材料，属于私有运行文件，不能称为无Secret模板。
第六轮新空workspace `/data/anas-immich-workspace-c8e14a67` 继续完整验收。

第六轮已真实通过new-apply、repeat apply（容器与Secret不变）、原生AD/Authentik OIDC首次
roleClaim管理员与普通锚点账号建号。LDAP blueprint中途error后原生重试成功，完整IAM/Immich/Valkey健康。
目录Hook另补同样的固定原生依赖；实际2026.5.6回滚事务中删除默认密码prompt，旧配置失败，
补依赖配置成功，回滚后原prompt保留。探针最初遗漏捕获原生日志清理阶段EntryInvalidError，已修正，
最终标记均通过。此新增directory依赖尚未同步正在执行的第六轮冻结源码，不能把该轮结果称为新目录依赖的完整新装验证。

第六轮在identity-lifecycle发现驱动直接json.loads原生ak shell输出，固定版本无条件打印两行横幅，
Django另打印自动导入提示，导致解析检查等待循环无法完成；同类整数Provider ID和Task ID也受影响。
已使用原生verbosity 0关闭自动导入提示，再严格验证并仅剥离固定两行横幅，其他stdout逐字节保留。
实际固定Auth镜像JSON探针通过；最新36项Linux非root回归通过、零skip。Darwin临时Go源路径下
fixture编译报标准库缺失，不计为通过；完整Linux验证通过。仅对核实cmdline的本次suite主PID用
pidfd发送SIGINT，交由现有异常保存/清理，计划第七轮新空workspace重跑；不操作无关进程。
目录原生缺失prompt探针最终通过：旧失败、新依赖成功、原prompt回滚保留；初期EntryInvalidError
在capture_logs清理阶段重新解析未捕获，已扩大该探针捕获范围，未修改上游或生产日志处理。

第六轮确认已退出，专用daemon无残留容器，报告保留三个真实通过项和identity-lifecycle中断现场。
最新版目录Hook和shell处理已同步到私有源码，开始第七轮新空workspace
`/data/anas-immich-workspace-c8e14a68`；前六轮未计完整e2e通过。最新docs:build、需求覆盖、状态、
索引、Module生成检查通过；本次仍不提交或推送。

第七轮实际运行的五份行为代码SHA256已逐项核对与本机一致，overlay清单保存在
`/private/tmp/anas-immich-finance-c8e14a62/source-overlay-c8e14a68.json`，与原始源码包SHA组合追溯；
CLI/helper行为代码未改变，继续使用同源码Linux二进制。文档修订在本地仓库完成，不把文档差异当作行为测试。

第七轮空库新装已通过new-apply、重复apply保持容器/Secret、原生AD/Authentik OIDC管理员及普通用户建号。
直接读取本次数据库确认客户端与LDAP blueprint均successful，新增directory依赖已在实际新装生效。
Authentik worker冷启动中曾暂态unhealthy，无重启/OOM，随后healthy；原生LDAP分页任务排队后
完成建号前同步。当前继续identity-lifecycle、媒体、升级与一致恢复，尚未宣称完整suite通过。

第七轮最终8项检查通过，包括真实邮箱变更、不同sub邮箱冲突、独立授权并发回调与原生软删除
锚点占位。照片/视频上传、缩略图、原件哈希及数据库检查已执行，但verify-state最后错误读取
AlbumResponseDto.assets，report为failed/reason assets，不能计该聚合项通过。固定3.2.4源码
确认详情仅有元数据/assetCount；已用原生search/metadata filter.albumIds.any查询精确成员并拒绝
剩余分页，增加错误成员/分页回归检查。本机27项驱动测试通过；第八轮
`/data/anas-immich-workspace-c8e14a69`从新空库开始，七轮证据与正常停止现场保留。

第八轮新增通过实际普通角色TCP/权限、PG扩展/preload、照片/视频hash/缩略图/精确相册成员、
带数据重复apply、真实restart及升级前两消费者/全workspace plan。升级apply在维护前
镜像归档失败(snapshot_failed)，ANAS previous_restore成功，数据升级尚未发生；完整suite仍failed。
专用Docker29真实探针复现：替换唯一标签后，旧容器仍在也无法按旧ID save；预先保留本地引用
后save成功，归档RepoTags为空。已在既有build流程前保留workspace/服务专属固定引用，
补定向测试和中英文文档；尚待新源码完整主机验收。所有镜像探针容器/标签/临时tar已按精确归属清理。

镜像保留修复后，目标主机TestSnapshotImagesDockerRoundTrip最终通过(9.30s)：旧实际ID
归档、最后引用删除后的真实缺失、archive加载和恢复精确ID均验证，移动后的目标标签保持原值。
首轮测试在容器/保留引用删除后额外rm旧ID时因Docker已移除该对象失败，明确接受No such image后
完成往返，其他daemon失败仍拒绝。本地Snapshot/Restore/Postgres/RelationalDatabase定向测试通过。
新CLI SHA256为1f81b70718cfd3bab8bcf1ffa400f78cc6d39c4f8f33276584c7d4c829980c93，
helper未变。按已通过项不重复无关验证，使用临时私有接续驱动从c8e14a69旧0.8.1现场
重新启动、完整verify-state后调用同一仓库verify_recovery_lifecycle；该方法由既有run尾部
抽出，默认完整suite行为保持。原第八轮failed报告不覆盖，接续保存至
`/data/anas-immich-workspace-c8e14a69.reports/recovery-continuation/report.json`，显式记录前报告
摘要和两套binary摘要；不将接续称为新空库单轮完整通过。当前接续仍在执行。

接续首轮完成新CLI verify-state后，旧r3重复apply因失败升级已更新r4 config lock而被
version_conflict降级保护拒绝；不是重复apply此前验收回归。已把run中的重复/重启验证与
升级恢复验证分成同一默认顺序的两方法，接续只调用升级恢复。旧r3只读plan同样不匹配当前
lock；r2在进入该已知错误前对精确所属PID发送SIGINT并正常清理，隔离daemon无遗留容器。
r3接续使用当前r4源码做只读plan，再实际升级；没有修改/降级lock或绕过保护。报告目录
`recovery-continuation-r3`保留前两次接续失败证据；此接续尚未通过。

接续r3已通过真实0.8.1→0.8.2升级：Docker事件核对所有旧写入者先停止，恢复点
20261003T064038Z-a38a2af4固定并verify成功，归档旧实际镜像，维护先于消费者启动。
升级后普通角色权限/密码/preload、真实旧HNSW索引查询、照片/视频hash、相册、锚点
和Auth共享库标记正确；前后plan全消费者/全workspace范围正确，直接旧artifact rollback
被postgres_restore_required拒绝。当前进入ANAS全workspace copy备份/受控损坏/恢复，未计恢复通过。

接续r3进一步完成ANAS全workspace copy备份与verify，包含两共享数据库、媒体、配置、Secret、
实际镜像归档；随后测试驱动config set未指定模块目录，module_root_missing，report仍failed。
损坏只涉及本次workspace的媒体、相册及测试数据库/Valkey标记，备份保留。修正使用既有
config set --root/--defer/--update-lock暂存配置，避免此损坏步骤触发无关apply；添加共享
verify_post_restore_lifecycle方法，默认完整suite仍依次执行原有验证。当前接续r4从已校验
备份继续配置/Secret损坏与ANAS整体restore，r3及更早failed报告不覆盖，不计恢复通过。
最新Linux非root workspace family 37/37通过、零skip；不能替代真实恢复结果。

r4在config-mutation收到lock_stale：此前独立PG验收新增旧trust/MD5初态断言时，
测试文件同步改变了远程Module摘要，已冻结部署和config lock未改变。这是正确的摘要屏障，
不是恢复成功。已把远程workspace suite源目录中的该测试文件恢复为已冻结candidate的字节；
新增更严格断言另存私有独立PG验收脚本。本地源码保留该断言，不修改lock或降低校验。
r5从同一verify过的备份接续，r4失败报告保留。另将大归档digest改为1MiB分块读取，
避免把整份tar装入内存；新增禁止read_bytes的回归，Linux family 38/38通过、零skip。

独立原生amd64 PG验收首次临时脚本路径未保留仓库相对布局，前置检查退出；独立fixture补齐
目录后新装场景通过，旧MD5初态断言发现官方initdb管理员保留SCRAM，未伪称迁移通过。
测试显式以旧MD5服务重设隔离postgres管理员密码，再检查三个旧角色确实MD5。首次上传
root所有者目录失败，误启动旧副本后已停止该精确unit，trap清理所有自有容器；正确副本
经用户私有tools上传再sudo复制到独立fixture，r4重新执行。不修改接入现场Module源或锁。
workspace恢复已完成匹配镜像load，当前在既有pre_restore恢复点保存原实际镜像；仍未计恢复通过。

native-pg-r4最终完整通过两组PASS：真实原生linux/amd64新装/重复/重启、普通角色与权限隔离、
readonly inspect、缺扩展/未知来源/preload漂移拒绝、retain；真实旧trust与三角色MD5初态
均断言成立，再迁移全SCRAM并升级真实0.8.1→0.8.2、查询旧HNSW、部分状态重试和另一消费者数据。
新镜像339c4dd98a38d35ef9ad4c9164231c9905c4cbd046287813fb8170f5f3ddd362，
旧fixture cf0a375028906623b41276dc65ae5b7a190585e197e687c535c0f6da259c1ce8。
r5 backup restore实际返回ok，restored包含config/lock/secrets/local_admins/active_deployment/
data/userdata/state，verify ok/checked7/problems空；恢复后实际服务验证仍在运行。

r5已通过backup restore后的完整verify_state及精确实际镜像核对：普通TCP权限/SCRAM/
扩展/preload/旧HNSW、两用户anchor/internal id、PNG/H.264原件hash、缩略图、精确相册成员、
Auth共享数据库标记与Valkey标记正确，config/Secret字节hash恢复。随后已进入原维护前旧
snapshot restore，说明上述业务断言均成功；维护失败/中断、目录撤权尚未完成。

新增复用现有Playwright/Chrome和脱敏reporter的immich-browser入口，只允许本次隔离
photos.iw<hex>.immich.test；浏览器私有resolver配合同端口SSH转发，不修改host DNS/用户profile。
覆盖原生OIDC/引导、UI PNG/H.264上传、browser fetch原件hash/thumbnail、UI相册创建改名、
原生logout/旧token401/下次IAM重新认证。Chrome resolver不作用于Node request，验证使用
browser fetch；无截图/trace/video或新增依赖。语法检查通过，实际执行在服务器suite结束后，
不冒充移动客户端。旧point snapshot restore和启动已返回ok，旧业务再次核对后继续维护故障。

r5维护完成后Hook故障真实触发start_failed，所有消费者保持停止；恢复点
20261003T082431Z-a177ce29完整并pin/verify通过。下一步start实际被拒绝，但公共lifecycle
把postgres_recovery_required包装为start_failed，因此r5严格断言failed，未放宽测试。
本地四个start/restart×maintenance/restore原生入口负例均复现；修正在既有runtime锁内、
Compose探测与restart stop前检查持久guard并保留前置码。四例和相关定向测试通过，
Snapshot/Restore/Postgres/lifecycle定向26.597s通过、vet通过；Linux harness38/38零skip。
新同源码Linux CLI SHA256 4fb1998227fa27b1c46a1ff296b94414faaecd5a91e66a5f8821b35aeb0702fd，
helper未变；公共行为与双语PG技术文档同步，生成器通过。r6从未修改的故障现场
核对candidate与恢复集合，继续同一verify_failed_maintenance_recovery，未先启动旧artifact。
r5失败报告不覆盖；r6尚未通过。

本地浏览器夹具导出被自动审批拒绝：未明确授权远程私有测试密码/合成媒体跨主机传输，
命令未执行。改在远程专用daemon内执行浏览器，凭据和媒体留在原测试主机；不绕过拒绝的导出。
原生host无Node/Chrome，准备官方mcr Playwright1.62.1-noble（与现有依赖一致）的临时测试镜像。
仅上传已授权测试源码和本机现有三份公开Playwright npm包4.1MB，SHA256
38b7177d940c981ab317d6fa2a948cbabaa6172dff4be0dd5f87411aa4cf67f6；
公网软件代理增加仅mcr.microsoft.com及子域，仍拒绝私有地址。旧proxy未重启首pull403，
正确重启后r2在下载；尚未运行浏览器或宣称通过，无全局工具安装。

r6真实start/restart/different apply均postgres_recovery_required；rollback当前active返回
already_active，是正确的无操作拒绝，却没有覆盖非active旧artifact回退。r6仍failed并保存。
不为测试预期改生产rollback；两处故障/中断驱动改为选择真实18.4/r3、state previous且
非active/nonpending历史制品，缺少合格目标则失败。单测拒绝active/pending/r4/failed候选，
Linux完整39/39零skip通过。r7接续同一未改的维护故障状态，CLI仍4fb199...；尚未通过。
浏览器运行目录已在原主机私有tools准备，只包含公开工具/test源码、精选测试登录字段和
本次合成媒体变体；无fixture导出。本地未使用的29443 SSH forward已按精确PID/cmdline关闭。
实际app origin为photos.iw7e23583e.immich.test:29443，网页测试guard/双语说明已按当前文件
修正photos前缀；新增同主机原生浏览器路径，官方Playwright image仍下载中。

r7真实start/restart/different apply以及非active旧artifact rollback四项全部返回
postgres_recovery_required，随后故障对应旧point restore实际ok、旧start返回ok。
旧业务再次核对与新版备份恢复还在执行，未计失败恢复整段完成。39项Linux回归零skip。
网页改名等待真实PATCH成功后reload，避免刷新中断请求；仅语法检查，不计浏览器验收。

r7已真实PASS维护后故障→四项阻断→matching old snapshot0.8.1恢复：所有旧媒体、
HNSW、anchor/internal id、Auth共享库标记与精确旧实际镜像通过，恢复后正常start成功。
正恢复新版whole-workspace backup0.8.2，随后进入SIGKILL/同冻结candidate retry，未计中断通过。

r7故障恢复两方向均实际PASS：matching old snapshot18.4/0.8.1和upgraded whole-workspace
backup18.4/0.8.2，媒体/hash/album/anchor/internal id/旧HNSW/Auth共享库及精确images通过。
当前进入extension_crash_retry，不计SIGKILL/冻结候选重试或目录/浏览器完成。


r7维护中断驱动在真实post-maintenance暂停点访问冻结hook/main.go失败；冻结制品按既有freezeHookBinary只保存.hook.bin，并非生产维护失败。finally按CLI会话归属终止自有进程，报告failed且容器清理成功。修正为封存二进制与本次编译缓存摘要相等、/proc实际exe/cwd/父子会话验证及重试后相同二进制/manifest，保留私有fixture源码摘要。r8接续因已停止现场runtime()断言“无运行容器”提前failed；仅匹配旧point恢复允许显式空初态，普通业务/启动核对仍严格要求运行容器。新增回归，Linux非root 40/40零skip通过。r9重新执行完整中断场景，旧失败报告保留，尚未计通过。

官方MCR浏览器镜像两大层在公开网络下载很慢，另准备同Playwright1.62.1对应的Chromium headless shell151.0.7922.34/linux64。公开运行时tar SHA256 0cb1bb52c156cc2a40cb5ef0a5852ceb64d0af80f9626545f6e8a5be5deaa6ae，106MiB，仅公开二进制与依赖，无凭据/媒体。上传授权主机后将使用专用容器；不安装宿主全局工具，不替代移动端。


r9匹配old snapshot restore CLI实际ok，但停止事件验证被并行临时browser-dependency-probe的start触发。事件明确name为自有探测容器、working_dir缺失；基础Compose构建镜像携带project标签，不是恢复启动了业务Consumer。探测无挂载/凭据，已自动删除；未为此修改生产恢复或放宽默认事件检查。r10从r9已校验匹配恢复的停止现场继续，复用拆出的verify_extension_crash_retry；默认suite仍先真实restore再调用同方法。临时r10脚本首次缩进错误在解析时退出，无动作，修正并编译检查后启动；日志保留。Linux40/40零skip再次通过。后续主机业务验收期间不并行启动辅助探测容器。


r10已核对真实post-maintenance暂停时vector0.8.2，运行模块仅lego/postgres/samba_dc/traefik，无Immich/Authentik消费者。/proc证明实际封存.hook.bin、父子会话与工作目录，CLI真实退出-9（SIGKILL）。原恢复点20261003T103144Z-1c8d076e已pin/verify，start/restart/different apply/nonactive旧rollback四项均postgres_recovery_required。现在执行原冻结candidate精确retry，未提前计重试成功。公开Chrome运行时hash一致；首解包被单个macOS ._browser-public-linux元数据条目拒绝，校验确切条目并排除后解包通过，未取消路径检查；临时browser依赖镜像已进入导出。


r10完成真实SIGKILL→原冻结candidate retry、原point/镜像/二进制摘要不变与新whole-workspace backup恢复后全业务核对。随后真实LDAP停用已同步is_active=false，但HTTP旧凭据未全失效，Task queued/retries5；该轮因directory-revocation readiness timeout failed，清理成功。按backchannel对应日志分类（非其他公共update后台ProxyError）确认requests SSLError/CERTIFICATE_VERIFY_FAILED、unable to get local issuer certificate，目标本次photos入口。Worker已有受管trust bundle，仅导入LDAPS CertificateKeyPair，HTTP requests未使用。最小修正worker-entrypoint在现有只读/tmp副本后export REQUESTS_CA_BUNDLE，保持TLS peer verification；双语Authentik技术输入/生成文件同步，shell语法通过，无新依赖/协议/状态机。

r11通过现有ANAS apply --build --update-lock构建当前未发布r15组合，未修改运行容器补丁或关闭TLS。native managed/default HTTP positive + public-only CA negative、原生五种目录变化和当前镜像组合全workspace恢复接续中。默认suite的全备份/受控损坏/恢复提取为verify_whole_workspace_restore同一方法，原执行顺序不变；Linux40/40零skip通过，旧报告不覆盖。当前Module仍developing。


r11诊断探测在docker exec中读取entrypoint导出的环境而失败；exec新进程不继承运行worker环境，报告failed保留。r12读取实际worker PID的/proc环境并显式传递同受管CA给原生get_http_session探测，正例200、仅公共CA负例SSLError均通过，未关闭验证。真实目录五场景与当前镜像全workspace恢复仍执行中。公开浏览器运行时依赖镜像已构建为89670d8ba913b114bfdc08df3029ad0209cb358d9b9a7e151aa17454fa0b5e6e；尚未运行浏览器。最新文档构建通过。


r12原生受管CA正反例及LDAP disable的旧Bearer/Cookie/API key/share失效、无关用户保护通过。随后新场景登录因原生OIDC拒绝回调无code触发驱动assert，整体failed/清理成功，未宣称五种撤权均通过。对固定上游authorize的拒绝响应补齐严格access_denied处理：必须本次app /auth/login、同state、单一error且无code/fragment；错state/重复字段/异域/http/其他path/server_error等反例拒绝。Linux非root41/41零skip通过，r13重跑原生五场景及当前镜像整体恢复；未改生产身份或放宽既有凭据失效断言。浏览器源码补齐RP和直接IAM退出两个方向，语法通过但尚未实测。


r13再次通过CA正反例及LDAP disable撤权，但随后相同无code/state断言失败；严格access_denied分支没有覆盖实际返回路径，不能据推测计目录验收完成。增加仅callback固定路径/匹配origin与state布尔值/有无code/有限error类别的诊断，不输出URL或grant。从已清理且当前CA/基础业务验证的停止现场启动独立浏览器验收，保留其自己的前置检查和报告；不依赖目录整轮passed，也不将此条件改动视作目录通过。诊断复用同主机已有业务容器，不创建并行辅助Compose标签容器；合成用户名/密码/媒体仍留服务器。当前镜像组合再次整workspace恢复尚未执行。


原生脱敏诊断确认active=true/admitted=false时返回IAM origin的授权入口、同state、无code/error；固定Authentik PolicyAccessView/AccessDeniedResponse使用HTTP200的policies/denied.html。驱动补齐严格同origin/path/state/client_id、无code/fragment、英文Permission denied标题/Request has been denied/ak-back-home标记；普通HTML/异域/错state或client/带code等反例拒绝，Linux42/42零skip通过，原生小范围复核中。浏览器首轮admin在ANAS按钮locator超时，整体failed；公开页三次探测确认button存在、disabled=false、accessibility name ANAS，另有内部CA ServiceWorker SSL错误（普通页面ignoreHTTPSErrors不能替代系统CA）。尚未定位具体超时步骤，增加安全PHASE标记与90秒初始控件等待；r2用同主机新合成PNG/合法MP4 free box变体执行，旧报告不覆盖，不计浏览器通过。


浏览器r3的安全阶段证明admin已完成OIDC、上传/原件hash/缩略图、UI相册和两个方向HTTP旧session失效，最后等待ANAS按钮与原生SessionDelete后的自动IAM导航竞争而failed。按固定源码支持的autoLaunch=1发起下一次原生OIDC，仍保留旧Bearer/Cookie401和IAM uidField必须可见断言；首登录仍实际点击ANAS。r4两角色全通过：admin111124ms、user108090ms，Playwright1.62.1/Chromium151.0.7922.34/Linuxamd64，依赖镜像89670d8...，合成PNG/H.264原件hash/缩略图、UI创建改名相册、RP logout和直接default-invalidation-flow的IAM logout、重新认证均通过。r1/r2/r3失败报告保留，未覆盖，浏览器不代替手机；内部CA的ServiceWorker注册在ignoreHTTPSErrors环境报SSL错误，不宣称PWA安装/ServiceWorker离线功能。原生r14策略拒绝小范围四项通过（active无准入403、停用401、均无应用token），完整r14五场景及当前镜像一致恢复已从当前健康、仅本任务业务容器现场串行接续，未并行启动浏览器。


独立entry-restrictions-r14实际主机通过：使用本次真实OIDC管理员/普通角色，对admin-sign-up/admin-users/local-password/change-password/link/unlink/admin-global-unlink等8条固定原生HTTP入口请求，返回[400,403,403,403,403,403,403,403]；不是404路由不存在。请求前后主用户id/oauthId/isAdmin不变，sentinel本地账号不存在、全库unbound=0、主用户无非空password。验证方法加入默认workspace suite复用，未新增工具依赖，未输出响应body/凭据。完整r14目录disable、remove-app-group的旧Bearer/Cookie/key/share失效及无关用户保护已通过；后续准入恢复/原事件重试、delete/logout-first/admin-role-loss及整workspace恢复仍运行中。


r14原生目录disable、remove-app-group、delete，以及恢复准入后旧事件重试不撤销新凭据均通过；随后logout-first在解析原生IAM响应时出现TypeError（NoneType），整轮failed，finally清理容器成功。尚未证实为产品缺陷，不计全部目录验收通过。增加有限文件名/函数/行号和HTTP状态/body类型诊断，不记录源码行、局部变量、URL或凭据。r15保留旧报告摘要及已通过三场景证据，先验证当前CA修正镜像的全workspace备份/受控损坏/一致恢复，再补跑logout-first、admin-role-loss；默认仓库suite仍执行全部五场景，接续报告明确标记合并证据，不宣称单轮新装全suite通过。


r15当前CA镜像backup-create/backup-verify通过；post-backup-mutation已修改seed媒体、Authentik标记和Valkey值，随后原生/api/oauth/authorize未返回200/201，整轮failed，cleanup全部停止成功，尚未进入logout-first诊断。有限trace定位verify_whole_workspace_restore→login，不能据此认定IAM登出问题同因。r16直接复用该已验证备份，进一步真实修改config/Secret后通过ANAS restore恢复，核对媒体/hash、两共享数据库、Valkey、配置/Secret原字节和精确image；再补验剩余目录场景。新增授权入口有限HTTP状态/body类型记录，无私有HTTP内容导出。


r16当前匹配ANAS backup restore命令及config/Secret原字节核对已通过，正恢复启动并检查业务。实际Immich进程启动后仍health starting，harness现将备份后的既有verify_state置于任何受控损坏之前，等待实际HTTP/原生登录及业务状态，避免把进程已运行等同于应用就绪；持续失败仍由原900秒等待/原断言报告。最新Linux42/42零skip通过；r15授权非成功响应尚无原HTTP状态，未凭推测宣称生产问题已修复。


r16恢复启动Immich从starting到unhealthy并自动重启一次；有限日志诊断定位postgres客户端connection.js启动连接超时，非已证实认证错误。主机3306MiB物理内存、swap已用3296MiB，memory PSI some/full约60%/34%、IO约86%/35%，记录压力但不据此认定因果。首探测require路径错误（未访问DB），修正固定路径后Immich容器实际应用凭据连接1666ms，matching_role=true、superuser=false、vector0.8.2；随后healthy/restart_count=1。未改上游超时或加入内存档位，最终业务/目录验收仍进行。


r16最终failed于directory-revocation：native authorize201/dict、RP logout200/dict，原生IAM executor302/None且同IAMorigin；TypeError精确指向读取flow[component]。此前当前CA组合完整restore业务/原件/hash/两个消费者/Valkey/config/Secret/精确images均通过，cleanup全停止成功。harness处理固定原生SessionDelete的空302终结响应，仍验证原生IAM cookie在logout前200、之后401/403，AccessToken消失，旧Bearer/Cookie401且独立key/share200；不把302本身作为退出成功。r17复用r16恢复证据，仅补logout-first和管理员降权；默认完整suite五场景不变。


r17整体passed并cleanup全停止：logout-first验证IAM cookie退出前200/后401或403、AccessToken消失、旧应用Bearer/Cookie401且独立key/share200，再移出APP组后旧key/share也失效；admin-role-loss保留APP准入、旧四类凭据失效、新原生OIDC同内部id且isAdmin=false、新普通凭据有效、旧管理员凭据不恢复。合并r14已通过三场景后原生五类功能都通过，所有失败分轮报告保留，不声明单轮全新suite通过。尚需自动journal→subscriber→native sync路径及传播时限实测；r18串行进行，仅准备账号时手工同步，测试成员移除后严禁手工同步，以300秒作为该主机验收上限，核对匹配新事件seq、持久cursor/trigger、原生DONE任务、已验签接收截止和真实HTTP旧凭据失效；无关用户与媒体/绑定保留。该测试尚未计通过。


r18整体passed/cleanup全停止：成员移除后未手工sync，71.206秒内旧Bearer/Cookie/API key/share均401；匹配新Modify/member事件seq99、subscriber持久cursor99、last_trigger_at1791041434，LDAP DONE1、signed delivery DONE8，receiver该anchor已验签截止配对验证，媒体/hash/绑定与无关用户凭据保留。300秒为本次健康固定组合的真实主机验收上限，非4GB或普遍容量/故障时延保证；原生Task重试耗尽仍须现有任务入口处理，不虚构无限自动投递。手机上传/后台备份与移动会话、PWA内部CA夹具限制仍未完成，Module developing、M2/M3实施中。默认完整suite包含事件验收，既有分轮failed报告保留，不宣称一次新装全suitepassed。服务器自有临时Docker/containerd/namespace/proxy已停止、socket及namespace不存在；本地公开代理与SSH转发已关闭，工作区/匹配备份/images/私有报告保留。默认Docker运行容器仍24个，ip_forward=1与初态一致，仅做读取检查。


收尾：最新Linux驱动42/42零skip，Module/Contract生成检查、646项需求覆盖、两索引状态、94份文档状态及文档构建通过。无提交/推送；初始工作区差异保留。此前私有凭据/媒体向本地导出被自动审批拒绝，未执行或重试；全部浏览器与私有fixture在同一远程主机完成，仅上传公开代码/运行时。

### 2026-10-08 Casdoor 服务器与网页执行基线（9fc699c）


基线：2026-10-08，合入 `9fc699c` 后保留的工作树。固定 PostgreSQL
18.4.0-r4 / pgvector 0.8.2、Immich 3.2.4-r1、Casdoor 3.143.0-r11。
vector 0.8.1 旧夹具只用于真实维护验收，不是发布组合。

## 授权、隔离与原服务恢复

用户明确授权：“finance 可以暂时暂停任务，测试完恢复，继续测试吧”。
暂停范围是主 Docker daemon 的 `anas_finance_`，以及独立长期测试 daemon
的 `anas_cd261003_` 与 `anas_llng_anchor_llng`。只停止原运行的 28 个
ANAS 容器，保留 SSH、网络代理和其他非 ANAS 服务。未执行生产 apply、
Secret 变更或生产媒体操作；生产仍是 Casdoor r9、Nextcloud r10、Samba r12。

原容器 ID、名称、实际镜像 ID、原健康状态、socket 在远端 root 私有清单
保存。临时脚本 finally 按依赖恢复原容器并检查健康，正常退出、失败与
正常信号中断均进入恢复；`--restore-only` 使用同一清单。ID/镜像改变时
拒绝盲目启动。这是本轮操作脚本，不是新维护服务、状态机或调度器。

服务器窗口已完成：test_exit_code=0，restored=true，restore_errors=[]。
原 28 个容器全部恢复，ID/镜像未变，原健康服务全部健康。Nextcloud 启动
阶段正常执行目录配置并恢复健康；未以 running 代替 healthy。
服务器维护清单：
`/home/whl/anas-immich-e2e-c8e14a62/maintenance-20261008-b12/restore-state.json`。

## 全新服务器套件结果

新工作区 `/data/anas-immich-workspace-c8e14b12`，独立 Docker/netns 与真实
Linux/Btrfs。`server-immich-casdoor-workspace-e2e.sh` 单轮从空工作区运行，
status=passed、退出 0、17 组检查均通过；不是 b11 接续证据。
私有报告：`/data/anas-immich-workspace-c8e14b12.reports/report.json`。
清理确认 containers_stopped=true、工作区保留、未删除历史报告与备份。

- 新装首次 apply 成功，部署 `20261008T050932Z-823d1049`；工作区 CA
  校验的实际 Immich ping 为 200。
- 空库首普通用户拒绝、原生 Casdoor 首管理员与普通 JIT、
  anchor=sub=oauthId 通过。普通用户修改目录组、管理员标记和 anchor
  被原生服务拒绝。Immich 本地建号、密码、link/unlink 等八个入口拒绝
  变更，未产生空绑定或改变既有绑定。
- 邮箱变更保留原 user.id；不同身份不能占用绑定邮箱。四个独立授权
  在回调前同步等待，均 201，只有一个有效账号。软删除后新邮箱回调
  拒绝，tombstone 保留，旧 bearer 失效。
- 实际普通应用角色 TCP、扩展版本/preload、HNSW、PNG/H.264 上传、
  原件 hash、缩略图、相册、共享 Casdoor 数据与 Valkey 状态通过。
  repeat apply 和 restart 保留这些状态。
- 两轮五项自动目录撤权通过，无手工 sync 或构造策略 token。

| 场景 | 恢复前秒数 | 最终恢复后秒数 |
| --- | --- | --- |
| disable | 60.401 | 64.350 |
| remove-app-group | 63.313 | 66.825 |
| delete | 63.822 | 67.395 |
| logout-first | 53.993 | 59.172 |
| admin-role-loss | 67.525 | 69.028 |

每项旧 bearer/cookie/API key/share 均为 401，绑定和媒体保留，无关用户
可用；管理员降权重新登录为原 user.id 的普通用户。登出先只撤会话，
后续目录撤权仍撤 API key/分享。时限只证明本次健康固定组合符合 300 秒
验收窗口，不是容量或故障传播保证。

## 维护、失败、中断与完整恢复

- 实际 vector 0.8.1→0.8.2 受控升级通过。Docker 事件确认全工作区停写；
  特权维护先于应用启动，普通角色、HNSW、媒体和共享数据库保留。
  前后只读 plan 均检查 Casdoor/Immich 两消费者和全工作区范围，且未
  改动受管状态。直接 rollback 被 postgres_restore_required 拒绝。
- 旧实际镜像恢复点 `20261008T054321Z-e1207b69` pin/verify 通过。
  ANAS 一致备份 `20261008T060001Z-04014337` create/verify 通过，
  checked=1、problems=[]，实际镜像归档 SHA 与服务镜像 ID 核对通过。
- 仅在本轮工作区修改媒体、相册、共享数据库、Valkey、配置和 Secret，
  再用 ANAS backup restore/start。两消费者、身份、原件 hash、相册、
  配置/Secret 摘要及全部匹配镜像恢复一致。
- 真实维护成功后测试 Hook 故意失败，start_failed。active 保留原部署，
  维护目标持久化，消费者停止。恢复点 `20261008T062920Z-ccab1966`
  pin/verify 通过；start/restart/different apply/old rollback 四项均被
  postgres_recovery_required 拒绝。匹配旧 snapshot 恢复 18.4/0.8.1，
  较新全工作区备份恢复 18.4/0.8.2，两条业务与镜像恢复路径通过。
- 精确 SIGKILL：CLI exit=-9，暂停时实际 vector=0.8.2，应用消费者
  尚未启动。候选 `20261008T071949Z-b4835d30` 持久化，四项恢复屏障
  有效；显式同候选重试保留原 manifest/Hook/恢复点摘要，active 正确
  切换、屏障清除，业务检查和正常 start 通过。之后匹配新备份恢复、
  全部业务/镜像与恢复后的五项自动撤权再验证通过。

## 门禁与后续边界

预检查首次缺代理环境、第二次完整 GET 首页超时，均保留失败；改为 HEAD
只避免下载首页，固定源码摘要和真实隔离门禁不变。修正后
preflight_passed、workspace_mutated=false。

本机 43 项：41 通过、2 项平台条件跳过（0.917s）。Linux 同步新增回归
后 43 项运行，42 通过，Go 不在 PATH 的一项跳过；用同源工具链单项补跑
通过（6.047s），因此 43 项均有 Linux 通过证据。真实主机验收另见上文。
计划索引重新生成，docs:check-plan-status 和 diff 检查通过。

Casdoor 浏览器登录、上传、双向登出仍待验收；恢复管理员 OIDC
准入负例第二维护窗口已通过，见下文。生产已有精确版本源码已找到并只读核对，生产 Immich
部署仍未执行。真实手机条件缺失，未把服务器/API/桌面浏览器等同于移动
上传和后台备份；Module developing、M2/M3 实施中，不宣称 release 或
4GB 支持。其他仓库基线失败见[master 整合记录](../../../../dev-docs/reviews/2026-10-07-immich-master-integration.md)。

## 浏览器首轮与接续

- 首轮管理员浏览器在登录页未找到 ANAS 按钮，91.137s 失败，尚未进入
  上传。另一个恢复账号准入负例因服务器 Python 没有 crypt 模块，未
  执行到账号认证；不是准入拒绝证据。两个失败分别保留，不标记通过。
- 此窗口自动清理隔离服务并恢复原 28 个容器，restored=true、errors=[]。
- 用仓库已有 bcrypt 依赖与同源 Go 缓存编译只读校验器，替代缺失的
  crypt；没有安装新依赖或修改账号密码。第二轮先采集匿名页面及公开
 配置，在继续浏览器动作前核对实际界面。匿名页面返回代理 404；
  Docker host 网络未进入独立网络命名空间，浏览器回环地址连接了另一入口。
  增加受限 ANAS_TEST_HOST_ADDRESS，指定隔离地址 10.253.17.2 后真实浏览器
  已到达 Casdoor。
- 第二轮恢复账号现有密码只读 bcrypt 验证成功，确实执行原生认证；
  Immich 回调 500，无 token、账号数量不变。该负例通过；未据此新增身份补丁。
- 第二轮管理员浏览器等待 Casdoor #normal_login_username 失败（37.558s），
  尚未开始上传。固定源码确有对应 normal_login 表单，继续采集原生匿名页面
  和前端错误，不以修改选择器掩盖失败。此窗口原 28 个容器均按原 ID/镜像恢复健康，restored=true、errors=[]。
  第三轮采集 Casdoor 原生授权页面继续排查。

- 第三轮匿名诊断在等待 ANAS 按钮时超时，并因脚本未在超时后保存页面而退出；
  不能据此归因 Casdoor 产品行为，也未产生上传验收结果。修正诊断：记录入口
  响应、等待 200、即使按钮缺失也保存匿名 DOM/前端错误/公开登录配置。
  第三轮原 28 个容器按原 ID/镜像恢复健康，restored=true、errors=[]。
  第四轮诊断已启动，结果待补充。

- 第四轮匿名 DOM 证实 Casdoor 表单正常，用户名实际为 `form#normal_login input#input`，
  密码为 `normal_login_password`；先前用户名选择器错误。入口首次 21 次 404，
  随后 200（约 42 秒的路由就绪等待）；修正测试地址、等待及选择器，不改产品认证。
  仍有测试 CA 的 ServiceWorker SSL 错误，不声明 PWA 验收通过。
- 第四轮恢复账号原生负例再次通过；管理员浏览器 OIDC 成功进入 onboarding，
  但原生弹窗遮挡引导按钮，41.468s 失败，未上传媒体。第五轮将诊断实际弹窗，
  用正常界面操作处理，不强制点击。第四轮原 28 个容器按原 ID/镜像恢复健康，restored=true、errors=[]。第五轮已启动。

## 生产部署预检查

公开源码候选 `production-candidate-20261008-source` 已在 finance 准备：
Casdoor r11、PostgreSQL r4、Immich r1，保留生产 Nextcloud r10 和 Samba r12。
生产工作区/配置/Secret/数据均未修改，尚未 apply。

只读 plan 首次 `config_invalid`：旧本地 9fc699c 基线尚未支持 global.temp_path（最新 master aa1944a 已纳入；以下预览删除字段方案取消）。
私有预览副本只移除该字段，继续 plan 得到 `config_not_managed`：外部配置必须
经原生 config import 后才能用于工作区 plan。因此未声称生产计划通过；后续先用
现有 ANAS 机制取得原配置一致备份，再进行原生配置导入及正式 plan。没有增加
旧接口兼容层或绕过配置管理。生产现用二进制已定位，仅核对了 backup 参数。

## 第五轮网页验收通过

实际弹窗为 NEW VERSION AVAILABLE（v3.2.4 / 上游提示 v3.3.0），通过原生
Acknowledge 按钮关闭；没有升级固定镜像。管理员 38.385s、普通用户 36.804s，
两份真实浏览器报告 passed。包含 OIDC、引导、原件 PNG/H.264 上传/摘要、缩略图、
相册创建改名、RP/IAM 双向退出、旧会话 401 和再次原生 IAM 认证。
恢复管理员原生负例再次通过，browser_exit=0、probe_exit=0。窗口清理后原 28 个
容器按原 ID/镜像恢复健康，restored=true、errors=[]。手机/PWA/容量边界不变。

生产配置尚未修改时，利用本轮已停写窗口，用原 ANAS 创建 copy 备份
`20261008T101048Z-d4e50e36`，snapshot `20261008T101047Z-673b5d89`，
1,364,815,875 bytes；verify checked=1、problems=[]。目标根权限 0700。
原 CLI 对 --no-stop 标记 crash_consistent_only（另有明文 Secret 告警）；
本次不是在线复制：命令启动前逐一断言原 28 个容器停止且镜像未变，恢复仅在
套件结束后发生。保留 CLI 告警及维护停写证据，不以完整性 verify 声称恢复通过。
后续生产操作仍需新代码的匹配镜像恢复点和部署后验证。

### 2026-10-08 最新 master 再合并（aa1944a）

- 再 fetch 确认远端新增 8 个提交；从 9fc699c 快进到 aa1944a。两份 stash 均保留，全部既有改动恢复；12 个实际冲突已整合，正在编译及回归验证，无任务提交或推送。
- 早期旧本地 CLI 对 global.temp_path / temporary_directories 报错，是旧基线尚不支持，并非最新 master 已移除。最新 master 已纳入 finance 的临时目录能力；取消删除字段的预览，保留生产原配置及 Collabora r6 挂载功能。失败的 import 没有改动生产配置。
- 9fc699c 基线的 17 组服务器、两角色网页及五轮原 28 容器恢复通过保持历史有效；aa1944a 加工作树的编译、相关回归、重新构建与实机复验尚待完成，不能引用旧 CLI 测试代替。
- 生产尚未 apply。先重建当前 CLI/helper 和正确公开候选（保留 Nextcloud r10、Samba r12、Collabora r6），核对正式 plan、匹配镜像恢复点与部署；手机条件仍缺失，M2/M3 和 developing 不变。

### aa1944a 本轮验证进展

- 本地 Runner 的 PostgreSQL/Resource、备份/恢复/Snapshot 定向回归通过（37.453s）。PG、Immich、Casdoor Hook 分别通过；Casdoor helper 为独立 Go module，须在其目录单独运行，不把错误包路径的失败算作通过。
- gen-module-docs / gen-contract-docs 生成及检查、需求覆盖 718 项、56 个有效测试用例、需求/计划索引与 105 份文档状态检查通过。双语文档站点构建通过；修复架构页指向非站点计划的失效链接。
- Linux amd64 CLI/helper 已交叉构建；SHA256 分别为 b95e290740d4a5a87b2a421ded0336460b73952f3c74c8eff627a8c7708cba2e / 99259321f5e338c7c9f722092f1dc5b60b2d578bbfa599d7419ca916b47659c7。尚未生产部署。
- finance 公开源码包完整 SHA256 b169680229deb110ff6ab633e52cb443831e875912a5446eb43a8eb2bbe76550 已核对；新建目录解包完整。Linux 回归暂受公开依赖网络超时阻碍，正在传输 go.sum 校验的公开离线缓存。原生产服务未暂停或修改。

- Casdoor 独立 helper 回归通过（0.919s）；本机 harness 43 项运行、42 项通过、1 项平台跳过。docs:test-requirements 与 docs:test-status 均通过。Linux 实机与移动端结论不由这些测试替代。

- finance 原生构建 CLI/helper SHA256：ce20fc8a56cce59ca689e0e4d30953dc6804f567aa67e7c07f3e5fba87ece142 / 16ab41fed74de77aada10e3855d587debb1560850d4aceaa4255c93a916611ed（构建路径不同，不能以本机交叉构建哈希要求逐字一致）。
- Linux 首轮完整 Runner 未通过：默认 umask 和非 Btrfs /tmp 导致临时目录安全/拓扑判据失败，受限上传缺少两个公开 fixture；失败日志保留 latest-master-runner-offline-20261008.log。补齐 fixture、使用专属 Btrfs 临时目录和 077 后重新验证，未放宽产品权限检查。
- 最新生产公开候选保留 Nextcloud 34.0.2-r10、Samba 4.23.6-r12、Collabora 26.4.2-r6；仅本次 PG18.4-r4/Casdoor3.143.0-r11/Immich3.2.4-r1 接入。尚未 apply，先核对原配置的只读 plan。

### 最新 CLI 的 b13 实机复验启动

Linux/Btrfs、umask 022 下 PostgreSQL/Resource、Backup/Restore/Snapshot 与 Temporary 相关回归通过（13.803s），证据 latest-master-runner-relevant-20261008.log。完整 Runner 套件仍有受限源码包缺模块运行文件/fixture、Git 元数据及 umask 077 与测试期望冲突等失败，未声明完整门禁通过。

使用 finance 原生构建的新 CLI/helper，启动 maintenance-aa1944a-b13.py；新空 workspace /data/anas-immich-workspace-c8e14b13，恢复清单 maintenance-aa1944a-20261008-b13/restore-state.json、套件日志同目录/server-suite.log。测试前保存限定原容器 ID/镜像/健康状态，再停写；finally 逐组恢复。新套件结果仍待记录。

生产只读 plan 已通过配置/目录加载，当前因 PostgreSQL 新 bundle 与现有锁摘要不同而 lock_stale；尚未更新生产锁或配置、尚未 apply。Lego 候选已校正为线上 5.3.1-r6。

- 固定 Immich Node 补丁测试 19/19 通过，Authentik 固定源码测试 35/35 通过，go vet 相关 Runner/PG/Immich/Casdoor Hook 通过。Casdoor 升级目录校正 current r11，r8→r11/r10→r11 路径门禁通过（全目录 24 条）；这仅证明目录有效，真实旧版升级仍须主机验证。

- b13 暂停前清单 28 个原容器，已全部停止；当前新 init 返回 ok:true，首次 apply 已启动 Samba/PG/Casdoor/目录事件/Valkey 并达到 healthy，Immich 后续阶段待验收。此为进行中进度，不计为套件通过；原容器恢复仍须最终核对。

- 新 CLI b13 公开检查已通过原生首管理员/普通 JIT、普通资料角色/anchor 写保护、本地/密码/link/unlink 入口限制、软删除及邮箱/并发绑定场景，以及真实上传后的重复 apply。重启、撤权、升级与恢复仍在执行。
- 已生成固定公开镜像集合 production-fixed-images-aa1944a-20261008.tar，1,214,940,672 bytes，SHA256 8086eec867d219be21805a3311d4ed67ba3a8a22448e19aec8767d62e8b5c008，root 私有归档；生产 Docker29.7.2 的五个候选标签此前均不存在。只加载镜像以准备部署，不以此宣称已 apply。

- b13 真实重启及五项自动 Samba/Casdoor 撤权通过；升级前只读 plan 正确列出 Casdoor/Immich 与完整停启范围。扩展维护与恢复继续执行。
- 新版生产备份首次 dest_not_exist，补建 root 0700 目的地；第二次拒绝 --no-stop（postgres_backup_quiesce_required），未创建备份。改用原生停写流程重试，未绕过门禁。此前原 CLI 的外部停写 copy 备份保持历史记录，不用它替代新版匹配镜像恢复材料。

- b13 最新 CLI 真实 0.8.1→0.8.2 受控维护通过；升级前后只读 plan 都列全消费者/停启范围并保持状态不变。普通角色 HNSW、媒体与另一共享 Consumer 保留。全 workspace 备份/恢复及故障注入仍在执行。
- 生产新原生停写备份被 image_capture_failed 阻止：冻结制品中未运行的 Nextcloud Talk 与 PG Adminer 镜像缺失（18 个声明服务核对）。Adminer 从已有隔离缓存补齐；Talk 固定镜像待取得，不降低恢复材料检查。备份失败未修改生产配置或业务数据。

- b13 ANAS 一致备份已通过，包含数据库/媒体/配置/Secret/实际镜像归档；全工作区真实恢复继续核对业务。
- 本任务既有公开中继的反向 SSH 已断开（远端回环端口消失、本地代理健康）；恢复相同 21083→21084 转发，未改生产代理。Talk 固定上游 digest 2b9a7d12d3e644b90c1fa6c7e73da0aa083719d64692bf40a62baaad3c2aebd1 已下载并映射到冻结标签；Adminer c1ff0afec425378c4f8df82f816dec683c2fe2cc04284c122a5a892ee589012d 从已有隔离缓存取得，均只加载生产镜像缓存，未启用服务。原生生产备份 r4 重试中。

- 新版生产原生 copy 备份 r4 创建成功：20261008T130652Z-d04a2034，snapshot 20261008T125655Z-83ac843c；报告 transferred_bytes=1,365,031,893，含镜像元数据的目的地实际约 3.4GiB。原容器外部暂停期间执行，未使用 --no-stop；started 12:56:43Z / finished 13:13:00Z，CLI 停写计时 609s。完整性 verify 结果待记录，不以创建成功声称生产恢复通过。
- b13 全 workspace 实际恢复已通过：两消费者、身份、原件媒体、相册、配置/Secret 与镜像一致。旧恢复点/维护失败/SIGKILL 仍待验证完成；原 28 容器尚在测试暂停窗口，finally 恢复尚未执行。

- 生产 20261008T130652Z-d04a2034 原生 backup verify 通过：checked=1，problems=[]（progress 前缀用最终 API 信封解析，原始文件保留）。只证明完整性；尚未对生产数据执行恢复，不宣称生产恢复验收完成。

- b13 维护完成后注入失败，实际 start/restart/不同候选 apply/旧 rollback 均被 postgres_recovery_required 阻止；匹配旧快照恢复 18.4/0.8.1 并核对媒体/HNSW/另一消费者通过。匹配新备份恢复及 SIGKILL 精确重试继续执行，尚未作最终通过结论。
- 服务器磁盘曾仅剩约 7GiB，未开始生产 apply。核对主 Docker、旧 Casdoor 测试 Docker 和本轮 Docker 的全部运行/停止容器挂载，确认无 b12 工作区引用后，整理本任务已完成的运行副本；保留 b12.backups、b12.reports 与私有配置/部署元数据。已清理三个可重建的公开镜像运输 tar，释放 1,337,881,600 bytes；公开镜像仍按精确 ID 缓存，生产备份和恢复点未删除。证据 completed-public-image-transfer-cleanup-20261008.json，运行副本清理仍待完成。
- b12 运行副本清理完成；配置/部署元数据归档 completed-b12-metadata-20261008.tar.gz（root 0600），原 b12.backups 和 b12.reports 均保留。磁盘可用约 21GiB，生产工作区及两个生产备份未删除。
- b13 在旧恢复点成功恢复后，再通过 ANAS 恢复匹配的新版完整备份；18.4/0.8.2、媒体/HNSW/另一共享数据库及精确镜像全部核对通过。现转入真实 SIGKILL 中断与原始恢复点精确重试；原容器最终恢复尚待执行。
- b13 中断证据确认 cli_exit_status=-9；实际维护完成时 vector=0.8.2，运行集合仅 lego/postgres/samba_dc/traefik，Casdoor/Immich 未提前恢复。冻结候选 20261008T142013Z-c2fc558e，冻结 Hook SHA256 12c3e97197d1f6b906ebfebf0289d2b553aaf1c074e88c22ade35452b061cb20；证据 extension-crash-processes.json。中断后守门及精确重试仍在验证。
- b13 真实 SIGKILL 后持久屏障及精确候选重试通过；四类操作仍被禁止，原恢复点 20261008T142143Z-eaaf94a0 保留且再次 pin/verify 成功，重试后业务与普通启动核对通过。最后一次匹配新版全 workspace 备份恢复、恢复后的自动撤权以及原 28 容器最终恢复仍待完成。
- b13 中断重试后的匹配新版完整备份恢复也通过，媒体/HNSW/另一共享数据库与精确镜像核对一致；17 组公开检查已通过。恢复后的五种自动撤权、套件最终 report/退出码和原容器恢复继续执行，尚未宣布整轮套件完成。
- 完整本地 Runner 回归未通过（86.252s）：三个复制备份测试遇到 macOS rsync 不支持 -A；本任务 CLI 维护重试/PG 重新加入测试触发新 master 的本地 Linux 文件系统限制。保留 latest-master-full-runner-local-20261008.log，不以此声明完整 Go 门禁通过，不跳过既有安全检查。finance 的 Linux/Btrfs 定向回归与真实 b13 维护重试结论分别记录，不能相互替代全套回归。
- Linux/Btrfs 完整 Runner 复验通过（120.683s）：umask 022，公开模板/服务 run 脚本/两份身份 fixture/模块技术文档补齐（84 文件，tar SHA256 862348b2834239c073a08b6e42731d9d0428b97109b7eb217fa88354764632c6）；独立公开源码副本建立仅用于模块打包的 Git fixture，未传原仓库 .git、未提交任务代码。证据 latest-master-runner-full-fixtures-20261008.log；保留此前缺文件、权限与平台失败记录。
- b13 最终 report.status=passed、17 checks、cleanup.containers_stopped=true，恢复后的 Casdoor 自动撤权五类 disable/remove-app-group/delete/logout-first/admin-role-loss 全部通过，test_exit_code=0。原 28 容器均按原镜像启动；Nextcloud/Collabora 最终健康检查与 restored=true 尚待确认。报告 /data/anas-immich-workspace-c8e14b13.reports/report.json；原 workspace 及 2GiB 完整备份仍保留。
- b13 维护窗口最终 restored=true、restore_errors=[]；原 28 容器按原 ID/镜像全部恢复健康，套件 test_exit_code=0。生产准备首次 config import 被 config_import_failed 拒绝：PG bundle digest 与旧锁不同；尚未 apply，失败未导入配置。采用显式 native lock 更新当前模块后再 import/lock/plan，未绕过执行代码信任门禁。首次证据 production-prepare-aa1944a-20261008，重试 r2 尚待记录。
- 生产准备 r2 的 pre-import-lock/config-import/lock/plan 全部 ok:true；Secret 文件哈希未变。计划共享 PG 消费者为 casdoor,immich,nextcloud，停启范围为 lego,traefik,samba_dc,postgres,casdoor,eturnal,nextcloud,collabora,immich。固定版本 PG18.4-r4/Casdoor3.143-r11/Immich3.2.4-r1；保持 Nextcloud34.0.2-r10、Samba4.23.6-r12、Collabora26.4.2-r6、Lego5.3.1-r6 和其余线上设置，保留 global.temp_path。机器学习关闭，并发 1；尚未生产 apply，证据 production-prepare-aa1944a-20261008-r2/summary.json。
- 已启动正式 native apply（临时任务 anas-finance-immich-apply-aa1944a）；原生产应用按计划停写，自动匹配镜像恢复点 20261008T152630Z-5f70d61c 完整元数据已写入。生产候选后续启动、特权维护和消费者验证仍在进行；不能据此宣称部署通过。私有证据 production-apply-aa1944a-20261008。
- 正式 native apply 返回 ok:true、原生退出码 0；活动部署 20261008T152341Z-38dd538b，previous 20261007T144823Z-5bceb9fb、runtime_status=running。执行记录脚本 r1 错把 api_version 当作首字段，造成 ok:null；已按完整原生信封修正摘要并保留 summary-parser-r1.json，未重复 apply。现有 Nextcloud/Collabora/Casdoor 等已健康，Immich 首次健康及普通角色/扩展/OIDC/媒体验证继续执行。
- 生产只读 smoke r1 健康等待失败：脚本误把 Compose 明确声明 service_completed_successfully 的 anas_finance_samba_dc_events_init（Exited 0）作为常驻服务；保留 production-smoke-aa1944a-20261008.console。核对实际 Compose 和项目/service 标签后，只接受此精确任务正常退出，其余常驻服务仍须运行健康。全部 16 个常驻服务随后确认健康，Immich 无重启；首次 geodata 导入与 worker 连接波动、约 3.5GiB swap 使用已观察到，不宣称 4GB 支持。复验与原生 OIDC/媒体验收继续执行。

### 2026-10-09：生产数据库验收及首次登录阻塞

生产只读 smoke 复验通过：Casdoor、Immich、Nextcloud 的实际 TCP 应用角色均无超级用户、建库或建角色权限，错口令均拒绝；trust 规则为零。扩展为 vector 0.8.2、cube 1.5、earthdistance 1.2，PG 180004、preload 为空；OIDC 开启且密码登录关闭。证据为 finance 私有 stage 的 production-smoke-aa1944a-20261008.json。

生产原生登录 r2 返回 HTTP400：OAuth profile does not have an email address。目录管理员具备 anas/Admins 与 canonical anchor，但 email 为空；Immich 用户仍为零，没有创建未绑定账号。证据 production-native-oidc-media-20261008-r2/summary.json；等待用户指定该账号邮箱，global.email 的服务联系用途不自动授权修改共享目录身份资料。尚未执行生产媒体上传，不能将部署健康算作生产业务验收通过。

最新 aa1944a/b13 的需求结论：IMMI-R-001—005 身份及入口服务器验收通过；R-006 显式 ML/并发配置通过；R-007 原生 API 照片/视频/缩略图/相册通过，生产邮箱阻塞与手机验收保留；R-008—009 最新服务器退出路径与历史固定镜像两角色桌面退出证据通过，手机待验；R-010 五种自动撤权在首次及最终恢复后通过；R-011 repeat/restart 通过；R-012 匹配全 workspace 升级、失败、SIGKILL 精确重试和恢复通过；R-013—014 文档及公共要求复用保持完成。M2/M3 与 developing 状态保持，不能宣称 release。

- 收尾只读复验再次通过：活动部署 20261008T152341Z-38dd538b、16 个常驻服务运行，所有声明健康检查均 healthy，初始化任务正常完成。中英文文档构建 7.13 秒通过；Module/Contract 生成检查、718 项需求覆盖、56 项用例目录、需求/计划索引与 105 份文档状态检查及 git diff --check 均通过。邮箱问题仍未收到答案，未修改共享目录账号资料。
