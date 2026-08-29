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
    issuer_marker: /var/lib/anas/lego/.issuer
  temporary:
    certificate: /var/lib/anas/console-tls/temp-console.crt
    private_key: /var/lib/anas/console-tls/temp-console.key
    dns_names:
      - bootstrap.example.test
    ip_addresses:
      - 192.0.2.10
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
| `tls.lego` | lego 发布的长期证书、私钥、issuer、trust bundle 和 `.issuer` marker。`anasd` 只消费并验证这些文件，不签发长期证书。 |
| `tls.temporary` | 可选的临时自签叶证书路径和显式 SAN；至少给出一个 `dns_names` 或具体 `ip_addresses`，不做主机名、网卡或环境变量发现。文件名必须是示例中的固定名称，供显式 CLI 生成。 |

`tls.lego` 与 `tls.temporary` 都配置时，验证通过的 lego 证书优先。每次 TLS 握手都经动态 `GetCertificate` 读取当前验证快照；新 pair 只有在私钥匹配、有效期、ServerAuth、SAN、链和 issuer marker 全部通过后才原子切换。更新失败会继续使用 last-known-good，不会回退到临时证书或扩大明文能力。

服务配置只在 `anasd` 启动时读取；修改端口、模式、Host、workspace、store 或 TLS 路径后必须重启。证书路径指向的制品是例外：进程会在后续 TLS 握手重新校验并热切换，无需重启。

`allowed_dns_hosts` 不会从 `tls.lego.base_domain` 自动派生。完整级直连应使用 `anas.<base_domain>`，管理员必须同时把该名字加入 `allowed_dns_hosts`，并确保客户端 DNS 能解析到 NAS。lego leaf 必须同时覆盖 `<base_domain>` 与 `anas.<base_domain>`；长期 CA/ACME 流程不得为管理面向 `ca.sh` 增加 `IP:` SAN。需要按 IP 使用 TLS 时，只能在 `tls.temporary` 中显式声明短期自签证书的具体地址。

所有 TLS 制品都必须是 root-owned 普通文件且不能是符号链接。私钥不得对 group/others 开放，其他证书、issuer、trust bundle 与 marker 不得由 group/others 写入，任何制品都不得带执行位；CLI 生成的临时 cert/key 均为 `0600`。issuer marker 只接受精确的 `internal` 或 `acme`（可带一个换行）。

last-known-good 只保存在当前 `anasd` 进程内；服务重启后仍必须从磁盘验证出至少一个候选证书。没有有效候选时 TLS 握手失败，但明文 HTTP 仍只按当前 console state 和路由 allowlist 处理，不会因此开放额外能力。

## 监听与引导风险

`lan` 会绑定 `0.0.0.0`，并在系统支持时绑定 `[::]`。它表示全部接口，不是“识别出的局域网”：Wi-Fi、VPN、容器网桥和公网接口都可能被包含。首次引导明文 HTTP 不具备机密性或抗主动劫持能力；接口隔离和防火墙由管理员负责。这是 NAS 启动后可立即从同网设备访问的已接受产品边界。

不接受该边界时，把 `mode` 设为 `loopback`，仅绑定 `127.0.0.1`/`[::1]`，并使用 `ssh -L`。也可在当前 SSH 会话显式执行：

```bash
sudo anas console tls --self-signed
```

该命令只使用 `tls.temporary` 中声明的 SAN，打印临时证书与 SPKI 的 SHA-256 指纹，并同时签发一个默认 20 分钟的 bootstrap token。也可单独执行 `sudo anas console token --ttl 20m`；允许范围是 15—30 分钟。原 token 只显示一次，服务端只保存 SHA-256 摘要，重新签发会撤销旧 token 和旧 bootstrap session。进入 `enrollment` 后，该命令只能签发同一 transaction 的恢复 token，路由仅含 system/CA、job/events 与 handoff；不会重新开放 config、plan 或 apply。进入 `full` 后拒绝再签发 bootstrap token。

`anasd` 已持久化 `bootstrap → enrollment → full` 单向状态。验证通过的 lego `internal` 或 `acme` 证书会把 `bootstrap` 原子推进到 `enrollment`；临时自签证书不会改变 capability state。浏览器在旧 origin 用受限 bootstrap session 签发一次性 handoff，再以顶层 form POST 到 `https://anas.<base_domain>:<port>` 兑换 Secure enrollment session；服务端把 handoff 绑定到源/目标 origin 和该 TLS 连接握手时实际选中的证书 SPKI。直连管理 TLS 禁用 session resumption，确保每条连接都实际选择证书并记录该 SPKI。exchange 成功时设置 HttpOnly session Cookie 和同源 SPA 可读的独立 CSRF Cookie，并以 `303` 回到目标 origin 根路径，不在 URL 或 JSON 暴露 CSRF。创建首个 owner 时，CSRF Cookie 必须与 `X-CSRF-Token` 精确相等且通过服务端 session digest 校验；成功响应删除 enrollment session、CSRF 和 bootstrap Cookie。创建首个本地 owner 与撤销全部引导凭据、进入 `full` 使用可恢复事务；WAL 持久化后，即使浏览器断开也会在独立有界 context 中完成提交或回滚仲裁。整个流程不开放 CORS。当前仍未开放首次 config/plan/apply HTTP 写路由。

`anasd` 还会在开放监听器前验证或创建 `console_store/jobs.jsonl`，并在整个进程生命周期独占 `jobs.execution.lock`。只有取得该租约的 daemon 才执行启动恢复并把遗留 `running` 任务标记为 `interrupted`；第二个 daemon 会在恢复和监听前失败，不会改写首个 daemon 的活动任务。未变化的稳态读取复用已验证状态；同进程协作 Store 的追加只有在有界 receipt 链完整时才增量校验新 tail，跨进程或其他来源不明的增长会保守地全量重验。所有受支持 writer 都必须经 `consolejobs.Store`/`jobs.lock`；能写 root-owned `0600` journal 且无视该锁并发篡改的本地 root 等价进程不在缓存完整性威胁模型内。调用方导致的 record/批次超限返回输入错误而不会毒化 Store；物理 journal compaction 尚未实现。`GET /api/v1/jobs`、`GET /api/v1/jobs/{id}` 与 `GET /api/v1/jobs/{id}/events` 提供脱敏后的只读任务历史和 SSE 重放；bootstrap 与 enrollment 状态中的恢复凭据只能读取同一 transaction 创建的任务，full 状态的本地 owner 只能读取已注册 workspace 的任务。SSE 支持严格的 `Last-Event-ID`、事件缺口 `410`、heartbeat、逐次写期限和进程级连接上限；每个 replay batch、poll 与 heartbeat 边界都会以当前状态重做 session 和对象授权且不延长 idle TTL，失权后静默关闭。terminal job 会先排空最终事件再关闭，携已追平 `Last-Event-ID` 的重连返回 `204` 以停止浏览器 `EventSource`。任务创建与执行路由尚未开放。
