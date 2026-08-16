# 网络

## 域名与 HTTPS

Traefik 是公开 HTTP/HTTPS 入口，证书由所选 ACME/DNS 能力提供。部署前确认：

- 基础域名及服务子域名解析到正确入口；
- DNS API token 仅提供给需要它的 Module；
- TCP/UDP 端口已经在主机防火墙和上游路由器放行；
- 时间同步正常，避免 TLS 和目录认证异常。

服务专项配置见 [Traefik](traefik.md)。

## Docker 网络与 macvlan

普通服务通过 Module 声明的 Docker 网络连接。需要直接出现在局域网中的服务可能使用 macvlan，并需要最小化的主机特权辅助操作。

macvlan 常见限制是宿主机不能直接访问同一 macvlan 上的容器。ANAS 用一个只持有 `CAP_NET_ADMIN` 的独立二进制 `anas-helper` 建立桥接口来解决，不需要 sudoers 规则，也不需要任何 root shell。参考 [特权 helper Runbook](runbooks/privileged-helper.md)。

### 局域网地址必须排除出 DHCP 池

macvlan 上的地址由 Docker 自己的 IPAM 分配，**不走 DHCP**，也不做重复地址检测。默认从宿主网段顶部取（桥 `.241`、容器 `.242` 之类），这只是"路由器的 DHCP 池通常不发到段顶"这个约定，不是任何协议保证。

**部署前置要求：把这两个地址排除出路由器的 DHCP 池，或在路由器上做成保留。** 撞了不会有任何报错，两边都认为自己拥有该地址，症状是间歇性连接失败。

查看当前地址：

```bash
docker inspect anas_samba_fs --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}'
```

指定地址（推荐，这样地址是你选的，也能被上面的探测检查到）：

```bash
anas config set global.host_lan_ip 192.168.1.51
```

桥地址用 `global.host_lan_bridge_ip`。两者都指定后不再划分地址池，宿主前缀窄于 /28 也能部署。

### 启动前的地址占用探测

创建 macvlan 网络之前，runner 会对要占用的地址做一次探测：ping 一下触发 ARP 解析，再从邻居表里读结果。只有 `REACHABLE` 才算被占用——`STALE` 条目往往是本部署上一个容器留下的记忆，当成占用会让服务起不来。

探测只在**网络尚不存在**时进行。网络已存在时，回答探测的正是本部署自己的容器。这留下一个缺口：容器停着的时候地址被 DHCP 发给了别人。堵住它的办法在路由器侧，不在这里。

探到冲突会直接失败并给出占用方的 MAC。确知误报时可以关掉：

```bash
anas config set global.host_lan_arp_check false
```

## 排查顺序

1. 检查 DNS 实际解析结果。
2. 检查 `anas status` 和活动 deployment。
3. 检查 Traefik router、service 与证书状态。
4. 检查目标容器健康状态和 Docker 网络。
5. 最后检查主机防火墙、NAT 和上游路由。
