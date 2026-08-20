---
doc_type: research
created: 2026-08-19
updated: 2026-08-19
evidence_as_of: 2026-08-19
---

# BIND 9 开源 Web 管理工具调研

本报告研究 BIND 9 的开源 Web 管理工具，并专门判断它们是否适合 ANAS 当前的
`samba_dc` Module。动态项目状态采集于 2026-08-19；结论是选型快照，不是生产部署说明。

## 1. 结论先行

1. **ANAS 当前没有适合直接接入生产的第三方 BIND Web 写管理器。** `samba_dc` 运行的是
   Samba + BIND9-DLZ：AD zone 和记录存储、复制在 Samba 目录数据库里，不是普通 zone
   file。Webmin、facileManager/fmDNS、NicTool、ISPConfig 和大多数新兴 BIND 面板都假设自己
   能写 zone file、生成 `named.conf`，或成为 DNS 的新 source of truth；把它们直接指向
   `samba_dc` 会形成双写、覆盖或“界面显示成功但 AD 数据未变”的风险。
2. **现阶段写操作继续使用 `samba-tool dns`；Web 化应进入既有 `anasd` 管理面。** Samba
   官方同时把 `samba-tool dns` 和 Windows DNS MMC 列为 AD DNS 管理路径；前者覆盖 zone
   与 A/AAAA/PTR/CNAME/NS/MX/SRV/TXT 的增删查改。对 ANAS，最稳妥的产品方向是在既有
   [Web API 与管理前端规划](/plans/web-api-admin-console)中增加一个窄的
   Samba DNS adapter，而不是再部署一个拥有独立用户、Secret、数据库、审计和升级面的
   DNS 控制台。
3. **Samba Conductor 是最贴近当前数据面的第三方 PoC 候选，但不能生产化。** 它通过
   LDAPS + `samba-tool` 管理 Samba AD，Web DNS 页覆盖 zone、常见记录新增和删除；因此
   架构上能绕开 zone file 双写。可是上游明确标注为 WIP、尚未 ready for production，
   当前无稳定 release，社区和运维证据都很小。可用它验证交互与命令映射，不应先做 ANAS
   正式 Module。
4. **若目标是标准、非 DLZ 的独立 BIND 权威集群，首选评估 facileManager/fmDNS。** 当前
   fmDNS 7.4.2 支持多服务器、zone、views、ACL、TSIG/key、DNSSEC、配置检查、分区授权和
   REST API，且 2025—2026 持续发布。代价是 PHP + MySQL + 每台 DNS 主机的 client，且它
   会成为配置 source of truth；这是迁移项目，不是给现有 BIND 随手加一个 UI。
5. **若只需单机静态 BIND 管理，Webmin 仍是最成熟的轻量选择。** 它有长期维护、
   BSD-3-Clause 许可证、views 和细粒度到 zone 的权限。但官方文档明确说它直接改所有配置与
   zone file，并警告不要用于 DHCP 等动态更新 zone。这个限制同样排除 BIND9-DLZ。
6. **若只需监控，ISC Stork 是最可信的开源选择。** Stork 2.4.1 是 ISC 当前稳定版，能
   发现 BIND、查看配置、比较 zone serial、用 AXFR 显示记录并导出 Prometheus/Grafana
   指标；BIND 管理目前仍是只读。ANAS 当前全局 `allow-transfer { none; };`，所以接入后
   默认只能看到配置/状态，若要看记录必须新增仅 loopback/专用 TSIG 可用的 AXFR 策略。
7. **8perezm/bind-gui 的协议方向正确，但成熟度不足。** 它用 RFC 2136 + TSIG 改记录、用
   RNDC 管 zone 生命周期，并能展示 DNSSEC 和 statistics channel；这比直接改动态 zone
   file 更合理。但项目在 2026 年 7 月才公开，单用户、无审计，示例仍挂载 Docker socket，
   只适合作为 lab PoC。它也没有实现 Samba 所需的 GSS-TSIG/AD DNS 权限模型。
8. **PowerDNS-Admin、Technitium DNS、AdGuard Home 和 Pi-hole 不属于本题候选。** 它们
   管理各自的 DNS 服务，不能管理现有 BIND；迁移到它们等于替换 DNS 数据面。SpatiumDDI
   虽然内置 BIND、Web、API、IPAM 和 DHCP，但上游仍标为 beta，而且导入后由它接管配置，
   也不是 `samba_dc` 的外挂 UI。

