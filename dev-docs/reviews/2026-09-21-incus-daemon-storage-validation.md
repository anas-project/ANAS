---
doc_type: review
status: current
created: 2026-09-21
updated: 2026-09-21
---

# 指定主机独立 Incus daemon 存储验证与同步创建修复

承接 [原生读回修复](2026-09-21-incus-native-readback-fixes.md)，仍使用操作者指定的
`whl@finance.hlong.wang`。基线为 `claude/forgejo-docs-audit-20260920` / `3f5242e` 加
本对话保留的未提交修改；没有提交、推送或丢弃其他改动。Module 仍为 `7.3.0-r2 / developing`，
完成统计不因部分实机测试而提高，生产 ingress 继续关闭。

## 实际发现

独立 Incus 6.0.5 在私有 namespace 中可完成初始化，`/internal/ready` 和 `/1.0` 均响应。
以直接 Unix API 创建 dir 池、读取和删除都成功；不应继续把此前整个脚本超时归因于“存储池
创建一定卡住”或仅凭 idmap/cpuset 日志认定根因。

显式对照中，继承原运行环境的 CLI 建池在等待 daemon ready 前后都于 12 秒超时；同期直接
API 立即成功。daemon 调试日志记录直接 POST 的 HTTP 状态为 **201**，而 envelope 为 sync/
status_code 200。这暴露宿主客户端只接受 HTTP 200 的实际缺陷：资源已创建却返回未确认。

先新增协议回归，在旧实现下池与证书 POST 都失败。随后在原生用例中运行旧测试二进制，
真实建池同样被误判；原生用例仍通过独立读回清理已经创建的池，没有重发 POST。修复后的
Unix 客户端创建→读回→删除→确认消失通过。修复复用于固定证书 HTTPS 响应处理，但此原生
用例不假称调用过 HTTPS 客户端的每个写方法。

允许范围只扩大到 POST 的 HTTP 201，成功 envelope、无错误、无未确认 operation 和后续
资源读回要求不变。GET/PUT/PATCH/DELETE 的 201、204、冲突 status、错误内容、重复字段、
async 等反例继续拒绝，错误不回显 daemon 私有正文，也不自动重试已提交创建。

## 原生测试入口与边界

新增 `test-env/scripts/test-incus-daemon-native.sh` 与其 Python 行为检查器。要求明确的新
报告根、发行版解包依赖和预编译测试/Provider/test2json；不联网下载或安装包，不启用系统
Incus service/socket，不使用现有 Docker。外层创建新 mount/network/PID namespace、
重挂 proc、限定整体寿命，内部验证和父 namespace 不同且只有 lo。挂载传播为 private，
依赖 overlay 只读，私有 `/run` tmpfs 明确设为 0755；其内的测试状态、配置和密钥为私有权限。

首轮 native-before 在首次读取前被生产祖先权限检查拒绝：tmpfs 默认权限不合要求。修正的
是实验挂载 mode，不是放宽生产检查。保留该失败记录；native-before-permissions 才是
真正到达 HTTP 201 缺陷的旧二进制对照。

新的 CLI 对照使用干净环境与关闭的 stdin，分别以目录配置、显式 socket、force-local 读取
空池清单，三种均成功。再使用固定本地连接执行原先超时的 `storage create`，由独立 Unix API
确认 Created 后删除；`native-cli-create/cli-control.jsonl` 记录成功。它确认这条受控执行路径
可用，但没有逐一隔离旧继承环境的所有变量，不声称某一个环境变量或 idmap/cgroup 就是旧
超时根因。原生门禁也在该最终入口完整通过。

`TestNativeIncusUnixStorageLifecycle` 使用真实 `incusUnixClient` 和固定实验 socket，
要求 root 所有的独立 namespace 证据。普通回归未启用该夹具时会明确 skip；专用门禁要求
该具名用例和包终态 pass，skip、空匹配、子进程失败均不能成为通过。

真实 Provider 七项检查通过：dir 池正对照、重复拒绝、缺失 project 的只读 inspect、错误
server pin 拒绝、缺失池拒绝、完整租约相关库存不变、测试池最终移除。没有启动 guest，
没有把 dummy fingerprint 或 dir 池当产品镜像/受支持配额存储。

## 证据与验证

远端本轮根：`/home/whl/anas-incus-daemon-20260921.U7DRF4`。
`native-before/` 保留错误权限夹具记录；`native-before-permissions/` 保留旧客户端反例；
`native-fixed/`、`native-final/` 与 `native-cli-create/` 保留修复后通过记录。`host-client.jsonl` 是逐 Go 测试事件，
`provider.jsonl` 是 Provider 行为结果；没有复制完整证书、私钥或业务规则到仓库。

