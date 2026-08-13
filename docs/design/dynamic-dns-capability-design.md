# 动态 DNS 能力设计

## 1. 目标与硬约束

ANAS 应允许部署声明"把我自己的 A/AAAA 记录保持最新"，而不指定由哪个程序去做，
也不在任何 Hook 里按厂商名写分支。

本设计遵循四条硬约束：

1. **两个"provider"必须分开。** `dynamic_dns.provider` 选的是**实现**
   （`ddns_go` / `ddns_updater`），`dns_provider` 选的是**DNS 厂商**
   （`tencentcloud` / `cloudflare`）。把它们混成一个字段是这套设计里最容易犯、
   代价最大的错误。
2. **选中不等于独占。** 一个部署可以同时运行多个 DDNS 实现。能力绑定决定的只是
   *谁持有 ANAS 声明的那组记录*，其余实现照常运行、各管各的配置。
3. **证书和动态 DNS 不必在同一家厂商。** 每个引擎独立选择厂商，凭据按引擎隔离。
4. **凭据兼容性是推导出来的，不是声明的。** 见 §3.3。

约束 1 的理由：同一家厂商经常对不同用途给出不同的凭据对象。Namecheap 的 DNS API
用户和它的 Dynamic DNS 密码是两个东西，同一个账号无法从一个推出另一个。把"厂商"
和"实现"合并成一个概念，就无法表达这件事。

约束 2 的理由：两个 updater 更新不同的域名是完全合理的安排，禁止它没有依据。

## 2. 用户配置

最小形态是两行，不需要在 `modules` 里列任何 DDNS module：

```yaml
dynamic_dns:
  provider: auto
  dns_provider: tencentcloud

secrets:
  tencentcloud_secret_id: ...
  tencentcloud_secret_key: ...
```

解析结果：

```text
lego → traefik → ddns_go

dynamic dns: ddns_go (auto)
```

`ddns_go` 由能力拉入。它使用自己的 Web 登录，不会再因为动态 DNS 顺带拉入 IAM
和 `oauth2_proxy`。

### 2.1 每个引擎独立选厂商

```yaml
modules:
  lego:
    config:
      dns_provider: route53          # 证书走 Route53
  ddns_go:
    config:
      dns_provider: tencentcloud     # 动态 DNS 走腾讯云
```

`modules.<module>.config.dns_provider` 优先于 `dynamic_dns.dns_provider`。所以可以让
声明的记录在一家厂商，同时手工再跑一个 updater 在另一家。

### 2.2 凭据的两种拼法

同一套机制支持共享和独占，用户只需要选择怎么命名：

```yaml
secrets:
  # 共享：所有配置了这个厂商的引擎都用它
  tencentcloud_secret_id: ...
  tencentcloud_secret_key: ...

  # 独占：只有这个 module 能读到
  ddns_go_namecheap_ddns_password: ...
  lego_namecheap_api_key: ...
```

**DNS 凭据的 `config.consumes` 恒为空。** 见 §3.4。

## 3. DNS 平台注册表

### 3.1 单一真相源

[`internal/dns/providers.yml`](../../internal/dns/providers.yml) 是唯一的真相源。
每个平台按 engine 分别声明 provider 代码和凭据键：

```yaml
- name: tencentcloud
  title: Tencent Cloud DNS (DNSPod)
  engines:
    lego:
      provider: tencentcloud
      credentials:
        - {key: TENCENTCLOUD_SECRET_ID, role: id}
        - {key: TENCENTCLOUD_SECRET_KEY, role: secret}
    ddns_go:
      provider: tencentcloud
      credentials:
        - {key: TENCENTCLOUD_SECRET_ID, role: id}
        - {key: TENCENTCLOUD_SECRET_KEY, role: secret}
```

规范平台名取 lego 的 CLI provider 代码，因为 lego 覆盖的厂商远多于两个 DDNS 引擎，
最少产生歧义。engine 的 provider 代码自动成为别名，所以用户写 ddns-go 界面里看到的
`name_com`、`nsone`，或 lego 的 `namedotcom`、`ns1`，都能解析到同一个平台；
连字符和下划线等价。

### 3.2 投影到 module bundle

Module hook 是**自包含程序**，会被复制到本仓库之外分发，不能 import 这个包。所以
[`cmd/gen-dns-registry`](../../cmd/gen-dns-registry/main.go) 把每个 engine 的切片投影
成一个自包含 Go 文件写进对应 module：

