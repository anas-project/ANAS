---
doc_type: review
created: 2026-09-23
updated: 2026-09-23
---

# Incus 宿主卸载前置盘点与原生验收接续

对应 `incus-module` 计划 M10，重点为 R-048、R-051；不代替完整发行版矩阵、宿主动作
审批通道、消费者网络可达性或生产 ingress 验收。

直接修改 `/Users/whl/Documents/anas` 已有未提交工作树，基线 HEAD 为
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`。保留先前的 Provider 围栏、共享构建、宿主
原生夹具及其他未提交文件，没有提交、推送或重置。按用户要求不做互联网搜索。

## 发现与实现

原卸载只在开头检查运行中的受管实例，然后依次删除 bundle、信任、relay、防火墙、网络
和存储池。停止/冻结实例、保留卷、profile 对池的引用，以及仍连接控制网络的 Docker
端点可能直到后续删除才被发现；这会在明确仍有依赖时先撤销管理连接。

新增 `CheckUninstallResources` 作为每次确认卸载的强制只读前置检查，使用独立的 30 秒
总预算。按资源引用而非实例名称前缀判断占用，要求池引用/卷和网络端点有完整证据。
缺失/null 清单、选定字段的大小写别名、换了不可变 ID 的同名网络，以及池查询成功后的
卷列表 404 均不能作为空库存。取消在变更前再次检查。拒绝不创建变更 intent，不修改
管理连接和资源归属，排空后可重新确认，而非进入不确定副作用的恢复流程。

显式删除本次安装的软件包时，还检查共享 daemon 上后来新增的对象。包的安装归属不
授权删除管理员后来建立的实例、project、池、托管网络、profile、镜像或其他信任。
各删除仍单独复核归属/占用和缺席读回；预检不是跨 API 原子锁，不宣称消除了所有外部
管理员并发修改的窗口。

另外修复空的 `ControlBridge` 与实例不存在的 `parent` 字段相等时误判无关实例的问题。
Docker root 客户端改为固定网络生命周期允许列表，不接受 connect、disconnect、prune、
容器 API、可变名字删除或拼接路径。卸载不会为了删除网络而主动断开其他容器。

## 本机验证

新增回归先在旧实现上失败，复现提前撤销连接、取消漏检和空网桥误匹配；随后实现修复。
原协议夹具补充明确的空 `used_by`，而不是放宽生产检查以接受缺失数据。

`GOPROXY=off GOMAXPROCS=4 go test -p 2 -count=1 ./...` 已实际通过，退出 0。
初版宿主 Python guard 的 5 项单测通过；`git diff --check` 在该阶段通过。
一次定向命令误写了不存在的 `internal/hostcommand`，整体为 setup failed；同一命令中
实际存在的三个包均通过，后续全量命令已重新验证，不将误写命令报告为通过。

Linux amd64 和 arm64 的原生验收测试程序均已交叉编译成功；arm64 只证明构建，不是
实际运行验收。真实 amd64 relay 和 `test2json` 同步构建。源码清单记录 214 个本地依赖
与嵌入输入的字节摘要，以及实际 Go 1.26.6、工作树 HEAD 和二进制摘要。

本轮第一份 amd64 原生程序 SHA-256：
`25eb511d5535c86ce245c128f7f25839b491a6754b326881c2eccf64b44b902e`。

后续 `go vet ./...`、三个相关包的 `-race` 检测、模块与契约文档生成核对、共享构建静态
门禁、升级测试覆盖、需求与计划状态检查、Web API 生成核对均已执行通过。新监督器的
8 项单测通过，Incus 相关 Python 单测合计 38 项通过；在下面的 Linux VM 中也实际执行
了相同 8 项监督器单测，退出 0。

## 指定 SSH 主机与隔离实验

目标为用户指定的 `ssh whl@ln.hlong.wang -p 2200`。起始只读基线记录物理宿主
24 个 Docker 容器、17 个网络，以及卷、服务 PID/启动身份、配置摘要、nft、路由和命名
network namespace。所有新增实验资源都在本次私有目录下，旧 VM 盘和报告保留不动。

本轮实验根为 `/home/whl/anas-incus-followup-20260923.4p9ob_nk`，新 VM 的精确身份是
`anas-incus-host-b14801`，1 vCPU / 2048 MiB，回环 SSH 端口 22135。缓存官方基础镜像
创建 overlay 前再次校验 SHA-256：
`4908fb59ccd4e87ae4e8e973b7ef56f535448eacb24a87fd787270c0048987bc`。

部分工具请求被安全检查拦截，未执行；最小只读 SSH 随后成功。提权的 KVM 启动请求
没有执行，实际另用普通 whl 用户运行 QEMU TCG。没有修改主机组成员或 `/dev/kvm`
权限，不把该实验描述为 KVM 或 VM 计算档验收。新 VM 已启动，核对到精确 cloud-init
身份、QEMU DMI、Ubuntu 镜像内核 `7.0.0-31-generic` 和 systemd running。

VM 仅使用 user-mode 网络和回环端口，不挂载物理 Docker socket、业务目录、块设备，
不建立宿主 TAP/bridge。Docker 安装和专用数据根的 daemon 只能在该精确 VM 的双重
身份 guard 之后执行，起始容器必须为空，不操作物理宿主的现有容器。

## 原生门禁范围与当前状态

完整入口增加 `uninstall_preflight_preserves_retained_storage`，现在要求父测试及十个
子项共 11 个事件全部 run/pass；旧 10 项报告不能满足新门禁。新用例建立精确归属的实验
custom volume，验证卸载拒绝时连接、所有权、intent/receipt 保持，再按精确身份清除
该实验卷并读回缺席。没有删除整池、重置状态或自动绕过不确定 intent 的处理。

第一轮已实际执行：缺少确认拒绝、显式跳过且没有宿主变更两项通过；官方包安装阶段失败，
后续配置/登记/保留卷/卸载没有运行。失败时仍为 disabled，没有凭据或 bundle，存在
`install.packages` failed intent 和对应失败 receipt。Incus、incus-client、dnsmasq-base
仍未安装；不能将该结果报告为安装成功或完全没有副作用，APT 配置和索引缓存已写入。

### 第一轮失败原因与监督器修复

旧 Python 入口对子进程设置 32 MiB 的 `RLIMIT_FSIZE`，原意是限制日志，但它也限制了
APT 及其子进程的普通文件写入。真实现场的 universe 包索引恰好停在 33,554,432 字节，
`apt-get update` 失败，生产后端正确保留了不确定副作用的拒绝状态。

新增回归先复现 40 MiB 数据文件不能正常写入，以及日志预算没有被单独执行。改为父监督
器按 stdout/stderr 管道分别限额，超限时停止本次进程组并拒绝验收，不再对子进程施加
普通文件大小限制。进程身份在发出组信号前保持未回收；即使两条输出流先关闭，运行期限
仍然有效。没有通过改生产错误处理、修改包来源或删掉 failed intent 来恢复“成功”。

第二版交付清单记录 215 个源码/测试输入，Go 源码未变，复用上述已编译原生程序；只更新
监督器和增加对应测试输入。新监督器 SHA-256 为
`887b5fc0ce8e6ea0b21e78a5e3ddd79b599a0b3d9c484b2b12853618cfdfe6d8`。
重跑采用单独的新 overlay：`host-v2`，VM 身份 `anas-incus-host-81c335`，回环 SSH 22136。
第一轮失败磁盘、intent 与报告保留，不作为可重新确认的干净宿主使用。

第二轮完整生命周期结果仍待记录；M10 和三发行版支持矩阵保持未完成。物理宿主最终的
Docker、服务与网络状态前后对照须在本轮全部实验 VM 停止后填写。

## 非特权 broker 原生验证

在指定物理宿主以 whl 的非 root UID/GID 1000 运行预编译测试程序，范围限定私有临时
Unix socket、实际进程身份及作业归属；不安装服务、不使用生产 socket、不调用 Docker。
内核为 `5.15.0-186-generic`。9 项 job-owner 必需项通过；7 项 hostaction 必需项只有
2 项通过，整体门禁失败，其中两个进程身份用例明确拒绝缺少 `SO_PEERPIDFD`。
没有将这些失败变成可跳过，也没有升级物理宿主内核或修改权限。

同一组二进制随后在内核 `7.0.0-31-generic` 的上述 QEMU VM 中以非 root UID/GID 1000
执行，7 项 socket/pidfd 与 9 项 job-owner 必需项共 16 项全部 run/pass，两个包退出 0，
没有 skip。该正向证据不替代真实安装的 root/systemd 宿主审批通道，也不把物理宿主
内核 5.15 标为支持。

二进制摘要：`hostaction.test` 为
`12bd59cddf204062f26d2d6c78dac113365debbbe251f9614d9d43482c0db424`；
`jobexecutor.test` 为
`9f877dd2906ec359be4c3e1bb00238e44a672f06a4e7e2301cb706cf6523d672`。
物理宿主结果位于实验根 `broker/reports`，VM 结果位于
`/home/anas-test/native-diagnostics-v2/reports`。首轮 native 四份公开报告已归档到
`host/reports/native-run01-evidence.tar.gz`；broker 报告的归档请求被工具拦截，没有执行，
报告保留在第一轮 VM 磁盘内。

第一轮 VM 经 QMP 协议读回精确名称 `anas-incus-host-b14801` 后请求正常关机，
QEMU 最终退出码为 0，已确认该实验进程消失。没有删除它的磁盘、root 状态或实验报告。
后续才启动第二轮独立 VM，未同时运行两个实验虚拟机挤占物理宿主资源。

本轮公开文档站点已实际 `npm run docs:build` 构建成功；仅有既有的大 chunk 提示，
没有通过忽略当前文档死链来关闭门禁。

## 仍需独立验收

Debian 13、Ubuntu 24.04/26.04 的完整支持矩阵，未适配发行版降级、宿主审批/进程身份、
消费者 bridge 与 LAN 隔离，不能由本机协议测试或一个后端 VM 生命周期替代。VM/ARM64、
ZFS、双栈、正式镜像签名分发和生产 ingress 继续沿各自里程碑执行；Module 保持
`developing`，不改生产发布开关来消除未完成状态。
