# Incus 后续服务器 E2E 清单

状态：待执行。先完成代码、双语文档和本地门禁，再按本清单在本轮指定宿主执行；本清单不代表
用例已通过，也不新增或替代[需求矩阵](../../../dev-docs/requirements/incus-module.md)。
逐项保留 fixture/revision、操作、预期、实际、脱敏证据与清理结果。历史 namespace 实验与 daemon
供给探查单独记录，不汇总成完整 E2E 通过。TCP/UDP 发布不在本轮范围。

## 执行前置条件

- 明确本轮宿主、kernel/Incus/Docker/Traefik/nft/conntrack 版本及架构。发行版 Incus 6.0.5 与源码
  核验基线 7.3.0 分列；不能跨版本套用结论。缺少 `/dev/kvm` 时 VM 记为未执行，不自动改测容器。
- Incus 使用独立 daemon、数据根、证书与运行目录；Docker 使用独立 socket、数据根、exec-root 和
  namespace，不使用默认 daemon 或其他任务的测试 daemon。预先记录全局规则/路由/服务基线。
- guest image 固定 fingerprint、架构、档位；临时 BusyBox 夹具不作为 distrobuilder 产品镜像或
  发布目录。镜像导入与产品烘焙的验收分别记录。
- 存储正例使用独占、可清理的 btrfs/zfs 测试池。循环文件只能新建于测试目录，不格式化既有块设备。
  负例 `dir` 池只用于证明拒绝，不作为有效租约继续生命周期测试。
- 先验证所有正反探针在加载待测规则前可达，避免把无路由、未监听端口或 DHCP 失败误判成隔离成功。
- M2 helper/最小权限测试走其独立门禁；手工 sudo 实验不替代该验收。宿主安装与生产 ingress 仍未交付。

## 固定控制转发的独立验收（2026-09-18；全部待执行）

实现入口：`modules/incus/control-relay`；设计边界：宿主供给架构 §3.6—§3.8。
本节不授权安装服务、开放端口或执行网络变更。需先完成宿主通道及安装/卸载动作评审，再在独立
Linux 实验宿主按前置约束执行；本地 TCP 夹具不能替代该验收。

| 范围 | 待验证内容 | 当前证据 |
| --- | --- | --- |
| 配置与目的地 | 重复/大小写/未知字段、null、尾随数据、超限、任意 upstream/TLS key 均拒绝；只向 `127.0.0.1:8443` 拨号，客户端载荷不能改变目标 | `config_test.go` 已编写，未运行；实际 connect 目的地待独立观测 |
| 安装身份 | root、real/effective 身份不一致、额外附加组及任一 capabilities 拒绝；配置和祖先必须 root 所有且不可被组/其他用户写入，拒绝符号链接/硬链接及读取前后元数据漂移；路径替换不得逃逸已持有描述符 | 代码已编写；独立 UID、配置所有权与竞态夹具待验证 |
| 监听与网络 | 单个受管私有 IPv4/高位端口；通配、IPv6、IPv4-mapped、网段/广播地址拒绝；验证 LAN、其他 Docker 网络、全部 guest 和伪造源地址不能绕过入接口限制 | 必须结合宿主 INPUT/FORWARD 实测；源 CIDR 单测不构成通过 |
| 端到端身份 | 合法客户端成功；错误 pin、错误证书、跨 project 访问失败；服务不持有管理/消费者私钥、不终止 TLS | Incus 与 Provider/消费者实机验证待执行 |
| 字节与清理 | 双向原始字节、半关闭后响应、空闲期限、取消关闭两端；超过容量和连接失败不泄漏连接或 goroutine | `relay_test.go` 已编写，未运行；容量/资源压力仍待验收 |
| 拓扑变化 | interface index/name/address/up 任一漂移后停止接入并关闭现存连接；不改绑通配，不沿用旧配置接管新接口 | 独立控制 bridge 重建与服务重启待验证 |
| 启停/卸载 | 先网络拒绝规则再服务，再身份验证，最后 endpoint；失败不发布就绪；卸载关闭新租约和现存连接、撤销自身产物，不删除外部 daemon/其他部署资源 | 宿主动作、服务单元及所有权状态机未接入 |
| 发行版 | Debian 13、Ubuntu 24.04、Ubuntu 26.04 分别验证；未适配发行版不自动安装 | 全部待执行 |

低层测试入口为 `go test ./modules/incus/control-relay`，**本轮未执行**；该命令本身只运行本地
配置/TCP 夹具，不创建 Docker/Incus 网络，也不证明任何宿主规则或默认安装路径已通过验收。

## 镜像与消费者接入

| 场景 | 对应需求 | 操作与通过标准 | 状态 |
| --- | --- | --- | --- |
| 结构化 apply | R-019、R-066、R-067 | Forgejo 单对象与 AI Agent runtime→对象配置实际 apply；`spec_from` singleton/values 进入 ensure，消费者收到同一冻结摘要/绑定；旧字符串、混合或未知字段在副作用前拒绝 | 待执行 |
| 目录与回滚 | R-066、R-067、R-072 | 按架构/档位命中目录；revision 冲突和缺条目失败；变更当前目录后重放旧 deployment，仍使用旧摘要；缺失旧产物失败而不重建/换摘要 | 待执行 |
| 镜像 metadata | R-019、R-085 | 缺失、错误架构/档位/摘要时 ensure 失败且不登记新证书；导入精确产物后重试幂等；另用受限身份探测非 allowlist 镜像导入/启动，记录 daemon 与共享库各自边界 | 待执行 |
| 密钥与配置边界 | R-017、R-020、R-092 | 两消费者运行配置互不可见；重复 apply 与实际备份恢复保持各自命名密钥；manifest/state/日志只保留引用；Provider 和 guest 不收到命名密钥 | 待执行 |

## Provider、配额与生命周期

| 场景 | 对应需求 | 操作与通过标准 | 状态 |
| --- | --- | --- | --- |
| 存储池拒绝 | R-005、R-013、R-032 | 在缺失、未就绪、`dir`、未准入驱动池上执行真实 ensure；在 project/network/profile/trust 写入前失败；既有 project 的 inspect 保留 exists/restricted，但 ready/quota_enforced 为 false | 待执行 |
| 支持池供给 | R-005、R-012、R-032 | 在 btrfs/zfs 池分别重复 ensure；读取真实 pool、project、profile 和 trust；只产生一套资源，四项 limits 正确、证书精确限定本 project | 待执行 |
| 实际磁盘上限 | R-032 | 池空闲容量大于测试额度；guest 连续写入并 fsync 到限额，预期得到 EDQUOT/ENOSPC，且卷实际使用未持续超限；并行对照卷仍可写，排除整个池写满的假阳性；清除测试文件后恢复 | 待执行 |
| 总量超配 | R-008、R-032 | 绕过共享客户端，用受限证书直接请求超出 instances/CPU/memory/disk project 总量的创建；daemon 拒绝。CPU/memory 运行时限制另读 cgroup/guest 证据 | 待执行 |
| 同名前缀双消费者 | R-007、R-008、R-011、R-031 | 两个 project 使用相同实例名前缀并行创建；受限身份无法列出/操作另一 project，不能创建/修改网络或接入对方 bridge；拒绝 host source、额外 NIC、特权与 raw config | 待执行 |
| 完整生命周期 | R-025、R-027、R-029、R-030、R-033、R-034 | 实际共享客户端 create/start/exec stdin/stop/delete；Forgejo one-job 与 Agent 作业；取消/crash 后 janitor 回收；管理证书轮换不杀运行 guest；container/VM 分别记录时延 | 待执行 |

## HTTP 受管路由原型

| 场景 | 对应需求 | 操作与通过标准 | 状态 |
| --- | --- | --- | --- |
| 真实身份与路由 | R-011、R-053、R-054 | 从 Incus UUID、project、NIC、租约 IP/MAC 与 Docker veth 对端采集观测；Traefik 不加入 guest bridge，经显式 /32 路由与指定源 IP 连接 guest NIC:7000；不使用 proxy device/forward | 待执行 |
| 防火墙共存 | R-053、R-054 | 保留 Docker/Incus 原生 base chains，记录 hook/priority/计数器；确认生成表的许可穿过后续规则，不能靠关闭原生防火墙或全局 ACCEPT 获得成功 | 待执行 |
| HTTP 路由渲染 | R-054 | 在独立 Traefik 用现有 anas-entrypoint.sh 和 ANAS_TRAEFIK_ROUTE__* 渲染；按 Host 请求获得 guest 内容，未发布 Host 不可访问；公网 HTTPS/IPv6 入口另有真实前提才测 | 待执行 |
| 拒绝绕行与冒用 | R-011、R-053 | 7001、同 bridge 其他容器、LAN、跨租约、源 IP/MAC 冒用、guest 反向发起、IPv6 后端均拒绝；批准来源正例同时仍可达 | 待执行 |
| 撤销与超时 | R-063 | 先移除路由，再撤销 tuple，清除精确 conntrack；持续 HTTP 流停止且新连接失败；过期不靠旧磁盘请求自动续期；保存连接/计数器证据 | 待执行 |
| IP 复用 | R-063 | 撤销确认前不释放地址；新 UUID 的第二个 guest 复用 IP 后，旧域名与旧许可仍不可访问。单纯等待 TTL 不证明提前复用安全 | 待执行 |
| 发布授权与对账 | R-063、R-087、R-088、R-095 | 等中介/宿主动作实现后，测试停止/暂停/删除、事件丢失、重启恢复、路径穿越/符号链接/超大请求、越租约域名、auth 降级与非 HTTP 请求拒绝；原型生成器不承担这些保证 | 待实现后执行 |