```text
modules/lego/hook/dns_registry_gen.go          仅含 lego 能用的平台
modules/ddns_go/hook/dns_registry_gen.go       仅含 ddns-go 能用的平台
modules/ddns_updater/hook/dns_registry_gen.go  仅含 ddns-updater 能用的平台
```

改完 `providers.yml` 后运行：

```bash
go run ./cmd/gen-dns-registry .
```

`TestProjectionsMatchCommittedFiles` 会逐字节比对，忘记重新生成就会失败。另有测试
断言投影不会串台——lego 的表里不能出现 `dnspod`，ddns_updater 的表里不能出现
`tencentcloud`。

### 3.3 兼容性是推导的

两个引擎的凭据键集合完全相同 → `shared`；都支持但键不同 → `separate`；
一方不支持 → `unsupported`。

**不设 `compatibility:` 字段**，因为那会成为第二个真相源，可能和紧挨着它的键列表
互相矛盾，而推导是精确的。

```text
tencentcloud  lego/ddns_go = shared        lego/ddns_updater = unsupported
cloudflare    lego/ddns_go = shared        lego/ddns_updater = shared
namecheap     lego/ddns_go = separate      （DNS API vs Dynamic DNS 密码）
huaweicloud   lego/ddns_go = separate      （lego 多一个 REGION）
rainyun       lego/ddns_go = separate      （ddns-go 多一个产品 ID）
dnspod        lego/ddns_go = unsupported   （lego v5 已删除该 provider）
```

Cloudflare 是推导优于声明的最好例子：lego 也接受"邮箱 + Global API Key"，但 ddns-go
只发 Bearer Token。注册表里只登记 Token 形式，于是推导出的 `shared` 恰好只覆盖真正
可互换的那种配置。

`plan` 会报告推导结果：

```text
dns platforms:
  ddns_go -> alidns
  lego -> alidns
  ddns_go/lego credentials: shared
```

### 3.4 凭据物化

Runner 在任何 hook 运行之前，把用户写的凭据归一化到每个引擎自己的命名空间
（[`internal/runner/dnscred.go`](../../internal/runner/dnscred.go)）：

```text
用户写 tencentcloud_secret_id
  → LEGO_TENCENTCLOUD_SECRET_ID      (owner=lego)
  → DDNS_GO_TENCENTCLOUD_SECRET_ID   (owner=ddns_go)

canonical 拼法本身不下发给任何 module
```

这样做的关键收益是**隔离不需要新机制**：`envScopeFor` 的前缀所有权规则本来就管着
每个 module 自己的变量，`DDNS_GO_` 开头的键天然到不了 lego。所以 DNS 凭据的
`config.consumes` 恒为空。

物化出的凭据同时被标记为 sensitive。没有这一步，Traefik 会因为依赖 lego 而通过
依赖闭包拿到 lego 的 DNS API Token——它对此毫无用途。这个泄露在实现时被渲染产物
审计发现，现由 `TestMaterialisedCredentialsDoNotFollowTheDependencyClosure` 锁住。

校验也在 hook 之前：厂商名打错、凭据只配了一半，是 `plan` 阶段的配置错误，不是容器
反复重启之后才被发现的认证失败。

### 3.5 module 命名的硬约束

`isOwn` 用 `strings.HasPrefix` 判断前缀所有权。如果 updater 的 module 名叫 `ddns`，
它的前缀 `DDNS_` 是 `DDNS_GO_*` 的前缀，**它会拿到 ddns_go 的全部凭据**。

这就是把 `ddns` 重命名为 `ddns_updater` 的原因——不是整理，是这套隔离成立的前提。
由 `TestScopedEnvSeparatesPerEngineCredentials` 锁住。

## 4. 实现选择

### 4.1 部署级能力

与 IAM 不同，动态 DNS **没有消费方 module**：没有任何 module 声明"我需要自己的 A 记录"，
是部署整体需要。所以没有依赖边可以解析它，选中的 module 被作为模块图的**根**注入
（[`resolveOrder`](../../internal/runner/runner.go)）。

绑定记在锁文件的保留槽位下：

```yaml
capability_bindings:
  "@deployment":
    dynamic_dns: ddns_go
```

`@` 不可能出现在 module 名里，所以不会和真实模块撞。

### 4.2 auto 的解析顺序

1. **锁文件里已有的绑定优先**（前提是它仍能处理所选厂商）。这样将来新增第三种实现、
   或调整偏好顺序，都不会把已有部署悄悄挪到别的 updater 上。
