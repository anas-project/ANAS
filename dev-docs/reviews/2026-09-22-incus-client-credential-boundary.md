---
doc_type: review
status: current
created: 2026-09-22
updated: 2026-09-22
---

# Incus 共享客户端凭据与子进程边界接续

基线为 `claude/forgejo-docs-audit-20260920` / `3f5242e` 加本对话保留的未提交工作树。
未切换分支、提交或推送。本轮使用本地源码与测试，没有网页调研。

## 远端阻塞与范围调整

原计划接续 `whl@finance.hlong.wang` 的诊断状态清理和独立 btrfs/guest 验收。只读预检调用
在执行前被工具安全检查拦截，不能据此认定 SSH 失败、主机不可达或诊断状态已改变。没有
改用其他通道绕过拦截，没有发起本轮远端 guest/存储/网络变更，也没有清理四个旧诊断目录。
它们的最后确认状态以[上轮报告](2026-09-21-incus-daemon-storage-validation.md)为准。
随后一个合并的本地读取命令也被拦截，所需源码改由工作区原生 read 读取；实际通过的本地
测试命令分列下文。不存在本轮 Linux 实机通过记录。

## 先复现再修复

七个初始顶层测试在旧实现上全部失败。受影响的是消费者共享库，而非 Provider 的宿主供给：

- `writeCredentials` 用 `os.WriteFile` 跟随最终符号链接、覆盖硬链接目标和已有其他租约 key；
  测试明确读回外部文件被改写。并发两份不同 TLS 身份都可能报告成功，无法保证完整凭据元组。
- 无效证书/不匹配 key 可写入文件，base64 校验失败前已经创建目录；重复相同凭据也重写文件。
- CLI 继承整个消费者环境，真实本地可执行夹具读出了其他租约、代理和默认 Incus 连接变量。
- stdout/stderr 无上限，5 MiB 夹具输出被接受；超时变成 signal-killed 错误，丢失调用方取消身份。

修复把 TLS 校验放在所有目录创建之前：只允许有界单份证书、匹配客户端私钥及正确服务端
摘要。Linux/macOS 使用同一私有配置目录内的 flock 序列化准备，锁路径不删除；目录句柄和
no-follow/exclusive 文件打开避免跟随目标链接。全部已有文件先核对，再独占创建缺项并
fsync/读回。字节不符、0600/0700 权限不符、硬链接、FIFO、锁别名或身份漂移均拒绝，不截断、
不自动 chmod、不删除不确定文件。相同字节不重写，失败私有部分文件需显式处理。
所有权、0600/0700 与单硬链接判据复用已有 `internal/securefs`，不维护另一份判据。

原 `New` 接口保留，新增 `NewWithContext` 将准备、锁等待和初始 CLI 纳入最多 30 秒预算。
构造时复制镜像 allowlist。`execRunner` 只传本租约连接目录/project 与固定 PATH/locale；
stdout 4 MiB、stderr 64 KiB 超限主动取消子进程，返回固定错误且不返回部分输出。取消保留
`errors.Is` 语义，nil stdin 关闭；退出后管道等待由 `WaitDelay` 限制。没有新增依赖。

## 测试与限制

新增 11 个顶层 Go 行为回归。原来使用假 DER/假 key 的凭据测试改为生成真实 X.509/TLS
材料，没有把生产密码学检查改松来保留旧夹具。测试使用真实本地文件、链接、FIFO、flock、
并发 goroutine 与可执行 shell 夹具；CLI 替身不访问 Incus、不使用网络或业务凭据。

| 检查 | 当前结果 |
| --- | --- |
| 原七项回归在旧实现上复现 | 七项失败，含外部文件改写与继承环境证据 |
| computeclient、Provider、Forgejo controller 定向完整包 `-count=1` | 通过 |
| computeclient `-race -count=1` | 通过 |
| 最终源码 `go vet ./...` / `go test ./...` | 全仓通过；非修改包允许 Go 缓存，未启用实机夹具 |
| 最终源码 computeclient `-race -count=1` | 通过，包含凭据竞争、锁等待取消和真实子进程边界 |
| Linux amd64 / arm64 | computeclient 与 Forgejo actions-controller 共四份测试二进制编译通过，均未在 Linux 执行 |
| Contract/Module 双语文档生成与检查 | 通过，编辑源文件并生成镜像 |
| 需求覆盖、需求/计划索引与状态检查 | 通过，Incus 仍为 30/75 |
| 共享构建与升级目录 | 静态检查通过；未执行 Docker build 或升级 E2E |
| 双语 `docs:build` / `git diff --check HEAD` | 通过；构建仅原有非阻断 chunk 大小警告 |
| 远端 btrfs/guest/配额/诊断清理 | 未执行；工具拦截不计作执行 |

首轮全仓测试发现 `cmd/anasd/subprocess_inventory_test.go` 仍保留已不存在的
`(execRunner).Run os.Environ` 豁免。删除该过时豁免；新 `exec.go` 只使用带 context 的
构造环境，不加入任何新豁免，原导入图扫描仍会拒绝日后重新引入的不受控调用。

最终源码在统一复用 `securefs` 后重新跑完整回归、竞态与双架构编译，成功终态已确认。
本机结果日志为 `/tmp/anas-client-securefs-final.Of2ZLD`，编译输出目录为
`/tmp/anas-client-securefs-linux.cwVbGd`。这些是连接的 macOS 工作机路径，不是远端执行
证据或独立源码发行包。首轮审计清单失败和远端工具拦截均未被后续通过结果覆盖为成功。

凭据锁只覆盖准备阶段，不能当作运行期凭据轮换协调或对同 UID 任意恶意写入的隔离。既有
CLI `remote add` 的完整重启幂等、实际 TLS 登录、真实共享客户端 guest 生命周期尚未验收。
`NewWithContext` 是共享库新增入口，未改变现有调用者的信号装配。子进程取消也不代表 daemon
中的 guest 已停止，仍须独立 cleanup/Janitor 验证。本文不将以前的原生通过记录算作本轮证据。

模块保持 `7.3.0-r2 / developing`、30/75，生产 ingress 未开放。后续仍需在可执行的已授权
远端通道上确认旧诊断状态，并继续独立 btrfs 池与真实消费者生命周期/配额测试。