## 结束与判定

只删除本轮带归属记录的实例、证书、profile、网络、规则表、路由、daemon、循环文件及其 loop 设备。
确认测试进程停止、临时 namespace 消失、loop 解绑、宿主规则与服务基线不变；失败也必须执行清理。
若远程动作被工具审批阻止，记录最后确认状态和待清理对象，不能把超时停止配置当成已清理证据。

任何一项未运行/失败都单列；本地单元、合成 namespace、Provider ensure 成功均不能替代完整实机
验收。M6/M11 与 Module developing 状态在对应退出条件全部有证据前不改为完成。


## 2026-09-12 新代码的待验证项（本轮不执行）

| 新路径 | 后续必须验证的行为 | 状态 |
| --- | --- | --- |
| 本地采集输入 | Linux/单宿主限制、默认 socket 和默认 bridge 拒绝；完整 ID、schema、64 KiB、重复键/大小写别名、符号链接及输出目录冲突 | 待编写/执行 |
| 实际 GET 与 link 采集 | 固定 Incus/Docker/iproute2 版本的字段兼容；NIC 配置 MAC/状态/租约一致、host interface 接网、双向 ifindex/link_index，VM 的 host_name 能否获得 | 待执行 |
| 观测漂移 | 采样中停止/重启 guest 或 Traefik、替换端点/veth/IP；不得生成新发布。两次读取相等不证明后续不会变化，变更前再观测 | 待执行 |
| 有序撤销 | 先删路由、再 tuple/conntrack，保留 deny 表；撤销后经默认路由也不可直连，直到完整实验 teardown 才删表 | 待执行 |
| 同拓扑替换 | 旧连接先结束；两张旧表与新表在一个 nft 事务替换，失败时原拒绝状态仍在，不出现放行空窗；保留 Incus/Docker base chains | 待执行 |
| 新旧记录边界 | 旧记录未应用、损坏或来自其他实验时不得盲目执行；当前不可发布只生成 withdraw，退出码 0 只代表写出计划；不自动恢复磁盘请求 | 待执行 |
| 拓扑变化与清理失败 | 租约作用域或 bridge/子网/来源变化仅退役旧发布；失败不释放 IP；停止所有实验端点前不能移除 deny 表 | 待执行 |

先补本轮代码的编译与本地回归，再按以上场景到已授权服务器统一验证。当前没有执行任何新路径。

## HTTP 授权冻结与中介续作待测项（本轮不执行）

| 新路径 | 后续必须验证的行为 | 状态 |
| --- | --- | --- |
| schema 与实际 prepare | ingress 省略无授权；null/未知/重复字段、大小写别名、非整数/重复/越界端口、空列表或超过 64 个拒绝；Structured spec_from value 可完整传入；端口排序且不修改输入对象 | 待编写/执行 |
| 冻结与重放 | prepare 的 compute_ingress 与 spec/identity/密钥引用一致；改当前 base_domain/认证设置后旧 deployment 保持原值；篡改/丢失冻结、跨资源/部署搬移失败 | 待编写/执行 |
| 关闭边界 | 带 ingress 的 start/activate/restart 在任何消费者启动和 Provider ensure 前失败；原活动部署不被停止；无 ingress 及历史无授权部署行为保持原样 | 待编写/执行 |
| ForwardAuth 冻结 | 缺 capability 绑定、错误 interface、错误 env owner、空/非法 middleware 均拒绝；请求不能指定 auth/middleware/entrypoint；真实中间件不存在或拒绝时不得降级到 none | 待编写/执行 |
| 域名与稳定性 | fixed 单目标；named 标签与长度边界；random 128 位 HMAC 已知向量、跨进程/apply/备份恢复一致；不同租约不同输出；明确注入冲突证明拒绝覆盖 | 待编写/执行 |
| 命名空间 | 相同及层叠 prefix-*、fixed 对 named/random、Module 域名与字面 Host 路由碰撞均拒绝；无法解释的 route rule 拒绝；生产适配器另验证实际外部/静态路由冲突 | 待编写/执行 |
| 目录读取攻击 | Linux 下 ../、绝对名、多级路径、最终符号链接/竞态替换、硬链接、FIFO/设备在实际打开前拒绝、超 4 KiB、读中改写都拒绝且不阻塞；atime 更新不能误判内容改写；原子 rename 请求可读 | 待编写/执行 |
| 绑定与观测 | 绕过共享客户端直写请求，无法自报租约/IP/URL；活动 deployment、project/prefix、UUID/Running、隔离档、IP/MAC 分配任一不匹配不产生新许可 | 待编写/执行 |
| 预占与撤销 | 并发相同请求幂等、同实例端口改名拒绝、名称/身份变化先清理；带会话序号的旧回调不能删除新预占；停止/删除 guest 的撤销无需重新 Running | 待编写/执行 |
| 实验命令 | --mediation 支持 input/capture 与 previous/withdraw；只允许 example.test；固定/命名不需密钥文件，random 只从管理员文件读取；输出/错误无密钥；旧 mediated record 身份不一致拒绝 | 待编写/执行 |
| 真实隔离与生命周期 | 完成生产适配器后验证目录/mount 权限、只读身份、全局路由占用、IP 保留、探测/原子发布、停止/事件丢失/重启对账；单次计划生成与本地单元不替代 | 待实现后执行 |

进入测试阶段先执行相关编译、Go 回归及文档/Contract 门禁，再在已授权的独立服务器环境执行。
本表是待验证清单，没有执行结果，不能增加 M11 完成度。

## Core 活动授权与请求登记续作待测项（本轮不执行）

| 新路径 | 后续必须验证的行为 | 状态 |
| --- | --- | --- |
| 共享锁与取消 | 与 Core apply/stop/restart/rotation 锁互斥；锁等待取消/超时不创建 workspace、不触发恢复/Hook；破损 lock 或元数据拒绝 | 待编写/执行 |
| 活动元数据 | active running/state active/激活时间/manifest ID、compute/ForwardAuth 绑定及镜像冻结全部一致才返回；ready、stopped、partial、缺失/超大/符号链接/其他用户可写元数据均拒绝 | 待编写/执行 |
| 目录登记 | 仅创建新 0700 根和租约目录、0400 登记；无密钥；既有目标拒绝；权限失败、写中失败仅回收自身空对象，不递归删除后来内容 | 待编写/执行 |
| 登记代次 | 同授权重新核对成功；deployment/激活时间/清单变化、工作区移动或不同工作区复制登记后拒绝；密钥恢复的域名仍保持原值，重新登记不改变 HMAC | 待编写/执行 |
| workspace 模式 | 拒绝手填 authorization/active_deployment/request_directory/lease_secret_file；绑定跨租约、登记篡改/别名/重复键、目录换位或链接均拒绝；只从登记的句柄读请求 | 待编写/执行 |
| 命名密钥读取 | 复用 Store 格式、owner/kind/provenance 校验，缺失或错误引用不铸新值；只返回所选租约，输出无其他 Secret；读前后授权变更拒绝 | 待编写/执行 |
| 采集中切换 | 在独立元数据夹具模拟活动切换/停止/不可读，无 previous 失败；有同租约 previous 只生成撤销，无新 firewall/route；不自行修改实际部署状态执行正例 | 待编写/执行 |
| 接入限制 | normal apply 的启动拦截仍在；登记命令无挂载/网络副作用；全局 running 不视为每个消费者健康，部分停止/crash/重启重放另由后续事件/对账验收 | 待编写/执行 |

当前只写代码和文档，没有创建实际登记目录、读取真实工作区 Secret Store 或执行上述新路径。

## 持久化执行与 renderer 续作待测项（本轮不执行）

2026-09-13 追加：逐步注入 HoldAddress/EnsureGuestRoute/EnsureHTTPPermit 与探测前观测失败，
确认统一进入撤销；在写入 `retiring` 后每个清理步骤强杀，原请求恢复有效也不能中断退役。
注入撤销意图保存失败、未知 schema、非规范 epoch，记录拒绝与恢复行为。全部待执行。

