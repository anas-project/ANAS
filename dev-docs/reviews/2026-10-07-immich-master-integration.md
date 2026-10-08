# Immich 合入线上 master 与 Casdoor 接续核对

基线：2026-10-07，`origin/master` `9fc699c` 加保留的未提交工作树。

## 已实施

- 原 `fd734b8` 基线快进合入 master 的 9 个提交，逐项解决 17 处冲突；stash 保留，未提交或推送。
- Casdoor r11 候选复用 r10 canonical anchor subject 和原生定向撤权，增加受信管理员组角色与可选 CAEP session-revoked。角色不读取用户可编辑属性；策略事件保留原截止时间，持久重试不误撤重入后的新凭据。
- 新 Casdoor workspace 主机验收驱动复用现有 PostgreSQL 维护、媒体、备份恢复及隔离边界；使用原生 OIDC code/PKCE 和真实 Samba 事件。
- master 新增并发备份测试夹具改为有效 metadata tar，以适配恢复镜像验证。公共备份 plan ID 排除动态磁盘剩余量，仍绑定操作选择并执行前校验。

## 验证与限制

- Casdoor/Immich Hook 与 Casdoor helper 全部通过。固定源码 SHA-256 与全部七项 Casdoor 原生补丁测试通过，验证受信角色和 RS256 策略 Logout Token，包含无活跃 IAM token 的撤权；helper 验证重入后再次撤权保留独立截止时间。
- Linux Runner 全包通过（10.026 秒），备份并发、维护描述符及 plan ID 回归通过；原有 Immich workspace 42 项回归通过。
- Linux 全仓库测试尚未全通过。consolejobs 同大小日志重写、Incus 非 root 主机 bundle 测试失败在干净 `9fc699c` 上复现，独立于本次改动。macOS 系统 rsync 不支持 `-A`，不能代替 Linux 验收。
- finance 仅使用本任务独立 Docker/netns/Btrfs；生产 Casdoor r9 和其他任务的 Casdoor 实验 daemon 未修改。r11 镜像构建进行中，不计为通过。用户随后明确批准三份源码上传，已同步到本任务隔离目录。固定镜像成功后启动的主机验收已排队。另发现共享驱动两处停写断言硬编码 Authentik，已本地修正为实际 Provider；该第四份脚本的同步被自动审批以不在三份清单中为由拒绝，追加授权仍待回复，不绕过该拒绝。
- 用户明确目前没有手机验收条件；原强制手机上传、后台备份、退出及目录撤权要求保留。Immich 与新增 Casdoor r11 保持 developing，不能宣称 release 或 4GB 支持。

## 待完成

完成新固定组合的主机及桌面验收，核对生产 Nextcloud 旧身份绑定后再实施生产 IAM 升级与 Immich 部署；补齐真实手机验收后才能关闭发布里程碑。


### 获准上传后的测试启动记录

- 三份源码上传成功，隔离 netns 的三地址和现有 Go 工具链可用；未更新生产部署。
- 共享驱动两处停写断言已本地改为实际 Provider，Linux 42 项回归通过（1.866 秒、无跳过）。第四份脚本的远端同步仍等待逐项授权。
- 正式固定源码服务端编译容器 `b30cbc8971a0` 退出 0；制品在 finance 本任务目录保存，SHA-256 为 `3231889b9c36421bff7508fb18d3d30b8ec5d0def51ae671c512fb6c15a866e3`。
- 隔离 daemon 报公开 Casdoor 基础镜像内容层缺失，重新拉取固定 amd64 `casbin/casdoor:3.143.0`；上游镜像 digest 为 `sha256:1284af680ddf10aa80569f1f4a46210dd9875ce70845e67047053363d0c0ba58`。
- 编译退出不等于镜像已提交。Docker 线程栈确认请求等待 containerd `Diff` → `ImageService.createDiff` → `CommitBuildStep`；最终候选镜像尚不可用。不得把启动排队或正常编译退出记为应用 e2e 通过。最终 helper 构建与主机 e2e 已以镜像成功为启动前提排队，构建日志和制品保留。


