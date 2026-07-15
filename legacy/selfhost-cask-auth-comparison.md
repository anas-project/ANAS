# Selfhost Star List vs ANAS Cask Auth Comparison

整理日期：2026-06-25  
来源：<https://github.com/stars/whlsxl/lists/selfhost>，共 151 个仓库。

## 结论摘要

ANAS 当前 cask 更像一个“家庭/小团队 NAS 基础设施编排器”，已经有比较清晰的身份与入口基线：

- 入口层：Traefik + Lego，统一 HTTPS、域名、证书和 dashboard。
- 身份层：Samba AD/LDAP 作为目录源；LemonLDAP::NG 作为当前 SSO 门户、SAML IdP、OIDC Provider；Keycloak 仍是 scaffold。
- 数据与协作：PostgreSQL、MariaDB、Nextcloud、Collabora、MeshCentral、NetBird 等。
- 重要能力：自动生成 secrets、cask 依赖排序、env/template 渲染、可选服务、macvlan/LAN 暴露。

因此新增 cask 时优先级不应只看项目热度，而要看能否很好接入：

1. OIDC/OAuth2/SAML：优先，能复用 LLNG/Keycloak/Auth provider。
2. LDAP/AD：适合账号统一管理，尤其是 Nextcloud、MeshCentral、Grafana、XWiki、ERP/CRM、PAM。
3. Reverse-auth/proxy auth：适合本身没有账号体系或 SSO 弱的工具，接 oauth2-proxy / traefik-forward-auth / LLNG handler。
4. 单用户 CLI、客户端、库、底层 daemon：通常不应做成一等 Web cask，除非它提供明确服务端价值。

标记说明：

- `Y`：原生或主线明确支持。
- `P`：插件、扩展、企业版、外部 IdP、反代 SSO、或部署组合支持。
- `N`：通常不支持或不是该项目目标。
- `N/A`：客户端、库、协议组件、镜像、脚手架等，不适合用“多用户/LDAP/OIDC”评价。
- `?`：落地前需复核当前版本官方文档、许可或维护状态。

## ANAS 当前 cask 基线

| Cask | 定位 | 多用户 | LDAP/AD | OIDC/SAML/SSO | 说明 |
| --- | --- | --- | --- | --- | --- |
| `samba_dc` | AD-compatible domain controller | Y | Y provider | N | 当前目录源，提供用户、组、Kerberos、LDAP/LDAPS。 |
| `llng` | LemonLDAP::NG SSO portal | Y | Y client/backend | Y provider/client | 当前主 SSO，提供 SAML IdP、OIDC Provider、应用入口。 |
| `keycloak` | IAM scaffold | Y | Y | Y | 代码里仍标为 scaffold，适合后续完善为 LLNG 替代身份核心。 |
| `nextcloud` | 文件、协作、Talk、照片 | Y | Y | Y/P | 已有 LDAP filters、SAML SP 注册、应用入口元数据。 |
| `meshcentral` | 远程设备管理 | Y | Y | P | 当前 cask 走 LDAP，适合继续加强 OIDC/反代策略。 |
| `netbird` | WireGuard overlay network | Y | P via IdP | Y client | 当前 cask 注册 OIDC RP，依赖 SSO provider。 |
| `collabora` | 在线 Office 后端 | P | N | P via Nextcloud/反代 | 更像 Nextcloud 后端，不独立管理用户。 |
| `lam` | LDAP Account Manager | Y | Y admin | N | LDAP/AD 管理 UI。 |
| `postgres` / `mariadb` | 数据库 | Y at DB level | N | N | 可选 Adminer UI，但不是统一身份服务。 |
| `traefik` / `lego` / `bind` / `eturnal` / `ddns` | 基础设施 | N/A | N/A | N/A | 作为 cask 依赖与入口能力，不按应用账号体系评价。 |
| `freeradius` | RADIUS scaffold | ? | P | N | 当前仍是 experimental scaffold，不应视作生产 RADIUS。 |

## Star List 全量项目表

