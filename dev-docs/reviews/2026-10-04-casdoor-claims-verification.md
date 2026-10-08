---
doc_type: review
status: current
created: 2026-10-04
updated: 2026-10-04
---

# Casdoor 可信角色与撤权声明核对

基线：2026-10-04，`7d612be5` 加当前未提交工作树。工作树中的目录身份与撤权实现来自正在进行的
「确认 Casdoor 发布状态」任务；本次只核对调用链和运行本地测试，未修改其产品代码或执行远程操作。

## 核对结果

“现有声明包含 `sub`、`anasRole`、`ALLOW_GROUPS`、back-channel 和
`OIDC_CAEP_EVENTS=session-revoked`，Casdoor 只缺后两项实现”的说法不完全成立。

| 项目 | 当前可观察事实 | 依据 |
| --- | --- | --- |
| `sub` | 已接入目录锚点补丁；token、UserInfo、Logout Token、SAML 使用同一 ExternalId，缺失锚点拒绝签发 | `modules/casdoor/casdoor/patches/0005-directory-subject.patch` |
| `ALLOW_GROUPS` | 已生成 Group、Role 和 Application Permission；watcher 按 LDAPS 权威结果同步受管组 | `modules/casdoor/hook/iam.go`、`modules/casdoor/casdoor/helper/directory_ldap.go` |
| OIDC back-channel | 已按 Consumer 的 URI 和方法登记，声明移除后显式清空 URI | `modules/casdoor/hook/iam.go` 的 `oidcApplication`、`baseApplication` |
| 目录撤权通知 | 当前工作树已接入按应用捕获授权、撤销 Provider 会话与 token、发送 Logout Token、持久待办及重试 | `modules/casdoor/casdoor/helper/directory_revocation.go`、`0006-directory-revocation.patch` |
| 可信 `anasRole` | 仓库源代码和文档没有找到这项声明，也没有对应角色计算；任意未知属性目前映射到 `Properties.<source>`，不能据此声称角色来自受控组 | `modules/casdoor/hook/iam.go` 的 `oidcTokenAttributes`、`ldapCustomAttributes` |
| `OIDC_CAEP_EVENTS` | 仓库源代码和文档没有找到此字段、注册校验或事件签发实现；现有目录通知是 back-channel Logout Token | `internal/runner/capability.go` 的 `validateIAMClientRegistrations`、`0006-directory-revocation.patch` |

CAEP（持续访问评估协议）的 `session-revoked` 使用独立的事件类型和主体标识格式，不能把现有 OIDC
back-channel 通知等同于 CAEP 支持。协议依据：[CAEP 1.0 §3.1](https://openid.net/specs/openid-caep-1_0.html#section-3.1)、
[OIDC Back-Channel Logout 1.0](https://openid.net/specs/openid-connect-backchannel-1_0.html)。

## 本地验证

- `go test ./modules/casdoor/hook`：通过。
- `go test ./...`，目录 `modules/casdoor/casdoor/helper`：通过。
- `bash test-env/scripts/test-casdoor-identity-source.sh /tmp/anas-casdoor-fixed-source-20261003.tar.gz`：通过；
  校验固定源码 SHA-256 并应用实际镜像补丁，覆盖统一主体、缺失锚点拒绝、签名 Logout Token、sid 和定向撤权隔离。

以上结果是本地 Hook/helper 与固定源码验证，不代表 Consumer 实机会话验收通过。另一个任务仍在处理真实
Nextcloud 账号改名复用与撤权接收问题，本次未独立复验其远程结果。

## 待确认的实施输入

已请求引用内容的任务名称或链接，以确认尚未在本工作树出现的声明来源、`anasRole` 的取值和受控组规则、
CAEP 的接收 endpoint 与事件约定。在来源确认前，不把猜测写成既有通用契约，也不重复实现当前已接入的目录撤权。
