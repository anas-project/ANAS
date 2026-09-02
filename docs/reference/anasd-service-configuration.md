# `anasd` 服务配置

`anasd` 使用独立于 workspace `config.yml` 的宿主级配置。默认路径是
`/etc/anas/anasd.yml`；启动时只可用 `--config` 选择另一个绝对路径，HTTP 请求和进程环境变量都不能覆盖其中的监听、workspace、存储或证书设置。

配置文件必须由 `root` 拥有，权限不得宽于 `0600`，且必须是普通文件而非符号链接。格式是严格的单份 YAML 文档；未知字段、第二份 YAML 文档、读取期间被替换或修改以及超过 1 MiB 都会使服务拒绝启动。

```yaml
api_version: anas.console-config/v1
mode: lan
port: 8080
allowed_dns_hosts:
  - anas.example.test
console_store: /var/lib/anas/console
workspaces:
  - id: main
    path: /srv/anas
tls:
  lego:
    base_domain: example.test
    certificate: /var/lib/anas/lego/example.test.crt
    private_key: /var/lib/anas/lego/example.test.key
    issuer: /var/lib/anas/lego/example.test.issuer.crt
    trust_bundle: /var/lib/anas/lego/anas-trust-bundle.crt
    internal_ca: /var/lib/anas/lego/anas-internal-ca.crt
    issuer_marker: /var/lib/anas/lego/.issuer
  temporary:
    certificate: /var/lib/anas/console-tls/temp-console.crt
    private_key: /var/lib/anas/console-tls/temp-console.key
    dns_names:
      - bootstrap.example.test
    ip_addresses:
      - 192.0.2.10
trusted_proxy:
  bind_address: 0.0.0.0
  port: 8443
  public_url: https://anas.example.test:9000
  allowed_source_ips:
    - 172.18.0.5
  allowed_dns_hosts:
    - anas.example.test
  oidc_issuer: https://iam.example.test:9000
  platform_admin_group: Admins
  client_ca: /etc/anas/trusted-proxy/traefik-client-ca.crt
  client_spki_sha256:
    - 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
```

示例路径只说明字段形状；lego 路径应指向当前部署实际发布的 `ANAS_TLS_*` 制品。

## 字段

| 字段 | 约束与作用 |
| --- | --- |
| `api_version` | 必须是 `anas.console-config/v1`。 |
| `mode` | `lan`（默认）或 `loopback`。这是静态策略，不随管理员、证书、IAM、Traefik、网卡或 workspace 状态变化。 |
| `port` | 固定管理端口，默认 `8080`，范围 `1..65535`；同一端口承载受状态限制的 HTTP 与 TLS。 |
| `allowed_dns_hosts` | 额外允许的精确 ASCII DNS Host；不接受 IP、端口或 wildcard。数值 Host 仍必须等于该连接实际命中的本机地址。 |
| `console_store` | 审计、单向 capability state、认证与任务事件状态的绝对路径；目录为 `0700`、私有状态文件为 `0600`，且必须在所有注册 workspace 之外，避免 snapshot/backup/restore 覆盖控制面状态。 |
| `workspaces` | 服务端注册的 `id -> absolute path`；客户端 API 只提交 ID。workspace 必须已存在并包含 `.anas/`。 |
| `tls.lego` | lego 发布的长期证书、私钥、issuer、trust bundle、独立 `anas-internal-ca.crt` 和 `.issuer` marker。`anasd` 只消费并验证这些文件，不签发长期证书。 |
| `tls.temporary` | 可选的临时自签叶证书路径和显式 SAN；至少给出一个 `dns_names` 或具体 `ip_addresses`，不做主机名、网卡或环境变量发现。文件名必须是示例中的固定名称，供显式 CLI 生成。 |
| `trusted_proxy.bind_address` / `port` | 可选的独立 TLS-only 监听器；地址必须是 IP literal，端口必须与直连端口不同。它不接受明文，也不复用直连 listener。 |
| `trusted_proxy.public_url` / `allowed_dns_hosts` | Traefik/OIDC 对外 HTTPS origin 与该 listener 接受的精确 DNS Host。`public_url` 的 Host 必须在清单中。 |
| `trusted_proxy.allowed_source_ips` | 可连接该 listener 的精确 Traefik 源 IP；不接受 CIDR、未指定地址或“全部 RFC1918”。这是 mTLS 之外的纵深限制，不能替代客户端证书。 |
| `trusted_proxy.oidc_issuer` / `platform_admin_group` | 固定、规范的 HTTPS issuer 和解析后的目录管理员组。只有 issuer+subject 稳定且属于该组的身份才映射为 `owner`。 |
| `trusted_proxy.client_ca` / `client_spki_sha256` | 验证 Traefik 客户端证书的 root-owned CA 文件，以及允许的叶公钥 SPKI SHA-256（64 位小写 hex）。CA 验证与 SPKI allowlist 必须同时通过。 |