| 新路径 | 后续必须验证的行为 | 状态 |
| --- | --- | --- |
| 发布顺序 | 真实类型化 backend 精确执行地址保留→`/32`→tuple→身份探测→原子路由；每步读回后才写回执，探测或发布失败完整逆序清理 | 待实现适配器后执行 |
| 撤销顺序 | 路由缺失确认后删除许可，再清精确存量连接、删除 `/32`，最后释放地址；任一步失败保留回执与地址，重试幂等 | 待实现适配器后执行 |
| 崩溃恢复 | 在每个外部步骤之后、回执写入前后分别强杀；重启不得仅凭状态文件恢复访问，先与 Core epoch 和新鲜 observer 求交并清理未知结果 | 待编写/执行 |
| epoch 与实例变化 | deployment 切换、停止/暂停/删除、UUID/IP/MAC 变化、请求撤销及漏事件都在周期对账中撤销；旧 IP 未完成清理前不能分配给新 UUID | 待实现适配器后执行 |
| 全局归属 | 两租约/两进程争用相同 Host 或 reservation 时只有一个成功；不同内容的既有 Traefik 文件既不覆盖也不删除；单实例 leader 丢失后 fail closed | 待实现 leader 后执行 |
| 文件 renderer | 管理员目录、最终文件及临时文件的 symlink/权限/竞态、磁盘满、fsync/rename/link 失败；成功文件为现有 file-provider 可加载的 HTTPS Host 路由，middleware 冻结 | 待编写/执行 |
| 类型化宿主边界 | 请求中注入 IP、URL、argv、nft 文本、entrypoint、TCP/UDP 字段均无法到达宿主动作；宿主端再次按受信租约映射校验目标 | 待实现适配器后执行 |

本轮只新增状态机、文件状态仓库与受约束文件 renderer，未接入生产 daemon、宿主动作、observer、
probe、leader 或事件源；因此不能勾选 M11，也不能把接口级代码称为真实网络执行通过。

## 2026-09-13 持锁周期循环与工作区请求源（全部待测）

| 场景 | 后续验证内容 | 状态 |
| --- | --- | --- |
| 会话互斥 | 两进程使用同一状态根、同目录别名时只有一个持锁；锁覆盖外部动作和清理；另一进程立即得到 busy，不执行 observer/网络动作；取消持锁进程后才可再次获取 | 待编写/执行 |
| 锁身份 | 锁文件/目录改名替换、符号链接、硬链接、owner/权限变化、关闭后的 Journal 复用均拒绝；不通过删除锁文件解除锁；所有 ingress 发布者统一一个本地状态根 | 待编写/执行 |
| 启动/退出 | startup 在读取请求前清理旧记录；源读取失败时停止续期并清理全部；清理超时留盘，下轮恢复完成前没有新发布；取消后清理上下文仍可用、锁仍在 | 待编写/执行 |
| 稳定 token 与 tombstone | 多次轮询不重新发号；同请求退役后下一轮不能复活；新 token 必须重新核验请求和事实；NIC MAC/UUID/IP 变化先退役旧目标；4 MiB 上限不自动按 TTL 删除历史 | 待编写/执行 |
| 清理屏障后恢复 | 全部清理确认前不清空 token 缓存；确认后停止再启动或短暂故障恢复的请求经新的完整授权/观测得到新 token；旧 token 与迟到回调仍不能恢复或删除新发布 | 待编写/执行 |
| 清理故障跨租约 | 一条撤销失败仍尝试其他租约；Save 不确定后重新加载，磁盘排序变化不会遗漏其他记录；重启只有已确认清理的对象可释放 IP | 待编写/执行 |
| 当前请求源 | 每租约 256 项和全局 1024 个请求的边界；多文件争用同实例端口拒绝；临时非 JSON 文件不作为请求；发布前文件撤销/替换、epoch/auth/Host 变动都阻止新许可 | 待编写/执行 |
| 路由库存 | 外部/静态 Host 冲突、库存不可读或无法解释规则时失败；不能以空列表续期；归属不能仅按文件名推断；跨状态根和孤立路由由待补库存/恢复适配器验收 | 待适配器后执行 |
| 共享 renderer | 用正常模式与仅渲染模式比较同一 ANAS_TRAEFIK_ROUTE 字段产物；none/forward_auth、引号及换行拒绝、HTTPS/TLS 保留；仅渲染无 cert.yml 和 Traefik 启动，正常模式回归 | 待编写/执行 |
| renderer 文件边界 | 私有 staging、干净子进程环境、5 秒渲染限时、固定入口文件校验、输出 8 KiB 上限、no-overwrite 与 fsync 失败恢复；模板变化冲突保留待清理记录，不能称文件落盘为运行路由确认 | 待编写/执行 |
| Traefik 确认 | 缺少 RouteConfirmation 适配器时无文件变更；加载/撤销未确认、auth middleware 不存在或版本不符时不标记就绪，不释放地址；文件已不存在也仍需实际撤销确认 | 待适配器后执行 |

Controller、WorkspaceSource 和 renderer 均未实际调用；真实受限 FactReader、外部路由库存、
宿主动作/IP 保留、probe、窄密钥交付、孤立工件恢复和 Traefik 消费确认仍待接入。生产启动保护
继续生效，门禁、单元及服务器 E2E 暂缓，M6/M11 完成度不变。

## 2026-09-13 Incus GET 事实读取与实例代次（全部待测）

以下新增代码尚未编译、实例化或执行；仍先记录用例，恢复测试时使用已指定的独立宿主。

| 场景 | 后续验证内容 | 状态 |
| --- | --- | --- |
| HTTPS 与证书 | 精确服务端 pin、客户端身份不符、双方过期/尚未生效（含复用连接）、多 PEM/错误 key、TLS 1.2 拒绝；HTTP/含路径或凭据的 endpoint、重定向、代理环境不能绕过固定目标；失败不回显 endpoint/key/响应 | 待编写/执行 |
| GET 与响应边界 | 观察器只访问安装映射对应的固定 GET 路径；每 GET 8 秒/2 MiB、双采样 30 秒、取消/慢正文/超大 header/压缩响应；拒绝非 JSON、重复字段、超过 32 层嵌套、非 sync 或错误 envelope、缺失身份字段 | 待编写/执行 |
| 映射与版本 | 消费者/资源、Provider、project、interface、前缀或端口不符时无 API 调用；重复 project 映射拒绝；精确版本、server/client fingerprint、TLS 身份、非 cluster 条件；版本匹配不能替代其他字段核验 | 待编写/执行 |
| 服务端只读身份 | 在实际 daemon 上证明专用身份可读选定 project/bridge/实例/分配，同时拒绝任何写入、其他租约及非受管网络读取；普通 restricted TLS 证书反例必须揭示仍可写，不能用仅 GET 的请求日志算通过 | 待授权供给后执行 |
| 授权共存 | 若选择 scriptlet/OpenFGA，验证现有消费者受限证书与 Provider 行为、项目隔离和失败恢复；不得靠改全局授权绕过读取失败；当前代码未安装或修改这些策略 | 待设计/供给后执行 |
| 受管 NIC/分配 | project 网络限制被改、bridge owner/NAT/外部接口变化、额外 NIC/proxy/VLAN、运行态缺失、重复 MAC/IP、错误 netmask、网关/广播/网段外地址、分配归其他 NIC均拒绝；静态/DHCP 分配正例分别验收，未返回状态的 guest 不猜测 IP | 待编写/执行 |
| 双采样漂移 | 两轮之间替换实例/NIC/host link、改变 bridge 地址、daemon 重启或暂停 guest 均失败；无关流量计数及其他 guest 租约变化不改变选定身份；不同 API 之间仍非原子快照，宿主执行前独立重查不可省略 | 待编写/执行 |
| 快速重启与旧回执 | 保持 UUID/IP/MAC，改变 generation 或 last_used_at 后旧 token 先撤销，新实例代次只能得新 token；缺失/无效/未来启动时间拒绝，旧回执缺 incarnation 拒绝且保留外部工件待恢复；两档真实 guest 均需验证字段语义 | 待编写/执行 |

`WorkspaceSource.Facts` 现在有可注入的 Incus GET 实现，但专用只读身份供给、运行服务组装及
宿主动作仍未完成。生产 ingress 继续拦截，普通受限证书、实验管理 socket 或 fake 测试都不能
作为真实权限/网络验收证明；M6/M11 与 30/75 状态不变。

## 2026-09-13 Traefik 库存与加载确认（全部待测）

`TraefikReader` 及持锁 Journal 接线只完成编码，未实例化、编译或执行。下列验证恢复测试时再开展；
既有 API、凭据、规则和业务数据不得作为实验破坏对象。