### 构建与启动后续

- 用户批准第四份共享脚本上传，已完成同步；此前同步阻塞解除。
- 镜像提交随后完成，旧构建器在正式配方的 `COPY --chmod` 处退出。仅远端临时配方将此操作等价改为复制后 `chmod 0755`，保留固定源码与三项平台参数；正式 Dockerfile 未修改。最新 helper 候选镜像构建成功，短 ID `f606c9081978`。该临时构建适配不等于正式 BuildKit 配方验收。
- 首次启动因 workspace 名称不符合既有隔离规则被拒，未创建应用。第二次因源码包遗漏公开 `pgvector-0.8.1.fixture` 退出；夹具已逐行检查并补齐，原失败报告保留。
- 公开依赖反向代理在会话接续时断开，已恢复相同公开域名白名单代理，GitHub 连通检查返回 200。
- 新的 `/data/anas-immich-workspace-c8e14b08` 主机验收已启动，正在新装；尚未形成应用验收通过结论。


### 新装失败后的独立检查

- 新装 `c8e14b08` 在 Samba 健康阶段失败（`start_failed`），没有算新装通过；失败报告保留为 `first-new-apply-failed-report.json`。镜像内时区初始化及 structure 脚本 SHA-256 均匹配当前修复后的源码，时区修复缺失猜测排除。
- 同一 workspace 的诊断 apply 随后退出 0；Samba 三标记、四端口、named/域/管理员/DNS 查询全部通过，PG、Casdoor、Immich 健康。首次冷启动失败原因尚未闭环，重试不替代完整新装验收。
- 安装后检查明确标为续跑。Casdoor dirwatch 的 get-ldap-users 持续 403：服务客户端与数据库匹配，容器继承 HTTP_PROXY 却未绕过 `anas_casdoor`。仅对不存在账号的只读 API 禁用代理即成功，确认代理误路由。
- helper 改为使用直接连接的私有服务 HTTP 客户端，保留原超时与连接池；不新增配置、服务或依赖。真实 HTTP 回归确认 Basic 凭据只到后端、测试代理零请求，helper 全包通过（3.148 秒）。本任务代理配置补齐私有服务名并仅重建订阅器，续跑仍在进行。


### 内部 API 修复后的候选

- 仅重建 dirwatch 并补齐代理绕过后，续跑推进到原生首次授权，但在 Casdoor 返回授权码前失败（native-first-users / AssertionError / login）；不是管理员建号或媒体通过。原始 HTTP 响应由共享驱动读取后删除，没有从残存文件获得错误消息，因此下一轮新增脱敏失败记录。
- helper 直接连接修复已构建为独立测试标签，候选短 ID `00b78c312d45`；当前续跑停止后才切换 r11 标签，验收期间没有换用不同镜像字节。
- 移除临时 `anas_casdoor` 主机名代理绕过，以检验产品修复自身；新的 `/data/anas-immich-workspace-c8e14b09` 完整主机验收已启动，结果待定。

### Casdoor 首次登录的实际主机失败与修复

隔离工作区 `c8e14b09` 完成新装，各服务健康，且目录同步在未将
`anas_casdoor` 加入 `NO_PROXY` 的条件下健康，验证了内部 API 直连修复。
首次用户流程仍失败，原生 Casdoor 返回 HTTP 200 / `status=error`：
`User's tag:  is not listed in the application's tags`。尚无 OIDC 或媒体验收通过。

原因是候选实现把 CAEP 协商标记写入原生 Application `tags`；该字段参与
登录许可检查。改用固定上游最小补丁中的 `anasPolicyRevocation` 布尔字段，
继续沿用既有 Application 表、初始化投影及目录撤权流程。该字段不加入
OIDC claims，不改变用户标签或目录组。Hook 回归测试要求协商字段为 true
且不设置 tags；签名策略事件测试使用同一字段。修复后须重建并重新验收，
本次失败及原始报告保留，不计为 release 通过。