`tls.lego` 与 `tls.temporary` 都配置时，验证通过的 lego 证书优先。每次 TLS 握手都经动态 `GetCertificate` 读取当前验证快照；新 pair 只有在私钥匹配、有效期、ServerAuth、SAN、链和 issuer marker 全部通过后才原子切换。更新失败会继续使用 last-known-good，不会回退到临时证书或扩大明文能力。

服务配置只在 `anasd` 启动时读取；修改端口、模式、Host、workspace、store、受信代理或 TLS 路径后必须重启。证书路径指向的直连服务证书制品是例外：进程会在后续 TLS 握手重新校验并热切换，无需重启。

`allowed_dns_hosts` 不会从 `tls.lego.base_domain` 自动派生。完整级直连应使用 `anas.<base_domain>`，管理员必须同时把该名字加入 `allowed_dns_hosts`，并确保客户端 DNS 能解析到 NAS。lego leaf 必须同时覆盖 `<base_domain>` 与 `anas.<base_domain>`；长期 CA/ACME 流程不得为管理面向 `ca.sh` 增加 `IP:` SAN。需要按 IP 使用 TLS 时，只能在 `tls.temporary` 中显式声明短期自签证书的具体地址。

所有 TLS 制品都必须是 root-owned 普通文件且不能是符号链接。私钥不得对 group/others 开放，其他证书、issuer、trust bundle、internal CA 与 marker 不得由 group/others 写入，任何制品都不得带执行位；CLI 生成的临时 cert/key 均为 `0600`。issuer marker 只接受精确的 `internal` 或 `acme`（可带一个换行）。内部 CA 必须是当前有效的单个自签 CA，并包含在 trust bundle 中；服务证书切换为 ACME 后仍保留它供客户端下载。

last-known-good 只保存在当前 `anasd` 进程内；服务重启后仍必须从磁盘验证出至少一个候选证书。没有有效候选时 TLS 握手失败，但明文 HTTP 仍只按当前 console state 和路由 allowlist 处理，不会因此开放额外能力。

## 监听与引导风险

`lan` 会绑定 `0.0.0.0`，并在系统支持时绑定 `[::]`。它表示全部接口，不是“识别出的局域网”：Wi-Fi、VPN、容器网桥和公网接口都可能被包含。首次引导明文 HTTP 不具备机密性或抗主动劫持能力；接口隔离和防火墙由管理员负责。这是 NAS 启动后可立即从同网设备访问的已接受产品边界。

管理界面随 `anasd` 二进制嵌入，不需要单独部署 Web 服务。`bootstrap` 状态下，根路径可从直连明文 HTTP 或 TLS 加载主界面；`enrollment`/`full` 的明文根路径只跳转到规范 HTTPS origin，TLS 根路径加载主界面。固定的 `/emergency` 页面使用不依赖 Vue 的独立小包，可在主 SPA 损坏时执行最小健康检查；它遵循相同的 state、transport 与直连 listener 限制，不是认证绕过入口。

不接受该边界时，把 `mode` 设为 `loopback`，仅绑定 `127.0.0.1`/`[::1]`，并使用 `ssh -L`。也可在当前 SSH 会话显式执行：

```bash
sudo anas console tls --self-signed
```

该命令只使用 `tls.temporary` 中声明的 SAN，打印临时证书与 SPKI 的 SHA-256 指纹，并同时签发一个默认 20 分钟的 bootstrap token。也可单独执行 `sudo anas console token --ttl 20m`；允许范围是 15—30 分钟。原 token 只显示一次，服务端只保存 SHA-256 摘要，重新签发会撤销旧 token 和旧 bootstrap session。进入 `enrollment` 后，该命令只能签发同一 transaction 的恢复 token，路由仅含 system/CA、job/events 与 handoff；不会重新开放 config、plan 或 apply。进入 `full` 后拒绝再签发 bootstrap token。

