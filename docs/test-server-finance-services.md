# finance 测试服务器：服务域名与访问方式

> ⚠️ **这是测试服务器，不是生产环境。**
> `finance.hlong.wang` 上的 anas 部署仅用于开发与验证：配置里使用弱默认口令
> （`default_service_root_password: ChangeMe1!`），TLS 由内置自签 CA 签发，
> 数据随时可能被重建。**不要存放真实业务数据，不要把这些入口对外公开分发。**

采集时间：2026-07-29，对应部署 `20260728T131040Z-cd6fc061`。

## 基础信息

| 项 | 值 |
| --- | --- |
| SSH | `ssh whl@finance.hlong.wang` |
| 部署目录 | `/home/whl/anas-deploy`（配置 `config.yml`、锁 `config.lock.yml`） |
| 基础域名 | `finance.hlong.wang` |
| 内网地址 | `192.168.31.100`（网卡 `enp3s0`） |
| 公网地址 | `1.58.6.121`（由 `ddns` 模块推送到 DNSPod，会变） |
| HTTPS 入口端口 | **9443**（`TRAEFIK_BASE_PORT`，Traefik 未监听 443） |
| 虚拟域名 | `virtual_domain: true`，各服务是 `finance.hlong.wang` 的子域 |
| 容器前缀 | `anas_` |

**所有 Web 入口都必须带 `:9443` 端口**，宿主机的 80/443 被服务器上另一套无关的
`nginx_feng` / `traefik` 容器占用，和 anas 无关。

## Web 服务域名

全部走 `anas_traefik`，形式为 `https://<域名>:9443`。

| 模块 | 域名 | 访问地址 | 现状 |
| --- | --- | --- | --- |
| authentik（IdP） | `auth.finance.hlong.wang` | https://auth.finance.hlong.wang:9443 | 正常，302 跳登录流程 |
| nextcloud | `nc.finance.hlong.wang` | https://nc.finance.hlong.wang:9443 | 正常，302 跳 `/login` |
| traefik dashboard | `traefik.finance.hlong.wang` | https://traefik.finance.hlong.wang:9443 | 正常，401 需 BasicAuth |
| LAM（LDAP 管理） | `lam.finance.hlong.wang` | https://lam.finance.hlong.wang:9443 | 正常，200 |
| netbird | `netbird.finance.hlong.wang` | https://netbird.finance.hlong.wang:9443 | 正常，200 |
| ddns（DDNS Updater） | `ddns.finance.hlong.wang` | https://ddns.finance.hlong.wang:9443 | 正常，200 |
| meshcentral | `meshcentral.finance.hlong.wang` | https://meshcentral.finance.hlong.wang:9443 | ❌ **502**，见「已知问题」 |

Nextcloud 的两个子路由挂在同一域名下，不是独立域名：
`/push`（notify_push）、`/talk`（Talk signaling）。
netbird 的 signal / management gRPC 同样复用 `netbird.finance.hlong.wang`
（`/signalexchange.SignalExchange/`、`/management.ManagementService/`、`/api`）。

### 未启用的域名

`postgres_adminer.finance.hlong.wang` 和 `mariadb_adminer.finance.hlong.wang`
在环境变量中存在，但 `POSTGRES_ADMINER_ENABLED=false`、`MARIADB_ADMINER_ENABLED=false`，
容器没有启动，域名不可用。

## 非 HTTP 服务

| 服务 | 地址 / 端口 | 说明 |
| --- | --- | --- |
| Samba AD DC | `finance.hlong.wang` → `192.168.31.100` | `network_mode: host`，占用宿主 53/88/389/445/464/636 等 |
| — Kerberos realm | `FINANCE.HLONG.WANG` | workgroup `FINANCE` |
| — LDAPS | `ldaps://finance.hlong.wang:636` | |
| — 内网 DNS | `192.168.31.100:53` | 容器内的 BIND，解析全部 `*.finance.hlong.wang` |
| — DC 主机名 | `fengoffice.finance.hlong.wang` | |
| Samba 文件服务 | `192.168.31.240`（macvlan），NetBIOS `SAMBAFS` | 共享：`\\SAMBAFS\Home`、`\\SAMBAFS\Share` |
| eturnal（TURN） | `turn.finance.hlong.wang:3478`（TCP+UDP） | relay 端口 50000–51000 |
| FreeRADIUS | `192.168.31.100:1812/1813` UDP | |
| MeshCentral MPS | `192.168.31.100:4433` | Intel AMT CIRA |
| PostgreSQL / MariaDB | 仅容器网络内（`anas_postgres:5432` / `anas_mariadb:3306`） | 未对宿主暴露 |

