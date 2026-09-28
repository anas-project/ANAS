---
doc_type: research
created: 2026-09-28
updated: 2026-09-28
evidence_as_of: 2026-09-28
---

# Incus 网络可视化开源项目调研

本报告回答：Incus 实例与宿主之间的网络（租约网桥、网关、出站策略），有没有现成的开源项目能做成类似 AWS VPC
控制台的可视化。项目状态采集于 2026-09-28，结论是选型快照。

## 结论

没有可以直接拿来用的项目：

- Incus 官方 Web UI 和 LXConsole 只提供表单和列表，没有拓扑图；
- 能画真实网络拓扑的 Skydive 已基本停止维护，也不理解租约、档位这类业务概念；
- OpenStack Horizon 的拓扑图最接近 AWS VPC 控制台，但它绑定 Neutron，不能单独用于 Incus。

Incus 自带的网络对象本身已经对应 VPC 的主要概念（见第 2 节）。因此建议在 ANAS 控制台里自己做一个只读的
「租约网络」视图，数据来自 Provider 已有的 `inspect` 结果和 Incus API。

## 1. 候选项目

| 项目 | 许可证 | 状态 | 能力 | 结论 |
| --- | --- | --- | --- | --- |
| [Incus UI](https://github.com/zabbly/incus-ui-canonical)（`canonical/lxd-ui` 的分支） | GPL-3.0 | 活跃，频繁跟随上游 rebase | 实例、网络、网络 ACL、存储、权限组等的表单与列表；Incus 可以直接托管 `/opt/incus/ui` 下的静态前端 | 没有拓扑图。上游需求 [Add network topology](https://github.com/canonical/lxd-ui/issues/151) 已关闭，没有实现 |
| [LXConsole](https://github.com/PenningLabs/lxconsole) | AGPL-3.0 | Beta | 多服务器管理：实例、快照、profile、网络、存储、项目、Web 终端、RBAC | 只有表单，没有拓扑图 |
| [Skydive](https://github.com/skydive-project/skydive) | Apache-2.0 | 最近一次提交在 2024-07，基本停止维护 | 实时网络拓扑与流量分析，能看到网卡、网桥、namespace、OVS | 能画出宿主上的真实连接关系，但已停更、组件较重（agent 加 analyzer），也不理解租约和档位 |
| OpenStack Horizon 网络拓扑 | Apache-2.0 | 本次未核实 | Neutron 网络、路由器、安全组的拓扑图 | 形态最接近 AWS VPC 控制台，但绑定 Neutron，只能作为界面参考 |

## 2. Incus 与 VPC 概念的对应

| AWS VPC | Incus | ANAS 租约中的用法 |
| --- | --- | --- |
| VPC、子网 | 受管网络（bridge 或 OVN） | 每个租约一张网桥、一个网段 |
| 安全组、网络 ACL | network ACL | 出站档位与来源围栏 |
| 托管前缀列表 | network address set | 局域网、宿主、Traefik 地址清单 |
| NAT 网关 | 网桥的 `ipv4.nat`、`ipv6.nat` | 出站地址转换 |
| 弹性 IP、端口映射 | network forward | 不使用，入站走 Traefik |
| Route 53 私有托管区 | network zone | 未使用 |
| VPC 对等连接 | network peer（OVN） | 不使用，租约之间互相隔离 |

对应关系参考 [Incus 网络文档](https://linuxcontainers.org/incus/docs/main/networks/)；address set 与 ACL 配合使用，
只支持 OVN 网络或使用 nftables 的 bridge 网络（见 [address set 文档](https://linuxcontainers.org/incus/docs/main/howto/network_address_sets/)）。

## 3. 建议：控制台的只读「租约网络」视图

- **每个租约一张卡片**：网桥名与网段、网关、出站档位、`module_access`、`intra_lease`、实例及其地址。
- **宿主一侧**：上行网卡与局域网清单、Traefik 固定地址、Docker 网段（标为拒绝）。
- **连线表示允许的方向**：互联网、局域网、宿主、Traefik，用颜色区分放行和拒绝，由当前档位推导出来。
- **数据来源**：Provider 的 `inspect` 结果，以及 Incus API 返回的网络、ACL、address set 和实例状态。页面只读，
  修改仍然走部署声明。
- **前端实现**：控制台基于 Vue 3，可以用 [Vue Flow](https://github.com/bcakmakoglu/vue-flow) 或
  [Cytoscape.js](https://github.com/cytoscape/cytoscape.js) 绘图，两者都是 MIT 许可；第一版也可以只用 SVG 手绘
  固定布局。
