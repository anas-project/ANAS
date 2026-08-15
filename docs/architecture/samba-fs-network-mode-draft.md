# Samba 文件服务网络模式（草案）

> **状态：草案，未实现。** 现行行为仍然只有 macvlan 一种，见 §2。本文描述把
> "DC 让出 445 + samba_fs 用 host 网络"做成**可切换**方案所需要的全部改动、代价
> 和未决问题，不构成操作指南。

## 1. 动机

`samba_fs` 是当前唯一声明 `features.host_lan: required` 的 module
（`modules/samba_fs/module.yml:105`），整条 macvlan 链路只为它一个存在。它带来
四项固定成本：

1. **需要 sudo。** `internal/runner/network.go:89` 是全仓库唯一一次 `exec.Command("sudo", ...)`，
   用来在宿主创建 `anas_bridge` macvlan 接口。没有这条 sudoers 授权部署就起不来
   （见 [macvlan sudoers Runbook](../operations/runbooks/macvlan-sudoers.md)）。
2. **宿主前缀必须宽于 /28。** `calcVLAN` 从宿主网段顶部切一个固定 /28 地址池，
   `/30`、`/32` 的普通 VPS 会直接 render 失败（`internal/runner/hostnet.go:286`）。
   这个限制已经被 `hostLANRequired()` 门禁挡住了一半——没有 samba_fs 就不计算——
   但只要启用文件服务就必然撞上。
3. **宿主访问不到容器。** 这是 macvlan 的固有限制。`anas_bridge` 存在的唯一理由，
   就是让 macvlan 上的容器能回访宿主 IP 上 DC 的 DNS：一整套特权网络配置，服务于
   加域时的域名解析。
4. **地址规划是隐式的。** 容器 IP 来自那个 /28 池，不在任何 DHCP 的视野里，排查
   时必须先知道 anas 的切法。

如果一个部署并不需要 samba_fs 以**独立 IP** 出现在局域网上，这四项成本可以一次性
去掉。方案 A 就是给这类部署一个出口，同时保留 macvlan 作为默认。

## 2. macvlan 现在撑着什么

| 依赖 | 位置 | host 模式下的处境 |
| --- | --- | --- |
| 445/139 的独占 | `modules/samba_dc/docker-compose.yml:13` 是 `network_mode: host`，DC 的 smb.conf 只关了 `-dns`（`smb.conf.envsubst:13`），smbd 照常监听 | **冲突，必须先解决**，见 §3 |
| 独立的 AD 主机身份（A 记录、`cifs/SambaFS.realm`） | `join_ad.sh` / `11-samba_fs.sh:18` 的 `net ads join` | 变成与 DC 同址，`\\SambaFS` 和 `\\DC` 解析到同一个 IP |
| 局域网发现（wsdd2 多播、avahi mDNS） | `root/etc/services.d/wsdd2/run`、`cont-init.d/12-avahi.sh` | **不劣反优**：直接在宿主二层，不再经过 macvlan 子接口 |
| 客户端真实源 IP | smb.conf 的 `hosts allow`、审计日志 | **保持真实**（桥接 NAT 才会丢，host 模式不会） |

结论：真正的代价集中在前两行。发现和客户端 IP 这两项，host 模式并不比 macvlan 差。

`VLAN_*` 的消费方只有两处，改动面很小：`modules/samba_fs/docker-compose.yml:40`
（网络名）和 `modules/samba_fs/hook/main.go:180`（DNS 兜底）。

## 3. 方案 A：DC 让出 445，FS 用 host 网络

### 3.1 形态

- `samba_dc`：`server services` 追加关闭文件服务，DC 只做目录服务（LDAP/Kerberos/
  DRS）和 DNS（bind9）。
- `samba_fs`：`network_mode: host`，直接占用宿主的 139/445，不再需要 macvlan 网络、
  `anas_bridge` 和 sudoers。
- `HOST_SEGMENT` / `VLAN_*` 不再计算，`/30`、`/32` 宿主也能部署文件服务。

### 3.2 代价：SYSVOL / NETLOGON 消失

DC 的 smbd 一旦关闭，`\\<dc>.<realm>\SYSVOL` 和 `NETLOGON` 就没有服务方了。域名
和 DC 主机名都解析到宿主 IP，而那个 IP 上的 445 已经是 samba_fs 的 smbd，它不提供
这两个共享。直接后果：

- **Windows 客户端加域会失败或不完整**（加域过程要访问 SYSVOL/NETLOGON）；
- **组策略（GPO）分发失效**；
- 依赖登录脚本的场景失效。

