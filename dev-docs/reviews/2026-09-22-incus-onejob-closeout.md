---
doc_type: review
status: current
created: 2026-09-22
updated: 2026-09-22
---

# 系统容器 Runner 的 cgroup 修复与作业闭环

接续[真实工作流验收记录](2026-09-22-incus-onejob-acceptance.md)。本轮范围是将已定位的
cgroup 修复构建为新的不可变镜像，执行正常、显式失败、controller SIGTERM 取消和
SIGKILL 后保留 state 的恢复，并回收实验资源、核验物理 Docker 基线。不把这一范围
等同于 Incus 全部 75 项要求、其他架构/隔离档或正式发布。

仓库为 `claude/forgejo-docs-audit-20260920` / `3f5242e` 加累积工作树；不切换分支、
commit 或 push。指定物理主机仍为 `whl@ln.hlong.wang:2200`，不修改、重启或使用已有
Docker 执行测试。本机日志根 `/tmp/anas-onejob-closeout-20260922.H8BqyH`，本轮物理
证据根 `/home/whl/anas-onejob-closeout-20260922.5jmvZA`。

## 基线和隔离环境

恢复前确认旧第三轮 QEMU 已退出，原始回执为 124（外部时限退出），不是正常关机 0。
保留其回执，不把上轮未观察到的清理事后写成成功。本轮重新读取 qcow2 元数据，确认
正确基础镜像绑定、无 dirty/corrupt 标志、文件归属及私有模式、回环 22131 空闲，才恢复
`anas-runner-bake-uefkek`。继续使用 Debian 13、1 vCPU、2048 MiB、用户态网络，无
TAP/物理网桥。仅 VM 内将 /run 的容量上限调整到 512 MiB，物理宿主未 remount。
实验 Incus 实例、镜像、池、信任库存均实际确认为空，只有 default project。

新物理前置基线记录 22 个容器、16 个网络，并保存服务身份、配置、卷、nft 规则及双族
路由。最终对照须以本轮后置回执为准，不能沿用之前某一轮的通过结果。

## 修复和验收增强

上一轮实际 workflow 已完成拉取、create/start，却在 exec 向 user manager 子树写入
cgroup.procs 时被拒绝。本轮首先真实构建 `lab-r9`，验证之前的 cgroupfs 修复；它允许
exec，却在更严格的实际限额测试中失败。Podman inspect 声称内存 134217728、PID 32、
CPU quota/period 100000/100000，实际进程属于 `/system.slice/anas-podman.service`，
读取 memory.max=max、pids.max=2337、cpu.max=max 100000。没有把配置值当作内核执行。

因此最终源设计改为真正的 engine **用户级服务/socket** 与 systemd cgroup manager。
只为 runner-engine 离线启用 socket；`user@1002.service` 固定 drop-in 在系统管理器侧
建立原有 PrivateTmp/ProtectSystem=strict 等限制，由 user units 继承。任务与 engine
由同一用户管理器维护，DNS 仍使用原私有 bus。没有关闭 cgroup、删除资源限制、启用
privileged job 或使用业务宿主 socket。

Provider 的 namespace 策略沿用前轮已登记变更：系统容器档固定 profile 允许内层 OCI
namespace，仍由 project 强制非特权并禁止 raw/device/host disk。它确实扩大了 guest
可用操作范围，不再描述为“nesting 没有变化”；跨信任域和不可信输入仍须显式 VM 档。

本轮不可变镜像 gate 新增必需子项 `rootless-oci-exec-limits`，在没有手工修改 guest
service 的情况下，以 agent 身份拉取固定摘要的 BusyBox，创建并 exec 真实 OCI 容器。
脚本按实际任务 cgroup 与可见祖先计算 cpu.max、memory.max、pids.max 的有效上限，
并读取 NoNewPrivs；要求 0.5 CPU、128 MiB、32 PID 和 no-new-privileges=1，低于外层
租约以排除仅外层限额导致的误判。info 成功、控制对照通过、缺失用例或 skip 均不能替代。

前几轮只增加观测，始终保留失败：最初探针读取挂载根；后续按 membership 读取、接受
闭集 unlimited 表达式用于诊断，仍拒绝未执行的限额。最终 r9 配置与实际进程对照确认
不是单纯格式问题。新的用户管理器设计先通过源回归，再在可销毁 guest 做组合控制。
control01—03 因 systemd 总线尚未建立失败，增加原引擎 API 就绪等待；control04 因测试
资产 0400 不符合复用的私有文件读取器所要求的 0600 被拒绝，按逐文件原摘要核对后只改
这三份测试输入的 owner 模式。control05 完成原服务停止、公开新 units 安装、用户管理器
启动和全部子项，实际读回 cpu=50000 100000、memory=134217728、pids=32、NNP=1。
此控制没有 Runner token，也没有改变不可变镜像或 Provider 策略，不算最终镜像验收。

