---
doc_type: plan
status: done
created: 2026-10-30
updated: 2026-10-30
---

# 门户 IAM 会话检查实施计划

规范来源：[需求](../../requirements/iam-session-check.md)。复用当前门户 OIDC 设计，不另建架构文档或服务。复杂度中等：复用一次性授权流程和登录页逻辑，增加两个 IAM 提供方的原生兼容补丁及反例测试；不新增服务或同步状态，请求错误和退出路径保留明确边界。

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：无交互检查与回退 | R-001—R-005 | 已完成；Casdoor 固定源与当前 LLNG 2.23.4 的真实会话、回退及近期认证边界通过，见最新复测 |

## 实施与验证

- [x] 授权请求、回调与前端启动接线；复用 OIDC `prompt=none`，不新增管理会话查询 API。
- [x] Go 单元和浏览器交互回归。
- [x] 本主题当前源码验收：Casdoor 固定源通过；LLNG 2.23.4 当前构建的 16 项真实浏览器检查和 30 项原生插件测试通过。邮件完整下游及 Core 整体 apply 由邮件计划另行验收。

| 需求 ID | 验证 | 环境 / 日期 | 结果 |
| --- | --- | --- | --- |
| PORTALSSO-R-001 | `iam_session_check_test.go`；`portal-iam-session-check.test.mjs` | 本机 Go、Chrome 受控接口 / 2026-10-30 | 本地通过：已有 IAM 授权建立门户会话，返回原路由；实际结果见下方 ln 小节 |
| PORTALSSO-R-002 | 同上 | 同上 | 本地通过：无会话及需要交互回到登录页，不自动循环 |
| PORTALSSO-R-003 | 同上 | 同上 | 本地通过：授权失败不建立会话，显示手动重试；IAM 网络错误发生在 IAM 页面时，浏览器返回门户后提示重试 |
| PORTALSSO-R-004 | 同上 | 同上 | 本地通过：退出、恢复和已登录分支不自动发起检查 |
| PORTALSSO-R-005 | 同上及门户现有 OIDC 单元回归 | 同上 | 本地通过：保留浏览器绑定、单次 state、PKCE、nonce、签名、人员准入及原 auth_time；重复或不完整回调拒绝 |

早期实际 IAM e2e 执行记录（后续结果见 ln 小节）：

| 需求 ID | 脚本 | 环境 / 日期 | 结果 |
| --- | --- | --- | --- |
| PORTALSSO-R-001 | 待部署后执行实际 IAM 会话用例 | Casdoor、LLNG / 未执行 | 未执行；上表的本地模拟回归不替代真实 IAM 验收 |
| PORTALSSO-R-001—R-005 | finance 专属隔离运行准备 | `whl@finance.hlong.wang` / 2026-10-30 | 阻塞：独立 Docker 已启动，但 PostgreSQL 镜像导入期间主机内存和 I/O 等待压力持续很高；受控取消后未启动 IAM，不能算实际会话通过 |

早期未执行项由下方 ln 结果接续；当前 LLNG 固定版本及正式部署验收仍阻止完整验收，计划不归档。