| # | 项目 | 定位 | 多用户 | LDAP/AD | OIDC/SAML/SSO | 对 ANAS cask 的意义 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | [cloudreve/cloudreve](https://github.com/cloudreve/cloudreve) | 文件管理/网盘 | Y | ?/P | ?/P | 与 Nextcloud 重叠；可作为轻量网盘候选，需先复核新版 SSO/LDAP 能力。 |
| 2 | [Ylianst/MeshCentral](https://github.com/Ylianst/MeshCentral) | 远程设备管理 | Y | Y | P | 已有 cask；继续围绕 LDAP、设备组、Traefik、MPS 端口完善。 |
| 3 | [FreeRDP/Remmina](https://github.com/FreeRDP/Remmina) | 远程桌面客户端 | N/A | N/A | N/A | 客户端软件，不适合作服务端 cask。 |
| 4 | [qjfoidnh/BaiduPCS-Go](https://github.com/qjfoidnh/BaiduPCS-Go) | 百度网盘 CLI | N/A | N/A | N/A | CLI 工具；可作为下载/同步任务插件，不适合核心 cask。 |
| 5 | [Netgear/wsdd2](https://github.com/Netgear/wsdd2) | WSD/LLMNR daemon | N/A | N/A | N/A | 与 Samba 文件发现有关；可作为 samba_fs 内部组件，不单独成 cask。 |
| 6 | [qbittorrent/qBittorrent](https://github.com/qbittorrent/qBittorrent) | BT 下载/Web UI | P | N | P via proxy | 适合下载 cask；账号体系弱，建议前置 LLNG/oauth2-proxy。 |
| 7 | [keycloak/keycloak-quickstarts](https://github.com/keycloak/keycloak-quickstarts) | Keycloak examples | N/A | N/A | N/A | 示例仓库，不做 cask；可作为 Keycloak 集成测试参考。 |
| 8 | [keycloak/keycloak](https://github.com/keycloak/keycloak) | IAM/SSO | Y | Y | Y | 已有 scaffold；是 LLNG 替代/并行身份核心的最高优先级候选。 |
| 9 | [sonatype/docker-nexus3](https://github.com/sonatype/docker-nexus3) | Nexus 镜像仓库 | Y | Y | Y/P | 面向开发团队；适合“开发者服务”cask，需注意资源占用。 |
| 10 | [minio/minio](https://github.com/minio/minio) | S3 对象存储 | Y | Y/P | Y/P | 很适合补齐对象存储；重点是 OIDC/LDAP、bucket policy、console 暴露。 |
| 11 | [immense/Remotely](https://github.com/immense/Remotely) | 远控/脚本 | Y | ? | ? | 与 MeshCentral 重叠；除非 MeshCentral 不满足，否则低优先。 |
| 12 | [gitpod-io/gitpod](https://github.com/gitpod-io/gitpod) | 云开发环境 | Y | P | Y | 复杂度高；更适合 Kubernetes，不适合当前 Docker Compose cask 主线。 |
| 13 | [stackblitz/core](https://github.com/stackblitz/core) | 在线 IDE 核心 | P | N | P | 更像平台/核心项目；自托管 cask 价值不如 Coder。 |
| 14 | [codesandbox/codesandbox-client](https://github.com/codesandbox/codesandbox-client) | 在线 IDE client | P | N | P | 不是完整自托管服务端，低优先。 |
| 15 | [coder/coder](https://github.com/coder/coder) | 开发环境平台 | Y | P via IdP | Y | 很适合新增 cask；OIDC 接 LLNG/Keycloak，配 Postgres。 |
| 16 | [coder/code-server](https://github.com/coder/code-server) | Browser VS Code | P | N | P via proxy | 轻量实用；建议用反代 SSO，不依赖内置多用户。 |
| 17 | [CollaboraOnline/online](https://github.com/CollaboraOnline/online) | 在线 Office 后端 | P | N | P via WOPI/反代 | 已有 cask；通常跟 Nextcloud 绑定，不单独面向用户。 |
| 18 | [LibreOffice/core](https://github.com/LibreOffice/core) | Office 核心 | N/A | N/A | N/A | 上游源码，不做 cask。 |
| 19 | [cryptpad/cryptpad](https://github.com/cryptpad/cryptpad) | E2EE 协作套件 | Y | ? | ?/P | 与 Nextcloud/Collabora 重叠但 E2EE 强；SSO 需复核。 |
| 20 | [penpot/penpot](https://github.com/penpot/penpot) | 设计协作 | Y | P | Y/P | 适合团队协作 cask；优先核对 OIDC/OAuth/SAML 版本与授权。 |
| 21 | [invoiceninja/invoiceninja](https://github.com/invoiceninja/invoiceninja) | 发票/项目/时间 | Y | P | P | 更偏小企业；可走 OIDC/SAML 或反代，优先级中。 |
| 22 | [NAStool/nas-tools](https://github.com/NAStool/nas-tools) | NAS 媒体库管理 | P | N | P via proxy | 适合媒体自动化 cask；建议外置 SSO。 |
| 23 | [fleetdm/fleet](https://github.com/fleetdm/fleet) | 设备管理/osquery | Y | P | Y | 与 MeshCentral/JumpServer 不同，偏安全资产；适合企业向 cask。 |
| 24 | [cockpit-project/cockpit](https://github.com/cockpit-project/cockpit) | 服务器 Web 管理 | Y via system | Y/P via PAM/SSSD | P | 可管理宿主机，安全边界敏感；不建议默认纳入 cask。 |
| 25 | [calcom/cal.diy](https://github.com/calcom/cal.diy) | 日程预约 | Y | P | Y/P | 适合团队应用 cask；OIDC/SSO 能力需按当前 fork/版本复核。 |
| 26 | [alextselegidis/easyappointments](https://github.com/alextselegidis/easyappointments) | 预约系统 | Y | ? | ?/P | 适合轻量服务；统一身份通常需要插件或反代。 |
| 27 | [lazy-luo/smarGate](https://github.com/lazy-luo/smarGate) | 内网穿透 | Y/P | N | ? | 与 rathole/pangolin/frp 类似；若有 Web 管理 UI 再考虑。 |
| 28 | [qdm12/ddns-updater](https://github.com/qdm12/ddns-updater) | DDNS Web UI | P | N | P via proxy | 已有 ddns cask 使用同类思路；继续保留为网络基础件。 |
| 29 | [jellyfin/Swiftfin](https://github.com/jellyfin/Swiftfin) | Jellyfin iOS/tvOS client | N/A | N/A | N/A | 客户端，不做 cask；如果做媒体栈，应看 Jellyfin server。 |
| 30 | [hongyonghan/Docker_Microsoft365_E5_Renew_X](https://github.com/hongyonghan/Docker_Microsoft365_E5_Renew_X) | M365 E5 续订任务 | N | N | N | 场景窄且依赖外部账号/API，低优先。 |
| 31 | [YoRyan/mailrise](https://github.com/YoRyan/mailrise) | SMTP to Apprise gateway | N/A | N/A | N/A | 可作为通知基础组件，非用户应用。 |
| 32 | [caronc/apprise](https://github.com/caronc/apprise) | 通知库/CLI | N/A | N/A | N/A | 适合作为统一通知能力依赖，不单独成 Web cask。 |
| 33 | [ultravnc/UltraVNC](https://github.com/ultravnc/UltraVNC) | VNC 服务/客户端 | P | P via OS/AD | N | 更适合 Windows 端工具，不适合 ANAS Web cask。 |
| 34 | [FreeRDP/FreeRDP](https://github.com/FreeRDP/FreeRDP) | RDP 库/客户端 | N/A | N/A | N/A | 底层库/客户端，不做 cask。 |
| 35 | [crazy-max/docker-nextcloud](https://github.com/crazy-max/docker-nextcloud) | Nextcloud Docker image | N/A | N/A | N/A | 镜像方案；ANAS 已自有 Nextcloud cask。 |
| 36 | [dbgate/dbgate](https://github.com/dbgate/dbgate) | 数据库管理 Web/桌面 | P | N | P/? | 可替代 Adminer，若要多 DB 管理可做 cask；需用 SSO 保护。 |
| 37 | [jesseduffield/lazydocker](https://github.com/jesseduffield/lazydocker) | Docker TUI | N/A | N/A | N/A | CLI/TUI，不做 cask。 |
| 38 | [shiningw/ncdownloader](https://github.com/shiningw/ncdownloader) | Nextcloud 下载插件 | P via Nextcloud | Y via Nextcloud | Y/P via Nextcloud | 作为 Nextcloud app 处理，不单独 cask。 |
| 39 | [spiral-project/ihatemoney](https://github.com/spiral-project/ihatemoney) | 共享账本 | Y | N | P via proxy | 小而实用；账号/SSO 弱，适合反代保护。 |
| 40 | [immich-app/immich](https://github.com/immich-app/immich) | 照片/视频管理 | Y | P via IdP | Y | 很适合新增 cask；OIDC 接 LLNG/Keycloak，LDAP 走 IdP。 |
| 41 | [discourse/discourse](https://github.com/discourse/discourse) | 社区论坛 | Y | P | Y/P | 适合公开社区，不一定适合 NAS 默认栈；配置复杂。 |
| 42 | [element-hq/element-web](https://github.com/element-hq/element-web) | Matrix Web client | Y via homeserver | Y/P via homeserver | Y/P via homeserver | 不是 homeserver；若做通信栈应评估 Synapse/Conduit。 |
| 43 | [xwiki/xwiki-platform](https://github.com/xwiki/xwiki-platform) | Wiki/知识库 | Y | Y | Y/P | 适合家庭/小团队知识库；与 AFFiNE/Outline 类项目对比。 |
| 44 | [Chocobozzz/PeerTube](https://github.com/Chocobozzz/PeerTube) | 联邦视频平台 | Y | P | P | 公共内容平台，家庭 NAS 价值视需求；SSO 多需插件/外部。 |
| 45 | [postalserver/postal](https://github.com/postalserver/postal) | 邮件投递平台 | Y | N | P via proxy | 更偏邮件基础设施，运维风险高，不建议默认 cask。 |
| 46 | [modoboa/modoboa](https://github.com/modoboa/modoboa) | 邮件托管 | Y | Y/P | P | 可做邮件栈候选，但公网邮件运维成本高。 |
| 47 | [zabbix/zabbix](https://github.com/zabbix/zabbix) | 监控 | Y | Y | Y/P | 企业级监控；与 Beszel/Grafana 对比，复杂但能力完整。 |
| 48 | [tiredofit/docker-collabora-online](https://github.com/tiredofit/docker-collabora-online) | Collabora Docker image | N/A | N/A | N/A | 镜像封装；ANAS 已有 Collabora cask。 |
| 49 | [Cisco-Talos/clamav](https://github.com/Cisco-Talos/clamav) | 杀毒引擎 | N/A | N/A | N/A | 可作为 Nextcloud/邮件扫描后端，不单独作为用户应用。 |
| 50 | [gravitl/netmaker](https://github.com/gravitl/netmaker) | WireGuard 网络管理 | Y | P | Y/P | 与 NetBird 重叠；若保留 NetBird，此项作备选对比。 |
| 51 | [strukturag/nextcloud-spreed-signaling](https://github.com/strukturag/nextcloud-spreed-signaling) | Nextcloud Talk signaling | N/A | N/A | N/A | 已在 Nextcloud Talk 方向；作为 nextcloud cask 子服务。 |
| 52 | [jitsi/jitsi-meet](https://github.com/jitsi/jitsi-meet) | 视频会议 | Y/P | Y/P | Y/P | 可作为独立会议 cask；ANAS 已有 eturnal，可复用 TURN。 |
| 53 | [jitsi/docker-jitsi-meet](https://github.com/jitsi/docker-jitsi-meet) | Jitsi Docker deployment | Y/P | Y/P | Y/P | 若落地 Jitsi，优先参考此部署仓库。 |
| 54 | [sosedoff/pgweb](https://github.com/sosedoff/pgweb) | PostgreSQL Web client | N/P | N | P via proxy | 可替代 Adminer 的 Postgres UI，建议只内网/SSO。 |
| 55 | [meetecho/janus-gateway](https://github.com/meetecho/janus-gateway) | WebRTC server | N/A | N/A | N/A | 实时通信后端，不是用户应用。 |
| 56 | [OpenVPN/openvpn](https://github.com/OpenVPN/openvpn) | VPN daemon | Y via cert/users | P via plugin/PAM | N | 与 NetBird/WireGuard 路线冲突；除非需传统 OpenVPN。 |
| 57 | [dockovpn/dockovpn](https://github.com/dockovpn/dockovpn) | OpenVPN Docker image | Y/P | P | N | 部署封装；优先级低于 NetBird/Pangolin。 |
| 58 | [h2non/imaginary](https://github.com/h2non/imaginary) | 图片处理服务 | N/A | N/A | N/A | 已作为 Nextcloud/照片处理后端方向；内部组件。 |
| 59 | [MetaProvide/talked](https://github.com/MetaProvide/talked) | Nextcloud Talk 录制 | P via Nextcloud | Y via Nextcloud | Y/P via Nextcloud | 作为 Nextcloud Talk 扩展评估。 |
| 60 | [pulsejet/go-vod](https://github.com/pulsejet/go-vod) | HLS VOD server | N/P | N | P via proxy | 媒体后端，需反代保护。 |
| 61 | [vrana/adminer](https://github.com/vrana/adminer) | DB 管理单文件 | P | N | P via proxy | 已作为数据库 cask 可选 UI；必须前置认证。 |
| 62 | [nextcloud/server](https://github.com/nextcloud/server) | 文件/协作平台 | Y | Y | Y/P | 已有 cask；是当前应用栈核心。 |
| 63 | [zitadel/zitadel](https://github.com/zitadel/zitadel) | IAM/CIAM | Y | Y/P | Y | 值得与 LLNG/Keycloak/Authenik 对比；多租户和 OIDC 很强。 |
| 64 | [hashicorp/vault](https://github.com/hashicorp/vault) | Secrets/PAM-ish backend | Y | Y | Y/P | 可补齐 secrets，但运维复杂；当前 generated secrets 可能够用。 |
| 65 | [sourcegraph/sourcegraph-public-snapshot](https://github.com/sourcegraph/sourcegraph-public-snapshot) | 代码搜索/AI | Y | P | Y/P | 开发团队价值高；家用 NAS 低优先，资源占用高。 |
| 66 | [Crivaledaz/Mattermost-LDAP](https://github.com/Crivaledaz/Mattermost-LDAP) | Mattermost TE LDAP 插件 | N/A | Y | N | 插件项目；若做 Mattermost/Rocket.Chat，可参考。 |
| 67 | [signalapp/Signal-Server](https://github.com/signalapp/Signal-Server) | Signal server | Y | N | N | 自托管难度和客户端生态限制大，不适合 cask。 |
| 68 | [42wim/matterbridge](https://github.com/42wim/matterbridge) | 聊天桥接 | N/A | N/A | N/A | 可作为通信 glue，不是用户 Web app。 |
| 69 | [mumble-voip/mumble](https://github.com/mumble-voip/mumble) | 语音服务器 | Y | P | P via proxy/plugin | 可做轻量语音 cask；比 Jitsi 简单。 |
| 70 | [wireapp/wire](https://github.com/wireapp/wire) | 安全协作平台总览 | Y | P | P | 自托管复杂且商业边界需复核，低优先。 |
| 71 | [oxen-io/lokinet](https://github.com/oxen-io/lokinet) | 匿名 overlay 网络 | N/A | N/A | N/A | 网络底层项目；不贴合 ANAS 当前方向。 |
| 72 | [TriliumNext/Trilium](https://github.com/TriliumNext/Trilium) | 笔记/知识库 | P | N | P via proxy | 个人/小团队可用；多用户能力有限，SSO 走反代。 |
| 73 | [Dolibarr/dolibarr](https://github.com/Dolibarr/dolibarr) | ERP/CRM | Y | Y/P | Y/P | 小企业套件候选；LDAP/OIDC 能力需按模块复核。 |
| 74 | [metasfresh/metasfresh](https://github.com/metasfresh/metasfresh) | ERP | Y | P | P | 重型业务系统，适合企业但不适合默认 NAS。 |
| 75 | [frappe/frappe](https://github.com/frappe/frappe) | Web framework/ERPNext 基础 | Y | Y/P | Y/P | 若做 ERPNext/OIDC 集成可考虑，但部署链较重。 |
| 76 | [idempiere/idempiere](https://github.com/idempiere/idempiere) | ERP/CRM/SCM | Y | Y/P | P | 重型企业套件，低默认优先级。 |
| 77 | [OCA/OpenUpgrade](https://github.com/OCA/OpenUpgrade) | Odoo 升级工具 | N/A | N/A | N/A | 工具，不做 cask。 |
| 78 | [Tecnativa/doodba](https://github.com/Tecnativa/doodba) | Odoo Docker base | N/A | N/A | N/A | 部署基座；若做 Odoo cask 可参考。 |
| 79 | [matrix-org/dendrite](https://github.com/matrix-org/dendrite) | Matrix homeserver | Y | P | P | 可做通信 cask；需和 Synapse/Conduit 比较成熟度。 |
| 80 | [getsentry/self-hosted](https://github.com/getsentry/self-hosted) | Sentry self-hosted | Y | P | P | 开发/运维团队价值高；资源较重。 |
| 81 | [goauthentik/authentik](https://github.com/goauthentik/authentik) | IAM/SSO | Y | Y | Y | 值得新增对比；比 Keycloak 易用，比 LLNG 更现代化。 |
| 82 | [LemonLDAPNG/lemonldap-ng](https://github.com/LemonLDAPNG/lemonldap-ng) | WebSSO/IAM | Y | Y | Y | 已有 cask 当前主 SSO；继续完善最佳实践。 |
| 83 | [logseq/logseq](https://github.com/logseq/logseq) | 知识管理客户端/同步 | P | N | P/? | 当前更偏客户端/云服务；自托管价值不如 Trilium/AFFiNE。 |
| 84 | [fusiondirectory/fusiondirectory](https://github.com/fusiondirectory/fusiondirectory) | IAM/LDAP 管理 | Y | Y | P | 可替代/补充 LAM，用于更完整 LDAP 目录管理。 |
| 85 | [freescout-help-desk/freescout](https://github.com/freescout-help-desk/freescout) | Help desk/共享邮箱 | Y | P | P | 小团队可用；SSO/LDAP 多依赖模块，需复核许可。 |
| 86 | [triggerdotdev/jsonhero-web](https://github.com/triggerdotdev/jsonhero-web) | JSON 浏览器 | N/P | N | P via proxy | 小工具，适合作静态/单用户工具，外置 SSO。 |
| 87 | [authorizerdev/authorizer](https://github.com/authorizerdev/authorizer) | Auth microservice | Y | ? | Y | 与 LLNG/Keycloak/Authenik 功能重叠；可作轻量 auth 对比。 |
| 88 | [jpillora/chisel](https://github.com/jpillora/chisel) | TCP/UDP tunnel | P | N | P via wrapper | 网络基础件；无 Web 用户体系。 |
| 89 | [netbirdio/netbird](https://github.com/netbirdio/netbird) | WireGuard overlay | Y | P via IdP | Y | 已有 cask；与 Netmaker/Pangolin/Headscale 对比。 |
| 90 | [NicTool/NicTool](https://github.com/NicTool/NicTool) | DNS 管理 | Y | P | P | 若 Bind 需要 Web 管理可评估；否则低优先。 |
| 91 | [nocodb/nocodb](https://github.com/nocodb/nocodb) | Airtable alternative | Y | P | Y/P | 很适合内部工具 cask；优先核实 CE 的 SSO 限制。 |
| 92 | [grocy/grocy](https://github.com/grocy/grocy) | 家庭库存/购物 | P | N | P via proxy | 贴合家庭 NAS；多用户/权限较轻，适合反代 SSO。 |
| 93 | [apache/ofbiz-framework](https://github.com/apache/ofbiz-framework) | ERP/CRM framework | Y | Y/P | P | 重型企业应用；安全维护要求高。 |
| 94 | [whyour/qinglong](https://github.com/whyour/qinglong) | 定时任务平台 | Y/P | N | P via proxy | 适合自动化任务 cask；务必加 SSO 和权限隔离。 |
| 95 | [yt-dlp/yt-dlp](https://github.com/yt-dlp/yt-dlp) | 下载 CLI | N/A | N/A | N/A | 作为其他下载 cask 依赖，不单独 Web cask。 |
| 96 | [navidrome/navidrome](https://github.com/navidrome/navidrome) | 音乐流媒体 | Y | P | P | 适合媒体 cask；SSO/LDAP 要核对当前支持或用反代。 |
| 97 | [Cloudbox/Cloudbox](https://github.com/Cloudbox/Cloudbox) | 媒体栈 Ansible | Y/P | P | P | 更像一整套竞品栈；可借鉴模块组合，不直接嵌入。 |
| 98 | [rockstor/rockstor-core](https://github.com/rockstor/rockstor-core) | NAS/BTRFS 管理 | Y | P | P | 与 ANAS 定位重叠，更多是竞品参考。 |
| 99 | [CorentinTh/it-tools](https://github.com/CorentinTh/it-tools) | 在线开发小工具 | N/P | N | P via proxy | 适合作轻量 cask，建议只加反代认证。 |
| 100 | [ellite/Wallos](https://github.com/ellite/Wallos) | 订阅/预算 | Y/P | N | P via proxy | 家用价值高；统一身份需反代。 |
| 101 | [ToolJet/ToolJet](https://github.com/ToolJet/ToolJet) | 内部工具/低代码 | Y | P | Y/P | 适合小团队；需核对 CE SSO/LDAP 授权边界。 |
| 102 | [rqlite/rqlite](https://github.com/rqlite/rqlite) | 分布式 SQLite | Y at API/auth | N | N | 基础数据库组件，不是用户应用。 |
| 103 | [AppFlowy-IO/AppFlowy](https://github.com/AppFlowy-IO/AppFlowy) | 协作工作区/Notion alternative | Y | ? | P/Y? | 与 AFFiNE/Trilium/XWiki 对比；SSO 需复核 self-host 版本。 |
| 104 | [pulsejet/memories](https://github.com/pulsejet/memories) | Nextcloud photo app | Y via Nextcloud | Y via Nextcloud | Y/P via Nextcloud | 已在 Nextcloud cask 方向；作为 app 开关处理。 |
| 105 | [h44z/wg-portal](https://github.com/h44z/wg-portal) | WireGuard portal | Y | Y | Y/P | 轻量 WireGuard 管理备选；和 NetBird/Netmaker/Pangolin 对比。 |
| 106 | [pgadmin-org/pgadmin4](https://github.com/pgadmin-org/pgadmin4) | PostgreSQL 管理 | Y | Y/P | Y/P | 比 Adminer 强但更重；适合作 postgres 高级 UI 可选项。 |
| 107 | [localstack/localstack](https://github.com/localstack/localstack) | 本地 AWS 模拟 | Y/P | N | P | 开发测试场景；不适合 NAS 默认。 |
| 108 | [Stremio/stremio-core](https://github.com/Stremio/stremio-core) | 媒体 core | N/A | N/A | N/A | 不是服务端应用。 |
| 109 | [antimof/UxPlay](https://github.com/antimof/UxPlay) | AirPlay receiver | N/A | N/A | N/A | LAN 服务，非账号应用。 |
| 110 | [jeessy2/ddns-go](https://github.com/jeessy2/ddns-go) | DDNS | P | N | P via proxy | qdm12/ddns-updater 替代品；可作为 ddns cask 对比。 |
| 111 | [tryzealot/zealot](https://github.com/tryzealot/zealot) | App 分发 | Y | P | P | 适合开发/测试团队；SSO 需复核。 |
| 112 | [zed-industries/zed](https://github.com/zed-industries/zed) | 桌面代码编辑器 | N/A | N/A | N/A | 客户端，不做 cask。 |
| 113 | [Jackett/Jackett](https://github.com/Jackett/Jackett) | Tracker API | P | N | P via proxy | 媒体自动化组件；外置 SSO。 |
| 114 | [Difegue/LANraragi](https://github.com/Difegue/LANraragi) | 漫画/归档阅读 | P | N | P via proxy | 家用媒体 cask 候选；账号能力较轻。 |
| 115 | [LANDrop/LANDrop](https://github.com/LANDrop/LANDrop) | LAN 文件传输 | N/A | N/A | N/A | 客户端/LAN 工具，不做 cask。 |
| 116 | [activepieces/activepieces](https://github.com/activepieces/activepieces) | 自动化工作流 | Y | P | Y/P | 很适合自动化 cask；注意 secret、Webhook 暴露和 SSO。 |
| 117 | [grafana/grafana](https://github.com/grafana/grafana) | 可观测性 dashboard | Y | Y | Y | 高价值 cask；和 Prometheus/Loki/Beszel 组合。 |
| 118 | [getappbox/AppBox-iOSAppsWirelessInstallation](https://github.com/getappbox/AppBox-iOSAppsWirelessInstallation) | iOS app 分发工具 | P | N | P via proxy | 老项目/场景窄；Zealot 更完整。 |
| 119 | [sickcodes/Docker-OSX](https://github.com/sickcodes/Docker-OSX) | macOS VM in Docker | N/A | N/A | N/A | 特殊虚拟化/CI 场景，不适合标准 cask。 |
| 120 | [m1k1o/neko](https://github.com/m1k1o/neko) | WebRTC 虚拟浏览器 | Y/P | N | P via proxy | 有趣的远程浏览器 cask；需注意安全隔离。 |
| 121 | [jumpserver/jumpserver](https://github.com/jumpserver/jumpserver) | PAM/堡垒机 | Y | Y | Y/P | 与 Samba/LDAP/NetBird/SSH 管理高度相关，强候选。 |
| 122 | [ovh/the-bastion](https://github.com/ovh/the-bastion) | SSH 堡垒机 | Y | Y/P | P | 更偏 SSH 专项，轻于 JumpServer；适合作高级网络 cask。 |
| 123 | [paperless-ngx/paperless-ngx](https://github.com/paperless-ngx/paperless-ngx) | 文档管理/OCR | Y | P | Y/P | 家用/小团队高价值；适合新增 cask。 |
| 124 | [SonarSource/sonarqube](https://github.com/SonarSource/sonarqube) | 代码质量 | Y | Y/P | Y/P | 开发团队 cask；注意 CE/商业版认证能力差异。 |
| 125 | [wiltonsr/ldapAuth](https://github.com/wiltonsr/ldapAuth) | Traefik LDAP middleware | N/A | Y | N | 可补足 Traefik forward auth 的 LDAP 模式，但需评估维护性。 |
| 126 | [thomseddon/traefik-forward-auth](https://github.com/thomseddon/traefik-forward-auth) | Traefik forward auth | N/A | N | Y | 适合保护无账号工具；与 oauth2-proxy/LLNG handler 对比。 |
| 127 | [oauth2-proxy/oauth2-proxy](https://github.com/oauth2-proxy/oauth2-proxy) | Auth reverse proxy | N/A | P via IdP | Y | 强烈建议作为通用 cask 或 Traefik 集成能力。 |
| 128 | [Qexo/Qexo](https://github.com/Qexo/Qexo) | 静态博客后台 | Y/P | N | P via proxy | 场景窄，低优先。 |
| 129 | [icret/EasyImages2.0](https://github.com/icret/EasyImages2.0) | 图床 | P | N | P via proxy | 家用可用；需要强制反代认证/限流。 |
| 130 | [casdoor/casdoor](https://github.com/casdoor/casdoor) | IAM/SSO | Y | Y | Y | 值得与 Keycloak/Authentik/LLNG 对比，中文生态较好。 |
| 131 | [portainer/portainer](https://github.com/portainer/portainer) | Docker/K8s 管理 | Y | Y/P | Y/P | 运维入口很敏感；可做可选 cask，但默认不暴露公网。 |
| 132 | [LycheeOrg/Lychee](https://github.com/LycheeOrg/Lychee) | 照片管理 | Y | P | P | 与 Immich/Nextcloud Memories 对比；轻量但 AI/移动端弱。 |
| 133 | [HeidiSQL/HeidiSQL](https://github.com/HeidiSQL/HeidiSQL) | 数据库桌面客户端 | N/A | N/A | N/A | 客户端，不做 cask。 |
| 134 | [coollabsio/coolify](https://github.com/coollabsio/coolify) | PaaS/部署平台 | Y | P | Y/P | 与 ANAS cask runtime 部分重叠；更像竞品/参考。 |
| 135 | [lissy93/dashy](https://github.com/lissy93/dashy) | Dashboard/导航页 | Y/P | N | Y/P | 可替代/补充 LLNG app launcher，但已有 LLNG portal。 |
| 136 | [TandoorRecipes/recipes](https://github.com/TandoorRecipes/recipes) | 菜谱/购物清单 | Y | Y/P | Y/P | 家用价值高，适合新增 app cask。 |
| 137 | [tonghohin/screen-sharing](https://github.com/tonghohin/screen-sharing) | 屏幕分享 | P | N | P via proxy | 简单临时工具；Jitsi/Nextcloud Talk 更完整。 |
| 138 | [readest/readest](https://github.com/readest/readest) | 电子书阅读器 | P | ? | ? | 当前自托管服务端价值需复核；先低优先。 |
| 139 | [valkey-io/valkey](https://github.com/valkey-io/valkey) | Redis-compatible KV | Y at ACL | N | N | 基础数据库/cache；可作为依赖，不面向 SSO。 |
| 140 | [Unleash/unleash](https://github.com/Unleash/unleash) | Feature flags | Y | P | Y/P | 开发团队 cask；家用低优先。 |
| 141 | [leaningtech/webvm](https://github.com/leaningtech/webvm) | 浏览器 VM | N/A | N/A | N/A | 前端实验项目，不做常规 cask。 |
| 142 | [HeyPuter/puter](https://github.com/HeyPuter/puter) | Web OS / cloud desktop | Y | ? | ?/P | 与 Nextcloud/Cloudreve/AFFiNE 有交集，需复核成熟度与 SSO。 |
| 143 | [shlinkio/shlink](https://github.com/shlinkio/shlink) | URL shortener | Y/P | N | P via proxy | 小服务可做 cask；外置认证即可。 |
| 144 | [KurtBestor/Hitomi-Downloader](https://github.com/KurtBestor/Hitomi-Downloader) | 桌面下载器 | N/A | N/A | N/A | 桌面工具，不做 cask。 |
| 145 | [toeverything/AFFiNE](https://github.com/toeverything/AFFiNE) | 知识库/白板 | Y | ? | Y/P? | 与 AppFlowy/Trilium/XWiki 对比；适合协作 cask，需复核 self-host SSO。 |
| 146 | [karakeep-app/karakeep](https://github.com/karakeep-app/karakeep) | Bookmark/read-it-later | Y/P | N | Y/P | 家用高价值；OIDC 若可用则适合直接接 LLNG。 |
| 147 | [rathole-org/rathole](https://github.com/rathole-org/rathole) | NAT tunnel/reverse proxy | N/A | N/A | N/A | 网络基础件；可与 smarGate/chisel/Pangolin 对比。 |
| 148 | [fosrl/pangolin](https://github.com/fosrl/pangolin) | Identity-aware VPN/reverse proxy | Y | P | Y | 很贴合“身份感知远程访问”，可与 NetBird/Traefik/LLNG 组合或竞争。 |
| 149 | [foru17/neko-master](https://github.com/foru17/neko-master) | 网络流量 dashboard | Y/P | ? | ?/P | 需复核项目实际范围；低优先。 |
| 150 | [henrygd/beszel](https://github.com/henrygd/beszel) | 轻量监控 | Y | P? | Y/P? | 很适合家庭/小团队监控；比 Zabbix/Grafana 轻。 |
| 151 | [YanG-1989/m3u](https://github.com/YanG-1989/m3u) | IPTV 源列表 | N/A | N/A | N/A | 数据源列表，不做 cask。 |

## 建议增加的对比项目

这些不在当前 Star list 中，但和 ANAS 的“身份、入口、NAS 应用”方向强相关。

| 项目 | 定位 | 多用户 | LDAP/AD | OIDC/SAML/SSO | 为什么要纳入对比 |
| --- | --- | --- | --- | --- | --- |
| [Authelia](https://www.authelia.com/) | 反代 SSO/MFA/OIDC provider | Y | Y | Y | 比 LLNG/Keycloak 轻，适合 Traefik forward-auth；可作为无账号 Web 工具的统一入口。 |
| [LLDAP](https://github.com/lldap/lldap) | 轻量 LDAP 目录 | Y | Y provider | P/? | 如果 Samba AD 太重，可作为家庭轻量目录候选；但 AD/Kerberos 能力不能替代 Samba DC。 |
| [Kanidm](https://github.com/kanidm/kanidm) | 现代身份管理 | Y | P | Y | 更现代的身份栈，值得和 Keycloak/Authenik/ZITADEL 对比。 |
| [Pocket ID](https://github.com/pocket-id/pocket-id) | 轻量 OIDC/passkey IdP | Y | N | Y | NetBird docs 已将它列为轻量 OIDC provider；适合家用 passkey-first 方案。 |
| [Headscale](https://github.com/juanfont/headscale) | 自托管 Tailscale control server | Y | P | Y/P | 与 NetBird/Netmaker/Pangolin 对比；客户端生态强，但管理模型不同。 |
| [Forgejo](https://forgejo.org/) / [Gitea](https://about.gitea.com/) | 代码托管 | Y | Y/P | Y/P | 开发者栈常见缺口；可接 LDAP/OIDC，适合与 Coder/SonarQube/Unleash 组合。 |
| [Vaultwarden](https://github.com/dani-garcia/vaultwarden) | 密码库 | Y | P via proxy/enterprise limits | P via proxy/SSO limits | 家庭 NAS 高价值，但官方 Bitwarden SSO 能力和 Vaultwarden 实现边界需谨慎复核。 |
| [Jellyfin server](https://github.com/jellyfin/jellyfin) | 媒体服务器 | Y | P | P | 当前列表只有 Swiftfin 客户端；若做媒体 cask，应比较 Jellyfin/Navidrome/Plex-like 栈。 |
| [Synapse](https://github.com/element-hq/synapse) | Matrix homeserver | Y | P | P | Element Web 需要 homeserver；Synapse 比 Dendrite 更成熟，适合通信栈对比。 |
| [Prometheus](https://github.com/prometheus/prometheus) + [Loki](https://github.com/grafana/loki) | 监控/日志后端 | N/A | N/A | N/A | 若新增 Grafana，应一并设计 metrics/logs 后端，而不是只有 dashboard。 |

## 优先级建议

高优先级，和当前 cask 架构最贴合：

- `oauth2-proxy`：作为通用反代认证组件，补齐无账号 Web app 的 SSO。
- `Immich`：照片/视频是 NAS 高频需求，OIDC 可接现有 SSO。
- `paperless-ngx`：文档管理/OCR 是家庭和小团队刚需。
- `Grafana` + 轻量监控后端：ANAS runtime 和 casks 需要可观测性。
- `Beszel`：比 Zabbix/Grafana 栈轻，适合家用监控。
- `Coder` 或 `code-server`：开发环境能力，OIDC/反代都容易接。
- `MinIO`：对象存储基础能力，可服务备份、应用附件、S3 API。
- `JumpServer`：如果目标包括小团队远程访问与审计，价值很高。

中优先级，按场景选择：

- `Penpot`、`AFFiNE`、`AppFlowy`、`XWiki`：团队协作/知识库，需要先定主路线，避免重复。
- `TandoorRecipes`、`grocy`、`Wallos`、`karakeep`：家庭 NAS 体验很好，统一 SSO 多靠 OIDC/反代。
- `ToolJet`、`NocoDB`、`activepieces`：内部工具/自动化，注意 secret 与 Webhook 安全。
- `Jitsi` / `Mumble`：通信需求明确时再做；Nextcloud Talk 已覆盖部分需求。
- `Pangolin` / `Headscale` / `Netmaker` / `wg-portal`：和 NetBird 竞争，需要先决定远程访问主路线。

低优先级或不建议作为 cask：

- 纯客户端/CLI/库：Remmina、FreeRDP、yt-dlp、lazydocker、HeidiSQL、Zed、LANDrop、Hitomi-Downloader。
- 重型企业套件：GitPod、metasfresh、iDempiere、OFBiz、Sourcegraph，除非明确面向团队/企业部署。
- 镜像/部署包装/插件仓库：docker-nextcloud、docker-collabora-online、OpenUpgrade、Doodba、Mattermost-LDAP。
- 高风险公网基础设施：Postal、完整邮件托管、Signal server，默认不建议放进家庭 NAS 一键栈。

## 参考链接

- GitHub Star list: <https://github.com/stars/whlsxl/lists/selfhost>
- ANAS 当前 runtime 与 cask 基线：[README.md](/Users/whl/Documents/anas/README.md)
- ANAS cask 设计与功能表：[refactor/docs/ai-design.md](/Users/whl/Documents/anas/refactor/docs/ai-design.md)
- Keycloak server admin docs: <https://www.keycloak.org/docs/latest/server_admin/index.html>
- Nextcloud LDAP docs: <https://docs.nextcloud.com/server/latest/admin_manual/configuration_user/user_auth_ldap.html>
- NetBird identity provider docs: <https://docs.netbird.io/selfhosted/identity-providers>
- Authentik docs: <https://docs.goauthentik.io/>
- Grafana LDAP docs: <https://grafana.com/docs/grafana/latest/setup-grafana/configure-access/configure-authentication/ldap/>
- Coder OIDC docs: <https://coder.com/docs/admin/users/oidc-auth>