| 场景 | 后续验证内容 | 状态 |
| --- | --- | --- |
| API 连接边界 | 已安装 HTTPS origin/证书 pin、BasicAuth、版本必须匹配；拒绝重定向、代理、过期证书和错误密码；失败不回显账号密码、endpoint、rawdata 或 middleware 中的 Secret；不新增 API/监听端口 | 待编写/执行 |
| 完整快照 | 每 GET 8 秒、rawdata 4 MiB、4096 HTTP routers/8192 services/8192 middlewares 上限；12 秒内两次间隔 250 ms 的完整匹配快照；重复字段、截断、空对象、API router/auth 缺失、版本/启动时间漂移与持续配置变动均不能变成空库存 | 待编写/执行 |
| Host 规则求值 | 当前 LLNG OR Host、NetBird/Nextcloud Host AND PathPrefix 正例；括号优先级、交并集、引号/转义、长度/深度边界；无 Host 的 catch-all、HostRegexp、否定、未知 matcher、v2 声明和 parentRefs 均拒绝 | 待编写/执行 |
| 实际路由范围 | HTTPS 的 warning/disabled 仍占 Host；明确其他 entrypoint 的 HTTP 路由不误占；未知有效 entrypoint 拒绝；HTTPS TCP router 抢占拒绝，普通 TCP/UDP 发布仍不可用 | 待编写/执行 |
| 回执与文件归属 | 只从当前持锁 Journal 取得候选，关闭/替换后的 Journal 拒绝且不重入 flock；回执、完整渲染文件和实际 router/service/auth 全部匹配才排除；伪 ANAS 名称、伪 owner 头、孤立文件、丢失回执都不可获信任 | 待编写/执行 |
| 文件读取权限 | 动态目录、入口脚本和工件属于 root/执行用户且无共享写权限；其他 owner、符号链接、读取中换文件/改权限、模板改变等拒绝；失败不删除外部文件或释放地址 | 待编写/执行 |
| 发布前检查 | auth pin 缺失、未知 middleware、定义改变、chain/非 ForwardAuth、Host/单独 service 槽位冲突时没有新的动态文件可见；API 首次读取不得自动建立 pin | 待编写/执行 |
| ForwardAuth pin | 从可信 Provider 安装产物（含 3.7.10 默认值）计算规范 JSON SHA-256；仅 key 顺序变化不改摘要，地址、header、TLS/认证字段变化必须失配；runtime status/error/usedBy 不进入定义摘要；验证可信输入与生命周期供给来源 | 待供给接入后执行 |
| 实际加载匹配 | 唯一 HTTPS/TLS router、Host、qualified service、精确私有 IPv4:port、单后端/default transport、middleware 和 UP 状态；额外后端、共享 service、service middleware、TLS/auth 降级及未知行为字段均拒绝 | 待编写/执行 |
| watcher 延迟/失败 | 文件已落盘但实际加载失败、超时或出现竞争 Host 时不记为就绪；等待期间文件改动拒绝；恢复/失败进入原有撤销顺序并保持回执 | 待编写/执行 |
| 撤销确认 | 磁盘删除后 API 仍有 router/service/直接 router 引用时不能释放 IP；连续快照确认移除后再删许可/存量连接/路由；文件重新出现、API 不可达或证书/账号轮换时保留清理状态，不能用旧快照确认 | 待编写/执行 |
| 状态与真实探测分离 | 后端未监听但 Traefik 默认仍报告 UP 的反例；独立 BackendProbe 必须失败，不能以 API 配置确认替代真实 guest HTTP/HTTPS/auth、长连接撤销或 IP 保留验收 | 待 Probe 接入后执行 |
| 双租约与重启 | 多租约 Host 冲突、完整库存读失败后的全量清理、Traefik/中介重启及文件先后恢复；认证/证书轮换需要新可信配置且不降低校验；实际 Docker/Incus 两档、公网双栈和防火墙共存按前文完整流程验收 | 待服务装配后执行 |

API 读取器、ForwardAuth pin 计算与文件确认均未运行。可信凭据/摘要交付、Incus 只读授权、宿主动作、
独立 Probe、运行服务装配与孤立工件恢复仍有待办，生产启动保护、M6/M11 和 30/75 状态保持不变。

## 2026-09-15 私有配置交付与 fixture Probe（全部待测）

新增交付/装配与探测代码未编译、测试或实际调用，没有生成真实凭据文件或连接服务器。恢复测试后，
先编写并执行边界用例，再在已指定的独立宿主 `ssh whl@finance.hlong.wang` 完成真实验收；不得用
修改生产 active 状态的方式绕开启动保护。配置与 fixture 输入仅由可信实验准备方供给。

| 场景 | 后续验证内容 | 状态 |
| --- | --- | --- |
| 最小交付范围 | 多租约只导出当前 random 所需 key；fixed/named 无 key，撤销/非活动租约无值；缺失、多余、重复、错误 key 格式、错误 Secret Store 元数据和 pin 拒绝；不挂载/回显完整 Store | 待编写/执行 |
| epoch 与供给来源 | 完整 workspace/deployment/activation/manifest/授权快照绑定；交付中切换 active 或修改原 key 时失败；含 fixed/named 的 source 也拒绝旧交付；Incus scope 不得暗中换成另一组授权；不能从运行 API 首次生成可信 pin | 待编写/执行 |
| 文件原子性/权限 | 私有 parent/0400 文件、owner、单硬链接、1 MiB、规范 JSON；symlink、FIFO、设备、共享访问、重复/未知/大小写别名 JSON、额外字段拒绝；已存在目标不覆盖；临时文件写入/同步/发布/移除失败及恢复只触及本次文件 | 待编写/执行 |
| 持续配置检查 | 读取器装配前后和每个 GET 前后发生内容、inode、目录或权限变化时失败；查询期间替换/删除交付不返回可用旧结果；新 epoch 要新交付；撤销先完成再停旧身份，不以删凭据代替撤销 | 待编写/执行 |
| 读取器实际装配 | `OpenWorkspaceReaders` 注入实际 Incus/Traefik、NamingKey、Configuration 与唯一 renderer Confirmation；无真实权限/endpoint 时不能通过空实现发布；安装 UID/只读挂载与 secret 不可见性需宿主验证 | 待安装接入后执行 |
| fixture 登记 | 每个完整目标预登记独有响应长度/SHA-256；1024 项、256 字节路径、16 B–64 KiB 响应边界；不接受生产域名、重复目标/响应、遍历/编码/查询路径；不能从第一次 HTTP 响应建立期待值 | 待编写/执行 |
| 进程与 namespace | 固定 visible PID/start tick/boot ID、真实 procfs/nsfs device/inode；停止/重启、PID 复用、旧 handle、另一 netns、假 proc 文件均拒绝；实际 socket cookie 与独立采集值一致；非 Linux、内核不支持 cookie 也拒绝，无 setns/管理 socket 回退 | 待可信启动接入后执行 |
| IPv4 目标与来源 | probe 预置于 Traefik netns，精确绑定入站 IPv4 与 guest tuple；错误源地址、仅 guest 回环监听、错误端口或错误 netns 在真实路由/防火墙上失败；禁止 DNS、环境代理、redirect、cookie、命名 key、认证/身份 header 出现在请求中 | 待真实 fixture 接入后执行 |
| HTTP 身份负例 | TCP 成功、空/通用 200、错误或其他目标的响应、截断/超长、压缩、3xx、4xx/5xx、慢响应均失败；已构建 UP 但没有监听仍失败；前后变更请求、epoch、UUID/incarnation、MAC/IP 必须失败 | 待编写/执行 |
| 期限与清理 | 连接/响应头各 3 秒、HTTP 5 秒、整体 25 秒和外层取消；不复用连接、不续期 nft 许可；Probe 失败触发持锁逆序撤销并保留失败回执；许可先到期不能成为已发布正例 | 待 HostActions 接入后执行 |
| 实际链路证据 | 探测使用 LocalAddr 不证明 Traefik 的实际选源；必须另外经真实 HTTPS/冻结认证验证正确入站 veth、guest /32 路由、两档、公网双栈、地址保留/IP 复用及长连接撤销；复制 fixture 响应不能绕过独立映射检查 | 待独立宿主 E2E |

以上没有通过记录。凭据自动供给、服务端只读身份、可信启动/cookie、fixture 准备登记、宿主动作、
生产应用 Probe 和孤立工件恢复尚待接入；生产保护、developing、M6/M11 和 30/75 保持不变。

## 2026-09-15 续作：启动身份采集与 fixture 登记（全部待测）

新增 `--capture-probe`、`--prepare-fixtures`、内核采集和持久登记 Probe 只完成编码，没有运行。
以下是恢复测试后要编写和执行的内容；原先完整网络/权限验收要求继续有效。

