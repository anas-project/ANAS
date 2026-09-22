---
doc_type: review
status: current
created: 2026-09-21
updated: 2026-09-21
---

# 指定 finance 主机的 Incus Linux 验证

本轮由操作者明确指定 `ssh whl@finance.hlong.wang`。验证的是
`claude/forgejo-docs-audit-20260920` / `3f5242e` 加前轮未提交工作树，不是该提交的干净版本。
保留此前暂存、未暂存及未跟踪修改，没有切换分支、提交或推送。

**结论：已执行 Linux 回归和真实隔离网络测试，但整体验收未通过。** 12 个选定包中 11 个
测试二进制退出成功，Runner 的 Incus 定向回归通过；7 项强制内核用例中 3 项通过、4 项失败。
独立 HTTP 原型的 20 项报文/连接行为检查通过，不替代生产中介、真实 guest 或 Docker/Incus
防火墙共存验收。没有解除生产 ingress gate，Incus 保持 `7.3.0-r2 / developing`、30/75。

## 主机与执行边界

| 项目 | 实际观测 |
| --- | --- |
| 系统 | Ubuntu 26.04 LTS，Linux 7.0.0-30-generic，x86_64 |
| 资源 | 约 3.2 GiB RAM、8 GiB swap，根文件系统可用约 133 GiB |
| 权限 | `whl` UID/GID 1000；SSH 已有可信主机记录，`sudo -n` 可用 |
| 既有 Docker | 29.7.2；主机有 11 个业务容器，未用于实验 |
| 原生工具 | nftables 1.1.6、iproute2 6.19.0、systemd 259 |
| Incus | 系统未安装；官方 Ubuntu 包 6.0.5-8 仅解包至测试目录 |
| KVM | `/dev/kvm` 不存在，未发现 vmx/svm 标志；VM 用例未执行 |
| 其他限制 | 远端没有可用 Go/gcc；没有配置真实 Btrfs 应用 E2E 夹具 |

远端独立根为 `/home/whl/anas-incus-verify-20260921.EY5yZQ`。原有
`anas-upgrade-e2e` namespace 未接管。软件包只通过现有官方源下载并解包，不执行系统安装，
不添加源，不安装/启动默认 Incus systemd 服务，也不修改默认 Docker daemon。

root 测试使用新的 mount/network/PID namespace，并先将挂载传播改为 private。测试依赖以
只读 `/usr` overlay 提供，`/run` 使用私有 tmpfs；进入后核对初始网络只有 lo 且没有 nft 表。
外层 `timeout` 和 PID namespace 约束子进程寿命，网络、挂载和实验 daemon 不在宿主业务空间运行。
原始业务规则与路由快照只留在私有报告目录，不复制进仓库。

## 源码、二进制与重跑

初始源码压缩包 SHA-256：
`244e72632ea99d17ad596f6d3bb460687c9e3ab4aa93ef3eba23918357aca1b9`。
初始测试二进制压缩包 SHA-256：
`212622bd981cfe02a202b6007ffcf7d1f60098da534d720398e7478fa4cd01b4`。

用本地 Go 1.26.6、`GOPROXY=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64` 编译后，
通过 SSH 在目标主机实际执行；不是仅交叉编译。预编译 `test2json` 保留逐测试事件，
`-test.count=1` 禁用测试结果复用，`GOMAXPROCS=2`、`-test.parallel=2` 限制并行度。
本轮没有 Linux `-race` 运行，不将前轮 macOS 竞态测试计入本轮证据。

首轮不是可用通过记录：远端默认 umask 让部分临时目录成为 0775，严格私有目录检查拒绝；
快照漏带 packaging/部分仓库元数据；少数源码审阅测试通过 runtime.Caller 找到的是编译机路径。
随后以 `umask 077` 重跑，补传 packaging、`.github` 和升级夹具，并为 Runner/anasd 编译
使用从本地仓库路径到实际远端 `src` 路径的 `-trimpath=old=>new` 映射。没有放宽生产权限检查。
初始记录保存为 `reports/packages-initial/`，修正后的完整包记录为 `reports/packages/`。

