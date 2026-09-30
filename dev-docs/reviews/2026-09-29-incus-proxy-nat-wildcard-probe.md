---
doc_type: review
status: current
created: 2026-09-29
updated: 2026-09-29
---

# Incus proxy NAT 通配监听实机探测

状态：实机核对记录，没有改动实现。日期：2026-09-29。基线：`7ad1876a` 加当前工作树。关联
[宿主供给设计](../../docs/architecture/incus-host-provisioning.md) §5.1.8 与
[Incus 要求](../requirements/incus-module.md) §7septies 的端口绑定。

## 结论

Incus 7.0.1 的 proxy 设备在 NAT 模式下监听 `0.0.0.0` 时，生成的 DNAT 规则只匹配目的端口，不匹配目的地址。
宿主自己往外连的流量、只是经宿主转发的流量，只要目的端口相同，都会被转给实例。这与 Docker 的端口发布不同：
Docker 只接管发往本机地址的流量。按 Docker 的做法加上 `fib daddr type local` 之后，行为与 Docker 一致：宿主的
所有地址都能访问，旁路流量不受影响。

## 环境

- `ln.hlong.wang` 实验根 `/data/anas-incus-lifecycle-20260926.616ts5`，轮次 `r12-proxy-nat-probe`。这是
  `hold-proxynat` 诊断轮：一次性 Debian 13 VM `anas-incus-lifecycle-4941c9`（3 vCPU / 3.5 GiB，user-mode NAT）。
  宿主侧所有者脚本 `test-env/scripts/server-incus-lifecycle-lab.py` 与仓库一致（SHA-256 `36db9f23…6c00`）。
- VM 内：内核 6.12.107+deb13-cloud-amd64，Zabbly `lts-7.0` 的 Incus 7.0.1，nftables 1.1.3。
- 探测脚本：`test-env/fixtures/incus-lifecycle-lab/proxy-nat-probe.py`（SHA-256 `a3e5b9aa…7727`）。首次尝试
  （`0d5b0ebd…9c0d`）在最后一步因 nft 语法错误退出，前三组场景已执行但没有输出；重跑版修正了语法，并改为可重入。

## 方法

- VM 扮演 Incus 宿主：dir 存储池、受管网桥 `probebr0`（10.123.0.1/24），一个 Debian 容器 `c1`，网卡固定地址
  10.123.0.10。
- 三个监听者，按标签回应：`INSTANCE` 在容器的网络命名空间里，`HOST` 在 VM 本身，`OUTSIDE` 在独立 netns
  （198.51.100.2）里，代表另一台机器。另一个 netns `probe-cli`（203.0.113.2）的默认路由指向 VM，代表只是经宿主
  转发的流量。
- 五个探测，端口都是 18443：

  | 探测 | 发起方 | 目标 |
  | --- | --- | --- |
  | A | 宿主自己 | 另一台机器 198.51.100.2 |
  | B | 经宿主转发的客户端 | 另一台机器 198.51.100.2 |
  | C | 外部 | 宿主地址 198.51.100.1 |
  | D | 宿主自己 | 自己的地址 198.51.100.1 |
  | E | 经宿主转发的客户端 | 宿主的另一个地址 203.0.113.1 |

- 四组场景：无规则；proxy NAT 通配（`listen=tcp:0.0.0.0:18443 connect=tcp:10.123.0.10:18443 nat=true`）；proxy
  NAT 具体地址（`listen=tcp:198.51.100.1:18443`）；自写 Docker 式规则（prerouting 为
  `fib daddr type local tcp dport 18443 dnat`，output 另外排除 127.0.0.0/8）。

## 结果

| 场景 | A 宿主连外部 | B 转发流量 | C 外部连宿主地址 | D 宿主连自己 | E 连宿主另一地址 |
| --- | --- | --- | --- | --- | --- |
| 无规则 | OUTSIDE | OUTSIDE | HOST | HOST | HOST |
| proxy NAT 通配 | **INSTANCE** | **INSTANCE** | INSTANCE | INSTANCE | INSTANCE |
| proxy NAT 具体地址 | OUTSIDE | OUTSIDE | INSTANCE | INSTANCE | HOST |
| Docker 式（`fib daddr type local`） | OUTSIDE | OUTSIDE | INSTANCE | INSTANCE | INSTANCE |

A、B 两列是关键：通配时，本来不是发给宿主的连接也被转给了实例。

实际生成的规则（`nft list ruleset` 中含 18443 的行）：

- 通配：prerouting 与 output 各一条 `tcp dport 18443 dnat ip to 10.123.0.10:18443`，没有目的地址条件；
- 具体地址：`ip daddr 198.51.100.1 tcp dport 18443 dnat ip to 10.123.0.10:18443`；
- 两种 proxy 都另有一条 `ip saddr 10.123.0.10 ip daddr 10.123.0.10 tcp dport 18443 masquerade`（实例访问自己的回环修正）。

## 收尾

- 正常关机，QEMU 退出 0，宿主十项基线前后一致（`cleanup_passed: true`）。诊断轮按设计记为未通过
  （`diagnostic hold round; no stage verdict`），VM 文件按脚手架规则保留（`lab.qcow2` 2.1 GiB）。
- 报告在轮次目录 `reports/proxy-nat-probe.2.json`；首次尝试的输出与错误在 `proxy-nat-probe.json` / `.err`。

## 对设计的影响

- 端口绑定不能用 proxy NAT 的通配监听。
- 要做到「宿主所有地址都能访问、又不劫持旁路流量」，需要 Docker 式的 `fib daddr type local` 匹配。Incus 当前不
  提供，ANAS 自己写规则可以做到（宿主供给设计 §5.1.8 的第二种方案）。
- 可以向上游报告：通配监听应当只匹配目的地址是本机的流量。
