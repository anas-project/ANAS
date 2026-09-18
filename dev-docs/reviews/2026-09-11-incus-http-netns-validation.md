---
status: current
---

# Incus HTTP 网络原型：Linux namespace 实验记录

基线：2026-09-11，ANAS HEAD `f7642c5` 加指定工作树的未提交实现。操作者在本轮明确指定 SSH
宿主，报告不保存其地址或任何凭据。此记录是实际 Linux 数据面实验，不是 Incus/Traefik 完整验收。

## 环境与执行边界

宿主只读观测：Ubuntu 26.04 LTS、Linux `7.0.0-30-generic` / x86_64、nftables `1.1.6`、
Docker `29.7.2`、Compose `5.5.0`。未发现 Incus CLI、`/dev/kvm` 或 conntrack CLI。
现有 Docker 与其他测试 daemon 均未用于本次实验，不安装软件、不修改宿主防火墙、不启停现有服务。
本次未执行 M2 helper 预检；手工 SSH 授权不能表述为 M2 最小权限门禁通过。

执行 `server-incus-http-netns.py`，它在新网络 namespace 中创建三个 bridge 与四个子 namespace，
以 Python HTTP 服务模拟后端和来源，不运行容器。六组可达基线通过后，实际加载
`cmd/incus-network-prototype` 从仓库合成观测夹具生成的原样规则。所有地址、MAC 和实例字段均为
实验数据，不是可信 Incus 实例观测。

本轮实际文件 SHA-256：

| 文件 | SHA-256 |
| --- | --- |
| `cmd/incus-network-prototype/main.go` | `6681668fad0cc810060cf6035e9d952d6a86e1b924f7788f69b1db70c6e36817` |
| `server-incus-http-netns.py` | `4b601837608023309b960a76c4e5ae9cf513c6c78bf56064f399e402ee6034a8` |
| 生成的 `firewall.nft` | `3a285327d909a55f314d26657713940e5637c4bfa111d142da01778d6d799709` |

## 实际结果

| 检查 | 结果 |
| --- | --- |
| 新 namespace 中 `nft --check --file` 与实际加载 | 通过 |
| 来源、其他端口、同 bridge、其他 bridge、IPv6、反向发起六组加载前可达基线 | 全部通过 |
| 生成 `/32` 路由的 interface 与源地址 | 通过 |
| 指定来源到 guest 指定 HTTP 端口 | 可达 |
| 未批准端口、同 bridge 其他来源、其他 bridge 来源 | 均拒绝 |
| 冒用合法来源 IP 与 MAC（静态邻居绕过 ARP 失败因素） | 拒绝 |
| guest 反向主动连接、IPv6 后端绕行 | 均拒绝 |
| 冒用测试后恢复合法来源 | 修正实验接口恢复步骤后通过 |
| 撤销 tuple 前的持续 HTTP 数据 | 确认持续传输 |
| 删除 tuple 后的既有 HTTP 流和新连接 | 旧流停止接收，新连接拒绝 |
| 重建固定实验许可后等待 31 秒 | 许可过期，新连接拒绝 |
| 运行前后宿主 nft 无状态摘要、调用者网络 namespace | 不变 |

最终脚本退出码 `0`，20 项 namespace 检查通过，加上宿主规则不变检查。首轮在合法来源恢复检查
失败并正常清理；补齐接口 down/up 后的生成路由与邻居恢复步骤后，完整第二轮通过。没有更改
生成器规则来让测试通过。实验进程在 finally 中终止，临时网络随最后一个进程退出销毁；本轮上传
文件目录在摘要核对后清理。

## 尚未证明的行为

- Incus container/VM 的真实身份、UUID、NIC/IP/MAC 分配与生命周期，restricted project 围栏；
- 实际 Docker/Incus base chain 的规则顺序与可达性；本实验没有这两套规则；
- Traefik 配置渲染、公网入口、认证、域名授权和真正的受管 guest HTTP；
- conntrack CLI 删除、IP 分配复用、暂停/停止事件与中介重启对账；
- 宿主自动供给、镜像导入/烘焙和 KVM 能力。

因此 M6 及 M11 的宿主退出条件仍未完成，Module 仍为 `developing`。这些是 HTTP 传输测试，不是
TCP/UDP 端口发布实现。复现步骤见[网络原型说明](../../test-env/fixtures/incus-network-prototype/README.md)。
