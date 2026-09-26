# Incus 一级发行版与证书登记兼容性

状态：实际发行版接续核对。日期：2026-09-23。

最新矩阵：Debian 13、Ubuntu 26.04 和 Ubuntu 24.04 的三个独立 amd64 VM 已用相同
`.8` 产品工件分别通过新版 **25 项、18 个作业和 18 次退出观察**，包含精确受管包删除、
原有包保留和重复卸载。正常关机与物理宿主对照均通过，实际摘要与边界见
[同工件三发行版终态](2026-09-23-incus-storage-inventory-compatibility.md)。下文的 Ubuntu
23 项记录与 Debian 准备失败是较早快照；它们不被改写，也不再代表最新完整门禁结果。

本轮从已有 dirty checkout 继续 M10，未提交、推送或修改指定服务器的既有 Docker。
主要环境 Ubuntu 26.04 amd64 的完整 23 项结果见
[消费者控制桥与过期验收](2026-09-23-incus-consumer-control-bridge.md)，不据此推导其他
发行版通过。本记录只追加实际独立发行版运行，不复用旧环境来消除 failed intent。

## 独立测试输入

在 `ssh whl@ln.hlong.wang -p 2200` 的私有实验目录取得两个官方 cloud image 及同站点
checksum 清单，下载后完整核对摘要、qcow2 格式及无 backing file，再以只读 backing 加
独立写盘运行。验证边界是官方 HTTPS 与 checksum 清单，不宣称验证了发行签名。

| 系统 | 输入文件 | SHA-256 |
| --- | --- | --- |
| Ubuntu 24.04 amd64 | `ubuntu-24.04-server-cloudimg-amd64.img` | `612b2c0cc1bc413a6cb8c38fd611794caf0f2b436c50013d8b3794db12ad7354` |
| Debian 13 amd64 | `debian-13-genericcloud-amd64.qcow2` | `5754395abffb1d384d50f6d0945d46d1beb7be42a7e786e4fc4a6f27270ab16f` |

Ubuntu 来源为 `cloud-images.ubuntu.com/releases/noble/release/` 的 SHA256SUMS；Debian 来源
为 `cloud.debian.org/images/cloud/trixie/latest/` 的 SHA512SUMS。原清单与来源元数据保留在
远端实验根的 `distribution-inputs/{ubuntu-24.04,debian-13}`。这里的基础镜像不是默认
ANAS guest 镜像，也不属于正式 guest catalog 发布。

## Ubuntu 24.04 首轮实际失败

第十四轮 VM `anas-incus-host-262caa` 已通过真实 owner、共享 job/hostd、确认、安装与配置，
在 `enroll.trust` 失败。实际安装版本为 `incus 6.0.0-1ubuntu0.3`，旧私有状态保留：
disabled=true、credential 已持久化、bundle 尚无，trust intent 为 failed。
这不是服务配置、池、网桥或防火墙失败，不把执行成功的其他步骤一起标成未实现。

此轮测试退出 1，VM 由外层监督正常关机、退出 0；物理宿主全部独立对照一致。失败公开
报告归档摘要为 `87a7e607799d1236a0dc8be406907f63a969f8089bc40869200db4e6635e6425`。
停止后的 VM 通过只读分区导出/debugfs 查询固定状态字段；派生 raw 文件已删除，没有
启动旧盘、修改它的记录或将完整凭据归档。

源码核对发现，已有 Provider 在 POST `/1.0/certificates` 使用 base64 DER，但宿主
登记路径直接发送 PEM。新增协议测试实际复现这一差异，另验证错误名称/摘要、缺失 PEM、
多证书、私钥或尾随内容不能触发写请求。宿主路径改为先校验固定管理名称、实际 DER 与
持久 fingerprint，再只把公钥证书转换为 base64 DER 发出一次请求；私钥不进入请求，
没有重试或宽松响应分支。原状态、bundle 与 GET 读回仍用 PEM，不改变批准凭据或权限。

