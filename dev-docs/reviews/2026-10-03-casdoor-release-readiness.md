# Casdoor 发布就绪评估

结论：**不通过（按现行完整发布要求）**。后续实施已将 Casdoor 更新为 r10、`developing`，
主体标识符、OIDC 定向撤权与持久重试已接入；完整发布验收仍未闭环。最初 `release` 标记来自
2026-08-27 的历史验收。用户在本轮后续明确
产品尚未发版：**不要求旧账号迁移，也不把历史 r8→r9 迁移作为本次首发整改的前置条件**。
已有运行记录支持当前部署的 OIDC 登录可用，不能据此认定全部发布条件成立。

## 最初评审基线（实施前）

- 日期：2026-10-03，Asia/Singapore。
- Module：`casdoor`，上游 `3.143.0`，当前工作树 revision `9`，清单状态 `release`。
- 源码：HEAD `c789116295e14322b0c698825bf89404caef3c12` 加现有未提交工作树；HEAD 中 Casdoor
  仍为 r8。未提交变更包含入口配置权限修复、Compose 挂载修改、r9 投影和专属升级 suite。
- 目标：判断现行发布要求是否满足；评审者：Codex。
- 平台：历史验收为 amd64 真实运行、arm64 固定源码构建及非特权运行探针；本轮静态检查在 macOS
  执行，未重新运行 Linux 实机矩阵。
- 数据库与 IAM：独立 PostgreSQL Resource、Samba AD/LDAPS、Casdoor OIDC/SAML；近期真实应用记录
  为 Nextcloud OIDC。

最初评审仅检查现状。后续实施、验证及实机环境记录见下节；以下原始发现保留为整改依据。

## 已有证据

1. [归档实施计划](../../modules/casdoor/dev-docs/plans/archived/casdoor-iam.md)记录 M1—M5 完成，
   覆盖目录收敛、OIDC/SAML 登录、OIDC 用户/管理员会话撤销、恢复管理员、空 workspace 恢复、
   凭据轮换和生命周期。其 r9 是当时的测试 revision，不能替代本次启动权限修复的精确升级验收。
2. finance 部署任务记录（当前本地工作树 `2026-10-02-finance-workspace-deployment.md`，本提交不含该独立任务工件）确认 r9 的配置权限修复后，
   服务健康、恢复管理员登录以及真实目录账号经 Casdoor SSO 进入 Nextcloud 并编辑保存均成功。
   镜像由已验证 r8 叠加审核脚本构建，记录明确没有重跑完整上游 Dockerfile 构建。
3. 固定版本不支持 SAML SLO，当前保持不发布 endpoint/binding。这是历史发布时明确的支持边界，
   不把它单独算成一次失败的 SLO 验收。

## 最初发现与整改依据

### 1. 主体标识符不满足当前目录身份键要求

`DIRKEY-R-008` 要求 OIDC `sub` 与 SAML `NameID` 直接取 `anasIdentityAnchor`。
当前 OIDC E2E 从 Casdoor `.id` 读取期望 `sub`（`server-casdoor-oidc-e2e.sh:279`），SAML E2E
显式断言 `NameID` 为用户名（`server-casdoor-saml-e2e.sh:139`）。
[Module README](../../modules/casdoor/README.md)也明确列出这两项缺口。

额外发送 `externalId` 锚点属性能供配置正确的 Consumer 稳定关联，仍不满足主体标识符直接取锚点的
新要求。SAML `NameID` 随改名变化，不能当作应用的永久绑定字段。此项是已记录、由当前测试契约
再次确认的实现缺口；本轮未重新抓取真实 token/assertion。

整改属于[目录身份键计划](../plans/directory-identity-key.md) M2；切换前还必须逐个检查 Consumer
是否把主体标识符用于用户名、URL 或文件路径，不能直接修改签发值后跳过 Consumer 投影与改名验证。

### 2. 目录准入丧失尚未自动撤销既有应用会话

`DIRSYNC-R-013` 要求停用、删除或组撤权等事件，在声明的最大传播时间内使受影响用户既有应用
会话失效。当前 `casdoorLDAPSyncer.sync` 读取目录、执行 LDAP 同步和 `update-user` 补丁；
`planPreSyncUserPatches` 禁用用户并清组，`planPostSyncUserPatches` 更新属性和组，没有由这些事件
调用 session 删除或标准登出通知的路径。

管理员手工删 session 的 exact-`sid` OIDC E2E 已完成，但触发条件不同，不能证明目录事件自动撤销。
[目录事件计划](../plans/directory-event-subscription.md) M3 仍为未开始，README也要求人工在
Consumer 侧执行紧急撤权。此项是当前调用链中的实现缺口；具体旧 token/refresh token 的存活期限
本轮未实测，不以文档中的推断替代实机结论。