另经当前固定源代码核对，组织不配置 `accountItems` 时，原生用户更新检查
会跳过字段编辑规则。候选 Hook 正是未配置该项，故新增最小服务端检查：
普通用户不得修改 `anas` 账号目录组；此检查位于既有
`CheckPermissionForUpdateUser`，不依赖前端。固定源代码回归验证该请求被拒绝，
可信角色及签名策略事件测试通过。Hook 测试通过；中英文文档构建通过（7.10s）。
真实主机身份及完整备份验收仍须继续。

普通用户检查同时保护 `isAdmin` 与 `externalId`，避免显式 columns 更新绕过
默认列限制。固定源代码测试覆盖三种拒绝；真实工作区脚本新增三种原生 API
修改请求和数据库不变检查，尚待主机运行。候选镜像两次在 goproxy.cn 下载
不同固定 Go 依赖时出现 unexpected EOF；停止仅本任务的等待进程，改用
`https://proxy.golang.org` 构建，不改变 go.sum 或依赖版本。

官方源可达，但构建长时间仍下载依赖图。为继续主机验收，临时测试 Dockerfile
复用本任务此前成功的固定源编译层 `16be0f765f42` 的公开 Go 模块与编译缓存，
仍按当前源码重新编译服务器，并增加 `go mod verify`。只改远程临时测试配方；
仓库正式 Dockerfile 不引用本地缓存层。停止并删除的仅是本任务构建容器
`910e11939988`。不能据此宣称正式 BuildKit 配方已验收。

复制大缓存层和 legacy ARG 提交均明显延迟，停止对应本任务客户端，改为临时
测试配方直接 FROM 同版本成功编译层，严格撤销其旧 0007 补丁，再应用当前
0007（均 `--fuzz=0`）。当前网络下 BuildKit 能运行此临时配方，Helper 编译
完成（209.8s），全部 Go 模块校验通过（171.1s），服务器正在重新编译。
中途远程配方链接参数引号丢失的旧构建已停止并修正；不能把其脚本最终
`tail` 的退出码计作镜像构建成功。新构建须检查实际 image ID 后才能用于验收。

当前临时缓存层 BuildKit 配方产出镜像索引
`sha256:be519a02dfacdf03246e875b270b23098d93a927e43e9529556b4361b31140f2`
（配置 `sha256:92c7f187fe91e186899975499f306853d7c5b342ae142867ba070c8c75c2637d`）。
在专用 daemon 无容器时将其标记为候选 r11，启动全新隔离工作区
`/data/anas-immich-workspace-c8e14b10`。初始化已成功，正在首次 apply；
生产部署未修改。该轮完整验收尚无最终结果。

`c8e14b10` 的首次 apply 成功，所有服务健康；原生 Casdoor 登录不再返回
tags 拒绝。但首个普通用户回调断言失败（脚本 215 行：HTTP 400 且空库），
还未进入首个管理员或上传流程。没有 `native_login_failure`，不能据此判断
是回调认证错误还是首次账号限制问题。保留原报告，使用同一隔离工作区
诊断回调状态和数据库账号数量；不修改生产，也不放宽验收断言。

同工作区诊断确认普通用户回调 HTTP 500，用户表仍为 0；脱敏服务端日志
明确显示 `OAUTH_JWT_CLAIM_COMPARISON_FAILED`，原因是
`unexpected ID Token "nonce" claim value`。固定源的 `getClaimsCustom` 无条件
写入空 nonce，而原生标准 token 已使用 `omitempty`。新增最小固定版本
补丁，只在 nonce 非空时写入自定义 token；请求 nonce 时保持原值。两种情况
及可信角色/签名策略事件固定源测试通过（1.699s）。不绕过 Immich 校验。
诊断期间也观察到目录用户刚导入而 groups 尚为 JSON null 的短暂状态，
脚本改为继续等待全部组就绪，未删除授权检查。该脚本问题不解释原来的
回调 HTTP 500。停止仅本任务诊断工作区，重建后须复验。

