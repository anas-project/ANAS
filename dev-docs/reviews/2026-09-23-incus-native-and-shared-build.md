# Incus 原生管理证书、设备围栏与共享镜像构建核验

日期：2026-09-23。对应 `incus-module` 计划 M2a / M6 / M8b。
本次直接在 `/Users/whl/Documents/anas` 已有未提交改动上接续；基线 HEAD 为
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`，不是把旧提交或空工作树当成本次完整源码身份。
开始时保留 tracked patch 与未跟踪文件清单，没有 reset、提交、推送或覆盖其他改动。

## 目标与隔离范围

操作者明确指定 `ssh whl@ln.hlong.wang -p 2200`，并禁止改动现有 Docker 容器。
物理宿主只进行只读盘点以及本次私有 QEMU 文件/进程管理。软件安装、Incus、btrfs 池、
Docker 构建与测试容器都在全新可销毁 VM 内执行。VM 使用 user-mode NAT 和回环 SSH，
不创建宿主 TAP/bridge，不接入业务卷、物理块设备或业务 Docker socket。

本次物理报告根为 `/home/whl/anas-incus-20260923.p69c6ydg`，模式 0700。
开始基线盘点包含 **24 个容器、17 个网络**，并记录卷、服务进程、配置摘要、nft、路由和
network namespace。不能用昨日的 22/16 或测试开始后的快照替代这次基线。
最终物理前后对照须在本次全部 VM 清理后单独记录。

缓存 Ubuntu 26.04 基础镜像在创建全新 overlay 前再次逐字节验证，SHA-256 为
`4908fb59ccd4e87ae4e8e973b7ef56f535448eacb24a87fd787270c0048987bc`。
没有恢复旧实验系统盘或复用旧 SSH 身份。首个 QEMU run-as-group 请求被 sudo 拒绝，
保留退出日志；改用已有授权的 `setpriv` 降为 whl UID / kvm 组运行 QEMU，没有修改组成员。

## 已完成：新版真实容器生命周期

环境为 Ubuntu 26.04 amd64、内核 `7.0.0-31-generic`、Incus `6.0.5-8`，
`dnsmasq-base 2.92-1ubuntu0.4`、`btrfs-progs 6.17.1-1build1`、`nftables 1.1.6-1`。
VM 身份为 `anas-incus-lifecycle-p69c6y`，1 vCPU / 2048 MiB，SSH 只绑定物理回环 22132。
没有在该 VM 安装 Docker。使用本次交付的生产 Provider 与共享客户端原生测试二进制。

| 二进制 | SHA-256 |
| --- | --- |
| Provider | `a050279c2997b548e3eb511b9181f78827b82777dd916f013e36734a76c77aa7` |
| computeclient.test | `7ba5b56859593da7d87c989073b1d6b3093cc2a507105c8d13cacaa40079d821` |
| guest fixture | `50e538b92553defc1df5be94364ac9d69221702a8ba461cab0f1377f5fba23b7` |
| test2json | `6764c51ff7b3586eb99c9f11e0b8f834807fedae901fcf6c7141bd1746dd0cf4` |

`server-incus-lifecycle-e2e.py` 完整入口退出 0，**15 个指定原生事件全部通过**，不是只运行
新增用例或把旧 13 项结果充作新版门禁。两份 Provider project 的重复 ensure/inspect、
同名实例跨租约隔离、直接绕过客户端的配额/设备反例、真实 stdin、取消、根盘写满、
stop/delete 和清理均通过。

新增管理证书场景验证旧凭据正常访问、新凭据登记后的重叠访问、旧凭据按精确证书摘要撤销、
旧 inspect 失败以及新 ensure/inspect 成功。两份原消费者凭据未变，运行实例 UUID、
generation、创建/启动时间未变，仍能执行 guest 就绪检查。直接向 daemon 创建 proxy
设备的请求被拒绝，证明不是仅靠共享客户端参数校验拦截。

真实最小测试镜像 fingerprint 为
`387ebd6c2ad44a325919474deb3d44c3d6bd2591b3ddc98891b2bda7a01256f8`。
它来自实际归档，不是产品 Runner 或正式 catalog 条目。两份租约创建/启动/就绪耗时分别为
730 ms、554 ms。4 GiB 根盘实际写入 4,291,694,592 字节后受限；池容量 12,884,901,888
字节，测试后可用 12,326,961,152 字节，不是靠整个池耗尽制造配额通过。

入口清理读回成功；额外只读查询确认实例、存储池和信任库为空。报告归档只包含报告目录，
不含 `/run/anas-incus-lifecycle` 的 TLS/rotation 私密配置。
`reports/lifecycle-evidence.tgz` 的本地与远端 SHA-256 一致：
`d7807caf7400a3e4656a8f99aae66f19d9ba5faa9a44414108b58e292addd576`。

QMP 先核对精确 VM 名称，再请求正常关机；QEMU 退出 0，pid/socket 消失，回环端口可绑定。
随后按固定文件清单核对所有权、普通文件和 link count，再删除该 VM 的 overlay、临时 SSH
与 cloud-init 文件。原只读基础镜像和其他实验不在清理清单中。
清理证据为 `reports/lifecycle-vm-cleanup.json`。

该证据完成默认容器档的管理证书运行边界，不证明 Core 凭据事务、自动宿主轮换命令、
ZFS、VM 档、ARM64、双栈或生产 ingress。M2a/M6 保留剩余矩阵，不改 `developing`。

## 实施：共享构建输入与传输策略

新增 `check-shared-build --json` 的只读构建输入报告。构建根与匹配共享根按真实 Compose
路径解析，输入摘要绑定共享文件、Module 文件、执行位和 build 声明；运行时 `.env`、命令、
凭据、网络与挂载不进入报告。未知 build 字段、SSH/secret 上下文和凭据 build args 拒绝。
原默认文本输出保留，报告始终声明 `docker_executed: false`。

新增可销毁 VM 原生构建入口，要求独立测试 Docker socket、实际独立数据根、固定 daemon ID
与空容器库存，先执行既有 Docker 隔离 guard。对三个计算镜像分别构建源码和 build-only
staging 两种布局，共六次 `--no-cache`；比较业务二进制摘要，验证非 root 身份和精确入口。
缺少 staging shared override 时必须得到真实共享文件缺失错误，网络/镜像失败不能顶替。
任何缺项、输入后续漂移、未复核或未确认清理都不能报告通过。

首次原生构建准确暴露 Docker Hub 访问超时，原始日志与 `passed: false` 保留于 VM 的
`/home/anas-test/build-run01`，未把失败算作缺少共享路径的成功反例。修复另外发现的配置断层：
三个计算镜像的构建层与运行层均接入现有 `DOCKER_HUB_REGISTRY` 策略；已存在的
`GO_BUILDER_REGISTRY` 显式覆盖仍优先，外部模块代理回退到 `GOPROXY_URL`。
先添加回归并记录失败，再修改 Dockerfile/Compose 并验证通过。
Provider/controller 的仓库代码仍 `GOPROXY=off`；Go checksum database 没有关闭。

构建 VM 身份 `anas-incus-build-p69c6y`，与生命周期 VM 使用不同全新系统盘和密钥。
其官方包为 Docker `29.1.3`、Compose `2.40.3`、Buildx `0.30.1`；安装时临时阻止默认服务
启动，随后只启动专用 `anas-shared-build-test` daemon。数据根为
`/var/lib/anas-shared-build-test`，socket 为 `/run/anas-shared-build-test.sock`。
daemon ID 为 `4aa34ff4-eb9d-4ac5-8d54-e3987ccdb87e`，不是物理宿主 daemon。

第一份源码包 1,378 个文件，SHA-256 为
`eb0e314086d8e00a74e0cad90511a463da36e124382778cb4ff81854b5b8df63`；
镜像源修复后的第二份源码包同为 1,378 个文件，SHA-256 为
`9c41df47076831115884590eddf4e68ab18287e7e33659ae2fcdcec87864fcea`。
两个版本均单独保存逐文件摘要和权限，没有覆盖失败源码/报告。

第二轮的 Provider 源码构建通过；Forgejo 镜像也完成构建，但探针把上游模块 `v7.3.0` 与
CLI 自报字符串 `7.3.0` 混为一谈，导致最终命令失败。保留 `build-run02` 的非通过总结。
单独的**缓存构建诊断**读取实际 CLI 输出为 `7.3`，诊断不充作无缓存验收。
修正探针后增加精确路径/行数/版本校验、CLI 二进制摘要和两条路径的 CLI 一致性回归，
拒绝其他版本、开发版本、缺项、额外输出或错配二进制；固定模块版本没有改变。

第三份源码包仍为 1,378 个文件，SHA-256 为
`19921d526ceed888cffe7495ecff047af000bf47025dae227641a2a1f1c896e6`。
`build-run03` 完整入口退出 0，六次 `--no-cache` 构建全部通过，实际反例明确因缺少共享
输入失败，最终复核输入未漂移。结果如下，耗时包含镜像探针：

| 真实镜像 | source / staging 耗时 | 两条路径共同的业务二进制 SHA-256 |
| --- | --- | --- |
| Incus Provider | 226.895 s / 308.263 s | `ae5746a2fbd13e188f71f6146209e68b2024d4f02e5de5072f2ff037dc72ca2b` |
| Forgejo Actions controller | 326.798 s / 575.445 s | `e4c297868f051ab61c24832b9ad7b72dcfe09ec00c2d3fd53325ec7ee4cafef4` |
| AI Agent orchestrator | 206.900 s / 70.931 s | `1e24ec93b815b11d0c690a33d2e8047b5b0d881e9115c8df41e1c59870d1de` |

三份共同输入摘要分别为 Provider
`f3dd1a0eb4b58f197195e285cf271bbaaf8be116d8682711015adb8e3552141e`、controller
`4c47149ed6d7fcb2f6a4ff30a2c5f6b95d3aee05d9159a7bb3588f12070d9d5`、orchestrator
`06276fc7e70b7d7858a48d41d902b75b87c4b7d499847a37584aa4bdb67a2dc1`。
Forgejo 两条路径的 Incus CLI 均自报 `7.3`，共同 SHA-256 为
`a5321049b5e9b592bf027c2157d5e48a58b7652855eafc7e354624d787d146e7`。
回到当前实际 checkout 重新生成输入报告，四个 Compose 服务的输入摘要均与验收版本一致。

入口读回无测试容器并按归属清理全部测试镜像。额外独立核对固定 daemon ID、空容器库存及
测试 label 镜像为空，然后停止 `anas-shared-build-test.service` 并确认 VM 内无 dockerd。
QMP 正常关闭精确 VM，QEMU 退出 0，pid/socket 消失、回环端口空闲；按固定清单清理该 VM
overlay、临时 SSH 与 cloud-init 文件，保留基础镜像和其他实验。

失败两轮、版本诊断、成功六轮、原始输入和最终输入报告一起归档，未包含 runtime bundle
或临时 SSH 密钥。`build/reports/build-evidence.tgz` 本地/远端 SHA-256 相同：
`5ec95b619bd41f979e5488462dede9aa59c90fd8ab375346535ec5e67b1016ba`。
清理记录为 `build/reports/daemon-cleanup.json` 与 `build/reports/build-vm-cleanup.json`。

据此关闭 M8b/R-084；入口严格标注 build-only staging，不冒充完整 Core
`anas build/apply`、运行作业或签名镜像发布验收。

## 接续：宿主供给生命周期

读取生产后端发现并用回归复现：历史 `Skip` 置位 `Disabled` 后，即使后续管理连接登记成功，
该状态仍不解除。新增完整跳过→安装→配置→登记回归，并覆盖 trust 与 endpoint 验证失败。
修复仅在登记已验证且 bundle 已持久化后清除历史禁用位；安装、配置和失败登记都保持禁用，
成功登记仍保持 `compute_ready=false`。先记录断言失败，再运行回归通过。

新增 Linux 实际后端测试、精确 VM 身份/输入摘要 guard 和 Python JSON 门禁，要求父测试
加九个子项共十项均通过。它从未安装 Incus 的 VM 执行真实 install/configure/enroll/uninstall，
包括确认拒绝、skip 无效果、重复调用、默认保留包与显式删除新增包，不能用手工预先配置
daemon 替代。该入口不是 host-job 权限、Web/CLI 审批或完整消费者 bridge 的验收。

首轮新 VM 身份为 `anas-incus-host-p69c6y`，独立系统盘、1 vCPU / 2048 MiB、物理回环 SSH
22134。VM 内只预备官方 Docker 与当前构建的真实 relay/unit，不提前安装或配置 Incus。
专用 Docker data root 为 `/var/lib/anas-host-provision-test`，daemon ID
`a5a35a58-180f-41bf-9a3f-9e8924fed552`。生产后端固定的 `/run/docker.sock` 仅在这台精确
识别的 VM 内使用；没有接入物理宿主 socket，也没有放宽已有构建隔离 guard。

55 个宿主测试源文件的摘要和权限清单记录于 `host/reports/host-source-manifest-v1.json`。
测试程序 SHA-256 为 `11f88c523f85453bbe3108d4ca78e2bb2532349034e461347d5deeeab6cc3a63`；
relay 为 `ec1b27d3cd3ad73804cf47547a1f5a6f05c997881e89035a2dc8779e8e8be7a8`。
实际宿主运行结果待核对后记录，M10 不因新增入口或交叉编译而关闭。

## 当前本机回归与失败记录

本机使用缓存 Go 1.26.6，`GOPROXY=off`；首次错误强制本地工具链选中旧 1.24.2 而拒绝启动，
切换到缓存工具链后通过，未修改 go.mod 或拉取新依赖。Provider、共享客户端与构建检查的
定向测试通过；新增构建检查、传输策略回归及 28 项 Python 测试通过。

`go vet ./...` 通过。首轮高并发 `go test -count=1 ./...` 出现
`TestAddressRoutingFailedHoldUsesNormalWithdrawal/route_add_table` 的 conntrack 读回失败。
该用例随后单独连续执行三次通过（10.626 s）。保留首轮日志，不能据此宣称已修复生产问题，
也不能删除失败用例或扩大生产命令期限。随后 `GOMAXPROCS=4 go test -p 2 -count=1 ./...`
全量通过；没有排除用例。Provider、共享客户端、构建检查的 `-race -p 1 -count=1` 通过。
契约文档、升级测试目录和测试案例文档检查通过。加入后续宿主用例后仍需再完成最终门禁。

后续仍按计划保留宿主发行版/服务矩阵、生产入站身份与挂载、双栈、VM/ARM64、正式签名
镜像分发/回滚及受限 prune 等独立退出条件。没有生成正式签名身份、填充产品 catalog 或
移除生产拦截来换取“全完成”。