### 3. 当前 r8→r9 缺少精确升级往返证据，脚本也有待修正项

此节保留原始静态审查发现。产品尚未发版后，它是历史测试夹具与通用升级门禁的待办，**不是本次
首发身份键整改需要执行的迁移**；不要求为旧主体标识符写兼容层。

catalog 已登记 `casdoor-r8-r9` / `modules-casdoor`，静态校验通过。但
临时存储任务的 10 月 3 日验收记录（本提交不含该独立任务工件）明确指出 Casdoor
r8→r9 专属升级 E2E 未运行。当前服务器上的登录 smoke test 不验证旧发布产物、持久状态迁移或回退。

本轮还静态发现：`server-upgrade-verify-casdoor.sh:9` 在所有阶段都要求 `/conf/app.conf` 为
`1000:1000:600`，而 runner 在 `server-module-upgrade-e2e.sh:235` 回退到 r8 后也调用同一 verifier。
r8 直接挂载冻结配置，其 root 所有权及 `0400` 权限正是本次修复的原因；旧端不符合 r9 的运行配置
布局。脚本没有区分回退阶段，这个断言与旧端布局冲突。未执行实机复现，不能写成已经观察到的
升级失败；但需要在补跑前修正验收条件并处理真实旧产物的启动边界。

现有 seed 只写依赖服务的持久目录 marker，Casdoor verifier 只检查 marker、健康、权限和 discovery；
尚未校验旧端建立的 Casdoor 用户、client、签名材料和恢复账号在整个往返中保持。补充这些断言，
再按 `UPGRADE-R-014`—`R-018`、`R-021` 运行真实旧版→新版→旧 deployment→新 deployment。
本轮未验证当前 r9 的双架构完整镜像构建或公开制品可拉取性。

## 最初评审验证

以下均通过：

- `go test ./modules/casdoor/hook`。
- Casdoor 独立 helper module 的 `go test ./...`、`go vet ./...`。
- `go run ./cmd/gen-module-docs --check`。
- `npm run test-upgrades:catalog`。
- `npm run docs:check-requirements`、`docs:check-requirement-status`、`docs:check-plan-status`、
  `docs:check-status`。
- 入口与 Casdoor 升级 verifier 的 shell 语法检查，以及相关文件的 `git diff --check`。

Hook/helper 首次执行被沙箱对 Go 构建缓存的访问限制拦截；获准后在沙箱外重跑通过。
需求覆盖校验对已归档 Casdoor 主题只校验需求归属，不复核历史 E2E 记录，更不证明新通用要求已满足。
评审阶段没有运行远程 E2E、完整仓库测试、文档站构建或发布流水线；后续探针与文档验证见下节。

## 后续核实（2026-10-03，未发版前提）

- 已新增 `test-casdoor-identity-source.sh`：六项固定源码配置探针通过。仅配置无法统一 token、
  UserInfo、Logout Token 与 SAML 主体标识符。
- 测试夹具里的统一锚点候选补丁已通过八项源码检查及两项既有回归。签名 JWT/Logout Token 与
  UserInfo/SAML 模板统一取锚点，缺锚点拒绝；恢复管理员 OIDC 保留内部身份。候选未接入镜像。
