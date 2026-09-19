---
doc_type: review
status: current
created: 2026-09-19
updated: 2026-09-19
---

# 宿主动作最终接线核对

本轮在既有只读 `incus.status` 基础上补齐宿主动作的产品入口接线：编译期清单新增
`incus.install.plan`、`incus.configure.plan`、`incus.enroll.plan`、`incus.uninstall.plan` 以及对应
`incus.install`、`incus.configure`、`incus.enroll`、`incus.uninstall`。入口仍只接受动作 id 与类型化
JSON 参数，不接受命令、argv、路径或脚本。

计划动作经同一 host action job 队列执行，结果包含 `incusprovision.InspectResult`、待 apply 的公开
`parameters` 以及动作 ABI 的确认摘要。执行动作必须用 5 分钟一次性 confirmation token；Store 在创建
apply job 前消费 token，root 对端在写动作前再从固定 `/run/anas/confirmations` ledger 独立 claim 已消费
批准。`incusprovision.Binding` 只作为后端 drift 校验输入，不作为授权 token。

HTTP、CLI 和 Web 均走同一套共享 Store、ActionRecorder、broker 和审计路径。HTTP 新增 plan、confirm、
apply 路由；CLI 新增 `anas host incus-plan`、`incus-confirm`、`incus-apply`；Web 维护页提供最小可用的
阶段选择、计划、确认和执行入口。公开 response 不投影连接 bundle、私钥或 root 端错误细节。

## 实际验证

- `GOCACHE=/private/tmp/anas-gocache go test ./internal/runner ./internal/consoleclient` 通过。
- `GOCACHE=/private/tmp/anas-gocache go test ./internal/api/httpapi ./cmd/anasd` 通过。
- `GOCACHE=/private/tmp/anas-gocache go test ./internal/jobexecutor -run 'TestHostActionService|TestHostActionBinding|TestActionRecorder|TestModuleActionDispatcherDoesNotConsumeOrExposeHostActions'` 通过。
- `GOCACHE=/private/tmp/anas-gocache go test ./internal/hostaction -run 'TestHostActionRegistry|TestHostExecutionUsesExistingAuditWriter|TestPreflight|TestInstallation|TestActivation'` 通过。
- `cd web && npm run generate:api && npm run test -- src/api/maintenance.test.ts && npm run typecheck` 通过。

## 未通过与限制

- `GOCACHE=/private/tmp/anas-gocache go test ./internal/hostaction ./internal/jobexecutor` 在当前 macOS sandbox
  失败，失败点均为 Unix socket `listen ... bind: operation not permitted`。未放宽测试或改用假成功。
- 未执行真实 root/systemd/Incus/KVM 验收；本轮没有修改真实系统、SSH、sudo、服务或网络。

## 剩余风险

生产可用性仍依赖另一线程完成 `internal/incusprovision` 的真实宿主后端、服务安装迁移和 systemd 原生证据。
本轮只确认了受限通道、确认 ledger、队列和产品入口的本地逻辑接线。

## 2026-09-19 final-integration follow-up

本轮继续修复 host action 接线闭环，限定在 host action/API/CLI/Web/packaging/install 相关路径内；未执行 git add/commit/reset，未运行 sudo、真实 systemctl/service/apt/nft/SSH。

### 已修复

