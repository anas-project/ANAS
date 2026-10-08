# Incus 租约围栏、归属与 7.x 限制键实机核验

状态：实机核验记录。日期：2026-09-26。基线：`0b614488` 加累积工作树；Provider 由工作树构建。
对应[审查](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-25-compute-contract-incus-review.md) §1.1—§1.3 的修复与 7.x 限制键处理，
需求 `INCUS-R-008`、`INCUS-R-011`、`INCUS-R-052`、`INCUS-R-106`，并补充 `INCUS-R-085` 的证据。

## 范围与隔离

操作者授权使用 `ssh whl@ln.hlong.wang -p 2200`。物理宿主只做只读盘点和本次私有 QEMU 文件/进程管理；
Incus、存储池、证书与测试都在全新一次性 VM 内。VM 使用 user-mode NAT，SSH 只绑物理回环端口，
不接 TAP/bridge、业务 Docker socket、块设备或共享卷；一次只运行一台 VM。每轮开始和结束各记录宿主
Docker 容器/网络/卷、服务身份、配置摘要、nft 与双栈路由并逐项比较。

实验根为 `/data/anas-incus-fence-20260926.VtWvkv`（0700）。各轮只删除本轮固定的 VM 文件清单，
报告与证据保留在该目录。

| 输入 | SHA-256 |
| --- | --- |
| Provider（`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath ./modules/incus/provisioner`） | `04c34634eec72b0eeee33769dd00e4d085f4b4ed2c47af3f4da1fd41c496f8aa` |
| `test-env/scripts/server-incus-fence-e2e.py` | `19c164aa6348c66bb45ff6c479cf52f0cfc7d077157760a62f7edd6c715b702f` |
| VM 镜像 rootfs 夹具（64 MiB 空 qcow2，从不启动） | `1b7af784841125887e688254229e282829e17245d0e47fe7280da9b68a30d9e1` |

## 入口做什么

`server-incus-fence-e2e.py` 直接调用生产 Provider 二进制（`ensure`/`inspect`/`revoke`，经环境变量），并用
受限客户端证书绕过共享客户端直接请求 daemon。限制拒绝以同步错误加 daemon 原因字符串判定；通过项目
准入以返回异步操作（HTTP 202）判定。Provider 错误只含 HTTP 状态，daemon 拒绝原因另由管理 CLI 复现。
必需检查按 daemon 代际固定：6.0 为 16 项，7.0 加存储池越界与 `restricted.images.servers` 共 18 项，
7.5 再加 VM nesting 共 19 项；缺项、失败或多出的失败项都使整轮失败。

## 结果

| 轮次 | 系统与 daemon | 来源 | 必需检查 | 证据归档 SHA-256 |
| --- | --- | --- | --- | --- |
| r1 | Ubuntu 26.04 / Incus 6.0.5-8 | 官方 Ubuntu 仓库 | 16/16 | `df56c47874ff8bcde3c53fdf2932c5d2175546722f5458c057e045b19b17f528` |
| r4 | Debian 13 / Incus 7.0.1-3~bpo13+1 | Debian trixie-backports，经 `mirrors.aliyun.com` | 18/18 | `1ff8b44111a69485366edd8e4b82368dba34aa92db50202570ed49a64b2859d2` |
| r3 | Ubuntu 26.04 / Incus 7.5.1（`1:7.5.1-ubuntu26.04-202609250208`） | Zabbly stable，签名密钥指纹 `4EFC…C838 DCFD` 校验 | 19/19 | `e97a9a7cc633f81ad34dbc9e7f3fa24bca3b3c13046ff3ffa2a3cd5019ca8237` |

daemon 声明的限制扩展与预期一致：6.0.5 无；7.0.1 有 `projects_restricted_image_servers`、
`projects_restricted_storage_pool_access`；7.5.1 另有 `projects_restricted_virtual_machines_nesting`。

三档共同通过：

- **围栏完整性（R-011）**：未标记、带全部放宽限制的旧 project 被采纳，写入键收紧为严格值，
  idmap/网络/磁盘路径等清除键被删除，`user.*` 保留；再放宽一个键后 `inspect` 报未就绪且配置不变，
  `ensure` 重新收紧。