综合建议：**ANAS 不新增通用 BIND Web Module；先给 `anasd` 增加只读 Samba DNS 页面，再
通过窄权限 adapter 逐步开放记录 CRUD。** 独立标准 BIND 的未来 Module 才评估
facileManager/fmDNS；监控需求单独评估 Stork。

## 2. 范围、方法与判定口径

```yaml
topic: bind9-open-source-web-management
title: BIND 9 开源 Web 管理工具
snapshot_date: 2026-08-19
decision_for:
  - ANAS samba_dc 的 DNS Web 管理路径
  - 未来独立 BIND Module 的候选
must_be:
  - 可获得源代码并允许自行部署
  - 具有 Web 管理或 Web 监控界面
  - 明确支持 BIND 9，或明确支持 Samba AD DNS
deployment_target:
  os: Linux
  preferred_runtime: Docker Engine + Docker Compose v2
  ingress: Traefik HTTPS
current_dns_backend: Samba 4.23.6 + BIND9-DLZ
questions:
  - 是否管理已有 BIND，还是替换并接管它？
  - 通过 zone file、数据库生成、RFC 2136、RNDC、AXFR 还是 Samba RPC 工作？
  - 是否兼容动态 zone、DNSSEC、views 和 Samba AD DNS？
  - 是否有 RBAC、审计、API、备份和可验证的升级路径？
search_date: 2026-08-19
```

候选发现使用 GitHub 主题/仓库搜索、ISC/Samba 官方工具与文档、项目官网，以及社区中
“BIND GUI”“DNS management”反向检索。功能、许可证和状态结论最终回到官方文档、官方
仓库、release/changelog 与仓库安全说明核验。Star 只作生态规模信号，不作为质量分数。

本报告把“管理 BIND”拆成五类，不能混为同一能力：

| 类型 | 数据写入方式 | 代表项目 | 对现有服务的含义 |
| --- | --- | --- | --- |
| 原地文件管理 | 直接改 `named.conf` / zone file，再 reload | Webmin | 适合静态 file-backed zone；动态/DLZ 有覆盖风险 |
| 独立控制面 | 数据库存记录，生成并下发 BIND 配置 | fmDNS、NicTool、ISPConfig、DNSHTTP、SpatiumDDI | 控制面成为 source of truth，必须正式迁移 |
| 协议管理 | RFC 2136 更新记录，RNDC 管运行态/zone | bind-gui | 更适合动态 zone；仍取决于认证与 backend |
| 观察面 | RNDC/statistics/AXFR 只读采集 | ISC Stork | 不改变 DNS；不能替代记录管理 |
| Samba AD 管理 | Samba DNS RPC/`samba-tool`，记录进入 AD 数据库 | Samba Conductor、ANAS 目标 adapter | 与 BIND9-DLZ 的真实数据面一致 |

评分只表达本报告目标的适配度：`A` 可进入生产前 PoC，`B` 有明确场景但需改造，`C` 仅
lab/迁移研究，`D` 不再建议投入。没有运行候选做真实变更，本报告中的 `A` 仍不表示已经
生产验证。

## 3. 先理解 ANAS 的 BIND9-DLZ 边界

### 3.1 Zone 不在文件里

