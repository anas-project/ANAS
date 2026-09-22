---
doc_type: review
status: current
created: 2026-09-22
updated: 2026-09-22
---

# 真实 btrfs 容器租约、越权拒绝与取消回收

继续操作者指定的 `whl@ln.hlong.wang:2200`，保留“不修改既有 Docker”的边界。基线仍为
`claude/forgejo-docs-audit-20260920` / `3f5242e` 加此前工作树；未切换分支、提交或推送。
本轮不做网页检索，使用仓库、发行版已安装元数据和实际测试。关联 INCUS-R-007/R-008、
R-011、R-023/R-025、R-030—R-032、R-048/R-049；只记录验证到的子范围，不宣称整个 M6 完成。

## 测试环境与隔离

物理宿主保持 Ubuntu 22.04 / kernel 5.15，既有 Docker 不作为执行面。复用上轮保留的
Ubuntu 26.04 官方 cloud image，并重新核对原 SHA-256；创建新可写 overlay、临时 SSH
身份和 cloud-init 实例，不复用上轮已删除的可写盘。独立 QEMU/KVM 使用 1 vCPU、1536 MiB
内存、24 GiB 稀疏磁盘和用户态网络，SSH 只绑定物理回环端口，无宿主 TAP/bridge 接入。
整个 QEMU 会话有 2400 秒外部时限和低 CPU 调度优先级。

本轮根为 `/home/whl/anas-incus-lifecycle-20260922.Ua8jM4`，本机编译/检查根为
`/tmp/anas-lifecycle-20260922.i4io6K`。物理 Docker 基线由原只读 `baseline.py` 在新报告根
重新采集，不借用上轮前置快照。VM 内是 Ubuntu 26.04.1 / kernel 7.0.0-31-generic / Incus
6.0.5；官方软件包仅安装在该可销毁系统中，宿主软件源、内核与 Docker 不升级。

## 实际发现与修复

第一轮已成功创建真实 12 GiB btrfs 池、导入真实摘要的镜像并更新 project，但 Provider
在 POST `/1.0/networks?project=default` 返回 HTTP 500。原始失败留在 run01，不以之后
通过覆盖。只读检查确认 `dnsmasq` 不存在，`incus-base 6.0.5-8` 的安装元数据把
`dnsmasq-base` 放在 Recommends，而宿主安装使用 `--no-install-recommends`。

先新增声明表反例，三个发行版行全部失败，再将 `dnsmasq-base` 加入原声明式 Packages。
没有为发行版增加代码分支、第三方源或单独 DNS 服务配置。逐包归属继续区分预装与本次
安装，新增测试要求卸载保留预装 helper。测试期望最初未按既有排序规则填写而失败，按
真实的规范排序修正后相关四包通过；没有放宽归属检查。

只在实验 VM 补装 `dnsmasq-base 2.92-1ubuntu0.4` 后，**同一 Provider 二进制**的两份
ensure/重复 ensure/inspect 及完整容器测试通过。随后增加直接越权反例并以最终客户端
测试二进制重跑全套，run03 退出 0。

一条独立网络写诊断和一条运行期日志读取请求被工具安全检查拦截，均未执行；未改用另一个
工具执行这些请求。排查使用已有失败与安装元数据，后续通过原有完整测试入口取得结果。

## 新增可复现入口

`server-incus-lifecycle-e2e.py` 在所有变更之前要求 root、精确 cloud-init ID、QEMU 标识、
无 Docker，以及初始空 Incus 实例/镜像/存储池、只有 default project。使用 VM 自身的
Incus/cgroup/idmap 执行实际容器，不在物理业务宿主启动另一套容器服务。

`incus-guest-fixture` 是有界原生检查程序，真实 tar 字节的 fingerprint 为
`2d60a53cd631b83d2ba62976b45d0344895137931349d67e335cdf6613b03e81`。
它作为独立测试镜像预装到两份项目，再由实际 Provider 收敛受限租约。不伪造摘要、注册
生产 catalog 或宣称 distrobuilder 已运行，也不把预装步骤算作 Provider 镜像供给验收。

客户端测试使用真实 `NewWithContext/Create/Start/WaitForGuest/ExecStdin/Stop/Delete`，
两份 project、证书、profile 与 bridge 均由实际 Provider 处理。stdin 随机值只在内存流中，
不进入参数、镜像或报告。取消用例先确认 guest 已进入等待，再取消客户端调用，最后用
独立预算删除并读回；它不假称 CLI 退出本身已经销毁 guest。

## 最终实机结果

run03 强制要求 **13 个指定 pass 事件**：一个父用例、七个子项、五个嵌套反例，以及包终态；
不允许 fail、skip 或空匹配。结果全部满足。

