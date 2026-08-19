# Samba 文件服务网络模式（草案）

> **状态：§1–§7 已实现**，见本文件 §10 的影响面清单。§8 的 host 网络模式仍是草案。
>
> 主线是**保留 macvlan**，把容器地址从隐式分配改成可显式指定，并在占用地址之前做
> 一次 ARP 探测。局域网部署的最优解是这个，不是换网络模式。
>
> §8 记录 host 网络模式（原"方案 A"）。它的价值场景是**没有局域网**的部署，在
> 局域网里不是最优解。

## 1. 现状的三个问题

`samba_fs` 是当前唯一声明 `features.host_lan: required` 的 module
（`modules/samba_fs/module.yml:105`），整条 macvlan 链路只为它一个存在。

### 1.1 地址是本地分配的，不是 DHCP 拿的

`ensureMacvlan` 建网络时传 `--subnet HOST_SEGMENT --ip-range VLAN_SEGMENT
--gateway --aux-address bridge=...`，地址由 Docker 自己的 IPAM 从 `--ip-range` 里
分配。Docker 不发 DHCP 请求，也不做重复地址检测。

`calcVLAN` 的注释把这件事说得很清楚（`internal/runner/hostnet.go`）：固定 /28
是**为了让容器不撞上网段底部的 DHCP 租约**。也就是说，防冲突靠的是"挑一个 DHCP
大概率发不到的位置"这个**约定**，不是任何协议保证。路由器的 DHCP 池只要覆盖到段
顶，就会撞。

### 1.2 冲突是静默的

撞了不会有任何提示。Docker 直接使用该地址，两边都认为自己拥有它，表现为间歇性的
连接失败、ARP 表抖动——排查成本极高，而且很难联想到是文件服务器干的。

### 1.3 /28 的宽度限制与实际用量无关

实际出现在网线上的只有**两个**地址：`VLAN_BRIDGE_IP`（宿主侧 `anas_bridge`）和
容器自己那一个。`--ip-range` 的其余 14 个地址只是 Docker IPAM 不会再分配，网络上
没有任何东西占用它们。

但 `calcVLAN` 要求宿主前缀宽于 /28，这是一条**渲染期的硬限制**，和只用两个地址
这个事实不匹配。

> 修正：本文早期版本称 macvlan 会"占用 16 个地址"，这是错的。占用的始终只有两个。
> /28 是分配池的宽度，不是占用量。结论不变，但动机的份量要按这个事实来掂。

## 2. 方案总览

三件事：

1. **地址可显式指定**：`global.host_lan_ip` 与 `global.host_lan_bridge_ip`。（§3）
2. **占用前做 ARP 探测**：地址上已经有人应答就直接失败。（§4）
3. **两者都显式时不划分地址池**：解掉 /28 宽度限制。（§3.3）

不改网络模式，不改 macvlan 机制，不动 samba_dc。

## 3. 配置面

### 3.1 参数

```yaml
global:
  host_lan_ip: 192.168.1.51          # 容器在局域网上的地址
  host_lan_bridge_ip: 192.168.1.50   # 宿主侧 anas_bridge 的地址
  host_lan_arp_check: false          # 可选，关掉占用探测
```

**三个都是全局参数，不是 samba_fs 的 module 参数**——这一条与本文早先的设计不同，
原因是实现时才暴露的：`anas start` 和 `anas rollback` 不重新规划，它们用
`adoptReleaseEnv` 从制品里的 `.global.env` 恢复部署环境，而那个文件按构造只包含
**全局所有权**的键。module 所有权的地址在创建 macvlan 的那一刻恰好是不可见的。

顺带也解决了另一个问题：runner 自己要读这些值，而 runner 读全局参数是正常的，读某
个 module 的私有参数是层次倒置。

### 3.2 为什么声明在 `changes` 而不是 `defaults`

`defaults` 里的键——**哪怕默认值是空串**——会写进每一个渲染出来的 `.env`。给这三个
参数一个空默认值，等于给每个部署的每个 module 都加一行，包括根本没有 host_lan
module 的部署。