- broker 执行预算从固定 15s 改为已认证 canonical request 对应的编译期 `ActionSpec.Timeout` 加 3s 握手余量；输入帧仍保持 3s，避免未认证输入选择长超时。
- `HostJobBinding.matches` 不再要求 `LastSeq == 0`，允许同一 ActionRecorder 写入非终态 progress 后继续校验当前 running job。
- apply 参数边界拆成 public/wire 两层：public 参数拒绝额外 reserved key，wire 参数必须携带完整固定 confirmation reserved set，额外未知 `_...` key fail-closed；`prepare` 走 wire canonical，confirmed job 不再被 reserved 字段拒绝。
- Incus 请求默认值在摘要前 canonicalize，避免 plan 省略默认值、apply 展开默认值造成 full request digest 漂移。
- confirmation 签发增加 plan action 与目标 apply action 校验；ledger 对同一过期 binding 不再重新签 token。
- ledger claim receipt 返回可信绑定字段；root claim 后在副作用前核对 action、apply job/invocation、full frozen request digest、plan digest、release digest，并且 claim store `Close` 失败时 fail-closed。
- host action projector 允许固定 phase 的 progress 事件，仍拒绝任意自由输出和敏感错误。
- CLI `host incus-apply` 拒绝 `--confirmation-token` argv secret，改为 `--request-json -` 单一 stdin envelope 同时传 session、plan job、token、parameters；普通 `incus-confirm` 输出不再打印 token，JSON 模式仍可供管道消费。
- `anas-hostd@.service` 运行预算改为 615s，开放 AF_UNIX/AF_NETLINK/AF_INET/AF_INET6，并移除会阻断包管理/网络动作的窄 capability/no-new-privileges 限制；仍保留 `ProtectSystem=strict` 和显式写路径。
- release 包新增同版本 `anas-incus-control-relay` binary 与固定 service unit；install 会安装 binary/unit 但不默认 enable relay。uninstall 会先 stop hostd socket、hostd template instances、relay 和 anasd，且不再吞掉 systemctl stop/disable 失败后继续删除二进制。
- OpenAPI 类型已重新生成，Web API 维护测试和 typecheck 通过。

### 本轮实际验证

- `GOCACHE=/private/tmp/anas-gocache go test -o /private/tmp/hostconfirmation.test ./internal/hostconfirmation` 通过。
- `GOCACHE=/private/tmp/anas-gocache go test -o /private/tmp/hostaction-nosocket.test ./internal/hostaction -run 'TestRegistry|TestIncusParameters|TestClaimConsumed|TestExecution|TestHostExecution|TestPreflight'` 通过。
- `GOCACHE=/private/tmp/anas-gocache go test -o /private/tmp/jobexecutor-nosocket.test ./internal/jobexecutor -run 'TestHostAction|TestHostBinding|TestHostService'` 通过。
- `GOCACHE=/private/tmp/anas-gocache go test -o /private/tmp/runner.test ./internal/runner -run 'TestHost'` 通过。
- `GOCACHE=/private/tmp/anas-gocache go test -o /private/tmp/actionabi.test ./internal/actionabi -run 'Test.*Confirmation|TestProtocol'` 通过。
- `GOCACHE=/private/tmp/anas-gocache go test -o /private/tmp/consolejobs.test ./internal/consolejobs -run 'Test.*Confirmation|TestAction'` 通过。
- `GOCACHE=/private/tmp/anas-gocache go test -o /private/tmp/httpapi.test ./internal/api/httpapi -run 'TestHost|TestOpenAPI|TestSecurity'` 通过。
- `GOCACHE=/private/tmp/anas-gocache go test -o /private/tmp/cmd-anasd.test ./cmd/anasd -run 'TestHost'` 通过。
- `GOCACHE=/private/tmp/anas-gocache go test -o /private/tmp/consoleclient.test ./internal/consoleclient` 通过。
- `GOCACHE=/private/tmp/anas-gocache go test -o /private/tmp/cmd-anas-hostd.test ./cmd/anas-hostd` 通过。
- `bash scripts/ci/install-test.sh` 通过；该测试使用 fake systemctl，没有操作真实服务。
- `cd web && npm run generate:api && npm run test -- src/api/maintenance.test.ts && npm run typecheck` 通过。

### 仍未验收

- `GOCACHE=/private/tmp/anas-gocache go test -o /private/tmp/hostaction-full.test ./internal/hostaction` 在当前 macOS worker sandbox 失败于 Unix socket `listen ... bind: operation not permitted`。
- `GOCACHE=/private/tmp/anas-gocache go test -o /private/tmp/jobexecutor-full.test ./internal/jobexecutor` 同样失败于 Unix socket bind。
- 未做真实 Linux/systemd/root/Incus/KVM/apt/nft/relay 端到端验收；需要父线程在允许 AF_UNIX socket 和 systemd 的环境继续跑。
- `anas-hostd@.service` 为支持包管理器必须允许系统包路径写入；这不是全盘可写，但仍需 Linux systemd 安全审计确认与实际 action 后端路径一致。