本轮远端准备与清理详见[邮件计划的 finance 隔离执行补充](../../../../../dev-docs/plans/mail-integration.md#finance-隔离执行补充2026-10-30)。专属 `anas-pm-1030-test` 的 Docker/containerd 进程、socket 和网络已确认退出；不清理其余正在使用的测试引擎。Linux 门户测试 ELF SHA-256 `53e40eaca00269d7b74cd7e8588dbaacfb23f202457eefbc5d5cdaf6e0a70ef5`，本轮证据归档 SHA-256 `2143b373468cbf127c1c2931dc1af479821318b7b577e7d60a468433d2c17adb`。没有实际 IAM 结果，仍待主机容量恢复后的串行验证。

## ln 首轮实际会话验证（2026-10-30，历史）

按用户最新指示改用 `whl@ln.hlong.wang:2200`。使用独立 socket、Docker/containerd root、网络命名空间与随机假域账号；主 Docker 仅只读采样和导出固定镜像，不修改已有容器。与[邮件计划 ln 补充](../../../../../dev-docs/plans/mail-integration.md#ln-隔离执行补充2026-10-30)共享运行目录、基线和清理证据。

| 需求 ID | 实际结果 | 验收边界 |
| --- | --- | --- |
| PORTALSSO-R-001 | LLNG 实际已有 IAM Cookie 无门户会话进入原页面通过；页面内跳转丢失门户会话的 401 漏检已补修，实际重跑通过 | LLNG 运行时为缓存 2.23.2/r9 加当前源码覆盖；Casdoor 固定源同样通过 |
| PORTALSSO-R-002 | LLNG 与 Casdoor 均实测匿名 `prompt=none` 返回登录页且只请求一次；正常密码登录成功 | 需要交互错误分支另有 Go/受控页面及提供方原生单元验证 |
| PORTALSSO-R-003 | Go/受控浏览器失败回调与明确重试通过；两个专属 IAM 分别停机均产生真实 502；返回门户显示手动重试，无门户会话且无循环 | 早期浏览器路由拦截未命中重定向，保留失败，不计为实测通过 |
| PORTALSSO-R-004 | LLNG 原生退出确认后到达 signed-out，刷新不自动登录；恢复页及已登录门户不发起检查 | Casdoor 同样通过退出结果、恢复和已登录跳过检查 |
| PORTALSSO-R-005 | LLNG 与 Casdoor 均使用真实超过 300 秒的 IAM 认证建立门户会话，`recent_authentication=false`；Mail 同期写入拒绝，真实本人密码重新认证后才允许 | 未修改 Cookie 或令牌的认证时间；state/PKCE/nonce/签名反例仍由 Go 和提供方原生单元验证 |

本轮增加提供方接线修复：LLNG 匿名授权只在核对 callback、code/openid/S256 后返回原生登录所需错误，避免匿名目录组准入误报；Casdoor `prompt=none` 保留原生签发/准入路径，严格回调及 S256，匿名/需交互时返回标准错误。LLNG 原生 Perl 30 项通过，Casdoor 固定上游源 14 个单元子用例在本机与 ln Linux 测试 ELF 均通过。门户 401 补修新增受控浏览器用例，四套相关页面回归合计 66 项通过。

当前门户运行 ELF SHA-256 `edc4a943e7675470a3c807d74dab064a29abce84cc6f8697326ca4ec9703523e`；Casdoor 服务 ELF `437db9325c25f20576bb8d518c862044e17a6b93d05f5c7d50dc1ac059cbda34`，Linux 单元测试 ELF `ba1af03379f26658593982742c8990f351537799988e3b612946d9a4a855415a`。当前相关 334 文件摘要清单 `/tmp/anas-portal-mail-ln-20261030/final-source-manifest.json` SHA-256 `b3eb4a08b4f9d48d6e90a39f8b2fc6cb441462f6121f1addca598f931ac9963b`；它包含本轮提供方源码，早期 279/259 文件清单仅用于此前构建。

LLNG 当前 2.23.4/r13 镜像从公共源拉取被拒绝或超时，未把 2.23.2 兼容结果外推为当前版本验收。当时未获只读转入 finance 固定镜像的单独授权，没有读取该机私有脚本、配置或凭据。随后用户要求清理 finance 测试环境，该独立引擎及镜像缓存随测试数据删除，不再等待这条转入路径；后续需从其他可验证来源补齐当前版本。当前版本及未执行项继续阻止完整验收与归档。

本轮资源已清理：专属引擎中零容器，Docker/containerd inactive；socket、网络命名空间、veth 及控制器已消失。主 Docker 41 个既有容器的 ID、配置、启动时间和重启次数与测试前相同；私有凭据、签名密钥、浏览器 Cookie、假域数据与本地隧道已清除。公共证据归档 `ln-isolated-evidence.tar.gz` SHA-256 `a4bf1aba55fde7ac5d23bba4c02db4e0513251baafc6a216086c5f239a1bc1ce`，位于本机 `/tmp/anas-portal-mail-ln-20261030` 和远端 `/home/whl/anas-portal-mail-test-ln-iam-v2-20261030`。逐项结果、失败历史、清理和隐私扫描均保留；不是共享耐久档案。

本轮需求覆盖、需求状态、计划状态与文档状态门禁通过，用例文档已生成；全站 `docs:build` 仍被其他工作树缺失 `modules/bookkeeping/module.yml` 阻塞。当前版本 LLNG 验证未完成，计划继续实施中，不归档。

## 早期 CI 门禁与文档同步（历史）

基线为 `a9242c2acef71e14a1a3097c3fb59bf5fc1a9d59` 加未提交工作树，未构建发布制品、未部署。279 份相关源码与浏览器脚本摘要清单位于本机 `/tmp/anas-portal-mail-source-manifest.json`，SHA-256 `e4cf9379f97fe0426f3f9904fdc0daebdddfd197772044bc44c8de55230be67a`。清单包含本轮相关包的依赖源码，不代表所有实机路径已执行。

`go test ./modules/user_portal/portal ./modules/user_portal/hook` 和相关 `go vet` 通过。Chrome 执行 `portal-iam-session-check.test.mjs`、`mail-management-ui.test.mjs`、`mail-home-ui.test.mjs`、`user-portal-ui.test.mjs`，合并运行 64 项通过；随后邮件地址页新增 1 项并单独运行 15 项通过，四套脚本共 65 项。浏览器使用受控 API，不是真实 IAM 验收。

日志位于 `/tmp/anas-portal-mail-tests.log`、`/tmp/anas-portal-mail-ui.log` 和 `/tmp/anas-portal-mail-address-ui.log`。合并 Go 日志中的邮件 service 夹具失败已单独修复重跑，详见[邮件计划](../../../../../dev-docs/plans/mail-integration.md#4219-地址管理职责迁移2026-10-30)。合并 UI 日志 SHA-256 `4de049e36cb05d00cd0fe777b6ba118dfad6402c613d41edb315304dd8d2b1d2`，补充 UI 日志 `d01afebf55e3ad4fabcc43aec590d56e4ff7e17dfb7f1be6c0a714e68c798881`。本机临时证据不是共享耐久档案。

门户中英文技术文档已同步；需求、计划状态索引及用例文档已生成。`docs:check-requirements`、`docs:check-requirement-status`、`docs:check-plan-status` 和 `docs:check-status` 均通过。全站 `docs:build` 被其他工作树内容 `modules/bookkeeping/module.yml` 缺失阻塞，未改动该目录。历史门户归档验收保留，不用于本项新增行为。


## 当前 LLNG 与配置重生成复测（2026-10-30）

用户要求“修复并继续测试”，沿用 `whl@ln.hlong.wang:2200`、原有容器不变的限制。已从官方 Docker Hub 下载 `lemonldapng/lemonldap-ng:2.23.4`，以官方 manifest `sha256:f5286ec04f74ab91a17cb1b52ed23dea07a6b964dc027740889b579b0d9711b6` 校验 config 和全部 9 层，并检查解压层的 diff ID。官方 Docker archive SHA-256 `bb6ca2ddff3307e93e1d0a88a70d67abd029c6b705570d2b20a60ff006551582`；使用当前 `modules/llng/llng/Dockerfile` 构建所得镜像 ID `sha256:ce3a59157f5f96dad3c832bcd8bc6cb2fb9f515f21a86b976065665d7f07ebe2`，容器内报告实际版本 `2.23.4`。当前版本阻塞已解除，不依赖 finance 缓存。

| 需求 ID | 当前实测与回归 | 结果 |
| --- | --- | --- |
| PORTALSSO-R-001 | 当前 LLNG 真实密码会话，在门户 Cookie 清除后通过无交互授权恢复原页面；页面内 401 分支同样触发一次检查 | 通过；Casdoor 固定源相同路径的既有通过基线保留，相关源码未变化 |
| PORTALSSO-R-002 | 当前版本匿名进入门户只检查一次并返回可手动登录的页面；需要交互错误由原生插件和 Go/浏览器反例覆盖 | 通过 |
| PORTALSSO-R-003 | 只停止本轮专属 LLNG，实际 IAM 请求返回 502；返回门户显示明确重试，bootstrap 未认证；finally 恢复测试 IAM | 通过；原有容器未动 |
| PORTALSSO-R-004 | 当前版本退出确认到 signed-out 后刷新无自动检查；恢复页及已有门户会话均跳过检查 | 通过 |
| PORTALSSO-R-005 | 实际等待 306 秒；无交互登录后 `recent_authentication=false`，Mail 写入被拒绝；真实本人密码重新认证后允许写入且保留代办目标 | 通过；沿用同源码的 state、浏览器绑定、nonce、PKCE、签名、单次回调 Go 反例及 30 项当前版本 Perl 测试 |

本轮最终浏览器脚本正常退出 0，16 项检查全部通过；另外首批已通过的实际页面创建附加箱结果保留，重跑复用该测试箱，避免超出已确认箱数。通过实际 `render_env`/`runtime_restore` 重生成门户与 Mail 文件并核对内容一致，门户不接收地址写凭据，writer 仅在 Mail 私有 identity 配置；按正式私有路径分别挂载，两个 Go 服务都以只读根文件系统启动并降权为 UID 65532。

失败历史保留：夹具最初遗漏完整应用准入组、选择受保护的管理员作为地址维护目标、以及未把冻结应用目录交给 LLNG Hook，造成维护链接隐藏。修复夹具后普通人员正例通过；受保护目标仍不提供维护链接。没有放宽目录或 IAM 校验。门户建号→Mail 新标签页→原建号页面重新检查的实际链路通过。

文档阻塞也已修复：Module 身份按实际 `module.yml` 发现，未声明 manifest 的开发目录不进入 inventory；已登记却缺失、存在却无效的 manifest 仍拒绝。修复 Forgejo 开发计划的站内死链后，全站 `docs:build` 通过。相关 discovery/schema、发布清单与文档生成器回归、vet 通过。完整 Runner 包的 macOS 运行发现三项 Linux 文件系统前置失败；同一 Linux 测试 ELF 在 ln 断网、只读根和无宿主 socket 的专属容器中执行这三项全部通过，未将 macOS 全包报告改成成功。

门户运行 ELF 仍为 `edc4a943e7675470a3c807d74dab064a29abce84cc6f8697326ca4ec9703523e`，相关应用源码与首轮基线一致。复测相关 342 文件清单 SHA-256 `3a5e285e1331d01052f0fe496e735d315353b1037efdedcba40b7ef99793e27d`；脚本与制品清单 SHA-256 `d5b444621bfc409a227c13911747a812b7533c6c40794c564a61235c255afed6`。CLI Linux 测试 ELF SHA-256 `4f5bc9f46bdff388b03bbcef8a73bd59a7c13f32be8a48083e6f8b4857e61d9d`。清单是本轮相关文件与已编译制品基线，不是工作树所有其他修改的验收。

本主题五项标准已满足，完成归档；完整邮件发送、Webmail/worker、管理员地址移交及 Core 整体 prepare/apply 保留在邮件计划，不由本项门户会话验收关闭。原有 41 个容器前后不变，专属引擎零容器且 Docker/containerd inactive，socket/netns/veth、控制器、凭据、假域数据库、签名密钥、私有浏览器输入及隧道已清理。最终公共证据 `ln-current-followup-evidence.tar.gz` SHA-256 `e1805b64c5498d288c36c6bba728fd9066cd5a44f22e1ac04a66aa709f4be053`，保存在本机 `/tmp/anas-portal-mail-ln-continue-20261030` 及远端 `/home/whl/anas-portal-mail-test-ln-iam-v3-20261030`。相关 346 文件最终复核清单 SHA-256 `0afb9e08ed1c42b3cba7d007f4580a4572ba9827456f28ae3f54d0afcaa669eb`；同名 342 文件清单保留为联测编译阶段基线。需求、双状态索引、用例文档与文档状态门禁以及完整文档构建均通过。清理细节及后续邮件范围见[邮件复测计划](../../../../../dev-docs/plans/mail-integration.md#ln-当前版本与配置重生成复测2026-10-30)。