2. 用户已在 `modules` 里列出的实现优先——不平白给部署多加一个容器。
3. 固定偏好顺序 `ddns_go → ddns_updater`。

顺序写死在代码里而不是靠目录枚举，否则新增一个 module 就可能改变已有部署的解析结果。
`ddns_go` 在前的理由是它会探测宿主 IPv6，而这正是家用部署真正需要的场景。

锁定的实现不再支持所选厂商时会重新解析，而不是硬保留。

### 4.3 重叠是警告，不是拒绝

两个 updater 维护同一条记录时**只警告，退出码为 0**：

```text
warning: more than one dynamic DNS updater maintains these records:
  ddns_go and ddns_updater both maintain A *.nas.test at cloudflare
```

理由：两边都用外部探测时，IPv4 在同一个 NAT 后面会得到同样的地址，各自比对后都认为
"没变化"而不写，首次之后就收敛，**通常并不严重**。

但仍然提示，因为分歧的场景不罕见，且症状难查：

- IPv6 隐私扩展下网卡有多个全局地址，出口地址会变，两边探测结果会真的不同
- 两边探测端点不同，多 WAN 或 CGNAT 下答案可能不一致
- API 调用翻倍，在限速的免费版套餐上是实际成本

分歧的表现是记录**抖动**而不是**错误**，事后归因比直接写错难得多。

判定用「厂商 + 地址族 + 域名」三元组，所以**不同厂商不算重叠**——迁移期一个域名挂
两家是合法安排。

## 5. 地址发现

两个实现的能力**不对称，且是结构性的**：

| | ddns_go | ddns_updater |
|---|---|---|
| 问外部 HTTP 服务 | `gettype: url` | `http` fetcher |
| 问 DNS 解析器 | 无 | `dns` fetcher |
| **读本地网卡** | `gettype: netInterface` | **不支持** |

所以即使宿主的公网地址就挂在自己网卡上，ddns-updater 也只能从外部问回来。

两边的默认都是"问外部服务"，理由相同：一块网卡上常有同族的多个地址——IPv6 临时
隐私地址与稳定地址并存、ULA 与全局地址并存——只有外部观察者能说清哪个真正可达。

具体配置见 §8 和各 module 的 README。

### 5.1 宿主 IPv6 探测在 runner

Runner 的 `applyHostNetwork` 导出 `HOST_IPV6` / `HOST_IPV6_INTERFACE` /
`HOST_HAS_IPV6`，两个 DDNS module 都读它而不是各自实现一遍。这是关于宿主的事实，
不属于任何一个 module。

探测**排除 ULA（`fc00::/7`）**：Go 的 `IsGlobalUnicast()` 对 ULA 也返回 true，
但把 ULA 写进 AAAA 记录会得到一个外部客户端连不上的地址。

这只是关于**宿主**的判断。它不说明 bridge 网络里的容器能不能走 IPv6 出站——那是
每个 module 自己回答的另一个问题。

## 6. Web 界面与认证

两个界面都能改写这个部署拥有的全部 DNS 记录，但认证方式不同：

- `ddns_updater` 没有自己的登录，因此把 `forward_auth` 声明为硬依赖；
- `ddns_go` 有不能关闭的本地登录，只使用自己的账号，不依赖 IAM 或
  `forward_auth`。

`ddns_updater` 的 `forward_auth` 当前由 `oauth2_proxy` 提供。它自己是 IAM 的 OIDC 消费者，并通过既有的
`ANAS_IAM_CLIENT__*__ALLOW_GROUPS` 契约声明只允许 `Admins`。**组判定不在网关里**，
由 IdP 执行——不在该组的用户走不完 OIDC 流程，所以网关不可能和 IdP 对"谁是管理员"
产生分歧。管理员组名取自 `SAMBA_DC_ADMIN_GROUP_NAME`，不写死。

在此之前 `ddns` 的 router 上**一个中间件都没有**，界面是裸奔的。

### 6.1 ddns-go 的登录关不掉

经上游源码核对：

- `web/auth.go` 的 `Auth` 包装器在没有会话 cookie 时一律重定向到登录页
- `web/login.go` 的 `LoginFunc` 拒绝空用户名或空密码
- `main.go` 里所有功能路由都包在 `Auth` 里
- 命令行没有关闭鉴权的开关，`-noweb` 是整个关掉 Web 服务