| 场景 | 后续验证内容 | 状态 |
| --- | --- | --- |
| CLI 分支 | 两个新模式彼此及与旧模式互斥；必需输入/新输出目录/绝对安装路径；未知、重复、大小写别名和超大 JSON；正例使用独立元数据，不修改实际 active 绕过保护 | 待编写/执行 |
| Docker 映射 | 全长容器/网络 ID、Running、无 pause/restart、单一指定 network endpoint；bridge/local、容器与网络分配的 endpoint/MAC/IP/prefix 一致；两次读取间替换容器、PID、endpoint 或源地址拒绝 | 待独立 daemon 执行 |
| 内核采集 | 已处于选定 netns 且能看到 Docker PID；self/thread-self/目标句柄一致；锁 OS 线程到 socket 创建及 cookie 读取；真实 procfs/nsfs、带空格/括号的 comm、启动 tick/boot ID、PID 复用、namespace 漂移和不支持 cookie 拒绝；socket 无 bind/connect/listen，无 setns 回退 | 待 Linux 执行 |
| 采集权限/输出 | 默认系统 socket 拒绝，独立实验 socket 的 GET 不等于服务端只读授权；不把管理 socket 挂入中介；输出只含来源 ID、时间和 identity，无输入路径或凭据，旧采集不能跳过实际探测的活性检查 | 待真实权限执行 |
| fixture 生成 | 从 CSPRNG 和稳定目标预生成独有响应，长度/SHA 与实际 bytes 一致；不从 guest 响应学习摘要；固定安全路径，不允许生产域名、双斜杠/遍历路径；不同目标无共享摘要 | 待编写/执行 |
| 准备会话 | 真实 WorkspaceReaders 与同一 Journal；有未撤销 publication 时拒绝，缺失请求/变更 epoch 拒绝；不调用 HostActions/Probe/动态写入，不用空库存或 fake identity 代替；90 秒命令、30 秒登记超时和取消 | 待装配后执行 |
| 登记边界 | 0400/private owner/单硬链接/1 MiB/1024 项、规范 JSON；重复 Host/实例端口/响应、非空 reservation、外租约/端口/auth、未知字段和截断拒绝；前后请求/Core/实例变化时不成功交付 | 待编写/执行 |
| 部分失败 | 响应文件/计划准备成功但登记写入/同步/当前验证失败，报错且不进入发布；保留私有准备文件可检查，不覆盖既有输出；不删除其他内容，清理失败明确恢复；最后 Journal.Check 失败不能当就绪 | 待故障注入 |
| 重启与重放 | 中介重启产生新 token，全部稳定身份匹配才能使用旧 fixture；退役 token 在执行器仍拒绝；guest 重启、UUID/incarnation、MAC/IP、端口、workload/label、Host/auth、epoch 任一变化要求重新准备 | 待 Controller 接入执行 |
| 登记文件变化 | Probe 前后改内容、inode、目录、权限、删除文件、旧 snapshot/路径替换均失败；不能拿登记文件作为当前授权或持久网络回执；静态构造器仍只接受原完整 target | 待编写/执行 |
| guest 与真实链路 | 仅把对应 response 装入对应 guest，正确路径/端口返回原字节；错 guest、复制响应、错误路径、服务未安装/回环监听时失败，并验证 HostActions 地址保留；独立测试真实 Traefik HTTPS/auth、选源、IP 复用与连接撤销 | 待服务器 E2E |

没有测试通过记录。特权启动/网络动作仍依赖统一动作 ABI 与宿主通道，代码不增加第三个提权入口。
生产 ingress、M6/M11、30/75 和 developing 状态保持不变。

## 2026-09-15 续作：外部工件盘点与有回执恢复（全部待测）

本节对应 R-053/R-054/R-063/R-087/R-088/R-095 的恢复及清理边界细化，不是通过记录。
`Recover`、文件/API 盘点、地址释放拦截和临时文件清理仅编码；没有运行命令、编译或测试。

| 场景 | 后续验证内容 | 状态 |
| --- | --- | --- |
| 完整作用域 | 空 journal 配合非空目录/加载对象/宿主许可均不得开通；缺失 API/局部列表/超时不作为空库存；损坏状态不覆盖；必需 HostActions/Renderer 盘点接口不能用空实现接入 | 待编写/执行 |
| 恢复与持锁 | startup/失败/取消使用同一 Journal，不重新 flock；撤销后盘点成功才调用 AfterRetirement；取消独立 cleanup deadline；未知工件持续阻止恢复，不重置缓存为可发布 | 待 Controller 接入执行 |
| 开通与地址释放 | 对账开通/续租前后发现孤立工件即撤销已知目标；地址释放前发现未知文件/许可/连接时保留 hold 和 retiring；先关闭已知路由/许可/连接，其他租约仍尝试清理；盘点不依赖当前 Core 或 Running guest | 待 HostActions 接入执行 |
| 专属目录 | 正确实际 file provider 挂载/观察专属目录；未知 YAML、auth 等混放文件、隐藏项、子目录、symlink/FIFO/设备、超大文件、超 4096 项全部拒绝；NOFOLLOW/NONBLOCK 避免打开时被替换为 FIFO 后阻塞 | 待文件与安装验收 |
| 文件一致性 | API 前后替换目录/文件、权限、inode、内容、项集、模板输入时拒绝；相同 owner header/名称但不同内容无效；30 秒整体期限及渲染/API 子期限；多路由规模与保守全局失败的可用性代价 | 待故障注入 |
| API 孤立对象 | 文件存在但 loaded router/service/auth 不完整或漂移拒绝；只有加载对象而无文件也拒绝；其他 provider、TCP/UDP 或新 section 的保留前缀、未知槽位、weighted/mirror/跨服务引用均不能自动归属 | 待真实 Traefik 执行 |
| 认证使用关系 | 多个合法路由共享已 pin 的直接 ForwardAuth 时仅忽略其对应 usedBy；伪造 middleware、错误 usedBy、未知前缀引用、认证配置内前缀字符串不得借此跳过；不输出 API body/credentials | 待编写/执行 |
| 临时文件恢复 | 在写入、sync、hard link、移除临时文件各阶段中断；只有完整字节和规范 nonce/名称匹配未完成回执才删除；部分写入/外来内容/非法名称/模板升级保留；删除前替换、删除或 sync 失败保留回执与 hold | 待进程故障注入 |
| tombstone 与 IP 复用 | 已退役 token 不进入盘点候选/不复活，不能据旧目标回收新实例地址或连接；遗留工件只靠 retired 记录仍拦截；独立宿主 receipt/实际映射漂移拒绝清理，完整 IP 重分配须实测 | 待服务器 E2E |
| 撤销消费确认 | 文件删除后 API router/service、直接与跨服务引用、middleware usedBy 仍残留时继续等待或失败，不释放地址；实际 reload、共享认证及双稳定快照；没有 API UP/文件成功即通过的捷径 | 待真实 Traefik 执行 |
| 宿主盘点适配 | 从独立宿主证据读取完整 hold、guest /32、HTTP permit、已存在连接、namespace/interface 身份和拒绝基线；过期许可与失配拓扑仍纳入；文件/API/内核跨源竞态、长连接撤销与两档 guest | 待宿主通道与真实 E2E |
| 管理员恢复边界 | 缺失/损坏状态及未知归属保留现场，不删锁、不按 prefix 清理、不从 owner 重建许可；独立备份/host receipts/对应版本模板的证据不一致时保持关闭；受限恢复动作尚未提供 | 待设计与接入 |

测试暂停要求不变；真实宿主适配器和受限管理员恢复仍待实施。不能将接口定义、文件/API 代码或
此前模拟 namespace 结果当成本轮网络清空证明，M6/M11、30/75 与 developing 保持不变。

## 2026-09-16 续作：宿主通道的共用动作 ABI 前置（全部待测）

对应独立需求 ACTABI-R-001/R-002/R-003/R-005 的协议及终态前置，完整验收仍归
[动作 ABI 计划](../../../dev-docs/plans/action-abi.md)。本节先登记要编写/执行的场景，不代表协议
已接入 job、Module Command、CLI/HTTP 或宿主通道；Incus 本身不新增宿主动作需求或特权入口。

| 场景 | 后续验证内容 | 状态 |
| --- | --- | --- |
| 有界请求 | 64 KiB（含 LF）、32 KiB 参数对象、深度 16 边界；单请求后 EOF；空行/CRLF/缺 LF/尾随 JSON/重复与转义重复/大小写别名/未知字段/显式 null/无效 UTF-8 拒绝；错误不回显参数 | 待编写/执行 |
| 流式分帧 | 不同 chunk 大小、短读、EOF 与非 EOF 读错、超长无换行、半截 JSON、终态后坏字节；错误后不能接收新的合法行；实际读取超时由进程/通道适配器提供，不能只靠字节上限 | 待编写及适配器接入 |
| 类型化边界 | ABI/ID/action 语法、job/invocation 交叉拒绝；Request 没有 handler/argv/env/secret 等选择字段；parameters 的 action 专属 schema、权限和敏感值过滤必须在独立注册表完成，codec 合法不代表授权 | 待注册表接入后执行 |
| 事件形状 | 唯一 payload 与 type 一致；success 必须显式 changed/value；cancelled 不带成功值，error 只 failed/unknown；warning/progress 字段互斥与控制字符；超限结果及未知 outcome 拒绝 | 待编写/执行 |
| 序号所有权 | executor 禁止 seq（含显式 0）和 truncated；只有 job writer 分配正整数序号；从任意 from_seq 重放，重复/回退/跨 job/invocation/缺口/溢出拒绝；覆盖缺口的 trusted marker 正例及不覆盖/伪造反例 | 待编写及日志接入 |
| 精确进度 | uint64 及大于 2^53 的计数往返不失真；无 current 时不得有 unit/total；total/total_estimated 互斥，current 超估算保持原值；跨客户端整数精度另验，不能依赖 JS 浮点 | 待编写及前端接入 |
| 终态与退出 | 恰好一个终态；成功/失败与零/正退出码一致；没有退出或真实 EOF、负/异常退出、缺终态、坏输出、重复终态均 unknown；ExecutionReader 不能采信调用方伪报 EOF | 待编写/执行 |
| 取消与强杀 | 仅 cancel 请求不能得到 cancelled；须执行器明确确认且零退出；取消宽限期强杀即使已输出 success 也 unknown；解析器不发信号，真实协作取消/进程组回收仍需适配器与独立宿主故障注入 | 待执行适配器/E2E |
| 订阅与执行寿命 | 重放 EOF/浏览器断连不结束 job；非终态尾部合法，终态后的数据拒绝；真实 invoke/attach/get/list 单一存储、执行租约和权限复用，不能通过新包另造按入口划分的 store | 待共用 job 接入/E2E |
| 日志与审计 | 帧不是脱敏器；public projection 先于 append，注入 Secret 与 raw stderr 不落日志；总限额、显式 truncated 的崩溃恢复、terminal 保留及既有 journal 压缩一致性 | 待持久化接入/E2E |
| 迁移兼容边界 | 当前 Module Command/CLI/HTTP 未切换；迁移时同步 manifest ABI、冻结 descriptor、executor、共享应用服务、job 状态与参考文档，不以 codec 替换作为完整迁移；不新增旧协议兼容桥或 root RPC | 待迁移阶段 |