实现位于 `internal/incusprovision/incus_api.go`，回归为 `certificate_encoding_test.go`。
incusprovision、hostaction、jobexecutor 与 Provider 相关回归通过；完整新发行版终态另记。

## 重跑门禁与产品身份

后续使用 `root-action-v10/source-manifest.json` 的同一批源码绑定工件，明确实验版本
`0.0.0-native.20260923.3`。393 个输入在编译前后摘要一致，不冒充正式签名发行。

| 工件 | SHA-256 |
| --- | --- |
| anas | `5ed728ab949c69458165d1f0b13d68a9b96b9e32a8d166a922818e3d04c4cb01` |
| anasd | `f4eecd2d2dfa8de73419c92af1b05f237114cdb1e3698db9e6fc9cc0e9797c99` |
| hostd | `8b89417c5bbbde1b6fee84708d13cc93da955a2757c7faaa767f524233121062` |
| runner | `8d4d21f68ef73bea255aa973ecff1d242af0e01753410aed94046b1d43e4fdb7` |

保持 23 项门禁，同时加强证明：每个阶段须为精确预期 disposition，不能把 disabled 或
partial 算成安装成功；每个负向消费者探针后立即再从正确控制桥验证 trusted mTLS，
避免把服务退出误判成网络或认证拒绝。失败报告只投影固定阶段/步骤/状态枚举，不输出
endpoint、证书、密钥、任意 detail 或完整 root 状态。新增脱敏诊断后共 15 项 Python
局部回归通过，之前 51 项整体数字只对应其原版本。

Ubuntu 24.04 新 VM 为 `anas-incus-host-f6ed4e` / 22149（第十五轮）；Debian 13 为
`anas-incus-host-7c3bda` / 22150（第十六轮）。二者串行使用 KVM 单核/2048 MiB，避免并行
资源竞争掩盖实际故障。每轮监督器独立归档、校验 QMP 身份、正常关机及检查物理宿主基线。

## 第十五轮终态：Ubuntu 24.04 完整通过

接续时已直接复核远端公开归档、监督器结果和独立宿主对照，而不是仅采用此前运行中的日志。
第十五轮完整 **23 项**全部通过，归档含 summary 与 **14 份**成功 job 报告，实验 Docker
库存恢复。监督器记录 test_exit=0、qemu_exit=0、正常关机已请求且无收尾错误；精确 QMP
socket 不存在、回环 22149 可绑定。物理宿主原有容器、网络、卷、Docker 服务/配置/单元、
nft、双栈路由和 named netns 全部与该轮基线一致。

归档 `host-v15/reports/root-action-run01-evidence.tar.gz` SHA-256 为
`8511c9c5a017753545ed86c6aed58cdee3d1d2cfa6664a079633b223359e5900`，实际字节与监督器
保存的摘要一致。Ubuntu 24.04 的实际审批/消费者传输/过期门禁因此有独立证据；这不等于
全部 M10、VM/ARM64、完整自动 Compose 投影或正式镜像分发已完成。

## 第十六、十七轮：Debian 仍停在实验环境准备

两轮都尚未运行产品审批入口。第十六轮下载 cloud image 默认 Sources/Packages 索引时
未完成；第十七轮已将实验 Docker 准备限定为官方二进制索引，但仍在 trixie/main 的
9.7 MB Packages 下载阶段被外层准备期限终止。不得把它们说成 Incus 证书登记再次失败，
也不能用 Ubuntu 的结果代替 Debian 验收。

第十六轮监督器记录 `PowerdownTimeout`，随后 QMP quit 收尾，QEMU 退出 0；这不是已确认
的正常关机。第十七轮记录准备步骤 `AssertionError`，QEMU 最终退出 0。两轮没有产品
reports 归档，不能伪造摘要；原 install/serial 日志保留。接续已核对两轮 QMP socket
消失、各自 SSH 回环端口释放，物理宿主全部独立对照项相同。