nonce 修复候选镜像索引为
`sha256:91c8c9a3b023e4279e02e2adc72ff730bbd156021b4417185ecad5c65b8908ad`
（配置 `sha256:1b25ac9939189acf5cb56374f56b8e922a87c6a962cab2f80871064ee6ed026d`）。
真实回调诊断通过：普通目录用户 HTTP 400，响应包含首次账号限制，用户表
保持 0；将该目录用户加入真实 Admins 组并等待原生同步后，回调 HTTP 201，
原生账号 `isAdmin=true`。没有本地建号、改绑或绕过客户端校验。此为保留
诊断工作区的针对性证据，仍须完整新工作区套件验证普通用户、入口、上传、
撤权与一致恢复。权限测试另增加同值 displayName 更新正向基线，再验证
受保护字段拒绝，避免把整体接口未授权计为通过。

`c8e14b11` 使用 nonce 修复镜像执行完整新装。首次 apply 成功，各应用
健康；真实主机检查已通过原生 OIDC 首个管理员/普通用户 JIT（anchor=sub=
oauthId）、普通用户 profile 受保护字段拒绝（含 displayName 正向基线），
以及 Immich local/setup/password/link/unlink/global-unlink 入口限制。
身份冲突、上传、撤权和一致恢复仍在执行，不能将这些阶段记为通过。

只读复核生产 finance：活动部署已由其他操作更新为
`20261007T080744Z-c4f84f18`（前序 `20261007T074736Z-bcf3a754`）。
运行镜像仍为 Casdoor r9、Nextcloud r10、PostgreSQL mirror 18.4-alpine、
Samba r12；相关服务健康。本任务没有改动该部署。之后部署必须基于该
最新状态重新生成 plan，并检查共享 IAM/数据库消费者，不能沿用早先
`20261003T080747Z-8b01d4f5` 的生产基线。

`c8e14b11` 完整报告为 failed，清理停止全部本任务容器并保留工作区。
失败精确位于 `identity_cases` 第 128 行（四个并发首次回调全部成功的断言）；
报告 failed_phase 沿用入口阶段，脚本现已补独立 identity-cases 阶段。
该断言比 `IMMI-R-005` 的“不得产生多个有效绑定或接管旧资产”更强，且与
现有 Authentik 原生并发测试标准不一致。复验改为四个独立授权在 callback
前会合，记录 HTTP 状态和有效账号行数；要求至少一个成功、恰好一个有效
同 sub 账号、所有成功请求命中同 id、失败请求无 accessToken，保留 400/500
竞争失败的实际结果。不是将所有 HTTP 错误视为通过，仍必须具备上述正向
证据。原失败报告未覆盖或修改。

在保留的 b11 工作区创建 `continuation-r1` 诊断报告和新目录 fixture，继续
身份、上传及生命周期。报告明确不是新装完整验收；之后完整新装仍须复跑。
登录 fixture 密码仅写远端 root 私有报告，未复制到本地或公开源包。

`continuation-r1/concurrent-callbacks.json` 已记录四路独立授权在回调前会合：
HTTP `[201,201,201,201]`，有效同 sub 账号行数 1；同内部 id、普通角色与失败
凭据限制断言通过。这个复验没有出现竞争错误，不能推断原失败四路具体
HTTP 状态（旧脚本未记录），也不能据一次全成功宣称所有竞争请求均成功。
仍继续软删除绑定、上传与生命周期；原完整报告保持 failed。

`continuation-r1` 在软删除重登断言（第 154 行仅接受 HTTP 400）失败，报告
和停止状态均保留。Casdoor 脚本现与通用原生 IAM 验收一致：删除后先确认
保留同 id 的非空 deletedAt，再同步一个新邮箱，记录回调状态，要求 4xx/5xx、
无 accessToken、完整 tombstone 行不变，并在完整新装中验证旧 bearer 401。
不能仅以邮箱重复或一个错误码证明 anchor 保护。
`continuation-r2` 复用 r1 私有登录 fixture 诊断该 tombstone 并继续媒体及
生命周期；r1 未保存已删账号的旧 bearer，r2 明确记录该项不可用，须完整
新装复验，不据诊断标记 release 完成。