未编译或执行上述新路径，没有通过记录。ABI 0/9、宿主通道 0/13 与 Incus 30/75 不因原语代码增加
而变化；继续等待测试恢复后运行相关门禁及真实独立宿主验收。

## 2026-09-17 续作：共用动作 journal 与执行 recorder（全部待测）

对应 ACTABI-R-001/R-002/R-003/R-005/R-008 的持久化与监督适配前置。这里只登记测试计划；本轮
不编写/运行测试，不运行编译、门禁或服务器操作。新路径仍无 dispatcher/真实进程/CLI/HTTP 接线，
不能用内部存储方法的测试冒充动作通道或 Incus HTTP E2E。非动作 job 保持现有协议。

| 场景 | 后续验证内容 | 状态 |
| --- | --- | --- |
| 单一存储 | 旧 job 与动作 job 在同一 jobs.jsonl；互相交错全局 Event.ID，两个动作各自 seq 从 1 连续递增；Get/List 来源一致；真正 CLI/HTTP 可见性和查看者权限仍待入口迁移后验收 | 待编写/接线/E2E |
| binding 与写入入口 | ABI/name/invocation 创建后不可改；普通 Create/Start/ClaimNext/Append/UpdateRunning/Transition/CancelQueued 不得领取或修改动作路径；跨 job/invocation、executor seq/truncated 及伪造 legacy/action 混合记录拒绝 | 待编写/执行 |
| 执行租约与审计 | 不同目录/已关闭/nil lease 拒绝启动与运行中写入；创建/启动/终态/排队取消/恢复缺 observer 或审计失败均不提交；租约释放与旧进程补写、新 owner 恢复竞态；worker 与客户端断连分离 | 待编写及适配器 E2E |
| 原子终态 | 单行 action_event 同时含 terminal 和新 job 状态；在 write、fsync、目录 sync、rename/压缩各边界故障注入，重开只能看到完整前态或完整后态；半行恢复无单独 succeeded 事件；不确定 append 结果禁止重跑执行 | 待编写/执行 |
| 显式截断 | 容量 2/default1024/不足2；64 KiB 帧、marker 计入容量；容量与追加时过期共同触发，原子替换整个前缀为累计 marker+新事件；多轮截断覆盖先前 marker，无单独 marker 或 silent prune | 待编写/执行 |
| 任意游标重放 | from_seq=0/旧前缀中/through_seq/marker seq/最后事件/未来值；恰好已读到 through_seq 的订阅者正常消费下一条 marker；limit 分页恰停 marker，之后继续；两个 job/global SSE ID 不得混用；末页不推断取消 | 待编写/执行 |
| 终态尾部保留 | 在终态之后跨 EventRetention、普通读写/定时 prune、重开和压缩，仍只保留原唯一 terminal 与有界尾部，不追加 terminal 后 marker；实际只在追加检查年龄，不能把它测成严格 TTL；全 store/终态 job 清理仍待实现 | 待编写及后续保留策略 |
| snapshot 与重启 | job binding/LastSeq/outcome/结果/时间、事件序号与全局 cursor、digest 和 generation 一致；拒绝回退、terminal 变更、marker 后无事件；多 job 恢复正确分配全局 ID；running 动作恢复 unknown，不重执行 | 待编写/执行 |
| 旧日志与格式边界 | 新读者恢复/压缩原 v1 非动作日志不变；新 action 字段/record 的旧 reader 拒绝；无降级转换；混合 job 审计、compensation gate、旧幂等路径回归，不能宣称动作 key 已有一小时过期 | 待编写/执行 |
| 公有投影 | projector 强制存在，执行器 raw 消息/结果含随机 secret 时由动作 allowlist 过滤后才 append；回调错误不泄漏原文；回调不能修改 binding/type/outcome/changed/精确计数/单位，不能通过改入参绕过比较；结果持久化前再次校验/脱敏 | 待编写及动作注册表接入 |
| 数值与防别名修改 | >2^53 及 uint64 边界跨 decoder、projection、journal、snapshot、ReplayAction、Get 不损精度；current 超估算不钳制，不塞百分比；修改传入对象/返回 Job/Event/observer 快照不影响已持久化状态 | 待编写/执行 |
| recorder 终态 | success/failed/cancelled 先暂存，真实 EOF+退出证据后才提交；终态后多余帧/坏字节、缺 LF、投影/写入失败、强杀、未确认退出均只写 unknown；伪报 StreamEOF 不生效；两次 Finish/终态后追加拒绝 | 待编写及监督进程 E2E |
| 排队取消与补偿 | CancelQueuedActionObserved 与 StartActionObserved 并发只允许一方获胜；running 不能通过 queued cancel 结束；失败/未知 mutating 动作阻断新 mutation，补偿确认不改原 outcome/terminal；running 的协作取消仍需通知、宽限期与收割接线 | 待编写及适配器 E2E |

恢复测试后先完成相关 package/文档门禁，再在已授权独立 Linux 宿主连接真实受监督执行器与
Docker/Incus/Traefik 场景；实际 guest、默认拒绝基线、地址复用及存量连接验收仍独立记录。
本轮没有通过证据，ACTABI 0/9、HOSTACT 0/13、Incus 30/75 及生产 ingress 阻断均不变化。

## 2026-09-18：消费者请求文件完整性（测试源已编写，未执行）

实现：`internal/computeingress/request_writer*.go`、共享严格读取器
`internal/computeingress/request_linux.go`，以及显式共享客户端发布请求接线。
独立文件回归源为 `internal/computeingress/request_writer_integrity_test.go`，仅面向 Linux
amd64/arm64 的受支持本地文件系统；不使用 Incus、Docker、网络特权或服务器凭据。

| 待验证项 | 应观察到的结果 | 当前证据 |
| --- | --- | --- |
| 原子提交与相同请求重试 | 中介读取器能读取完整私有 JSON；同请求不改 inode，其他 workload 不覆盖占用槽位 | 回归源已编写，未执行 |
| 精确撤回、旧回执与槽位复用 | 完整请求和 inode 同时匹配才删除；相同 JSON 的新 inode 也不能被旧回执删除 | 回归源已编写，未执行 |
| 消费者重启后的 `Resume` | 只接管与持久化预期相同的已有请求；不同任务拒绝，文件缺失不重新发布 | 回归源已编写，未执行 |
| 原位改写与特殊文件 | 同 inode 内容变化、符号链接、硬链接及 FIFO 均不误撤回/覆盖；错误不包含外部路径 | 回归源已编写，未执行 |
| 目录身份与配置准入 | 零值/缺失目录/共享权限/目录 symlink/已打开目录被替换均拒绝；不创建安装目录 | 回归源已编写，未执行 |
| 容量、协作锁和并发 | 忽略文件也计入 256 项上限；等锁响应 context；独立 writer 的相同请求收敛到一个文件 | 回归源已编写，未执行 |
| 关闭本地句柄与任务结束 | `Close` 不撤回持久化意图；精确撤回不留下每个任务的 revoke 文件 | 回归源已编写，未执行 |
| rename/unlink 同步失败与崩溃遗留 | 结果不确定不冒充成功；重试核验已有文件，未知临时工件不批量删除；本地句柄容量保持有界 | 需补故障注入/进程崩溃执行证据 |
| 端到端装配 | UID/挂载、运行实例 workload、策略及 Host 冲突、真实中介观测、撤销顺序与地址释放均符合冻结授权 | 仍需独立宿主及应用生命周期接线 |

恢复测试后再在符合条件的 Linux 环境执行：

```sh
go test ./internal/computeingress -run TestRequestWriterIntegrity
```

该命令本轮**没有执行**。其他平台上没有匹配的测试文件不表示通过；文件层通过也不能替代实际
镜像构建、Core/消费者装配、Traefik 加载、认证、网络许可/conntrack 或地址保留验收。
`PublishPort` 返回的文件回执及预测 URL 不能直接更新为网络 ready，`UnpublishPort` 成功也不等于
旧连接或宿主工件已清理；生产 ingress 继续关闭。