声明在 `changes` 下同样让参数可被 `anas config set` 寻址、并带上变更策略，但在有人
真正设值之前不发布任何东西。

### 3.3 显式指定时不划分地址池

`calcVLAN` 存在的唯一目的是**自动**挑地址。两个地址都被显式给出时它没有工作可做，
连带 `prefix > 28` 那条限制一起跳过。

实现后的门禁：

```
需要 macvlan          → hostLANRequired()
需要划分地址池        → hostLANRequired() && (容器地址或桥地址为空)
发布 VLAN_SEGMENT     → 容器地址未被指定
```

第三条是独立的：`VLAN_SEGMENT` 会成为 `docker network create --ip-range`，而 Docker
会拒绝范围外的静态地址。地址一旦被指定，这个范围就没有约束对象了——它约束的本来
就只是 IPAM 自己的选择。所以**只指定容器地址、不指定桥地址**也是合法组合：桥地址
仍从池里取，但 `--ip-range` 不再发布。

`HOST_SEGMENT` 从 `calcVLAN` 里拆了出来（新的 `hostSegment`）：网段是关于宿主的
陈述，每台主机都有，包括没有余量划池的 /30 和 /32。

副作用：显式给地址后，`/29`、`/30` 宿主也能部署文件服务。

### 3.4 自动分配时的默认值

桥取地址池的第一个（`.241`，与现状一致），容器取第二个（`.242`）——也就是
`--aux-address` 排除掉桥地址之后，IPAM 手里的第一个地址。

**这里有一个升级注意事项。** compose 现在**总是**固定容器地址
（`ipv4_address: ${HOST_LAN_IP}`），因为 compose 无法有条件地省略这个键。如果某个
现有部署的 IPAM 当初选的不是 `.242`，升级会让容器换一次地址。要避免，先把当前地址
钉住：

```bash
docker inspect anas_samba_fs --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}'
anas config set global.host_lan_ip <上面输出的地址>
```

即使没钉住，§5.2 的 A 记录重注册会让目录里的记录跟上；受影响的只有按 IP 硬编码的
客户端。

## 4. ARP 预检

### 4.1 探什么、什么时候探

探 `VLAN_BRIDGE_IP` 和 `HOST_LAN_IP`。

时机：**只在 Docker 网络尚不存在时**。网络一旦存在，回答探测的就是本部署自己的
容器——这一点比它看起来重要：宿主重启后 `anas_bridge` 接口没了，但容器可能已被
restart policy 拉起并持有该地址，用"接口不存在"当触发条件会探到自己，然后硬失败。
网络存在与否没有这个问题。

### 4.2 怎么探（不需要新特权）

```sh
ping -c 1 -W 1 <addr> >/dev/null 2>&1
ip -4 neigh show <addr>        # 有 lladdr 且状态 REACHABLE = 被占用
```

原理是 ping 会触发内核做 ARP 解析。**即使目标过滤 ICMP，只要它是活的就必须回 ARP**，
邻居表就会拿到 MAC。

只认 `REACHABLE`。`STALE` 条目往往是本部署上一个容器留下的记忆，当成占用会让服务
起不到一个根本没人持有的地址上；而这个取舍的误差方向是安全的——解析太慢时报"无
冲突"，不会报假冲突。

两条命令都不需要权限。只有配置了 `NETWORK_NAMESPACE_PATH` 的隔离测试环境例外：进入
命名空间本身就是特权操作，那里探测走 `sudo nsenter`。

（本节写作时桥还由 `sudo` + 生成脚本建立，探测"不新增特权"是相对那个基线说的。桥
现在由 `anas-helper` 建立，见[特权 helper 草案](privilege-helper-draft.md)，普通部署
已经完全没有 sudo。）

### 4.3 探到冲突怎么办

硬失败，错误信息包含冲突地址、应答方 MAC，以及三条补救：换地址、把地址排除出路由器
DHCP 池、或关掉探测（`global.host_lan_arp_check: false`）。

### 4.4 覆盖不到的窗口（如实说明）

