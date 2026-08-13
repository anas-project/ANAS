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

macvlan 常见限制是宿主机不能直接访问同一 macvlan 上的容器。不要为绕过限制授予 ANAS 任意 root shell；使用受限脚本和 sudoers 规则，参考 [macvlan sudoers Runbook](runbooks/macvlan-sudoers.md)。

## 排查顺序

1. 检查 DNS 实际解析结果。
2. 检查 `anas status` 和活动 deployment。
3. 检查 Traefik router、service 与证书状态。
4. 检查目标容器健康状态和 Docker 网络。
5. 最后检查主机防火墙、NAT 和上游路由。