Samba 官方的 [BIND9_DLZ 文档](https://wiki.samba.org/index.php/BIND9_DLZ_DNS_Back_End)
说明，DLZ 插件直接访问 Samba AD 数据库，zone 存储并复制在目录中；BIND 必须与 AD DC
位于同一主机语义中。当前 ANAS 配置也印证了这一点：

- `modules/samba_dc` 挂载 `/var/lib/samba` 作为关键状态；
- `named.conf` 只定义 resolver、安全策略和两个本地 file-backed zone；
- AD zone 来自 `include "/var/lib/samba/bind-dns/named.conf"`；
- secure dynamic update 由 Samba DLZ 与 `tkey-gssapi-keytab` 授权；
- 全局 zone transfer 当前是 `allow-transfer { none; };`。

因此，能解析 `named.conf` 不等于能枚举或修改 AD 记录；能写 `/etc/bind` 更不等于写入
Samba 的 DomainDnsZones/ForestDnsZones 分区。

### 3.2 正确的管理边界是 Samba DNS API

Samba 官方 [DNS Administration](https://wiki.samba.org/index.php/DNS_Administration)同时
给出三条管理路径：

- Linux/Unix 使用 `samba-tool dns`；
- Windows 使用 RSAT DNS MMC；
- 历史 Linux `admin-tools` GUI 以 `samba-tool` 为 backend。

当前 [`samba-tool` 手册](https://www.samba.org/samba/docs/current/man-html/samba-tool.8.html)
列出 `dns zonelist/zoneinfo/zonecreate/zonedelete` 和
`dns query/add/update/delete`。支持记录类型为 A、AAAA、PTR、CNAME、NS、MX、SRV、TXT；
这也是 ANAS 第一版 Web DNS 功能应保守采用的能力边界。

### 3.3 通用 BIND UI 的三个典型失败模式

1. **看不到 zone。** UI 只扫描 `zone { file ...; };`，DLZ zone 没有可供它读写的普通
   zone file。
2. **覆盖动态状态。** BIND 动态更新会写 `.jnl`；直接改静态 file 而不正确执行
   freeze/sync/thaw，会丢更新或让内存、journal 与文件分叉。Webmin 官方因此明确警告不要
   管理动态更新 zone。
3. **建立第二 source of truth。** fmDNS/NicTool/ISPConfig 等把 SQL 数据生成成 BIND 配置。
   对普通权威 DNS 这是合理架构，但对 AD DNS 等于绕过 AD 复制、ACL、时间戳和安全动态
   更新语义。

## 4. 核心候选对比

| 项目 | 许可证 / 当前状态 | 写入模型 | RBAC / 审计 / API | 标准 BIND | ANAS BIND9-DLZ | 结论 |
| --- | --- | --- | --- | --- | --- | --- |
| **Samba Conductor** | MIT；WIP，无稳定生产承诺 | LDAPS + `samba-tool` | Domain Admins RBAC；会话凭据加密；API 稳定性未承诺 | 非主要目标 | **架构匹配，未验证** | **B，交互与 adapter PoC** |
| **facileManager/fmDNS 7.4.2** | GPL-2.0；2026-03 发布，持续维护 | SQL 控制面生成并通过 client 下发配置 | 分 zone 权限、变更日志、REST API、2FA | **强** | 不兼容现有 AD source of truth | **A-，独立 BIND 首选** |
| **Webmin 2.653** | BSD-3-Clause；成熟、持续发布 | 直接编辑 BIND 文件 | Webmin 用户/组、分 zone 权限；无 DNS 专用稳定 REST | 静态单机强 | **禁止**：动态/DLZ | **B，静态单机** |
| **ISC Stork 2.4.1** | MPL-2.0；ISC 稳定版 | 只读 RNDC/config/statistics/AXFR | LDAP 管理认证；监控 API/指标；无 BIND CRUD | **观察面强** | 可做只读 PoC；AXFR 当前被禁 | **A，监控；非管理器** |
| **8perezm/bind-gui** | AGPL-3.0；2026-07 新项目 | RFC 2136 + TSIG、RNDC | 单管理员、无审计；有内部 API | 协议方向好、成熟度低 | 无 GSS-TSIG/AD 模型 | **C+，lab PoC** |
| **NicTool 2.41** | AGPL-3.0；2026-05 release | RDBMS + BIND export | 用户/组、zone delegation、完整变更日志、SOAP API | 传统权威集群可用 | 不兼容现有 AD source of truth | **B-，托管商/存量系统** |
| **ISPConfig 3.3.1p1** | BSD；成熟完整 hosting panel | 自有 DB/模板生成 BIND 或 PowerDNS 配置 | admin/reseller/client 多级权限、remote API | 功能成熟但范围很大 | 不兼容现有 AD source of truth | **C，过重** |
| **SpatiumDDI** | Apache-2.0；2026 beta | Web/API 数据库控制面，托管自己的 BIND agent | 丰富 RBAC、OIDC/LDAP/SAML、审计、REST | 功能广但早期且资源重 | 只能迁移/替换，不能外挂 | **C，DDI lab/试点** |
| **Bind9 Web Manager / DNSHTTP** | GPL-3.0；当前称无新功能计划 | 自有 Web/DB + BIND/复制 | 用户组权限、token API | 需深度安全 PoC | 不兼容 | **C-，不优先** |
| **NamedManager** | AGPL；最后提交 2020-10 | MySQL + agent 生成 BIND 文件 | 用户、SOAP API | 历史方案 | 不兼容 | **D，停止维护** |

### 4.1 Samba Conductor：唯一直接面向 Samba AD DNS 的 Web 候选

[Samba Conductor 仓库](https://github.com/edimarlnx/samba-conductor)明确支持用户、组、OU、
计算机、DNS、GPO 和备份，Web 层可单独运行并连接外部 Samba DC。其
[DNS 文档](https://github.com/edimarlnx/samba-conductor/blob/main/docs/admin/dns-management.md)
覆盖 zone 列表、记录查看、A/AAAA/CNAME/MX/TXT/SRV/PTR/NS 新增与删除；技术栈声明 AD
集成使用 LDAPS + `samba-tool`。

这使它成为唯一不会天然误解 BIND9-DLZ 存储模型的 Web 候选。由于 `samba-tool dns` 面向
Samba DNS 服务，而不是某一种查询 backend，**推断**它应同时适用于 Samba internal DNS 和
BIND9-DLZ；但上游没有提供 ANAS 形态的兼容性矩阵，必须做黑盒 PoC 后才能确认。

不能直接选它生产化的原因也很明确：

- 上游 README 明示 WIP、not yet ready for production，API/数据格式可能无通知变化；
- 尚无稳定 release，项目历史和外部生产证据都很少；
- 它带入 Meteor、React、MongoDB 和自身会话/RBAC，和 ANAS 正在建设的 `anasd`、IAM、
  Traefik、Secret、审计面重复；
- DNS 页当前文档只有新增和删除，没有更新、zone 创建/删除、批量 diff、保护 AD 关键记录、
  操作审计和回滚的完整契约；
- [安全说明](https://github.com/edimarlnx/samba-conductor/blob/main/SECURITY.md)描述了会话内
  AES-256-GCM 凭据与 30 分钟 TTL，但“无落盘密码”仍意味着 Web 进程在会话期持有高权限
  凭据，必须纳入威胁模型。

适合用途：用它的页面与命令映射做 UX 参考，或在隔离环境验证 BIND9-DLZ 的读写兼容性。

### 4.2 facileManager/fmDNS：标准 BIND 集群的成熟首选

[fmDNS 7.4.2](https://www.facilemanager.com/modules/fmdns/)发布于 2026-03-06，支持一个 Web
控制面管理多台 BIND：zone、views、ACL、key、logging、controls、DNSSEC，以及全局配置和
单机 override。配置可经 cron、HTTP(S) 或 SSH 下发；用户可限制到指定 zone。7.2 起 REST
API 重写为 JSON，近期 release 仍在修复 SOA、动态 zone、DNSSEC、API reload 与 stored XSS。

优势：

- 多服务器和 split-horizon 是一等能力，不只是单机文件编辑器；
- 有 zone 级授权、配置/记录变更日志、模板、反向记录联动、配置检查与预览；
- 活跃版本已适配 PHP 8、BIND 新术语、`dnssec-policy` 和较新的 record type；
- 控制面宕机时，已下发的 BIND 配置仍可继续提供 DNS。

成本和限制：

- [官方仓库](https://github.com/facileManager/facileManager)要求 PHP、Web server、MySQL，
  每个 DNS 节点安装 PHP client；项目说明当前主要由一位维护者业余维护；
- 它的数据库与生成器成为 source of truth，必须禁止手工和其他 UI 同时改相同配置；
- [官方 FAQ](https://docs.facilemanager.com/faq/)说明不能导入完整 BIND 配置，只能导入 views
  和 zones；复杂 ACL、logging、options 与 include 需要手工重建和对照；
- 2026-03 的 7.4.2 刚修复多处 stored XSS，生产评估必须锁定已修复版本并做 Web 安全回归；
- 当前没有适合 ANAS 的官方容器拓扑，Module 封装要自行维护镜像、数据库迁移和 client
  rollout。

结论：未来若 ANAS 增加**独立权威 BIND**而不是 AD DNS，可把 fmDNS 作为首个完整 PoC；
当前 `samba_dc` 不应接入。

### 4.3 Webmin：成熟，但官方明确不适合动态 zone

[Webmin BIND 模块](https://webmin.com/docs/modules/bind-dns-server/)支持 BIND 8/9、主/从/
forward/stub zone、views、ACL、常见记录、反向记录联动，以及细到指定 zone 和功能的用户
权限。Webmin 本身是 BSD-3-Clause，当前仍有持续 release；2.653 发布于 2026-07。

决定性限制来自它自己的文档：模块“always updates all of these files directly”，如果 DHCP
等程序通过动态更新修改 zone，就不应使用此模块，因为它可能干扰这些变更。Webmin 又以
root 权限运行；虽然模块 ACL 能限制页面功能，一旦开放原始文件、命令或其他高权限模块，
边界就扩大为整机管理。

结论：适合管理员单人维护、file-backed、非动态的 BIND；不适合 Samba AD、DHCP DDNS、
inline-signed dynamic zone，也不适合为了一个 DNS 页面给 `samba_dc` 增加 root Web 面。

### 4.4 ISC Stork：官方 BIND 观察面

[ISC Stork 官方页面](https://www.isc.org/stork/)把 BIND 支持定义为早期、只读的 DNS 能力。
当前稳定版 2.4.1 可：

- 发现和展示 BIND/主机状态与版本；
- 通过 RNDC 取回并解析部分或完整配置；
- 轮询 BIND/PowerDNS 的 zone 与 serial，发现副本落后；
- 经 AXFR 获取 zone 内容并按 RR type 过滤；
- 导出 DNS 查询/响应和主机指标给 Prometheus/Grafana。

[Stork DNS 文档](https://stork.readthedocs.io/en/latest/dns.html)说明，非默认 view 的 AXFR
必须用 TSIG 消除 view 歧义。ANAS 目前 `allow-transfer none`，所以接入不能直接获得 zone
内容。若确实需要，应只创建供本机 Stork agent 使用的专用 TSIG，并将 `allow-transfer`
限制到该 key/loopback；不能改成 `any`，也不能把 secret 放进前端或通用日志。

Stork 不能新增或修改 BIND 记录，因此它和 ANAS DNS 管理页是互补关系。若只需健康、版本、
zone serial 和查询指标，引入 Stork server、agent、数据库和 Grafana 也可能过重；应先比较
直接把有限的 RNDC/statistics 数据接进 `anasd` 的成本。

### 4.5 bind-gui：值得观察的 RFC 2136 轻量实现

[8perezm/bind-gui](https://github.com/8perezm/bind-gui)使用 Next.js，无数据库，记录编辑走
`nsupdate` + HMAC-SHA256 TSIG，zone 创建/删除走 `rndc addzone/delzone`，还能开关 inline
DNSSEC、读取 statistics channel。它保留 BIND 为实际数据面，比每次编辑文件并重启容器的
设计更稳妥。

不过其[安全文档](https://github.com/8perezm/bind-gui/blob/main/docs/security.md)明确记录：

- 单一管理员账号；
- 尚无 audit log；
- statistics channel 没有认证，只依赖容器网络隔离；
- Docker socket 已不再是写操作必需，但当前 Compose 示例仍以只读方式挂载；
- GUI 需要对配置目录可写，以创建/删除 zone file；
- 所有 zone 默认共用一把更新/RNDC key，未形成每 zone/角色最小权限模型。

它用普通 TSIG，而 Samba AD secure update 使用 Kerberos/GSS-TSIG 和 AD ACL。即使把 GUI
连到 ANAS 的 53/953，也不能据此推导它可安全管理 DLZ zone。结论是标准 BIND lab 可测，
ANAS 不接入。

### 4.6 NicTool 与 ISPConfig：成熟控制面，但产品边界不合适

[NicTool](https://github.com/NicTool/NicTool)提供用户/组、zone/record delegation、全量变更
日志、RDBMS、Web CGI、SOAP API，并能导入/导出 BIND。当前 2.41 release 在 2026-05，说明
老一代 Perl/CGI 代码仍在维护；同时上游正在开发 Node.js + REST 的 NicTool 3。它适合已有
NicTool 运维经验或 DNS 托管/委派场景，但新旧代际并存、传统部署栈和 export-based 模型使
其不如 fmDNS 适合 ANAS 新建 Module。

[ISPConfig](https://www.ispconfig.org/ispconfig/)是成熟 BSD hosting panel，支持 BIND/
PowerDNS、多服务器、集群和 admin/reseller/client 权限。它的价值在网站、邮件、数据库、
FTP、DNS 统一托管；ANAS 已有各自 Module 和正在建设的统一控制台，再引入 ISPConfig 会产生
第二套服务生命周期、身份和配置 owner。只为 DNS 使用它不划算。

### 4.7 SpatiumDDI、DNSHTTP 与 NamedManager

- [SpatiumDDI](https://github.com/spatiumddi/spatiumddi)是 Apache-2.0 的完整 DDI，提供
  BIND/PowerDNS/Technitium、Kea、IPAM、REST、RBAC、LDAP/OIDC/SAML、审计和 appliance；
  上游明确标为 beta，只建议 lab、homelab 和非关键 pilot。其 BIND import 是一次性迁移，
  导入后 SpatiumDDI 成为 source of truth；推荐控制面为 4 vCPU、8 GiB RAM、40 GiB SSD，
  与 ANAS 当前轻量 DNS UI 需求不匹配。
- [Bind9 Web Manager / DNSHTTP](https://github.com/bugfishtm/Bind9-Web-Manager)提供记录、用户
  组、复制、token API 和 Docker/脚本安装，但仓库历史很短，README 表示暂无新功能计划，
  安装后初始凭据是 `admin/changeme`。在建立独立安全审计、升级和恢复证据前不应承载关键
  DNS。
- [NamedManager](https://github.com/jethrocarr/namedmanager)曾是常见的 AGPL PHP/MySQL +
  agent 方案，但官方仓库最后提交停在 2020-10，无 release 轨迹，不应新部署。

## 5. 针对 ANAS 的推荐架构

### 5.1 不新增独立 DNS 管理控制面

ANAS 已经规划 `anasd` 统一承担 HTTPS、IAM、异步任务、Secret 脱敏和审计。再加一个第三方
DNS UI 会重复：

- 登录、角色与破玻璃账号；
- 域管理员或 DNS 委派凭据保存；
- Traefik 路由和 TLS；
- 数据库、备份、升级、漏洞响应；
- 谁拥有 DNS 配置和谁记录审计事件。

DNS 记录 CRUD 是 `samba_dc` 的领域操作，不应伪装成通用 Module 配置项，也不能通过修改
部署 `.env` 后重建容器实现。它应作为 `anasd` 的运行态资源 API，底层调用窄的 Samba DNS
adapter。

### 5.2 建议的数据流

```text
Browser
  -> Traefik / oauth2-proxy / ANAS 管理会话
  -> anasd DNS application service
  -> narrow Samba DNS adapter
  -> Samba DNS RPC / samba-tool semantics
  -> AD DNS partitions
  -> BIND9-DLZ immediate read path
```

adapter 有两种可 PoC 的实现方向：

1. `anasd` 通过受约束执行器进入 `samba_dc` 容器，调用固定参数集合的 `samba-tool dns`；
2. 在 `samba_dc` 内增加只监听 loopback/Unix socket 的小型 helper，直接复用 Samba Python
   binding，并给 `anasd` 一个窄的类型化协议。

第二种能避免解析人类输出，也不需要把域管理员密码传给 Docker exec；但会增加 helper
协议和 Python binding 兼容成本。PoC 应先验证当前 Samba 4.23.6 的结构化输出或 Python API，
再做选择。无论哪种，都不得挂载 `/var/lib/samba` 给 Web 进程，也不得把 Docker socket
暴露给前端容器。

### 5.3 分阶段实施

#### 阶段 0：命令和权限 PoC

- 在测试域用 `samba-tool dns zonelist/query/add/update/delete` 完成 A、AAAA、PTR、CNAME、
  MX、TXT、SRV、NS 全路径；
- 同时在两个查询路径验证：`samba-tool dns query` 与 `dig @127.0.0.1`；
- 验证变更在重启 BIND/Samba 后仍存在，并在第二 DC（如未来支持）复制；
- 研究专用 DNS 管理账号的最小 AD ACL；若不能证明最小权限，不把 Domain Admin 凭据放进
  常驻 Web 进程。

#### 阶段 1：只读页面

- zone 列表、zone info、记录列表和搜索；
- BIND 版本、进程健康、RNDC status、cache/query 统计；
- 清楚标识 AD 系统 zone、反向 zone 和本地 file-backed zone；
- 默认隐藏或警示 `_msdcs`、`_sites`、`_tcp`、`_udp` 等关键命名空间。

#### 阶段 2：低风险记录写入

- 先开 A、AAAA、CNAME、TXT、PTR；
- 所有请求做 owner、FQDN、IP、TTL、record data 的类型化校验；
- 写前展示 diff，写后立即从 Samba RPC 和 BIND 查询双读确认；
- 使用幂等请求 ID、并发版本/前值匹配，避免两人互相覆盖；
- 审计 actor、zone、owner、type、旧值、新值、结果和关联任务，不记录凭据。

#### 阶段 3：高风险操作

- MX/SRV/NS、zone 创建/删除、批量导入必须二次确认；
- apex SOA/NS、DC locator SRV、ForestDnsZones/DomainDnsZones 关键记录默认禁止删除；
- zone 删除、批量变更前触发 Samba/ANAS 一致性快照或至少导出可回放清单；
- 失败必须停在可解释状态，不能通过“重启 BIND”掩盖数据库写入失败。

### 5.4 建议 API 最小面

```text
GET    /api/v1/dns/zones
GET    /api/v1/dns/zones/{zone}
GET    /api/v1/dns/zones/{zone}/records?name=&type=
POST   /api/v1/dns/zones/{zone}/records
PUT    /api/v1/dns/zones/{zone}/records/{record-id}
DELETE /api/v1/dns/zones/{zone}/records/{record-id}
GET    /api/v1/dns/server
```

`record-id` 不能只用 `name/type`，因为同一 RRset 可以有多个值；更新/删除必须带完整旧值或
服务器生成的 opaque ID/ETag。第一版不要开放任意 `named.conf` 编辑、RNDC 任意命令或 shell。

## 6. 安全与生产验收清单

### 6.1 身份与授权

- Web 登录复用 ANAS IAM；本地破玻璃账号与 Samba 管理凭据继续隔离。
- 建立 `dns.viewer`、`dns.editor`、`dns.admin` 三层权限；viewer 不能借 API 触发 AXFR Secret
  暴露、原始配置读取或命令执行。
- editor 默认不能写系统 zone、zone apex NS/SOA 和 DC locator 记录。
- 后端使用专用 DNS 管理身份或本地受限 helper；禁止复用普通 LDAP bind、password-bind、
  anchor 或全局 Samba admin Secret。

### 6.2 输入和命令边界

- 不拼 shell 字符串；命令与每个参数使用固定 argv，zone/name/type 做 allowlist 校验。
- 仅支持 `samba-tool` 官方列出的记录类型；未知类型在有类型化 parser 前只读展示。
- TXT、MX、SRV、FQDN 尾点、IDN/punycode、IPv6 压缩和反向 zone 必须有专项测试。
- 所有外部调用使用 context timeout、并发上限和输出大小上限；错误输出脱敏。

### 6.3 一致性和恢复

- 写前记录旧 RRset，写后分别经 Samba RPC 和 DNS query 验证。
- 验证同一变更在 BIND restart、Samba restart 和主机重启后仍然存在。
- 对批量导入执行 preview、逐 zone/逐 RRset 事务边界和失败清单。
- 备份覆盖 `/var/lib/samba`，不能只备份 `/etc/bind` 或 UI 数据库。
- 恢复演练必须验证 AD、Kerberos、LDAP 与 `_msdcs`/SRV 记录，不以 `dig A` 成功作为完整恢复。

### 6.4 Stork 或其他观察面的额外边界

- statistics channel 只绑定 loopback/管理网络，不能直接暴露到 LAN/Internet。
- 若启用 AXFR，只授权专用 TSIG 与必要 zone/view；保持默认拒绝其他客户端。
- Prometheus/Grafana 标签避免把敏感内网 qname、客户端 IP 无界扩散或形成高基数。
- Stork 版本告警不能替代 ANAS 自己的镜像/BIND 安全更新流程。

## 7. 候选去向台账

| 项目 | 发现分类 | 证据状态 | 去向 |
| --- | --- | --- | --- |
| Samba Conductor | Samba AD 专用 Web | verified / maturity partial | ANAS BIND9-DLZ 交互与兼容 PoC |
| facileManager/fmDNS | 多服务器 BIND 控制面 | verified | 未来独立 BIND Module 首选 PoC |
| Webmin | 通用主机面板 + BIND module | verified | 静态单机备选；排除动态/DLZ |
| ISC Stork | BIND/Kea 观察面 | verified | 监控 PoC；不计作写管理器 |
| 8perezm/bind-gui | 轻量 RFC 2136 UI | verified / immature | lab 观察，等待审计/RBAC/release |
| NicTool | DNS 托管控制面 | verified | 传统生态备选，不优先新建 Module |
| ISPConfig | 完整 hosting panel | verified | 范围过大，排除 ANAS DNS-only |
| SpatiumDDI | 完整 DDI | verified / beta | lab/非关键 pilot；属于数据面替换 |
| Bind9 Web Manager / DNSHTTP | 独立 BIND panel | partial / immature | 不优先，需安全和恢复审计 |
| NamedManager | 历史 BIND panel | verified / stopped | 排除新部署 |
| PowerDNS-Admin | PowerDNS UI | verified / category mismatch | 排除：不管理 BIND |
| Technitium DNS Server | 自带 Web 的 DNS server | verified / category mismatch | 排除：替换 BIND，不是管理器 |
| AdGuard Home / Pi-hole | 过滤 resolver | verified / category mismatch | 排除：不管理权威 BIND/AD DNS |
| Windows RSAT DNS MMC | Samba 官方兼容 GUI | verified / not web, not OSS | 运维备选，不进入开源 Web 排名 |
| OpenRSAT / admin-tools | Samba 桌面 GUI | partial / not web | 交互参考，不进入 Web 排名 |

## 8. 最终决策

### 对当前 `samba_dc`

**不安装 Webmin、fmDNS、NicTool、ISPConfig、bind-gui 或 DNSHTTP 来写 AD zone。** 近期保持
`samba-tool dns` 为受支持管理路径；把只读 DNS 资源页加入 `anasd` 规划，并以 Samba
Conductor 为 UX/兼容性参考。写操作按“低风险记录 → 高风险记录 → zone 生命周期”分阶段，
所有变更走 Samba DNS 语义、双读验证和 ANAS 审计。

### 对未来独立标准 BIND

若 ANAS 未来确实要提供与 AD 无关的权威 DNS Module：

1. 先 PoC **facileManager/fmDNS**，验证 amd64/arm64 镜像、MySQL/MariaDB 复用、Traefik、
   OIDC/LDAP 边界、升级、备份恢复和 client rollout；
2. 只需单机静态 zone 时，用 **Webmin** 作为功能基准，但避免给整个系统引入 root 面；
3. 只需 BIND 健康与 zone serial 观察时，PoC **ISC Stork**；
4. `bind-gui` 等新项目至少等到稳定 release、多人维护、审计/RBAC 和无 Docker socket 默认
   拓扑后再重新评分。

### Go / No-Go 门槛

任何进入生产的 DNS Web 管理路径都必须同时满足：

- 唯一 source of truth，无文件/UI/AD 双写；
- 最小权限凭据，不把 Domain Admin 或 RNDC 全权 key 暴露给普通 Web 进程；
- 完整 actor/change/result 审计；
- 写前 preview、并发保护、写后权威查询验证；
- BIND/Samba 重启后持久、双 DC 复制一致；
- 可演练的备份恢复和批量失败回放；
- HTTPS、CSRF/session 安全、rate limit、依赖漏洞和固定版本 release gate。

在这些门槛完成前，“网页里能新增一条 A 记录”只证明 UI 可用，不证明 DNS 管理链路可用。