## 2026-09-18：消费者发布 API 与最小投影（测试源已编写，未执行）

`internal/computeclient/http_publication_contract_test.go` 和
`http_publication_projection_test.go` 使用假 Incus runner 与假请求 writer，验证客户端边界，
不产生真实网络发布。`internal/computeingress/request_writer_receipt_regression_linux_test.go`
使用临时目录验证 Linux amd64/arm64 的实际文件操作，不启动 Docker/Incus/Traefik。

| 待验证项 | 必须观察到的结果 |
| --- | --- |
| 最小投影与完整授权 | 配置不包含完整 grant、Store 引用、middleware 或 entrypoint；命名密钥不进入默认 JSON/格式化输出；Policy 名称预测与授权推导一致，但无效完整授权仍被中介拒绝 |
| 精确实例与 workload | 忽略其他名称过滤结果，重复精确身份拒绝；非 Running、未受管、错误前缀、无效 workload 不向 writer 提交 |
| 端口、名称与请求 schema | 端口与 label 必须符合策略；固定 Host/相同 Host 的不同后端拒绝；只写既有小型 schema，不携带 IP/URL/auth/host port |
| 回执、重试与撤销 | 相同提交复用回执；文件被合作 writer 替换后旧回执不能删除新请求；停止/删除实例后仍可撤销，无额外 Inspect；已完成撤销的重试不影响后来发布 |
| 关闭、取消与结果不确定 | Close 不删除请求；排队等待遵守 context；未知提交或缺失底层回执不报成功；撤销失败保留回执供核验重试 |
| 实际文件边界 | 0600 文件兼容中介严格读取器；重开/跨 writer/并发收敛；符号链接、硬链接、FIFO、共享权限、目录替换、超过 256 项和零值对象均拒绝 |

恢复测试后执行以下命令，本轮没有运行：

```sh
go test ./internal/computeclient -run 'TestHTTP|TestInspectSelectsExactManagedInstanceForHTTP'
go test ./internal/computeingress -run TestRequestReceipt
```

假 runner 的通过不能代替 Incus 实际查询、运行实例身份和 TOCTOU 验收；客户端不是安全边界。
文件回执/预计 URL 不得更新为网络 ready，撤销意图也不代表路由、许可、连接或地址保留已清空。
自动配置投影、私有挂载、应用生命周期/恢复、宿主通道和真实中介仍待接线；生产 ingress 保持关闭。

## 2026-09-18：staging 预检与离线镜像核验（测试源已编写，未执行）

| 范围 | 待验证断言 | 实现位置 |
| --- | --- | --- |
| staging 静态边界 | 绝对 shared 覆盖值；匹配源码及复制树；缺失/新增/变化的共享文件、变化的 Dockerfile/构建源码/执行位、枚举符号链接、无匹配镜像均拒绝 | `cmd/check-shared-build/staging_test.go` |
| 冻结镜像身份 | 具名及裸摘要核验；目标架构/interface、版本键、recipe digest、fingerprint、片段长度/摘要、顺序/尾随字节变化均拒绝；已有摘要不被新字节覆盖 | `internal/computeimage/release_verify_test.go` |
| 读入与信任 | 连续无进展 Reader 有界失败；CLI 必须提供独立 fingerprint，不能用描述文件自证；本地特殊文件/末端符号链接、可见文件变更、摘要失配不输出成功 | `release_verify_test.go`、`cmd/compute-image-artifact/main_test.go` |
| 真正构建与部署 | 匹配 checkout 和实际 staging 的 Docker 构建；真实 distrobuilder 产物格式、Incus 导入与启动；恢复完全相同字节、再次 apply 不构建、回滚与 prune 保护 | 仍需独立构建/宿主脚本及环境，不由上述假字节或静态检查代替 |

恢复测试后运行 `go test ./cmd/check-shared-build ./internal/computeimage ./cmd/compute-image-artifact`。
本轮未运行此命令或新增 CLI，未创建真实镜像，也未导入、发布、操作服务器。`--staging-root` 仅检查
实际存在的相关构建目录，不代替完整 deployment manifest 审计；离线哈希匹配不证明镜像格式有效、
可启动或目录签名获信任。全部仍为待执行记录，不改变 M8b/M12/M13 的验收状态。

## 2026-09-18：本地镜像归档与历史保护（测试源已编写，未执行）

`internal/computeimage/artifact_test.go`、`artifact_archive_test.go` 和
`cmd/incus-image-artifacts/main_test.go` 使用合成字节，验证本地存储控制流，不是可启动镜像或
真实 distrobuilder/Incus 的替代证据。

| 范围 | 待验证断言 | 状态 |
| --- | --- | --- |
| 描述与摘要 | split 拼接顺序、unified 单文件、片段大小、空值、规范 JSON 和取消均按边界拒绝；不接受第二套记录格式 | 已编写，未运行 |
| 原字节恢复 | 删除已记录对象后 inspect 失败；相同输入重录只补回原摘要对象，不改 revision 记录 | 已编写，未运行 |
| 不覆盖 | 同 revision 不同配方或字节冲突；损坏对象与符号链接保留，不被重录覆盖或删除 | 已编写，未运行 |
| 归档控制 | 排他锁、重复 init、目录置换、根目录符号链接和预取消操作均拒绝；正常操作不隐式初始化 | 已编写，未运行 |
| 历史目录 | 必须显式首次发布或上一份可信目录；旧版本键丢失/变化、未知元数据、重复字段/别名或缺失历史拒绝 | 已编写，未运行 |
| CLI 输出 | record/inspect/catalog 只输出 JSON 元数据；冲突与无效参数不回显路径、配方或镜像内容 | 已编写，未运行 |
| 崩溃与容量 | 对象同步、link、目录同步、revision 提交各中断点的跨进程重开，以及容量/磁盘不足/ACL/特殊文件系统需独立故障注入验证 | 尚未实现完整故障注入 |
| 真实供给 | 可信构建 provenance、发布签名/分发、Provider 恢复导入、两档镜像启动、回滚和受限 prune | 仍待实现及真实环境验收 |

恢复测试后运行 `go test ./internal/computeimage ./cmd/incus-image-artifacts`，再实施真实发布/导入
和故障注入场景。本轮未执行该命令、归档 CLI、builder、导入、发布或服务器操作；受信目录仍为空，
M12/M13 不标为完成。

## 2026-09-18：执行失联阻断与 staging 说明（全部待执行）

新增 `internal/consolejobs/action_admission_test.go`，只编写测试源，未执行测试、编译或门禁。
这组存储回归不能代替真实 Linux 进程监督、宿主盘点或 Docker/Incus/Traefik E2E。

| 场景 | 后续验证内容 | 状态 |
| --- | --- | --- |
| 持久启动阻断 | containment-lost 与 daemon-restarted 的 unknown 动作阻止只读、写操作、跨 workspace、旧 Start/ClaimNext 与动作 Start；稳定有序 job ID 不受调用方修改影响 | 已编写，未运行 |
| 压缩、重开及补偿 | journal 压缩与重新打开后仍阻断；普通 compensation acknowledgement 不清除执行失联回执；允许读取、重放和取消排队任务 | 已编写，未运行 |
| 区分结果与清理 | 已确认清理而仅结果 unknown 的 execution_unconfirmed 不产生全存储阻断；原有写操作补偿规则仍成立；非动作 legacy restart 不误入动作专用规则 | 已编写基础选择测试，集成回归待补 |
| 真实进程与 writer | 关闭 stdout 的残留子进程、执行器不读取 stdin、取消忽略/显式确认、超过宽限期、进程组仍有任务、阻塞 public projector/store writer；未确认清理必须停止接纳、保留锁和执行租约 | 待 Linux 执行适配器/E2E |
| 重启所有权 | 旧 writer 未结束不得由新 owner 写 unknown 或启动新动作；断连不取消；停止整个服务、独立证明残留清空后才能设计受限恢复；不能通过重建 registry 或删除 journal 恢复 | 待服务接线及故障注入 |
| staging 与源码构建 | 双语示例的绝对源码根、匹配 revision、渲染 Compose/.env、缺失源码失败；分别完成 checkout/staging config 解析及 anas_incus_provision 实际构建 | 说明已补，构建未执行 |
| 存储默认值 | 自动供给目标遵守当前 Created btrfs/zfs 准入；无已证明配额能力时 compute 保持关闭，不退回 dir、不格式化或接管已有设备 | 设计已纠正，安装器和实机待实现 |

此段没有通过记录，不改变 ACTABI/HOSTACT/Incus 的验收数字，也不开放生产 ingress。

## 2026-09-18：动作级重试、合流与队列（测试源已编写，未执行）

对应 ACTABI-R-007/R-009，权限与跨入口部分同时关联 R-008。仅增加源码和待测记录，不改变
Incus M6/M11 或 30/75 完成数；真实 CLI/HTTP 迁移、宿主通道与服务器验收仍独立待办。