Linux/macOS 客户端只挂载文件共享、以及 LDAP 消费方（nextcloud、authentik、lam 等）
不受影响——它们都不走 SMB。

把 SYSVOL 搬到 samba_fs 上（bind mount DC 的 `/var/lib/samba/sysvol` 再定义共享）
在技术上可行，但 ACL 与 idmap 的一致性得另外论证，**不在本方案范围内**。

### 3.3 适用条件

方案 A 适用于：不需要 Windows 域加入与组策略、SMB 只用于文件共享的部署。这应当
写进开关的文档，并在切换时由 CLI 明确提示。

### 3.4 变体 A2（记录，不在本次范围）

保留 DC 的 smbd，改为**给宿主加一个 IP 别名**，两个 smbd 用
`bind interfaces only = yes` 分地址共存：DC 绑 `HOST_IP`，samba_fs 绑别名 IP。

- 优点：SYSVOL/GPO 全部保留，samba_fs 仍有独立 IP 和独立 A 记录，且**宿主能访问
  容器**（macvlan 做不到这点）。
- 代价：仍需要一次特权网络操作（`ip addr add`），但脚本比 macvlan 简单得多；
  samba_fs 的 smb.conf 需要新增 `bind interfaces only`（目前没有这一行）；
  `SAMBA_DC_INTERFACES` 要从接口名改成具体 IP，否则 `interfaces = eth0` 会把别名
  一起绑进去。
- 这两个参数（`SAMBA_DC_BIND_INTERFACES_ONLY`、`SAMBA_DC_INTERFACES`）已经是
  现成的 env，DC 侧不需要改镜像。

如果将来发现方案 A 的 SYSVOL 代价不可接受，A2 是替代路线而不是补丁。

## 4. 开关设计

### 4.1 开关放在哪里

**建议：`internal/runner/globals.yml` 里的全局参数**，而不是 samba_fs 的 module 参数。

```yaml
defaults:
  host_lan_mode: macvlan        # macvlan | host
changes:
  host_lan_mode:
    effect: container_recreate
    apply: switch-host-lan-mode
    description: 决定需要局域网身份的 module 是走 macvlan 还是宿主网络命名空间。
```

三条理由：

1. 它同时被 `samba_fs` 和 `samba_dc` 读取。全局所有权的 key 对所有 module 天然可见
   （`internal/runner/envscope.go:155`），module 所有权的 key 要靠 `consumes` 反向
   声明——而 samba_dc 是 samba_fs 的**依赖**，让依赖去 consume 依赖者的参数是把
   依赖方向拧反了。
2. runner 自己要读它（§4.2）。runner 读全局参数是正常的；读某个 module 的私有参数
   是层次倒置。
3. 它描述的是部署拓扑（"这台机器上的服务怎么出现在局域网里"），不是文件服务的功能
   设置。

`features.host_lan: required` **保持不变**——samba_fs 确实需要局域网身份，两种模式
下都需要；变的只是实现方式。

### 4.2 runner：把两个问题拆开

`hostLANRequired()`（`internal/runner/runner.go:680`）现在同时回答两个问题：
"有没有 module 需要局域网身份"和"要不要建 macvlan"。开关要求把它们分开：

- `hostLANRequired()`：保持原义，仅看 manifest。
- 新增 `macvlanRequired()` = `hostLANRequired() && HOST_LAN_MODE == "macvlan"`。

需要改门禁的三处调用：`hostnet.go:62`（VLAN 计算，改后 /30 宿主在 host 模式下可
render）、`runner.go:1077` 的 `ensureHostLAN`、以及 `runner.go:604` 与 `runner.go:672`
的 `removeMacvlan` 拆除路径。**拆除路径要特别小心**：从 macvlan 切到 host 时，旧的
`anas_macvlan` 网络和 `anas_bridge` 接口必须被拆掉，而此时新的判断已经是 false，
现有代码不会走到拆除分支——这是切换实现时最容易漏的一处。

### 4.3 compose 的两种形态怎么表达

一个 module 只有一个 compose 文件（`releaseComposeFile`，`runner.go:649`），且
`network_mode: host` 与 `networks:` 互斥，无法靠变量替换在同一份 YAML 里表达两种
形态。三条可行路径：

