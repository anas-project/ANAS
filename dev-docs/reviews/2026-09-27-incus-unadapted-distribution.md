# 未适配发行版上的宿主供给审批链路（INCUS-R-094、R-057、R-048）

状态：实机验收记录。日期：2026-09-27。输入与 Core 轮相同的快照提交 `c0119e85`（`0.0.0-native.15`）。

## 环境

`ln.hlong.wang` 上的一次性 QEMU VM，底座为 Debian 14 “forky” testing 的每日 genericcloud 镜像
（SHA-512 按官方 `SHA512SUMS` 核对，SHA-256 `ac06c2cc…b39c`）。`/etc/os-release` 为
`ID=debian`、`VERSION_CODENAME=forky`、无 `VERSION_ID`，不是编译声明表的三行之一，内核新到足以
运行宿主动作通道（需要 `SO_PEERPIDFD`）。安装步骤只为实验 Docker 夹具装 `docker.io`/`docker-cli`，
Debian 归档经保留官方签名的阿里云镜像读取，不添加任何 Incus 来源。

新增入口 `test-env/scripts/server-incus-unadapted-e2e.py` 复用已安装审批夹具
（`server-incus-host-action-e2e.py` 的真实 CLI/HTTPS owner、共享 job 与 socket 激活的 systemd hostd）。

## 结果（`host-unadapted-r6`）

| 阶段 | 结果 |
| --- | --- |
| `installed_release_identity` | 同版本 anasd/hostd 身份与服务单元核验通过 |
| `https_owner_enrollment` | 通过 |
| `installed_preflight_disabled` | 已安装预检返回 `disabled`、`distribution_not_adapted`、`compute_ready=false` 并给出手工指引 |
| `install_plan_leaves_compute_disabled` | 安装计划只有一步 `unsupported`（leave compute disabled），没有包或服务步骤 |
| `confirmed_install_records_disabled` | 确认后执行返回 `disabled`，只写 `install.disabled` 回执 |
| `host_unchanged` | dpkg 库存、APT 源与 keyring、`/etc/anas` 下 Incus 配置不变；没有 `/usr/bin/incus`、`/opt/incus`、`/var/lib/incus`；实验 Docker 基线不变 |

终态：正常关机、QEMU 退出 0、清理通过、物理宿主十项对照相同；归档 SHA-256
`732df00d4ac02df6b3fc2fcf6a59b3edab654ca17d09be9f050d4dcfd90720dc`。

## 失败轮

r1—r5 都保留在实验根：r1/r2 是 Debian testing 的 Docker 包拆分（CLI 在 `docker-cli`、`dockerd` 在
`/usr/sbin`），r3 的错误未带诊断码，r4 是我方准备脚本以 umask 077 重写输入丢掉执行位
（`input_identity`），r5 的判定器把回执名写成 `install.unsupported`——无配方行的主机走的是
`install.disabled`。它们都不是产品行为问题，产品在 r5 已经给出了与 r6 相同的禁用结果。

## 范围

它证明未适配的发行版在真实审批链路上保持关闭、不自动安装、不改宿主，且不以失败终止；不覆盖
ARM64、非 systemd 或 Core 渲染带 compute 消费者时的提示路径（后者见 Core 轮对撤销连接的拒绝）。