| 场景 | 必须核验的结果 | 状态 |
| --- | --- | --- |
| 动作 key 作用域 | 同 action/key 的跨 actor、CLI/HTTP 路径重试得到原 job；参数/workspace 改变冲突；不同 action 不串键 | 测试源已编写，未执行 |
| 合流与别名 | 并发同参数只产生一个 job；新 key 原子加入；无 key 不生成别名；64 键上限显式拒绝且旧键仍有效 | 测试源已编写，未执行 |
| 审计和权限 | 合流以实际加入者再授权/审计，失败不改 revision 或消费 key；既有 job 和冲突 ID 需要当前读取权限 | 测试源已编写，未执行 |
| 保留边界 | pending/running key 不过期；terminal+1 小时边界建立新请求；时钟回拨不复活旧 owner | 测试源已编写，未执行 |
| 键归属恢复 | 新 key 可加入较早创建的活动 job；压缩/重开保留正确归属链；拒绝重叠 owner 与篡改快照 | 测试源已编写，未执行 |
| 声明式并发 | reject 指向活动 job 但不阻止有效同 key 重试；queue 对只读同参数也排序阻塞；模式漂移在启动前拒绝 | 测试源已编写，未执行 |
| worker 等待分类 | queue 冲突进入等待分支；reject、不明确启动冲突和执行失联不得当作可自动重试的阻塞 | 测试源已编写，未执行 |
| 参数与返回值 | 对象键顺序不改变摘要，uint64 不舍入；会被通用脱敏改写的执行参数拒绝；返回值不能改写取消/策略状态 | 测试源已编写，未执行 |

源码位置：`internal/consolejobs/action_invocations_test.go`、
`internal/jobexecutor/module_action_idempotency_test.go`、`module_action_dispatcher_policy_test.go`、
`module_action_queue_policy_test.go` 与 `internal/deploymentaudit/action_join_test.go`。
这些测试不启动宿主服务、不代替真实双入口或 Linux 进程验证。当前仅运行格式检查和文档生成；
逻辑 key 到期不代表已有 job 物理删除或全 store 磁盘配额，本清单不得将这两项误报为通过。

## 2026-09-18：共用调用与订阅服务（测试源已编写，未执行）

`internal/jobexecutor/module_action_dispatcher_test.go` 使用隔离的临时 job store，不启动 Module
子进程、不操作服务器。它验证内部服务边界，不代表现有 CLI/HTTP 已迁移或 Incus E2E 已通过。
待恢复测试后执行相应 Go 用例，并单独执行真实跨入口、Linux 进程与宿主验收。

| 类别 | 待验证断言 |
| --- | --- |
| 调用与权限 | 请求结束后仍排队；等价重试返回同一 job；不同动作/参数不会被错误合并；执行前失权走未启动拒绝 |
| 读取与订阅 | 非创建者的授权视图、动作下线后的历史任务、断连与权限撤销不取消执行、真实截断标记和终态序号 |
| 取消 | 审计失败不通知、重复请求不重复通知、排队取消与 non-cancellable 策略 |
| 执行所有权 | 不自动重跑未恢复的 running job；Module 查询与 worker 不暴露、领取或预检拒绝宿主动作 |

目前仅核对代码与格式，以上用例未执行。worker 仍是唯一队列/取消实现，没有为订阅服务另开存储。

## 2026-09-18：排队调用的预检拒绝（测试源已编写，未执行）

测试源为 `internal/consolejobs/action_preflight_recovery_test.go`，覆盖存储及恢复边界，不启动
真实 Module executor，不验证宿主权限或网络效果。

| 场景 | 断言 | 状态 |
| --- | --- | --- |
| 原子拒绝与重放 | queued → failed 与唯一 action_not_started 事件同一次提交；StartedAt 为空，LastSeq=1，无业务补偿要求；压缩/重开后仍一致 | 已编写，未运行 |
| 审计与调用绑定 | 缺失/拒绝审计、invocation 不符均不改变 job revision、状态或事件数 | 已编写，未运行 |
| 启动竞争 | Start 与 Reject 并发恰有一个成功，另一方冲突；不出现既启动又声称预检拒绝的记录 | 已编写，未运行 |
| 执行器冒用 | running job 不可调用 queued 拒绝入口，也不可用保留错误码伪装从未运行以清除业务补偿要求 | 已编写，未运行 |
| 队列可继续 | 预检拒绝不阻断同 workspace 的独立后续 job；清理失联和启动提交不确定仍不适用此路径 | 已编写存储用例，完整 worker/进程验收仍待运行 |

没有运行上述测试或门禁；也不以它们替代独立宿主上的 Incus 两档、配额、入站和恢复验收。

## 2026-09-18：取消意图持久化（测试源已编写，未执行）

测试源为 `internal/consolejobs/action_cancellation_journal_test.go`。这些用例不启动执行器；
实际取消通知、宽限期、强杀与进程收割仍需独立的 Linux 进程测试。

| 场景 | 断言 | 状态 |
| --- | --- | --- |
| 先保存再通知 | 首次请求只保存操作者/时间与一次 revision，保持 running 和原事件 seq；缺失/拒绝审计、错误 invocation、空操作者不写入 | 存储用例已编写，未运行 |
| 重复与防别名修改 | 重复请求不改首次操作者/时间，不追加记录；observer 和返回 Job 的修改不影响已持久化状态 | 已编写，未运行 |
| 截断及恢复 | 容量 2 下多轮事件截断，再完成取消、压缩、重开；保留首次取消证据与唯一 cancelled 终态 | 已编写，未运行 |
| 取消证据 | running 在没有持久请求时不得提交 cancelled；有请求仍须实际执行器确认与 EOF/退出证据，强杀不等于取消 | 存储用例已编写，真实进程验证待执行 |

此处记录的是未执行的回归源，不是 ACTABI 或 Incus 的验收通过记录。

## 2026-09-18：Module 队列与持久取消适配（测试源已编写，未执行）

对应 `internal/jobexecutor/module_action_queue_test.go`：排队取消审计失败不改变 job；取消与启动
竞争；运行取消的审计/意图持久化先于通知；重复取消保留原始 actor/time；撤销权限后不能重复取消；
预检再授权失败落下未启动拒绝；未对账 running job 不被 worker 恢复；一次 worker 不能自动重启。

同文件还覆盖实际 EOF 缺失、成功后尾随坏数据、强杀、未经请求的 cancelled、已持久授权的协作
取消，以及公有投影拒绝。核对唯一终态与敏感错误不泄漏；这些是代码测试源，不是真实进程清理证据。
Linux 进程状态解析回归在 `module_action_group_linux_test.go`，覆盖 comm 中括号/换行及错误字段。

仍须补验收：真实 Linux worker 与原生执行器、取消/终态竞争、各类清理阻断、同 workspace FIFO、
并发外部 writer、控制台/CLI 可见性、主 daemon 装配及独立宿主 E2E。所有测试/门禁/服务器操作
继续暂停；未运行上述用例，不标记 ACTABI 或 Incus 需求完成。

## 2026-09-18：取消管道及原生执行回归夹具（测试源已编写，未执行）

| 场景 | 源文件与断言 | 状态 |
| --- | --- | --- |
| 严格协议与终态 | `internal/actionabi/protocol_regression_test.go`：重复/转义字段、类型/大小/深度/UTF-8、seq 冒用、精确整数、估算超限、真实 EOF、退出码与尾随坏数据、显式截断覆盖 | 已编写，未运行 |
| 专用取消管道 | `internal/actionabi/module_cancellation_test.go`：只有独立管道 EOF 才是请求；字节、I/O 错误与连续空读不得当作取消；开放管道不自行触发 | 已编写，未运行 |
| recorder 公有边界 | `internal/jobexecutor/action_recorder_regression_test.go`：成功先暂存、EOF/退出后才提交；投影不能改身份/计数/单位/changed；回调内存不影响已暂存结果；失败只落稳定 unknown | 已编写，未运行 |
| Linux 原生执行 | `internal/jobexecutor/module_action_native_regression_linux_test.go`：冻结 ELF 夹具验证工作目录/环境绑定、正常成功与失败、确认取消、忽略取消、成功后挂起、缺终态、尾随坏数据、stderr 超限 | 已编写，未运行 |
| proc 状态解析 | 同一 Linux 回归源：comm 包含空格、括号和换行；PID/状态/进程组错误必须拒绝，不能把解析失败当成没有残留进程 | 已编写，未运行 |

后续获准执行时，协议/recorder 测试可在开发环境运行；原生夹具必须在支持 pidfd 的无特权、
无 capabilities Linux 环境运行。平台、权限或测试二进制大小导致的 skip 不是通过，不满足实机
退出条件。这些夹具只操作测试进程及临时文件，不安装 Incus、不操作宿主网络或服务器。

```sh
# 以下是待执行入口，本轮未运行。
go test -count=1 ./internal/actionabi -run 'Test(Action|ModuleCancellation)'
go test -count=1 ./internal/jobexecutor -run 'Test(ActionRecorder|ModuleActionLinuxSupervisorFixture|ModuleActionProcessState)'
```

独立进程残留恢复、CLI/HTTP 断连重连、共享审计、特权通道及真实 guest/Traefik/网络效果仍需
各自的验收证据；本节不增加需求完成数，也不解除生产 ingress 的阻断。