为定位 nft 失败，另给 `nft_native_linux_test.go` 增加了失败时输出**该测试独立 namespace**
的实际规则和预期 AST 的诊断；重新编译并在远端运行，记录于
`reports/ingress-native-diagnostic.jsonl`。这是初始快照后的明确测试源码增量，不是生产修复。

## 普通 Linux 回归与 broker 原生验证

| 范围 | 本轮结果 |
| --- | --- |
| computeingressruntime、computeingress、computeclient、computeimage | 四个完整包退出 0 |
| incusprovision、modules/incus/provisioner、modules/incus/control-relay | 三个完整包退出 0 |
| hostaction、jobexecutor、cmd/anasd、cmd/incus-network-prototype | 四个完整包退出 0 |
| internal/runner 完整包 | 未通过，原因分列下文 |
| Runner 的 Compute/Incus/运行锁/独立轮换定向测试 | 退出 0，未跳过；`runner-incus-focused.jsonl` |
| 非 root broker 原生及执行所有权定向测试 | 两包退出 0，未跳过；原门禁所需 7+9 个父用例均有 pass 事件 |

broker 运行设置 `ANAS_REQUIRE_HOST_BROKER_NATIVE=1`；用预编译二进制执行原门禁对应用例，
而不是在无 Go 的主机上声称原 shell 构建入口执行成功。它验证真实 Linux socket/process
身份等边界，不等于 systemd 激活、root 执行或真实 Incus 供给已验收。

Runner 完整包仍有四类测试准备/夹具失败：私有 umask 下一个保持原 0640 模式的夹具实际初始
只有 0600；Module 打包集成测试要求 Git 元数据而快照没有 `.git`；Hook 编译用例找不到远端
Go 工具链；两个 server-identity 根目录 YAML 夹具未包含在传输集合中。真实 Btrfs 用例也没有
配置。不能由 Incus 定向通过反推 Runner 全包通过，更不能将这些条件缺失改为“无需验收”。

## 强制内核用例：3/7 通过

实际使用 `ANAS_REQUIRE_INGRESS_NATIVE=1`，没有将 skip 当通过。

| 用例 | 结果与实际错误 |
| --- | --- |
| TestNativeCommandPinsExecutableDescriptor | 通过 |
| TestNativeNamespaceSwitchRestoresOriginal | 通过，success/error/panic/cancel 四个子用例通过 |
| TestNativeConntrackBidirectionalCleanup | 通过，真实 conntrack 删除目标元组并保留相邻对照元组 |
| TestNativeAddressRoutingCannotFollowDeviceReuse | 失败：`isolated veth lacks a peer index` |
| TestNativeNamespaceKernelIdentityAndCookie | 批量失败：`opened network namespace cookie does not match installation` |
| TestNativeNFTScriptAndReadback | 失败：`owned nft baseline rule identity or order changed` |
| TestNativeReplyOriginRejectsSpoofAndDeviceReuse | 失败：同一 nft baseline 读回校验错误 |

对 namespace cookie 用例又执行了独立单用例对照，实际通过，见
`reports/namespace-single-control.jsonl`。因此批量结果仍失败，但原因可能包含测试间 namespace
干扰；目前不能认定生产 namespace 执行器出错，也不能用单用例成功替换批量失败。

nft 诊断在 1.1.6 的实际 `-j -a -n` 输出看到：ct state set 为数字 `[2,4]`，比较器预期
`established/related` 字符串；带 IP payload 的规则省略了预期中的冗余 EtherType 条件；
单独 IPv6 EtherType 读回为 56710，而现有归一化只认另一数值表示。当前代码因此拒绝确认
自身基线。后续须保留完整约束/顺序的正反测试，不能通过忽略未知表达式或删除比较来“修复”。
veth 对端索引的实际输出兼容和 namespace 批量干扰仍待进一步定位，本轮未声称已修复。

## 真实 HTTP 原型：20 项通过