**留空不等于关闭，而是布下首次运行陷阱**：启动后 30 分钟内第一个到达的访问者选定
管理员账号，并用自己的 `Referer` 头决定要不要允许公网访问；30 分钟后界面锁死。

社区流传的"关闭外网访问就可以不设用户名密码"是**错的**。`notallowwanaccess: true`
只把 `CheckPassword` 的密码强度门槛从 30 bit 降到 25 bit，不是免密码。

所以密码是被托管而不是省略：Runner 为 ddns-go 生成独立随机本地密码，通过
`anas admin local credential ddns_go` 显式查询。它是 ddns-go Web 界面的唯一登录
凭据；Secret 变化时，存储的哈希验不过就会在下次 render 重算。

### 6.2 `notallowwanaccess` 保持 true

ddns-go 的公网访问检查针对 `r.RemoteAddr`——直连对端。对代理请求那就是 Traefik 在
共享网络上的地址，属于私有段。所以拒绝公网访问不影响通过 Traefik 的访问，还能把
直连宿主的请求挡在外面。

### 6.3 host 网络与路由

`ddns_go` 用 host 网络才能看到宿主 IPv6，但 Traefik 的 Docker provider 看不见 host
网络的容器。解决办法是 Traefik 的**声明式路由契约**（见
[traefik/README.md](../../modules/traefik/README.md)）：

```text
ANAS_TRAEFIK_ROUTE__<NAME>__RULE / __URL / __MIDDLEWARES / __ENTRYPOINTS / __TLS
```

Web 界面监听 host 端口，通过域名或宿主地址访问时都使用同一套 ddns-go 本地账号。
声明式路由不附加外部认证中间件，也不再为了认证固定 Traefik 子网。

## 7. ddns_go 的配置合并

`.ddns_go_config.yaml` **有两个作者**：ANAS 从 `config.yml` 声明记录，用户通过
Web UI 也写同一个文件。两种简单做法都不行——每次覆盖会删掉用户的界面配置，
干脆不写则声明的配置只是个建议。

所以容器启动时、ddns-go 运行之前，由
[`anas-ddns-go-reconcile`](../../modules/ddns_go/ddns-go/reconcile/) 合并。

**用 `yaml.Node` AST 合并而不是结构体往返**：后者会静默删掉它不认识的所有字段，
上游哪天新增一个就会在下次部署时被抹掉。

所有权规则：

| 情况 | 行为 |
|---|---|
| `anas-managed:<id>` 同名 | 整条替换（改厂商/凭据才能生效） |
| 手工条目、记录目标完全相同 | 接管，不追加 |
| 手工条目、记录目标部分重叠 | 报错并指名冲突条目，不猜 |
| 同域名但不同厂商 | 不算冲突 |
| 其他手工条目 / 未知字段 / webhook / lang | 原样保留 |
| 重复部署 | 幂等，逐字节不变 |

记录身份只看「厂商 + 启用的地址族 + 域名」。**凭据、TTL、地址探测方式不参与身份
判断**——那些正是接管要修正的东西。

写入是 0600 + fsync + rename，然后 `syscall.Exec` 交棒给 ddns-go：进程被整体替换，
容器里最终只有 ddns-go 一个进程、仍是 PID 1、信号直达。

reconcile 是**独立 Go module**，这样 Docker 构建上下文是 bundle 而不是整个仓库。
代价是 `go test ./...` 不会进入嵌套 module，所以 `test-static.sh` 里显式列出了它。

## 8. 环境变量契约

Runner 发布：

```text
<PREFIX>_DNS_PLATFORM              解析后的规范平台名
<PREFIX>_<CANONICAL_KEY>           物化后的凭据（sensitive）
<PREFIX>_DYNAMIC_DNS_MANAGED       是否持有 ANAS 声明的记录
```

ddns_go hook 额外发布给容器内 reconciler：

```text
ANAS_DDNS_GO_RECORDS         期望状态 JSON（不含凭据）
ANAS_DDNS_GO_PROVIDER        ddns-go 自己的 provider 代码
ANAS_DDNS_GO_CRED_ID_KEY     间接引用：哪个变量装着凭据
ANAS_DDNS_GO_CRED_SECRET_KEY
ANAS_DDNS_GO_USERNAME / _PASSWORD_HASH
ANAS_DDNS_GO_IPV{4,6}_GETTYPE / _URLS / _INTERFACE
```

凭据用**间接引用**而不是直接传值：期望状态里没有任何密钥，密钥只通过 0600 的
`.env` 到达容器一次。

