# Incus 消费者控制桥原生接续

状态：实施与原生验收核对。日期：2026-09-23。

沿用 `/Users/whl/Documents/anas` 的已有暂存和未暂存改动，HEAD 为
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`；没有提交、推送、reset 或覆盖既有工作。
需求归属为 Incus M10 的管理连接/网络边界，沿用 R-047—R-051，不扩展 guest/生产 ingress
权限。上一轮[已安装审批记录](2026-09-23-incus-installed-approval.md)的第十一轮已取得
完整 17 项通过，但其宿主本地 mTLS 探测不证明 Docker 消费者侧可达。

## 实施边界

新增 `test-env/helpers/incus-control-probe`，仅为测试辅助程序，不进入正式发行、宿主动作
清单或生产消费者。由固定来源的静态 Go 二进制生成 scratch 镜像，不下载基础镜像。
实际测试容器使用 UID/GID 65534、只读根文件系统、空 capability 集、no-new-privileges，
不使用 host network/PID、bind mount 或 Docker socket。容器/image ID、创建标签、网络
ID 与安全选项逐次读回，结束和删除也只接受本轮精确拥有的资源，不 prune。

管理连接在产品完成真实登记之后由 root 测试所有者读取，只经容器 stdin 传入；密码、
token、管理私钥不进入 Docker 环境、argv、镜像层、挂载或公开报告。探针只接受固定端口
的私有 IPv4 HTTPS endpoint，固定 GET `/1.0`，关闭代理和重定向并使用精确 server DER
pin。探针结果只包含 schema、模式及通过布尔值，不回显 endpoint 或响应正文。

完整已安装审批入口增为 21 项，在 enroll 后、uninstall 前增加：真实控制桥来源的
trusted mTLS、错误 pin 拒绝、无证书只得到 untrusted、另一 Docker bridge 即使持有正确
证书也无法建立 TCP。网络拒绝不能由 TLS 失败或任意错误替代；错误 pin 必须命中具体
pin verifier。清理后容器、网络、卷和镜像库存须恢复，再继续原审批链路卸载。

这是管理传输与来源边界验收，**不是**使用受限消费者证书的 project/配额验证，也不证明
Core/Compose 自动投影、双网络默认出口、跨重启接口恢复、IPv6、VM/TAP 或生产 HTTP
入站。原管理密码禁令、证书 pin、未完成环境拒绝及 `compute_ready=false` 保持不变。

## 来源和运行

第十二轮使用指定 `ssh whl@ln.hlong.wang -p 2200` 上新的独立 VM
`anas-incus-host-ce455a`，Ubuntu 26.04 amd64、KVM 单核/2048 MiB，SSH 仅绑定物理宿主
回环 22146。产品工件与第十一轮逐摘要一致，只更新受信测试入口与静态探针。
`root-action-v7/source-manifest.json` 绑定 392 个源码输入、父 manifest、产品工件及
测试 helper；runner 摘要为
`58262954ed74315c3ad485a0d138d47b3a74e9d2bef89b6e0bbd3df8e6d8920e`，probe 摘要为
`d3204c40f2f6d77f2fa25476343051f590c8d0bc0c6972c847186f1b6a7f1abe`。

本轮外层监督拥有 QEMU 进程句柄；不论入口成功或失败，都先收集仅 reports 的归档，再
核对 QMP 精确 VM 身份并发起正常关机，等待退出后核对 socket、回环端口和物理宿主基线。
VM 超时、收尾失败和测试失败分别记录，不再依赖后续聊天调用完成正常收尾。
没有对物理宿主原有 Docker 容器、网络、卷或服务执行写操作。

新增 helper 回归和 Python 容器安全/结果形状回归已先失败后通过；实际原生终态及最后
全仓检查在本记录后续小节列出，不能从单元回归推导。

## 第十二轮：创建态网络读回

第十二轮在已完成真实登记后，测试自身的 `probe_container_identity` 断言失败，尚未
向探针传入管理凭据或发起连接。失败归档摘要为
`b3a1490c5475b74cb06f5baacb2e0c63736682189b1d3e75db75e830b331e21f`。
监督器自动取得报告、精确 QMP 关机、QEMU 退出 0；物理宿主全部独立对照项相同。
实验 Docker 的基线为 false，因为失败保留了受管桥和新建探针资源，不删除后伪造通过。

离线只读检查这张已停止的 VM 磁盘确认：容器 UID/GID、只读根、空 capability、
no-new-privileges、无挂载及 HostConfig 的精确网络 ID 均正确，但未启动容器的实时
`NetworkSettings.Networks.*.NetworkID` 为空。测试原先过早要求了已连接状态。
修复仅在 `created`、未运行、PID=0 且 HostConfig 仍精确匹配时允许该空字段；实际探针
执行和退出后的检查仍要求精确网络 ID。运行/退出状态下的空 ID 及不匹配的安全选项均
由新增回归拒绝，没有放宽宿主防火墙、产品身份或证书验证。

## 第十三轮：消费者传输与真实确认过期

新 VM 为 `anas-incus-host-539c03`、回环 22147，其他硬件/发行版隔离条件相同。
产品工件和静态 probe 与第十二轮逐摘要一致，入口与新增回归由
`root-action-v8/source-manifest.json` 绑定；runner 摘要为
`868b31bd7220b79fd49c1fbf12cabb6aec6828416fa56a95986ba7942a314fd6`。

本轮将完整门禁扩为 23 项，额外验证真实五分钟过期：从计划创建时间核对服务端期限，
未用 token 在生命周期执行期间自然过期，随后旧 token 和旧计划续签均须准确返回
`409 confirmation_expired`。只能通过新的 plan/确认完成后续动作；不快进时钟、不缩短
TTL、不改 ledger，不把 consumed 或认证失败当成过期证据。浏览器重新展示/勾选仍是
另一条交互验收，不由 API 成功推导。

## 第十三轮终态与收尾

完整 **23 个不同必需阶段全部通过**，测试退出 **0**，14 个共享 job 成功，独立 journal
观察到 14 个 hostd 激活单元，结束时无活动 executor。四个消费者传输门禁实际执行并
通过；五分钟自然过期后，旧 token 执行与旧计划续签均准确拒绝，新计划/确认后再次卸载
成功。没有修改系统时间或 ledger，也未因等待过期而重发原写请求。

本轮恢复的不仅是空容器/网络：临时 probe 容器和 scratch 镜像均按精确身份删除，实验
Docker 的容器、镜像、网络和卷库存及 daemon ID/数据根全部恢复。随后监督器取得仅含
公开 reports 的归档，核对 QMP 身份后正常关机；QEMU 退出 **0**，socket 与回环 22147
端口均释放。物理宿主原有 24 个容器、17 个网络、卷、Docker 服务/配置/单元、nft、
IPv4/IPv6 路由及 named netns 的独立对照全部一致。

远端公开证据：
`/home/whl/anas-incus-followup-20260923.4p9ob_nk/host-v13/reports/root-action-run01-evidence.tar.gz`
SHA-256：`9dcd09cb55ea783e26ae52ecc1a72c9af8a7a070ed4f9c2409945b22c9fcb118`。
`supervisor-result.json` 独立记录 test_exit=0、qemu_exit=0、正常关机已请求且无监督器错误。
归档又独立解析确认恰好 15 份 JSON：完整 summary 与 14 份成功且不需补偿的 job 摘要。

当前源码已通过全仓 `go test -p 2 -count=1 ./...`，incusprovision、hostaction、
jobexecutor 与新 probe 的 race，全仓 `go vet`，**51 项** Incus Python 回归，以及
`bash -n install.sh`。Linux arm64 的 probe 与 hostaction 测试程序交叉构建通过，
不称为 ARM64 原生验收。日志在本机 `/tmp/anas-incus-bridge-20260923.WUcC2x/all-gates.log`，
退出码 0；文档生成与最终差异检查单列，不由代码门禁推导。