使用本轮编译的 `incus-network-prototype` 与仓库 `observation.json` 生成实验工件，再运行
`test-env/scripts/server-incus-http-netns.py`。真实 veth/bridge/nft 和 Python HTTP peers
全部位于隔离 namespace；外层再包一层隔离以避免误操作业务空间。

20 项检查覆盖加载前正对照、指定源接口路由、批准 HTTP、未批准端口、同 bridge/其他 bridge
来源、IPv6 后端、guest 反向连接、IP/MAC 冒用拒绝、合法来源恢复、已建立持续流、撤销后的
持续流停止与新连接拒绝，以及 30 秒许可到期后拒绝。全部通过，原始结果在
`reports/http-netns.log`，退出码 0。

该工具内部的 `host_rules_unchanged` 对应它的父 namespace；本轮还另外比较了**实际主机**
基线，不能只用嵌套工具的输出宣称宿主未变。此原型不覆盖真实 guest/VM 身份、Traefik 公网
TLS/认证、Docker/Incus 原生防火墙共存、生产中介自动装配或受管 IP 重分配。

## 独立 Incus 探测：响应成功，后续超时

使用官方 6.0.5-8 包的独立数据根、证书与客户端目录，仅在私有网络中启动。首次准备缺少
`setfattr`；补充解包 attr 后第二次启动成功，`/1.0` 返回并记录
`PASS isolated Incus daemon responds`。紧接的测试池创建未在总预算内完成，外层退出码 124。
因此计划中的实际 Provider `dir` 池拒绝测试没有执行到验证点，不能记作成功。

daemon 日志另有缺少 system idmap、不可用 KVM、host cpuset 读取错误，以及隔离网络无法查询
外部 instance-types 的提示。这些是实际环境/隔离差异，未通过修改业务主机权限、出网或 cgroup
来绕过。不能从日志单独认定超时根因。6.0.5 的响应也不代表 7.3.0 已兼容或发布通过。

## 清理与证据位置

测试结束后实际主机 nft stateless 全量规则与前置快照逐字节一致；Docker 容器 ID 集合及既有
命名 namespace 列表一致。路由前后均为 84 条，差异只有 IPv6 RA 的 `expires` 字段，去掉该
动态字段后完整内容一致。未发现 incusd/test2json/本轮测试二进制进程或测试根相关挂载残留。
Docker 仍 active，默认 Incus service/socket 仍未运行。

原有 frpc 容器首次检查就只有数秒 uptime，结束检查同样如此；本轮没有对它执行启停，不能将
容器集合一致解释为每个业务应用健康。没有改变已存在的其他测试 namespace。

所有报告、源码快照、二进制及解包依赖保留在上述私有测试根；临时 daemon 状态、客户端目录
及其中凭据在确认进程/挂载已退出后按本轮白名单清除。日志保持私有，不上传宿主规则或密钥到
仓库。一次批量回收报告因 root 所有的 API 快照不可读而部分失败；用例日志、退出码与基线文件
已逐项读回，未把未复制的 API 原文假称已审阅。

## 后续

收尾已运行本地 `go test ./internal/incusingresshost`、`go vet ./internal/incusingresshost`，
以及需求/计划索引生成、需求覆盖、索引一致性、文档状态和 `git diff --check HEAD`，均通过。
这里的本地检查不替代上文已经失败的 Linux 强制内核用例；本轮只增加失败诊断，没有提交
生产逻辑修复。最终清理再次确认本轮 daemon 进程及挂载不存在，两个私有 daemon 状态根和
客户端目录均已删除，未删除日志与源码证据。

先修复并复测 nft 原生读回兼容、veth 索引与 namespace 批量测试隔离；补全远端 Runner 测试
所需源码/Git/Go/权限夹具。独立 daemon 的池创建超时、idmap/cgroup 环境需另外定位，之后才
推进 btrfs/zfs 配额、容器创建/执行/删除、one-job、轮换与回滚。VM 需要真正可用的 KVM 目标。
生产启动、health、VM/TAP、持续地址身份和完整发行版/镜像矩阵不因本轮部分通过而取消。