四场景使用真实 TLS Forgejo 15.0.7、Runner 13.2.0、生产 controller 循环、共享 compute
客户端和 FileStateStore。测试唯一 guest 定制是公开测试 CA；步骤状态只读实验 SQLite
的非秘密列，不写状态、不读取 token 列。SIGTERM 是 controller context 取消，不是
Forgejo 网页取消；SIGKILL 保留磁盘 state，不是 state volume 丢失恢复。

## 源码回归

全仓 `go test ./...`、`go vet ./...` 通过。computeclient、computeimage、Incus Provider、
Forgejo controller、发布工具五包 `-race -count=1` 通过。Python Incus 18 项、Forgejo
20 项共 38 项通过。Linux amd64 程序已编译并交付实验 VM；arm64 两份测试程序编译
通过但未在 arm64 运行。共享构建与升级目录只做静态校验，不算 Docker build 或升级 E2E。

## 原生执行与收尾

### 冷启动差异与最终候选

`lab-r10` 的完整构建成功，但新鲜镜像 API 未就绪；其 `.config`、systemd、user 三层
父目录实际为 root:root/0755，只有 sockets.target.wants 为 engine:actions-engine/0700。
之前的控制先运行旧引擎，已经创建可写配置目录，因此不能替代冷启动。新对照先确认引擎
不能写入原父目录，再只调整四层目录归属和模式并重启用户 socket，不修改 units、镜像或
Provider；完整 create/exec/有效限额控制通过。原配方四目标回归先失败，随后在配方与
provision.sh 中显式为全部四层设置 engine:actions-engine、0700。新增必需子项
`engine-config-owned`，避免以后再用预热过的目录掩盖问题。

最终 `lab-r11` 由真实 distrobuilder 完整构建、退出 0。默认来源未改，实验配方仅将
Debian URL 改为明确记录的 TUNA，APT 签名校验始终启用；构建工具和 Runner 输入分别
固定 SHA-256。新镜像没有手工修改 guest 服务或目录就通过全部八个子项及父/包终态，
无 fail/skip；实际内层读回 CPU 50000/100000、memory.max=134217728、pids.max=32、
NoNewPrivs=1，配置和进程身份一致。重复 build 返回 existing=true、相同 release，
没有新增 attempt，未覆盖任何旧 revision。

| 属性 | 实际值 |
| --- | --- |
| 版本键 | `forgejo-runner / lab-r11 / amd64 / incus_container` |
| fingerprint | `0820cfca3e488874e363067c470eaafbd957ffc9414fecd009d8ff15d0299e49` |
| recipe digest | `79d4169155fdc32358d39b207596cf4592d234b16636e78c5ef01d1a76b29719` |
| metadata | 692 字节，`8602b1065b520a370b2bc157e71c6b263f99709fa69b3fa101bcd7802d37cbd5` |
| rootfs | 244,748,288 字节，`ad2b45f74e3127cb8ba605e7d19656ae6c25dceb77818054be4dff401c2b9a6a` |

### 真实工作流矩阵

`closeout-matrix-r11` 的驱动退出 0、summary.passed=true。并非只运行 Runner help、
替代 API 或调用一个 shell：真实 Forgejo 生成任务、ephemeral 注册，生产 controller
供给 guest 并通过 stdin 注入 token，实际 Runner 领取任务、拉取固定 OCI 摘要、执行
步骤，再调和回收。步骤开始/结束通过仅本次实验 SQLite 的非秘密列独立观察。

| 场景 | 实际结果 | 测试计时（秒） |
| --- | --- | --- |
| 正常 | 真实步骤成功；工作状态、实例、根盘、registration 均空 | 46.855 |
| 显式失败 | 步骤实际失败，工作流 failure；同样完整回收 | 28.509 |
| SIGKILL + 恢复 | 实际 controller 被强制终止，保留 state；重启后原实例身份/创建时间不变，作业成功且完整回收 | 72.857 |
| SIGTERM | 执行中的 controller context 取消，独立清理完成；状态、实例、根盘和 registration 均空 | 26.460 |
| 未授权仓库 | 整个矩阵期间保持 waiting，从未获得 Runner 或实例 | 与矩阵并行 |

这些计时是夹具的场景墙钟，含供给/调和和回收，不是纯脚本运行性能。取消明确是
controller-context-SIGTERM，不是 Forgejo 网页取消；SIGKILL 场景保留 state volume，
不代表 state 丢失回收。TLS 验证开启，唯一 guest 定制是公开的测试 CA。

M4a/R-025 和 M5a/R-027 的默认容器运行闭环据此完成，VM/ARM64 与其他平台运行矩阵
仍归 M6/M12。普通 shell 作业也不等于容器镜像构建工作流；Forgejo 的单开关/账号撤销、
state volume 丢失、正式签名分发、两档平台发布和 production ingress 均未因此完成。