容器停着的时候，DHCP 把这个地址发给了别人，而 Docker 网络仍然存在——下次启动不
探测，冲突照旧发生。

这个窗口是**故意留下**的，理由见 §4.1。真正的解法在路由器侧：把地址排除出 DHCP 池。
文档里这一条是**前置要求**，不是建议。

## 5. samba_fs 侧改动

### 5.1 compose 固定地址

```yaml
services:
  anas_samba_fs:
    networks:
      default:
        ipv4_address: ${HOST_LAN_IP}
```

顶层 `networks` 块原样不动。真实的 `docker compose config` 已验证这个写法与
`external: true` 的外部网络配合正常（`test-compose-config.sh`）。

### 5.2 启动时无条件 `net ads dns register`

`join_domain()` 只在 `net ads testjoin` 失败时才动作。地址变了加域**仍然有效**，
所以这段不会执行，AD DNS 里的 A 记录就一直是旧地址。

改法：加域流程之后无条件执行一次 `net ads dns register -P`（机器账号凭据，幂等）。注册
失败必须阻断启动，不能让健康检查通过但 AD DNS 仍指向旧地址。

这修的是一个**既有隐患**，不是本方案引入的。

## 6. 变更语义

三个参数都是 `container_recreate`；`host_lan_ip` 的 apply 动作是
`recreate-and-reregister-dns`，因为只重建容器会留下一个能连但解析不到的文件服务器。

不是 `data_migrate`：磁盘数据、机器账号、NetBIOS 名都不动，变的只是监听地址。

改变寻址方式（例如从自动池切到显式地址）会让现有 Docker 网络与新计划不匹配，
`ensureMacvlan` 拒绝自动替换。错误信息现在会说明补救：先 `anas stop`（它会删掉网络），
再 apply。

## 7. 升级路径

1. 什么都不设 → 桥 `.241`、容器 `.242`，与现状一致，除 §3.4 那一种情况外零影响。
2. 想保住现有地址 → 按 §3.4 先钉住再升级。
3. 想换地址 → `anas stop`，设 `global.host_lan_ip`，再 apply。
4. 部署前置：把这两个地址排除出路由器的 DHCP 池。见
   [网络运维文档](../operations/networking.md)。

## 8. 附：host 网络模式（另一档，仍是草案）

把 samba_fs 改成 `network_mode: host`，DC 让出 445。这条路**在局域网里不是最优解**，
记录在这里是因为它在没有局域网的部署（VPS）上是唯一可行的形态。

### 8.1 形态与代价

- samba_dc 的 `server services` 追加关闭文件服务，DC 只做目录服务和 DNS。
- samba_fs 用 host 网络，不再需要 macvlan、`anas_bridge` 和 sudoers。
- **代价：SYSVOL / NETLOGON 消失。** Windows 客户端加域和组策略分发失效。Linux/macOS
  挂载共享、以及走 LDAP 的消费方（nextcloud、authentik、lam）不受影响。

适用条件：SMB 只用于文件共享，不做 Windows 域管理。

### 8.2 局域网里为什么不选它

macvlan 的独立 netns 让 samba_fs 只有一个地址，发现协议没有歧义。host 模式下宿主
接口上有两个地址，而 **avahi 是按接口工作的，不区分同一接口上的多个地址**——它会把
宿主 IP 和文件服务地址都发布出去，客户端解析到宿主 IP 就会连到 DC 的 smbd，看到
SYSVOL 而不是文件共享。

加上 host 模式下 `hostname:` / `dns:` / `dns_search:` 三个 compose 键都不能用，而
`dns:` 正是现在加域的命脉，替代方案要靠 `extra_hosts` + krb5 写死 KDC。改动面比 §2
大一个量级。

### 8.3 落地时需要的东西

开关建议放在 `internal/runner/globals.yml`（`host_lan_mode: macvlan|host`）：runner
自己要读它，samba_dc 也要读——而 samba_dc 是 samba_fs 的依赖，让依赖去 `consumes`
依赖者的参数是把方向拧反了。（§3.1 的实现结论同样适用：跨 start/rollback 存活的值
必须是全局所有权。）