## 9. 密码存储策略

持久明文只在 Secret Store，其余全是 bcrypt 哈希：

| 位置 | 内容 | 权限 |
|---|---|---|
| `.anas/secrets.generated.yml` | `ANAS_LOCAL_ADMIN__DDNS_GO__PRIMARY__PASSWORD` 明文及 `DDNS_GO_WEB_PASSWORD_HASH` | `0600` |
| Hook calculate 请求 | `DDNS_GO_LOCAL_ADMIN_PASSWORD` 明文，仅进程内短暂存在 | 不落盘 |
| 渲染的 `.env` | `DDNS_GO_LOCAL_ADMIN_USERNAME`、`DDNS_GO_PASSWORD_HASH` | `0400` |
| `.ddns_go_config.yaml` | `user.password` bcrypt | `0600` |

Runner 把本地密码只注入 ddns-go 的 Hook 输入；Hook 发布用户名和 bcrypt hash，明文
不会进入 deployment `.env` 或容器环境。

哈希只算一次并持久化——bcrypt 每次加盐不同，每次重算会让配置文件每次重启都被改写。
只有当它验不过当前管理员密码时才重算，这既是密码同步的机制，也是"没改就不重写"的
机制。

ddns-go 自己默认把配置写成 `0644`，reconciler 显式 chmod 0600，因为同一个文件里还
有 DNS API 凭据。

## 10. 关键决策

1. **两个 provider 概念分开**，不合并成一个字段。
2. **兼容性推导而非声明**，消除第二真相源。
3. **凭据按引擎命名空间物化**，隔离复用既有前缀规则，`consumes` 恒为空。
4. **`dns_provider` 不是无条件必填**：它只服务于 ACME DNS-01 挑战，虚拟域名不会
   发起挑战，所以 lego 只在"要真的申请证书"时才要求它。
5. **选中不等于独占**，重叠只警告。
6. **绑定进锁文件**，auto 的结果稳定。
7. **注册表只登记核对过官方来源的平台**。凭据键名写错的表现是认证失败，回溯到这张
   表的成本很高，所以不凭记忆填。

## 11. 明确不做

- **不做 `cmd` 地址发现。** ddns-go 支持用任意命令产出地址，那是声明式配置里的
  命令执行面，这个部署不需要。
- **不校验 provider 名单。** ddns-updater 接受十一个名字外加任意 `url:` 端点，把
  那份列表抄进 hook 就多一处要跟上游同步的地方，而写错名字它自己会报。只校验
  `publicip_fetchers` 这种小的封闭集合。
- **不给 ddns_updater 登记 dnspod。** 旧 token 无法与 lego 共享凭据，而 ddns_go
  已覆盖仍持有旧 token 的人；在这里再开一条通往同一厂商的路，只会多一个不该被选的
  选项。
- **不做 `DNSPOD_API_KEY → TENCENTCLOUD_*` 自动转换。** 它们不是同一种凭据对象。
- **不改 ddns-go 源码来关闭登录。** 那意味着接管整个构建、放弃上游多架构镜像，
  并在每次升级时重打补丁。

## 12. 与上游核对的事实

以下结论均来自源码或官方文档核对，不是推断：

- lego v5 已删除 `dnspod` provider，官方替代是 `tencentcloud`
- ddns-go `GetIpv4Addr` 的 `default` 分支只打印 `get IP method is unknown` 并返回
  空串——未知的 `gettype` 不是回退，是永远读不到地址
- ddns-go Web UI 的 `gettype` 默认是 `url`（两个地址族的 radio 都是
  `value="url" checked`）；配置格式本身**没有**默认值
- ddns-go `ResetPassword` 失败时只打日志，`main.go` 无条件 `return`，**退出码始终
  为 0**；且它只写密码不写用户名，并走 `SaveConfig()` 的结构体往返
- ddns-updater 的公网地址发现只有 `http` 和 `dns` 两种 fetcher，**没有本地网卡模式**
- ddns-updater 默认轮换所有 echo 服务，README 明说是为了避免被限流
- oauth2-proxy 的 Traefik 集成有两种形态；`/oauth2/auth` 形态返回裸 401，需要额外的
  errors 中间件和 `statusRewrites: 401 → 302`，**漏掉该 rewrite 浏览器会显示
  "Found." 链接而不自动跳转**。本设计采用 static-upstream 形态
  （`--upstream=static://202` + `--reverse-proxy=true`），一个中间件即可