`anasd` 已持久化 `bootstrap → enrollment → full` 单向状态。验证通过的 lego `internal` 或 `acme` 证书会把 `bootstrap` 原子推进到 `enrollment`；临时自签证书不会改变 capability state。浏览器在旧 origin 用受限 bootstrap session 签发一次性 handoff，再以顶层 form POST 到 `https://anas.<base_domain>:<port>` 兑换 Secure enrollment session；服务端把 handoff 绑定到源/目标 origin 和该 TLS 连接握手时实际选中的证书 SPKI。直连管理 TLS 禁用 session resumption，确保每条连接都实际选择证书并记录该 SPKI。exchange 成功时设置 HttpOnly session Cookie 和同源 SPA 可读的独立 CSRF Cookie，并以 `303` 回到目标 origin 根路径，不在 URL 或 JSON 暴露 CSRF。创建首个 owner 时，CSRF Cookie 必须与 `X-CSRF-Token` 精确相等且通过服务端 session digest 校验；成功响应删除 enrollment session、CSRF 和 bootstrap Cookie。创建首个本地 owner 与撤销全部引导凭据、进入 `full` 使用可恢复事务；WAL 持久化后，即使浏览器断开也会在独立有界 context 中完成提交或回滚仲裁。整个流程不开放 CORS。enrollment 明文/TLS 可下载已验证的公开 `anas-internal-ca.crt`，enrollment/full 明文根路径只跳到上述规范 HTTPS origin；跳转不使用请求 Host、query、Cookie、Authorization 或 body。首次 config/plan/apply 与 full 直连本地 step-up HTTP 路由已经开放；完整级受信代理 listener 通过 mTLS、精确源 IP、固定身份头和 OIDC session 提供同一能力。本地登录、引导、注册与应急 UI 在受信代理源始终返回 `404`。

## Traefik / OIDC 受信入口

启用 `oauth2_proxy.console_proxy_enabled` 后，Module 会发布 `anas.<base_domain>` 路由、现有 `ANAS_FORWARD_AUTH_*` 中间件，以及名为 `ANAS_CONSOLE_MTLS` 的 Traefik `serversTransport`。oauth2-proxy 仍是 OIDC 客户端；`anasd` 不做 OIDC discovery、code exchange，也不接收 IdP 密码。它只在独立受信 listener 上消费 ForwardAuth 验证后生成的固定七个身份字段，并拒绝重复、逗号歧义、issuer/subject 缺失或不匹配的字段；直连 listener 会无条件剥离这些 Header。

Traefik Hook 为每个命名 transport 在 Secret Store 生成稳定的专属 CA 和客户端证书，将 CA 公钥、客户端 cert/key 与 SPKI 摘要投影到 Traefik 专用 runtime `dynamic/client-identities/ANAS_CONSOLE_MTLS/`；CA 私钥不投影。`anasd` 配置中的 `client_ca` 与 `client_spki_sha256` 必须取自该身份。将 CA 公钥复制到稳定的 root-owned `0600` 路径后再启动服务。当前 M1.5 尚未由安装器自动完成这一步；不要把 lego 服务端 CA、普通路由 TLS 或仅来源 IP allowlist 当作代理身份。

`allowed_source_ips` 必须填 Traefik 容器当前连接宿主时使用的单个精确地址，可通过 `docker inspect` 核对 `anas_traefik` 的网络地址。Docker 重建导致地址变化时，先更新 root-managed 配置并重启 `anasd`；请求在 mTLS 通过前不会进入 HTTP handler。代理高危操作只接受不超过 5 分钟的 OIDC `auth_time`，签发的 StepUpProof 还绑定 issuer、subject、动作/计划、一次使用和短 TTL；过旧时要求用户回到 IdP 重新认证，不回退到本地或 IdP 密码输入。

随时运行以下命令记录两个入口；它不依赖控制台页面：

```bash
sudo anas console status
```

`Direct recovery (local owner)` 是 IAM 故障时的直连恢复地址，`Traefik / OIDC` 是日常代理地址。控制台访问页也显示两者，但代理源上的直连地址仅为不可点击文本，不会发布本地账号 fallback 链接。

`anasd` 还会在开放监听器前验证或创建 `console_store/jobs.jsonl`，并在整个进程生命周期独占 `jobs.execution.lock`。只有取得该租约的 daemon 才执行启动恢复并把遗留 `running` 任务标记为 `interrupted`；第二个 daemon 会在恢复和监听前失败，不会改写首个 daemon 的活动任务。未变化的稳态读取复用已验证状态；同进程协作 Store 的追加只有在有界 receipt 链完整时才增量校验新 tail，跨进程或其他来源不明的增长会保守地全量重验。所有受支持任务/事件 writer 都必须经 `consolejobs.Store`/`jobs.lock`。调用方导致的 record/批次超限返回输入错误而不会毒化 Store。job journal 会在预计大小跨越内部 64 MiB 边界时评估 prospective state 的实际可回收量；达到收益门槛时写入带 generation、counts 和 SHA-256 seal 的分块 checkpoint，经 temp fsync、原子 rename 与目录 fsync 后才截断旧 inode。已打开且发现 canonical inode 换代的旧 Store 必须在 `jobs.lock` 下全量验证，只接受带完整 seal、更高 generation 且相对已验证状态无回退的 checkpoint，再切换到新 FD；receipt 不跨 inode。