其余细节（compose 两种形态如何表达、`hostLANRequired()` 的拆分、拆除路径的陷阱、
DC 侧 `server services` 的参数化）见本文件的 git 历史（`24c3853`）。

## 9. 实现中验证掉的事项

1. **`docker compose config` 接受 `ipv4_address` + 外部网络** —— 已用真实 compose
   验证（`test-env/reports/compose-samba_fs.log`）。
2. **全局所有权的键会进入 `.global.env`** —— 已验证，`anas start` 因此能复现地址计划
   和探测开关。这条否掉了原设计里的 module 参数方案（§3.1）。
3. **两个既有守卫测试需要新增"runner 消费"这一类**：`TestGlobalParametersHaveRuntimeConsumers`
   与 `test-render.sh` 的参数覆盖检查都假设每个全局参数都被某个 module 读取。桥地址和
   探测开关的读者是 runner，所以按名单转而校验 runner 源码——保留守卫，不放宽。

仍**未**验证、需要在真机上确认的：

1. `ping` 在目标发行版上是否对普通用户开放（取决于 `net.ipv4.ping_group_range`）。
   不可用时探测会安静跳过（安全网而非依赖），但也就失去了保护。
2. 现网实际分配到的地址是否就是 `.242`（§3.4）。
3. `net ads dns register -P` 在 BIND9_DLZ 后端上的幂等性与授权。
4. 桥地址为 `/32` + 显式 `/32` 路由时，宿主到容器的可达性（本机无 Linux 宿主，
   未实测）。

## 10. 影响面（已实现）

| 文件 | 改动 |
| --- | --- |
| `internal/config/config.go` | `Global` 新增三个字段与 `globalBindings` 条目 |
| `internal/runner/globals.yml` | 三个参数的 change policy |
| `internal/runner/hostnet.go` | `applyMacvlanPlan`、`hostSegment`、`poolAddress`、`validateMacvlanPlan`；`calcVLAN` 去掉从未生效的 `VLAN_GATEWAY_IP` |
| `internal/runner/network.go` | ARP 预检、`networkCreateArgs`、桥脚本的路由与残留地址清理 |
| `modules/samba_fs/docker-compose.yml` | 固定 `ipv4_address`；镜像 r2 → r3 |
| `modules/samba_fs/module.yml`、`localization.yml` | revision 2 → 3 |
| `modules/samba_fs/samba_fs/root/etc/cont-init.d/11-samba_fs.sh` | 无条件 `net ads dns register` |
| `docs/operations/networking.md`（含 en） | DHCP 池排除、地址查看与指定、探测说明 |
| `docs/operations/runbooks/privileged-helper.md` | 由 macvlan-sudoers.md 重写；桥改由 anas-helper 建立 |
| `docs/reference/module-environment-variables.md` | `HOST_LAN_IP` 与新的发布条件 |
| `internal/runner/hostnet_test.go`、`network_test.go` | 单元测试 |
| `internal/runner/globals_test.go`、`test-env/scripts/test-render.sh` | 守卫测试新增 runner 消费类 |
| `test-env/fakes/ip`、`fakes/ping` | 探测的可控边界 |
| `test-env/scripts/test-host-lan-e2e.sh`、`test-all.sh` | E2E |

~~sudoers 授权与 `anas_service.sh` 的授权规则不变。~~ 两者随后都被 `anas-helper` 取代，见[特权 helper 草案](privilege-helper-draft.md)。

## 11. 不做什么

- **不让 anas 去管宿主的 DHCP 客户端配置。** 想让第二个地址来自 DHCP，就得有第二个
  MAC（macvlan 子接口）并在上面跑 DHCP 客户端；那要求 anas 去改 netplan /
  systemd-networkd / NetworkManager / dhclient 四套互不相同的配置，还要抑制它带进来
  的第二条默认路由、处理 ARP flux。改错的代价是宿主断网。这超出部署工具的边界。
- 不做桥接 + 端口映射：NAT 会改写客户端源 IP，发现协议也不可用。
- 不做 SYSVOL 搬迁。