### 证据归档和 VM 内收尾

最终独立读回实例、池、镜像和信任列表为空，只有 default project，无受管 bridge。
测试 controller/Forgejo 进程不存在，TLS 测试端口 13001 可绑定；停止 VM 内 Incus
service/socket 后 incusd 不存在，8443 可绑定。选择性证据包只包含源码输入摘要、
构建记录、测试 JSONL、四场景结果、私密内容过滤后的日志和清理记录，不含数据库、
app.ini、CLI 凭据或私钥；实际 fixture password 未出现在选中文件中。

| 证据 | SHA-256 / 大小 |
| --- | --- |
| `final-r9-archive.tar`（被实际限额 gate 拒绝） | `b065eb2745aa7b5022cd338d21503564adfd908aaca7c97ed8aff01e14e33dac` / 267,120,640 字节 |
| `final-r10-archive.tar`（冷启动失败） | `806298d4576bab402e49a18c70569dd7d31ecc0a00f7cf55cb4a2d3cd4daa59d` / 267,120,640 字节 |
| `final-r11-archive.tar`（最终候选） | `68d4c3150c9fc65d6ebc53896fa17e006eaa3b0547d908093d8d06ed867fe525` / 267,120,640 字节 |
| `closeout-evidence.tar`（70 个已选文件） | `5cbd2a1b44f31823e4f187a470cc35f6bb8c1adc148b11fdbc63bdd2151a6cae` / 317,440 字节 |

### 物理宿主最终收尾

最终候选与证据包先复制到物理根 `reports/` 并独立计算上述 SHA-256，相符后才通过
核验名称的私有 QMP 请求正常关机。本轮 phase1、phase2 的真实退出回执均为 **0**。
最终确认相关 QEMU、QMP socket、pidfile 和测试挂载消失，回环 22130/22131 可绑定。

同时处理此前保留的 Ubuntu 实验文件：先确认 `K9kUYU` 没有运行中的 QEMU/挂载，
重新核对原 r5 归档和 native-evidence.tar 的已记录 SHA。随后只删除 Debian `UefkeK`
及 Ubuntu `K9kUYU` 两个确切测试根内的 lab.qcow2、guest-key、guest-key.pub、known_hosts、
user-data、meta-data、network-config、seed.iso，共 **16 个文件**。没有删除官方基础
镜像、下载校验记录、源码程序、历史归档或报告；文件移除不宣称物理介质安全擦除。
本轮实际删除回执保存在 `reports/final-cleanup.json`，不是沿用前轮“计划清理”的文字。

本轮新 baseline-before/after 独立比较全部相同：22 个 Docker 容器（状态、PID、启动时间、
重启数和 health）、16 个网络、已有卷、Docker service/socket 身份与启动信息、daemon
配置和单元摘要、nft stateless 规则、IPv4/IPv6 路由以及命名 namespace。没有修改、重启
或使用既有 Docker 作为测试执行面；此结构化基线不冒充逐业务功能探测。

### 最终源码交付边界

最终源 `go test ./...`、`go vet ./...` 通过；computeclient、computeimage、Incus
Provider、Forgejo controller 和发布工具五包 `-race -count=1` 通过。Python 38 项通过，
两份 Linux arm64 测试程序仅交叉编译。共享镜像构建上下文和升级目录是静态检查，不是
真实 Docker build 或升级验收。Go 全仓环境为 macOS/缓存 Go 1.26.6；Linux 真实结果为
本文所列不可变镜像和工作流矩阵，不将 macOS 单测等同于 Linux 全仓执行。

当前“默认系统容器 Runner 修复 → 不可变镜像 → 真实作业/中断回收 → 宿主收尾”任务完成。
M4a/M5a 更新为完成；VM/ARM64、其他发行版、state 丢失、网页取消、镜像构建工作流、
正式签名发布和生产 ingress 保持各自待验收状态。没有 commit、push 或更换分支。

最终 Module/Contract 双语生成与检查、需求覆盖、需求/计划状态、文档状态、docs:build
及 git diff --check HEAD 均通过；生成后的 Incus 完成统计为 **32/75**，Module 仍为
developing。文档构建仅有原有非阻断 chunk 大小提示，没有触发或宣称远端 CI。

物理 `reports/` 还保留 115 份相关源文件的快照（不是完整仓库发行包），以及当前三个
Linux 执行程序的摘要绑定。`source-files.json` 为 19,501 字节、SHA-256
`a3d0f59dd870331a227eba4f2b075d9c3cfb2f2a0c194f77e6fb7672499fa5fa`；
`source-snapshot.tgz` 为 178,556 字节、SHA-256
`380272bc5c329d0fef36efdc0318b1990f0cf6b4ca3ef05ed3f2f8ea7a8ec900`。
两份文件已上传到本轮物理证据目录并独立读回校验。