已实机执行的 Linux amd64 测试二进制 SHA-256：
`11c5323fe3cc9c34abf38f50bae87eaf34737242ee0dcceb44a5f15e5de3fdd4`。
旧和新二进制都来自本机 Go 1.26.6 / GOPROXY=off 的明确源码切片。Linux-only 用例实际在
指定主机执行，不把交叉编译或前轮通过记录冒充本轮执行。

本轮七份源码/测试/脚本的稀疏补丁归档已上传上述远端根，需与此前完整工作树结合使用，
不是独立完整源码发行包。`storage-validation-source.tgz` 的 SHA-256 为
`b1a640788dc5d0eabc9d87317543c0b98ae655541e75a1b36ef865fad5a36908`；逐文件清单
`source-files.sha256` 的 SHA-256 为
`cd32cd72889e05cd70cdc7ae01914b764a39ed8f622ce98b3c76de2e4db40c0f`，远端读回一致。

| 检查 | 当前记录 |
| --- | --- |
| HTTP 201 协议反例 | 旧实现失败；修复后 incusprovision 全包通过 |
| 旧二进制实机同步创建反例 | native-before-permissions 明确 fail，已创建池按原生清理路径删除 |
| 修复后二进制原生池生命周期 | native-fixed、native-final、native-cli-create pass，未跳过 |
| CLI 清单与创建对照 | 三种读取成功；固定本地连接、干净环境和关闭 stdin 的创建/独立 API 读回/删除成功 |
| 真实 Provider 拒绝/pin/只读/库存 | 七项通过 |
| Python 检查器协议边界 | 四项单元测试通过，不作为 daemon 实测 |
| 本机全仓 `go vet ./...` / `go test ./...` | 通过；未修改包允许 Go 缓存 |
| 本机两包 `go test -race -count=1` | incusprovision、modules/incus/provisioner 通过；不是 Linux 原生竞态运行 |
| Linux arm64 测试程序 | 交叉编译通过，未在 arm64 执行；一次旧 session 查询失效，重新编译取得明确成功终态 |
| Module/Contract 文档、需求覆盖、需求/计划索引和文档状态 | 生成与全部检查通过，Incus 统计仍 30/75 |
| 共享构建、升级目录 | 静态检查通过，没有执行 Docker build 或升级 E2E |
| 双语文档构建与 diff | `npm run docs:build`、`git diff --check HEAD` 通过，保留原有非阻断 chunk 大小警告 |
| 宿主基线 | nft、Docker 容器 ID、命名 namespace 相同；35 条 IPv4、49 条 IPv6 路由仅 expires 变化 |
| 清理 | 未发现本轮 incusd 或测试根挂载；原生入口私有 tmpfs 随进程退出释放；早期诊断目录仍保留，详见下节 |

新增三个 Go 顶层测试入口（其中一个需 Linux 原生夹具），另有四个 Python 协议/隔离
前置边界测试。后者不启动 daemon，未与实际 Provider 的七项行为检查混为一个计数。

## 清理状态：运行资源已退出，早期诊断文件未清完

默认 Docker 仍 active，默认 Incus service/socket 仍 inactive，没有操作业务容器启停。
实际主机规则/路由基线的逐项对比存于 `reports/baseline-result.json`。容器集合相同不是
全部业务健康检查。没有重新使用或清除上轮别的实验根和命名 namespace。

新原生入口的 daemon 状态、客户端配置和私钥均在私有 `/run` tmpfs，成功返回后随 namespace
退出释放。但早期定位实验使用了本轮根下的 `state-diagnostic`、`state-cli-control` 和对应
`client-diagnostic`、`client-cli-control` 四个持久目录。删除调用被工具拦截，未计为执行。
随后只读确认它们仍存在：两个 `server.key` 均为 root 所有、0600，外层本轮根为私有目录。
这不是现有业务凭据，但仍属待清理测试状态。没有为了回收文件放宽权限，也没有将这些密钥
复制到仓库或源码归档。日志、源码和二进制证据按计划保留。

## 仍未完成

本轮尚未验证 btrfs/zfs 真实磁盘限额或 guest 创建/start/exec/delete，没有修改宿主 subuid/
subgid、cgroup、块设备或已有存储池。目标仍没有 KVM。6.0.5 官方包响应也不代表已验证 7.3.0，
完整发行版/镜像、one-job、轮换、生产中介和默认安装退出条件均未完成。原 CLI 超时尚无
完整挂起栈证据，不能因新的独立执行路径成功就宣称所有原因已经确认。