`continuation-r2/soft-delete-callback.json` 实测新邮箱重登 HTTP 500，无
accessToken，保留同 id、同 deletedAt 的 tombstone；仅诊断此绑定限制，
旧 bearer 项仍未补测。上游将唯一约束拒绝映射为 500 的行为明确保留为
用户可见限制，不能宣称 400 或无错误体验。
续测已完成照片、H.264 视频上传，等待原生缩略图 200 并创建恢复相册，
生成远端私有 `private-state.json`。实际角色、扩展/preload、重复 apply、
重启、撤权和灾备步骤仍继续执行。

`continuation-r2` 上传后 `verify_state` 通过真实应用角色数据库访问、扩展
版本/preload、HNSW 查询、共享 Casdoor 数据、原件哈希、相册及身份检查。
重复 apply 的 runtime 与 Secret 摘要保持不变，全部状态再次验证通过。
当前仍为测试用 vector 0.8.1 基线，目标 0.8.2 的协调升级及恢复尚未执行。

`continuation-r2` 完整 ANAS restart 后再次通过 `verify_state`，原生 OIDC
角色和内部 id、媒体原件哈希、相册成员、HNSW、Casdoor 共享数据库记录与
Valkey 数据均保留。进入真实 Samba 禁用等五种自动目录撤权检查，未手动
生成或投递 logout token；该阶段仍待结果。

`continuation-r2/directory-cases.json` 五项真实目录撤权全部通过：禁用 54.371s、
移除 APP_immich 84.838s、删除 62.589s、先登出后撤权 53.167s、移除 Admins
61.781s。四种旧凭据（bearer、cookie、API key、分享）均 401，绑定和媒体
保留，未受影响用户仍可登录。先登出阶段明确验证会话 401 而 key/share 200，
再目录撤权验证全部 401；管理员降权验证同内部 id 的新普通角色登录。
使用实际自动 watcher 和原生签名策略事件，没有手工投递 token。
继续目标扩展升级、维护失败与 ANAS 完整工作区恢复；未据续测声称 release。

正式 Dockerfile BuildKit 构建在固定源 SHA 校验、七项补丁应用后持续下载
公开 Go 依赖（约 160MB）。升级准备期间主机 load 达约 18–25，vmstat 有
明显换页，隔离 Samba 连续 10s healthcheck 超时；可用内存约 1.7GB，不能
据此断言逻辑缺陷或特定构建是唯一原因。取消仅本任务已核实客户端
PID 3421920 的并行 canonical-check 构建（日志 `CANCELED/context canceled`），
之后串行复验。外层 tail 返回 0 不计构建通过。没有取消其他任务或操作
生产部署；扩展升级/恢复仍待实际结果。

`continuation-r2` 在维护前的全模块强制构建失败：Lego 基础镜像的 Docker Hub
匿名令牌 TLS handshake timeout。尚未执行扩展维护，原报告 failed 与工作区
停止状态保留。显式经任务公开代理 pull 固定 Lego v5.3.1 成功，摘要
`sha256:f4fd80df0ef94d2f536cc2e7fb5bdbd090fb0aa81b3595226b9fe814bb9a2bfe`。
补齐初始公开源包漏掉的 Samba 三份无扩展名启动文件，不涉及 Secret。

生命周期脚本升级步骤恢复使用常规 apply，复用已校验的固定 Provider/应用
镜像，单独执行镜像构建验收；避免仅升级 PG 时重建全部六个模块。实际应用
角色、扩展版本/preload/HNSW、旧实际镜像恢复点和全工作区恢复断言均保留。
本机回归 42 项通过（2 项 Linux 条件跳过，1.341s）。
正式 Casdoor Dockerfile 不改动，使用既有 GOPROXY_URL 指向只读公开依赖缓存
（缓存来自本任务已 go mod verify 的固定源），官方 Go 代理作为缺项回退。
缓存服务只读本任务公开目录，以主机已有 Python/普通用户运行于专用 netns，
无 Secret 或生产挂载。临时容器 httpd 和 CI 镜像尝试因缺组件而未启动，
均未计构建通过。正式构建仍待结果，之后移除临时缓存服务。