| 机制 | 做法 | 代价 |
| --- | --- | --- |
| **M1 hook 换文件（建议）** | module 同时保留 `docker-compose.yml`（macvlan）与 `docker-compose.host.yml`；`render_env` 阶段 hook 按模式把后者的内容写成前者 | 两份文件要同步维护；`docker-compose.host.yml` 不被 manifest 的 compose_file 校验覆盖 |
| M2 compose profiles | 单文件两个 service，用 YAML 锚点去重，靠 `COMPOSE_PROFILES` 选 | `COMPOSE_PROFILES` 是裸名，samba_fs 并不拥有它，得走 `exports` 越权发布；切换时 `down` 用的是新 profile，旧容器可能留下 |
| M3 `DisableServices` | 单文件两个 service，`services` 阶段禁用不生效的那个 | 两个 service 同名 `container_name`；compose 是否会为未启动的 service 校验 external 网络待验证 |

**建议 M1**，理由是渲染产物自解释：release 里的 `docker-compose.yml` 就是实际生效
的那一份，和这个仓库"制品是权威"的其他约定一致（如 `moduleEnv` 对 `.env` 的处理）。
机制已经现成：hook 的工作目录是 module 源码目录（`hook.go:118`），可以直接读同级
文件；`applyHookFiles` 在 `copyDir` 之后执行（`runner.go:998`/`:1012`），返回
`files: {"docker-compose.yml": ...}` 即可覆盖。

### 4.4 samba_dc 侧

- `smb.conf.envsubst:13` 的 `server services = -dns` 需要参数化为
  `${SAMBA_DC_SERVER_SERVICES}`，由 hook 按模式产出 `-dns` 或 `-dns -s3fs`。
- **注意**：模板里那行注释写的是 `-smbd`。AD DC 的文件服务在 `server services`
  里的名字是 `s3fs`（smbd 由它拉起），照抄 `-smbd` 很可能得到一个静默无效的配置。
  切换实现前必须实测确认（§6）。
- smb.conf 每次容器启动都会重新 envsubst（`11-samba_dc.sh` 的模板渲染在 provision
  分支之外），所以改 env + 重建容器即可生效，不涉及重新 provision。
- 镜像需要 bump revision（当前 `4.23.6-r5`）。

### 4.5 samba_fs 侧

host 模式下有三个 compose 键不能再用，需要替代方案：

1. **`hostname: ${SAMBA_FS_HOSTNAME}`**（`docker-compose.yml:13`）——Docker 拒绝
   host 网络下设置 hostname。影响：avahi 会用宿主主机名去广播。smbd 的 NetBIOS 名
   来自 smb.conf 的 `netbios name`（显式设置，不受影响），wsdd2 也是显式传
   `-N $SAMBA_FS_HOSTNAME`（不受影响）。**avahi 的广播名需要另行指定**
   （`avahi-daemon.conf` 的 `host-name=`），否则局域网里会看到宿主名而不是 SambaFS。
2. **`dns: ${LOCAL_DNS_SERVER}` / `dns_search:`**（`docker-compose.yml:20-23`）——
   host 模式下容器继承宿主的 resolv.conf。这两行现在是加域的命脉：没有它们，
   `net ads join` 会一直卡在 `kinit: Cannot contact any KDC`。
   samba_dc 用 host 网络且**恰好没有这三个键**，可以佐证这一限制。

   替代方案，建议 (b) 为主、(a) 写进文档前置条件：
   - (a) 要求宿主 resolv.conf 指向本机——DC 的 bind9 本来就在宿主 :53 上；
   - (b) 由 hook 显式定位 DC：compose 用 `extra_hosts`（host 模式下仍然生效，写的是
     容器自己的 /etc/hosts）把 realm 与 DC FQDN 指到 `HOST_IP`，krb5.conf 写死
     `kdc =`，smb.conf 补 `password server`，加域用 `net ads join -S`；
   - (c) 容器启动时自写 /etc/resolv.conf——host 模式下 Docker 是否仍给容器独立的
     resolv.conf 待验证（§6）。
3. `hook/main.go:180` 的 `VLAN_BRIDGE_IP` 兜底在 host 模式下没有意义，应改为按模式
   分支。

镜像同样需要 bump revision（当前 `4.23.6-r2`）。

### 4.6 变更语义

切换不动任何磁盘数据：共享内容在 `USER_DATA_PATH`，机器账号和 NetBIOS 名都不变，
变的只是监听地址。因此 `effect: container_recreate` 是对的，不是 `data_migrate`，
也不需要触发快照（对照 `docs/reference/contracts/snapshot.md:326` 的表）。

但它是**跨 module 的重建**：samba_dc 和 samba_fs 必须一起重建，且有顺序（先让 DC
放开 445，再起 FS）。`apply: switch-host-lan-mode` 需要覆盖这个次序。