审计独立使用 `audit.jsonl`、固定不换代的 `audit.lock` 与 reserved temp path `audit.jsonl.compacting`。首次初始化先持久化 checksummed pristine lock slot，其中固定 StoreID、携带当次策略且 generation/sequence/prune 均为 0、没有 commit time，再 fsync 匹配的 journal header；首次非零策略随后固定。空 lock 可配对既存空 journal，兼容旧格式时也可配对可明确识别为旧 Event 编码、没有完整 record 的单条残尾；首槽 torn 且无有效 revision 时只可配对精确空 journal 并重写 revision 1，或在 journal 完整验证为旧 Event-only 格式时按其既有水位/commit time 重试首次 metadata 迁移。已有有效 pristine 首槽时，仅可用原 StoreID/策略补完既存空或可证明同 StoreID 的规范 partial header。完整 envelope header 却没有有效 metadata 必须拒绝；非空 torn 首槽配任何 partial journal 也必须拒绝且不得截断。锁元数据位于两个固定 512-byte slot，每槽带递增 revision 与覆盖 revision/metadata 的 SHA-256；更新交替 `WriteAt` 且永不 truncate。恢复选择最高完整有效 revision，最新槽 torn 时回退旧槽，初始化完成后两槽均无效则 fail closed；旧单行 metadata 迁移到双槽期间若新槽 torn，也会回退仍完整的旧前缀。metadata 后续记录 generation、`last_sequence`、`pruned_through` 与 `last_recorded_at`；每个新已确认 append/compaction 在解锁和成功返回前更新。journal 已持久而 metadata 尚旧是允许的后续 crash window：Open 完整验证其为同 lineage 前进状态后写新槽追平；journal 缺失/空、相对 metadata 回退，或固定后的 policy mismatch 均 fail closed。`audit.Writer` 按 Writer commit time 淘汰连续 sequence 前缀，调用方 occurrence timestamp 不参与判断；旧 Event-only journal 以 inode mtime 作为 legacy `recorded_at` 并在首次 append 强制迁移，其他自动换代只在 obsolete history 与实测收益均达标时发生。checkpoint 封存 lineage、水位、event count、retained `recorded_at` 与 SHA-256 seal，拒绝时间倒退并要求 snapshot begin/end time 精确相等；temp fsync、原子 rename 与目录 fsync 后才确认换代并截断旧 inode。旧 Writer 只在锁内接受同 lineage、更高 generation 且 retained event/水位无回退的替代。尚未尝试 rename 且 temp 清理完全成功的取消不毒化 Writer，其他歧义或持久化故障 fail closed。rename 前中断留下的安全 temp 由下一次锁内 Open/Compact 清理；symlink、hardlink、权限过宽或非普通文件不删除并 fail closed。完整行按 2 MiB 上限有界拒绝，调用方超限不毒化 Writer，ENOSPC 保留底层原因并 fail closed。能无视锁篡改 root-owned `0600` journal 的本地 root 等价进程不在该威胁模型内。

当前 `anasd` 仍调用无破坏性保留默认的 `audit.Open`：`MaxEvents=0` 与 `Retention=0` 表示相应淘汰维度关闭，服务配置也尚无覆盖字段。也就是说 crash-safe retention/compaction 机制已经具备，但生产保留值、周期 `Compact` 维护与 `GET /api/v1/audit-events` 查询仍待接线；接线时 daemon 与 CLI 的全部协作 Writer 必须使用相同 Options。在这些产品值冻结前不会暗自删除审计历史。

`GET /api/v1/jobs`、`GET /api/v1/jobs/{id}` 与 `GET /api/v1/jobs/{id}/events` 提供脱敏后的只读任务历史和 SSE 重放；bootstrap 与 enrollment 状态中的恢复凭据只能读取同一 transaction 创建的任务，full 状态的本地 owner 只能读取已注册 workspace 的任务。SSE 支持严格的 `Last-Event-ID`、事件缺口 `410`、heartbeat、逐次写期限和进程级连接上限；每个 replay batch、poll 与 heartbeat 边界都会以当前状态重做 session 和对象授权且不延长 idle TTL，失权后静默关闭。terminal job 会先排空最终事件再关闭，携已追平 `Last-Event-ID` 的重连返回 `204` 以停止浏览器 `EventSource`。任务创建与执行路由尚未开放。