- **daemon 拒绝冲突的收紧**：已有特权容器时收紧 privilege 被拒（`Privileged containers are forbidden`）；
  已有容器时切换到 VM 档被拒（`"limits.containers" is too low`）。两种情况 Provider 都失败关闭，
  project 配置不变、证书未登记。
- **隔离档（R-008/R-052）**：容器档租约请求 VM、VM 档租约请求容器都被 daemon 同步拒绝
  （`Reached maximum number of instances of type ...`）；同档请求通过项目准入。6.0.5 与 7.0.1 的 VM
  随后因 VM 内没有 QEMU 而创建失败，属主机虚拟化能力，不是围栏结果。
- **租约归属（R-106）**：第二个工作区（同消费者、同 sandbox、不同证书）被拒；所有者证书 `revoke` 后
  仍被拒且标记保留；未标记且被另一受限证书信任的 project 被拒（错误列出该证书指纹前缀）；
  已就绪租约在额外受限证书存在时 `inspect` 未就绪，移除后恢复；`default` sandbox 被拒。

7.x 专有：

- 7.0.1 与 7.5.1 上 `restricted.storage-pools.access` 写为租约池，租约把根盘改到另一池被拒
  （`is not accessible from this project`）。6.0.5 没有该键，同一请求被接受，这是 6.0 上的已知缺口。
- 7.0.1 与 7.5.1 上设置 `restricted.images.servers` 后，从 project 内本地镜像创建实例被拒
  （`Image server "" isn't allowed in this project`），证实它不能作为「禁止远端镜像」开关；Provider 删除它。
- 7.5.1 上 `restricted.virtual-machines.nesting=block` 且租约 profile 写 `security.nesting=false`，租约 VM
  请求实际创建成功（Zabbly 包自带 QEMU）；显式 `security.nesting=true` 的 VM 被拒
  （`Virtual machine nesting is forbidden`）。没有单独运行「profile 不写 false」的反例；该情况下 VM 被拒
  来自 7.5.1 源码。

## 过程中的故障

- **r1 宿主基线**：测试通过、QEMU 正常退出 0，但结束快照（03:00:15）落在物理网卡 `enp1s0` 断链期间
  （内核 igb 日志 03:00:12 Down、03:00:34 Up）。9 月 19 日 03:00:08 有同样 22 秒断链，此前大量 VM 关机
  未触发；VM 只有 user-mode NAT。IPv4 路由随即与开始时一致，IPv6 在 03:09:25 路由器重新通告 ULA 前缀后
  一致。脚本因此保留了 VM 文件；按相同条件（QEMU 退出 0、无 monitor/进程、端口空闲、固定清单）另行
  删除并写入 `vm-cleanup.json`，不改写原基线失败记录。
- **r2 安装超时**：Debian 轮在 `apt-get update` 耗时 30 分钟后被安装期限终止（退出 124），VM 正常关机、
  基线一致、文件已清理。宿主测得 `deb.debian.org` IPv4 约 9 KB/s、IPv6 约 11 MB/s，而 user-mode NAT
  只走 IPv4。r4 改用仓库文档中的 APT 镜像 `mirrors.aliyun.com`（仍用 Debian 密钥环校验，不拉源码索引），
  只替换安装脚本，其余输入不变。

r1 之后的各轮基线全部一致；四轮结束后宿主无 QEMU 进程，所有 VM 可写盘、密钥与 cloud-init 文件已删。

## 未覆盖

- VM 实际启动与 guest 内运行、ZFS 池、ARM64、双栈；
- 修改 `storage_pool` 配置后已有实例在旧池时 daemon 的拒绝（仅源码依据）；
- simplestreams 镜像复制是否受 `restricted.images.servers` 约束（仅源码依据），R-085 的远程拉取/导入实测；
- 真实 Core/Compose 多工作区连同一 daemon 的部署，以及 Forgejo 在 7.x daemon 上的 one-job；
- 升级到 7.5 时已有 VM 档实例与新 nesting 限制的冲突（仅源码依据）。