## 5. 切换过程与运维

### 5.1 前置检查

切到 host 模式前应当在 apply 阶段失败，而不是等容器起不来：

- 宿主 445/139 是否已被占用（宿主自带的 smbd、或 DC 侧改动未生效）；
- 5353（宿主自己的 avahi-daemon）、3702/5357（wsdd）是否冲突；
- 宿主 resolv.conf 能否解析 realm（若采用 §4.5 的 (a) 路线）。

### 5.2 DNS A 记录：现有实现的缺口

`join_domain()` 只在 `net ads testjoin` 失败时才重新加域。切换模式后加域**仍然有效**，
所以这段不会执行——而 samba_fs 的 IP 已经从 macvlan 池换成了宿主 IP，AD DNS 里
`SambaFS` 的 A 记录还是旧地址，客户端会解析到一个不存在的地址。

建议：在启动流程里无条件执行一次 `net ads dns register`（幂等、开销可忽略）。
这同时修掉一个既有隐患——macvlan 池里的地址变化时，现在也没有任何东西去更新 A 记录。

### 5.3 回滚

切回 macvlan 需要：重建 `anas_macvlan` 网络与 `anas_bridge` 接口（runner 已有能力）、
DC 重新启用 s3fs、以及再执行一次 §5.2 的 A 记录注册。前提是 sudoers 授权仍在。

### 5.4 验证清单

1. `smbclient -L //<host> -k` 能列出共享；
2. Windows 资源管理器 / macOS 访达能自动发现（验证 wsdd2 与 avahi 在 host 模式下
   的广播名）；
3. AD DNS 里 `SambaFS` 的 A 记录指向宿主 IP；
4. `wbinfo -t` 通过（compose 的 healthcheck 已经在测这个）；
5. smbd 日志里客户端源 IP 是真实地址；
6. DC 侧 `ss -ltn` 确认 445 已让出，389/636/88 仍在。

## 6. 待验证事项

实现前需要逐条实测，任何一条为否都会改变方案形状：

1. `server services` 里关闭 AD DC 文件服务的正确 token 是 `-s3fs` 还是 `-smbd`
   （§4.4）。
2. host 网络模式下，Docker 是否给容器独立的 `/etc/resolv.conf`（决定 §4.5 的 (c)
   是否可行）。
3. host 网络模式下 `extra_hosts` 是否生效（预期生效，需确认）。
4. M3 路线中，`docker compose up <service>` 是否会为未选中的 service 校验/创建
   external 网络（决定 M3 是否可行）。
5. 关闭 DC 的 s3fs 后，`net ads join`、winbind 的日常运行、以及 anchor worker 是否
   完全不依赖 SMB（预期不依赖，走 LDAP/Kerberos）。
6. avahi 在 host 模式下的广播名与宿主既有 avahi-daemon 的共存行为。

## 7. 影响面

| 文件 | 改动 |
| --- | --- |
| `internal/runner/globals.yml` | 新增 `host_lan_mode` 参数与 change policy |
| `internal/runner/runner.go` | 拆出 `macvlanRequired()`，修正三处门禁与拆除路径 |
| `internal/runner/hostnet.go` | VLAN 计算门禁改用新判断 |
| `modules/samba_fs/docker-compose.host.yml` | 新增 |
| `modules/samba_fs/hook/main.go` | `render_env` 选 compose；DNS 兜底分模式 |
| `modules/samba_fs/module.yml` | revision bump |
| `modules/samba_fs/samba_fs/root/...` | avahi 广播名、krb5/smb.conf 的 DC 定位、启动时 `net ads dns register` |
| `modules/samba_dc/samba_dc/root/root/smb.conf.envsubst` | `server services` 参数化 |
| `modules/samba_dc/hook/main.go` | 按模式产出 `SAMBA_DC_SERVER_SERVICES` |
| `modules/samba_dc/module.yml` | revision bump |
| `docs/operations/networking.md`、`runbooks/macvlan-sudoers.md` | 说明两种模式与各自前置 |
| `test-env/` | 冒烟测试需要覆盖两种模式 |

## 8. 不做什么

- 不改默认值。默认仍是 macvlan，现有部署零影响。
- 不做桥接 + 端口映射模式：NAT 会改写客户端源 IP、且发现协议不可用，两头不讨好。
- 不做 SYSVOL 搬迁（§3.2）。
- 不做 A2 变体（§3.4）——先把开关框架落地，A2 只是同一个开关的第三个取值。