`turn.finance.hlong.wang` 只有公网 DNS 记录，AD DC 内网 DNS **没有**这条记录
（它不在 `DOMAINS` 注册列表里）。内网使用 TURN 需要走公网解析或手动加 hosts。

## 怎么访问

### 1. 解析

两套 DNS 给出不同结果，都能用：

- **公网 DNS**（DNSPod，由 `ddns` 模块维护）：全部子域 → `1.58.6.121`
- **内网 AD DNS**（`192.168.31.100`）：全部子域 → `192.168.31.100`

在内网直接把 DNS 指到 `192.168.31.100` 最省事；在外网走公网解析，需要路由器已把
9443 端口转发到 `192.168.31.100`。

绕过 DNS 直连时用 `--resolve`，不要改 Host 头（Traefik 按 Host 路由）：

```bash
curl -k --resolve nc.finance.hlong.wang:9443:192.168.31.100 https://nc.finance.hlong.wang:9443/
```

### 2. 证书

证书由**内置自签 CA** 签发，不是 Let's Encrypt：

- 签发者：`CN = ANAS internal CA finance.hlong.wang, O = ANAS`
- 主体：`CN = finance.hlong.wang`，SAN 含 `finance.hlong.wang` 与 `*.finance.hlong.wang`
- 有效期：2026-07-27 至 2028-07-26
- 路径：`/home/whl/anas-deploy/data/lego/certs/certificates/`

浏览器会报证书不受信任。要消除告警，导入 CA 根证书：

```bash
scp whl@finance.hlong.wang:/home/whl/anas-deploy/data/lego/certs/certificates/anas-internal-ca.crt .
```

命令行测试则直接用 `curl -k`。

### 3. 凭据

**本文不记录任何明文口令。**

- Samba DC 的 `Administrator` 与 `admin` 用初始默认口令
  （`config.yml` 里的 `default_service_root_password`）。`admin` 的 DN 是
  `CN=admin,OU=People,DC=finance,DC=hlong,DC=wang`（不在 `OU=Admins` 下，
  见 memory `admin-account-lives-in-ou-people`）。
- Traefik dashboard 走 BasicAuth，用户名 `admin`（`BASICAUTH_USER`）。
- 其余自动生成的密钥在 `/home/whl/anas-deploy/runtime/secrets.generated.yml`，
  包含 `POSTGRES_PASSWORD`、`MARIADB_ROOT_PASSWORD`、`AUTHENTIK_SECRET_KEY`、
  `SAMBA_DC_LDAP_BIND_PASSWORD`、`TURN_SECRET`、`TALK_*` 等。

### 4. 快速自检

```bash
ssh whl@finance.hlong.wang 'for h in auth nc traefik lam meshcentral netbird ddns; do printf "%-14s " $h; curl -k -s -o /dev/null -w "%{http_code}\n" --max-time 8 --resolve $h.finance.hlong.wang:9443:192.168.31.100 https://$h.finance.hlong.wang:9443/; done'
```

## 已知问题

**meshcentral 返回 502。** `modules/meshcentral/meshcentral/config.json.erb`
本该渲染出 `config.json`（其中 `port` 和 `AliasPort` 都取 `TRAEFIK_BASE_PORT`=9443），
但 `/home/whl/anas-deploy/data/meshcentral/meshcentral-data/` 下没有 `config.json`。
MeshCentral 因此按默认配置只监听 80，而 Traefik 的
`loadbalancer.server.port=9443` 指向一个没人监听的端口，于是 502。