正式未修改 Dockerfile 的串行 BuildKit 构建通过，镜像索引
`sha256:326be8ddbef0c5144be61aab4d01120d0bcae037c6706fb79758f56360241c03`
（配置 `sha256:5d2f1621035bc42877562e0fa35e1e556278ffb037465e896b82057dda27c647`）。
固定源校验、七项补丁和原生 helper/server 编译均完成；服务端阶段 1480s。
逐一对比正式与实机候选的二进制：server
`dd55d5462f7a19da7582929304f6a4ca08e6425eafa52028a304a1f345a2a826`，helper
`04d75dc4cbb4ea2e95b814ee75c85c4dfa8306be2cd1c93e3fce4e0316da99c1`，
完全相同。无运行中 retag。临时只读公开 Go 缓存服务已停止。
构建期间只降低本任务 Go 编译后代的 nice/I/O 优先级；只读复核生产 Samba、
Casdoor、PostgreSQL 均健康，没有改其部署。
启动 `recovery-continuation-r3`，复用 r2 私有媒体和账号，常规 apply 升级、
失败与完整恢复验收仍待结果；历史完整新装报告保持 failed。

`recovery-continuation-r3` 在升级前 plan 返回 `lock_stale`：r2 失败 apply 的
`--update-lock` 已将 desired lock 写为 18.4.0-r4，但续测沿用 r3 baseline
源作预检查。实际角色/媒体状态预检查通过，尚未维护数据库。停止并保留
r3 报告，r4 续测改用当前 r4 源进行 plan；不转换 lock、不绕过检查。
同期隔离和生产 Samba healthcheck 曾超时（生产其他核心服务健康），
作为独立负载/健康观察项保留，不能用它解释该确定的 lock_stale 失败。

r4 在只读 plan 的第二个明确检查处返回 `lock_stale`：Samba bundle digest
不符，因为公开源包已补三份遗漏脚本而 desired lock 仍记录旧不完整源摘要。
升级仍未开始，停止保留 r4 报告。r5 在启动/只读 plan 前使用已有
`anas lock --module-root <current-source> --update-lock` 显式刷新测试源 lock，
不修改生产 lock、不手改/转换旧 lock，也不绕过摘要检查。

r5 的显式 source lock 更新完成，但在升级前 `verify_state` 的
`/api/server/ping` 就绪等待超时；扩展维护尚未开始。报告为 failed，清理
仅停止本任务隔离容器，数据、回滚镜像和所有历史报告保留。该失败不能
计为扩展升级失败或通过。结束正式构建后清理本任务未使用 BuildKit 缓存
约 2.012GB，不删除镜像、卷或恢复点。主机仍有换页和较高负载，资源压力
与应用日志需要进一步核对；r6 使用独立报告继续同一保留工作区验收。

r6 的实际重启数据检查及升级前 plan 通过；apply 在 calculate 阶段返回
`calculate_failed: no usable IPv4 address detected`。续测入口遗漏整个进程的
`ip netns exec`，设置 ANAS_UPGRADE_NETNS_PATH 本身不会切换当前进程网络。
未进入特权维护；该失败属于测试启动错误，不是 Provider 升级结果。
修正独立续测启动方式，并在临时入口增加当前 netns inode 与指定 netns
一致断言。r7 断言路径类引用错误、未启动服务，保留 console；r8 修正该
引用后重新运行，所有历史报告保留。仓库正式 shell 入口的 netns 约束
未放宽。

只读复核 finance 正式部署已由其他线上工作更新为
`20261007T122012Z-e1125eab`：Casdoor r9、Nextcloud r10、Samba r12、
PostgreSQL r4。此任务尚未修改正式工作区，后续部署必须重新核对最新状态，
不能按此前生产标识覆盖其他改动。