- [能力核实](../../docs/research/casdoor-directory-subject.md)记录准确验证范围；
  [事件架构提案](../../docs/architecture/directory-event-journal.md#casdoor-session-revocation-proposal)
  已细化已有 watcher 的按应用撤权、共享会话隔离、refresh 准入重查与持久目标重试。
- 未新增运行服务、迁移逻辑或会话撤销实现，未执行远程登录/撤权 E2E；不能据此更改发布就绪结论。
- 需求/计划索引已重新生成，覆盖、状态、Module 文档生成校验与文档站构建通过。新增补丁重新以
  零上下文格式应用到固定源码，无偏移/fuzz，产物逐字节等于测试通过的源码。Go 校验的缓存访问
  被沙箱拦截后，获准在沙箱外完成；构建仅有非阻断的 bundle 大小提示。

## r10 实施与验收进展（2026-10-03）

- `0005-directory-subject.patch` 已接入生产 Dockerfile：四种 JWT 格式的 access/refresh、UserInfo、
  签名 Logout Token 与 SAML NameID 统一取 `externalId`，目录用户缺锚点拒绝签发，恢复 OIDC 保留内部 ID。
- `0006-directory-revocation.patch` 和既有 helper 已接入按用户/client 撤权、持久目标与独立重试。
  新授权签发独立 OIDC sid，Token 保存父 Beego session，refresh 保留 sid 并重查准入。
- 撤权快照在影子状态更新前落盘，含旧主体、应用、sid、token 名与会话标识，不含 bearer token。
  确认只接受接收端 2xx；超时、非 2xx 和进程重启保留目标，旧通知重放不匹配后来独立授权的 sid。
  helper 启动和每 300 秒全量对账；同名不同锚点隔离并撤准入，不能继承旧身份。
- Netbird 固定 `0.76.1` 的普通用户 PAT 路由使用 `/users/{userId}/tokens`，JWT sub 直接成为
  userId，违反 `DIRKEY-R-013`；Casdoor 在 binding 发布和应用渲染前拒绝该组合。
- 完整固定源码归档重新应用六份零上下文补丁通过：六项历史配置边界、八项主体检查、定向撤权
  回归和两项既有回归通过。定向撤权测试覆盖刷新换行、其他 client、同一父会话后的新授权及重复撤销。
  Hook、独立 helper 和 OIDC logout Consumer 的 Go 测试/vet 通过。上述不是实机 E2E 结论。
- 实机环境为 `whl@finance.hlong.wang`（原指定 IP 指向同一主机）。独立工作区
  `/home/whl/anas-casdoor-test-20261003`，独立网络命名空间、Docker socket/data-root 和容器前缀；
  未切换业务 deployment。r10 测试镜像以缓存 r9 为 UI/系统载体，叠加固定补丁源码编译的 Linux server/helper。
  这不替代完整 Dockerfile 的双架构构建。Nextcloud 使用主机已有 r10 镜像作为协议测试容器。
- 实机初始化和协议/撤权验收仍在进行。首次镜像下载超时后改用业务 Docker 的只读镜像导出；
  Samba 首次初始化等待时间仅在隔离测试副本延长，生产 Compose 保持原设置。
- 工作树无关的 `internal/computeclient/http_publication.go` 引用已删除的 Request 字段，阻断
  `go run ./cmd/gen-module-docs`。在临时副本保留当前生成器和 Manifest 解析器，并补回仅用于编译的
  四个旧字段后，生成文档与 `--check` 通过；此结果不能记为仓库原生命令门禁通过。未修改无关实现。

发布仍缺：同一 r10 候选的真实 Consumer 账号归属、标签回收、多 client 和完整故障矩阵，SAML
应用会话终止能力，以及完整构建与平台证据。SAML SLO 不可用时留有显式诊断，不记为已完成。

下一步：完成隔离实机协议、撤权与重试验收，按实际结果更新需求对应的检查项；Casdoor 保持 developing。


## 提交时验证边界（2026-10-04）

本次提交冻结 r10 的主体标识符、定向撤权和持久重试实现、启动配置权限修复、测试夹具及文档。
不提升生命周期为 release，也不包含临时存储、应用目录或 Nextcloud/Samba 的独立实现改动。
共享文档与索引按本任务内容拆分；Module 的生成文档在只包含本任务修改的干净快照重新生成。

「确认 Casdoor 发布状态」任务的最新进展：OIDC 撤权夹具报告改名、同名账号重建隔离和普通/管理员
登出后刷新拒绝已通过；真实 Nextcloud 初次登录、LDAP 锚点绑定和文件创建通过，但改名回调返回
HTTP 400，复用原应用账号的验收失败。不存在的 sid 返回 HTTP 400 也使通知保持待重试。两 client
脚本最近一次执行非零退出，未取得完整隔离验收结论。上述问题仍由原任务调查，本提交未独立重跑
远程 E2E，不能把源码/helper 回归结果推定为真实应用会话验收通过。

升级 catalog 的目标同步到 r10；专属 verifier 保留为测试入口。真实旧版升级/回退与完整双架构
Dockerfile 构建仍未验收。首发不要求旧账号迁移；遗留升级 verifier 的旧版权限冲突仍是测试待办。


本地提交验证在只包含本任务改动的干净副本完成：

- Casdoor Hook/helper 的 Go 测试与 vet 通过；OIDC/SAML Consumer 夹具的编译及 vet 通过
  （两个夹具包没有 Go 单元测试）。
- 固定归档 SHA-256、实际生产补丁的身份/撤权源码探针通过。
- Module 文档生成及 `--check`、需求覆盖与测试用例文档、需求/计划状态、文档状态门禁通过。
- 升级 catalog 校验通过，Casdoor 当前目标为 r10；没有运行真实升级往返。
- 文档站构建、相关 Shell/Python 语法与补丁格式检查通过。构建只有非阻断的 bundle 大小提示。
- Go 缓存访问被沙箱拦截的文档/升级校验已获准重跑通过。未运行全仓库测试或本轮远程验收。