| 检查 | 实际结果 |
| --- | --- |
| 两份 Provider 租约 | 各自 ensure → ensure → inspect 均报告完整 ready/restricted/quota |
| 同名实例 | 两个 project 同时 Running，分别读回各自 workload，不混用同名实例 |
| 第二实例超额 | 真实 daemon 拒绝创建，原实例库存不变 |
| 直接 CPU/内存/磁盘超额 | 绕过共享库校验的 CLI 请求被拒绝；同值合法请求正对照成功，原配置不变 |
| 直接 host disk / 另一租约 NIC | 拒绝，原配置不变 |
| stdin | 两个真实 guest 均完成随机输入摘要校验 |
| btrfs 根盘写满 | 实际写入 4,291,694,592 字节后出现限额错误，声明上限 4,294,967,296 字节 |
| 排除整池耗尽 | 开始时至少 8 GiB 可用，结束时仍有 12,326,961,152 字节可用；池总量 12,884,901,888 字节 |
| exec 取消 | 保留 context.Canceled；独立删除确认，另一租约同名实例仍 Running |
| Stop/Delete | 确认 Stopped，再删除并重复删除成功 |
| 阶段耗时 | 两份最小夹具 create/start/ready 分别 761 ms、556 ms；不是产品镜像或典型 one-job 基准 |

CPU/内存反例验证请求拒绝，不是 CPU throttling 或内存写满/OOM 实测。磁盘只覆盖此版本
的 btrfs 容器根盘，不外推 ZFS 或 VM。取消用例调用显式清理，不替代真实 controller crash、
迟到 Create 与 registration janitor；此前单元测试也不在这里被重复计为实机验收。

## 收尾验证

| 检查 | 本轮最终结果 |
| --- | --- |
| macOS / Go 1.26.6 `go vet ./...` 与 `go test ./...` | 通过；未改变包允许缓存，不算 Linux 整仓执行 |
| incushost/incusprovision/computeclient/Provider 四包 `-race -count=1` | 通过 |
| Linux amd64 原生客户端测试 | run03 全部必需测试与包终态 pass；实际执行，不是仅编译 |
| Linux arm64 测试程序与 guest helper | 编译通过，未在 arm64 执行 |
| Python harness 测试 | 七项通过，其中三项为本轮新增的 VM 准入/镜像字节测试；不当作实机案例重复计数 |
| 共享构建与升级目录 | 静态检查通过；没有执行 Docker build 或实际升级 E2E |
| Module/Contract 双语生成检查、需求覆盖、需求/计划索引、文档状态 | 全部通过，Incus 仍为 30/75 |
| `npm run docs:build` / `git diff --check HEAD` | 通过 |

真实测试完成后又独立确认实例、池、镜像、测试项目、证书和网桥均不存在，停止 VM 内 Incus
service/socket 并确认无 incusd 进程。报告先收回物理测试根，再通过核验名称的本轮 QMP
请求正常关机。QEMU 退出码 0；原 PID、QMP socket 和测试挂载不存在，回环 22127 可重新
绑定。仅删除本轮可写 qcow2、SSH 私钥/公钥、known_hosts、seed 和 cloud-init 文件；原有
基础镜像、旧实验与日志均保留。不把文件删除宣称为底层存储介质的安全擦除。

本轮物理前后比较所有字段一致：22 个 Docker 容器的状态/PID/StartedAt/RestartCount/
health、16 个网络、已有卷、Docker service/socket 身份与启动时间、配置及单元摘要、
nft stateless 规则、IPv4/IPv6 路由和命名 namespace。没有沿用上轮 IPv6 轮换解释；本轮
两个协议族实际比较都相同。未修改或重启已有 Docker，也没有逐个业务应用做功能探测。

## 源码和证据

远端根保留 `reports/lifecycle-evidence.tgz`、前后基线、VM 清理记录和最终程序。
`final-source.tgz` 包含 744 份本轮测试相关源码、内部依赖与脚本，不是整仓发行包；每份
文件有独立 SHA-256 清单。下列摘要在本机生成并在物理测试根读回相符：

| 工件 | SHA-256 |
| --- | --- |
| 最终源码归档 | `f8180383c1a694ea165fe512c57ab41a4a747e60b4dde236d7ab474495b65c2f` |
| 逐文件清单 | `005af9da8264fbc74e0c79d3fbda3925bcc1a4fbe9443e23ea0f1094ceb2749c` |
| 实际 run03 客户端测试二进制 | `5c92cb6f0477f16d719c5d4b0454ddb4f18b4978fe6e936b2c5ec878d5b3b444` |
| VM 证据归档 | `f135c376d9a3601e3e098364b76d8dbf5f65bc32161d6835aa1170d6c64c2691` |

首轮失败、第二轮生命周期通过及最终扩展反例分别保留，没有覆盖失败记录。报告不包含运行
TLS 私钥或一次性输入 secret。原始运行时钟保留，不调整服务器时间。本轮保持
`7.3.0-r2 / developing`、30/75 与关闭的生产 ingress；正式镜像、ZFS/VM/one-job、迟到
创建/crash、管理证书轮换和默认宿主动作安装仍按各自退出条件追踪。