网页验收补充 `ANAS_TEST_IAM_PROVIDER=casdoor` 原生表单与原生 `/api/logout`
分支，保留 Authentik 默认和共有媒体/旧会话断言；语法检查通过，Casdoor
真实浏览器仍待执行。公开脚本已上传本任务浏览器目录，未上传凭据。
恢复后目录测试调用复核发现 Casdoor 会继承 Authentik 专用 shell probe。
Casdoor 覆盖恢复后入口，依次保留维护失败、SIGKILL 恢复检查，再在恢复的
工作区重复其五种自动目录 watcher 撤权。新增回归明确禁止调用 Authentik
探针；本机运行 43 项，41 项通过、2 项 Linux 条件跳过（2.352s），不是实机验收。
已运行的 r8 仍加载修正前代码，其真实维护/恢复证据将逐项保留，不将错误
Provider 探针失败视为应用缺陷或完整通过。

r8 正确隔离网络下升级 apply 返回 `ok: true`，部署
`20261007T135946Z-65838b07`；升级前全工作区停止，实际镜像归档持续写入
（观察到约 982MB、1.54GB）后完成。恢复点
`20261007T140214Z-f169d3b7` 已由 ANAS pin，ANAS verify 返回 checked=1、
problems=[]。升级后实际角色/版本/HNSW/媒体验证及后续全工作区恢复仍在
运行，不能将 apply/恢复点 verify 成功计为完整升级或恢复验收通过。

线上源只读复核 Nextcloud r10 的 task.sh：
`ldapExpertUsernameAttr="$SAMBA_DC_USER_NAME"`、
`--mapping-uid=preferred_username`。与 master r11 不同，现有身份及旧会话
兼容性必须单独验证，不能直接覆盖其配置。生产工作区仍未由此任务修改。

r8 实际受控扩展升级验收通过：vector 0.8.1→0.8.2，stop evidence 覆盖
全工作区，旧实际镜像恢复点验证通过，消费者启动前维护完成。普通应用
角色、PG/扩展/preload、真实 HNSW 距离查询、原件哈希/相册、Casdoor 数据库
恢复探针及 Valkey 状态均通过。升级后只读 plan 通过且不改变受管状态；
直接 rollback 被 `postgres_restore_required` 拒绝。完整新装 suite 仍非通过；
续测继续执行完整 ANAS 备份恢复、特权维护失败和 SIGKILL 场景。

r8 ANAS 全工作区 copy 备份 `20261007T142719Z-03ca8113` create/verify 均
返回 ok；备份内数据库、媒体、配置、Secret、实际镜像清单/归档摘要核对
通过。复制阶段主机 load 观察到 53/48，换页明显，Immich 重启就绪延迟；
不能据此声称容量支持或把负载归因于唯一进程。尝试降低经祖先链限制的
本任务 rsync 优先级时原 ANAS PID 已退出，范围断言拒绝执行，没有调整
其他进程。备份通过不等于恢复通过；继续测试数据破坏后的完整恢复。

备份重启后的业务预检查尚未通过：实际 `/api/server/ping` 返回 404，
Immich running/unhealthy、restart=0、OOM=false，日志停在 API/microservices
worker 启动。主机 PSI（等待压力比例）观察：memory some avg10=94.67%、
full=48.93%；I/O some=97.02%、full=35.51%；load 约 48–53。不能据此将
问题归因于唯一进程或认定应用逻辑缺陷。为减少共享主机争用，在已核实
create/verify 通过、尚无 config-mutation、且 PID 命令行精确匹配本任务
r8 入口后，仅向该测试 runner 发 SIGINT，使用其 finally 停止隔离容器。
数据、备份、旧镜像和报告保留；完整恢复、维护失败与 SIGKILL 尚未通过。
更空闲 Linux/Btrfs 主机或更多 finance 内存条件已向用户询问；独立本地
工作继续。此任务未改变生产部署，也不声称 4GB 支持。

r8 中断报告已完成：status=failed、failed_phase=verify-state、
error_type=KeyboardInterrupt；cleanup.containers_stopped=true、
retained_by_request=true、workspace_removed=false。逐项保留受控扩展升级和
一致备份的通过记录；备份重启后业务未就绪，未执行媒体破坏与完整恢复。
现有媒体、Secret、配置和备份没有为此次中断而删除。尚待更空闲实机条件，
不能按此报告标记完整新装、恢复或 release 通过。
