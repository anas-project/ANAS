# Incus HTTP 网络原型

状态：实验观测采集与计划生成器，完整 Incus 宿主验收未完成；不接入生产 ingress。

`cmd/incus-network-prototype` 从**管理员采集**的 Docker/Incus 观测生成以下基础文件，不执行宿主修改命令：

- `firewall.nft`：专用 bridge 的 Traefik veth 来源限制、guest IPv4/TCP 端口 tuple、30 秒有效期。回包也必须命中有效 tuple，撤销后不会被该表中的 ESTABLISHED 放行。
- `traefik.env`：复用 `ANAS_TRAEFIK_ROUTE__*`；仅测试 `.example.test` 域名与 HTTP entrypoint。
- `operations.json`：在 Traefik 网络 namespace 内执行的 guest `/32` 路由与指定源地址，以及宿主上的 tuple 撤销和 conntrack 清除 argv。

```bash
go run ./cmd/incus-network-prototype \
  --input test-env/fixtures/incus-network-prototype/observation.json \
  --out /tmp/anas-incus-http-lab
```

输出目录必须不存在。随仓库夹具仅用于离线回归；其中 IP、MAC、UUID、bridge、veth 不是可部署事实。

具体待测场景、通过标准和证据要求见 [服务器 E2E 清单](e2e-plan.md)。先完成代码与本地门禁，再执行服务器验证。

## 只读采集与生命周期计划（2026-09-12，代码未验证）

本轮按操作者要求只编写代码和文档；未运行编译、单元、文档门禁或服务器 E2E。2026-09-11 的
namespace 实验仅覆盖此前生成的基础 nft，不覆盖新采集器或生命周期计划。

保留 `--input observation.json` 离线入口。新增 `--capture capture.json` 在 Linux 实验宿主采集：

```bash
incus-network-prototype --capture /var/tmp/anas-lab/capture.json --out /var/tmp/anas-lab/first
```

配置形状见 [capture.example.json](capture.example.json)。两个 64 位 Docker ID 与 socket 路径均为
占位值，需替换为本轮专用 daemon 的容器/网络完整 ID 与 socket。Collector 必须运行在两个实验
bridge 所在的网络 namespace，能够读取 Docker 容器 PID 的 `/proc` 并通过 `nsenter` 只读查询
其网络 namespace。所需程序为 `ip` 和 `nsenter`，网络查询有整体 30 秒上限与响应大小限制。

采集器仅经显式 Unix socket 发送 GET，不使用默认连接配置，不带 TLS 管理凭据，也不保存完整
Docker inspect（其中可能含应用环境变量）。拒绝已知系统默认 socket 路径、Incus cluster、
Docker 默认 bridge、非 Running/暂停中的来源及不完整 ID。**路径拒绝不能证明 daemon 独立**，
操作者仍须按 E2E 前置条件准备专用数据根/namespace；这也不是生产只读身份或宿主 helper。

交叉核对的字段包括：

- project 的精确网络授权与 bridge 的 consumer/sandbox 归属；bridge 名复用 `computeclient.NetworkName`；
- guest UUID、隔离档、唯一受管 NIC、配置 MAC、运行态 MAC/IP 与唯一匹配的 Incus 地址租约；
- guest 的 host interface 实际挂在该 bridge，Docker endpoint 同时出现在容器和网络记录里；
- Traefik namespace 内地址、MAC、ifindex/link_index 与宿主 veth 双向对得上，且 veth 属于入站 bridge；
- 前后两轮选择结果、guest host interface/实例 generation、Docker endpoint、拓扑与源 namespace 一致，才输出归一观测。

采集只证明这些读取时刻的一致性，不预留 IP、不扫描全部 LAN/VPN 路由、不防止采集结束后的实例
变更；变更执行前仍须重新观测并持有地址。不能拿本地特权 socket 替代未来中介的受限只读身份。

新产物：

| 文件 | 内容与边界 |
| --- | --- |
| `observation.json` | 归一观测，离线输入模式也会输出 |
| `capture-evidence.json` | 仅采集模式输出版本、时间、Docker ID 与 namespace；`reobserve_after` 是重采样提示，不是许可或自动到期执行器 |
| `publication.json` | 带版本的拟发布记录，不是“已经应用”的回执 |
| `lifecycle.json` | `publish` / `replace` / `withdraw`、前置条件、有序步骤、失败撤销步骤及独立 teardown；条件由操作者核验，工具不执行 |
| `firewall-replace.nft` | 仅同拓扑替换时产生；删除旧的两张受管表并创建新表需在单次 nft 事务内加载，不能逐条执行 |

输出目录必须不存在，目录 0700、文件 0600；写入使用目录句柄和独占创建，失败只回收本次写入。
输入限 64 KiB，拒绝最终文件符号链接/设备、未知或重复 JSON 字段、非小写 ASCII 字段名、尾随内容与过深嵌套。
这些是管理员实验文件的读取规则，不是生产消费者请求目录实现。

在操作者确认第一份计划确实已经应用后，才以其 `publication.json` 生成下一份计划：

```bash
incus-network-prototype --capture /var/tmp/anas-lab/capture.json \
  --previous /var/tmp/anas-lab/first/publication.json --out /var/tmp/anas-lab/next
incus-network-prototype --withdraw --previous /var/tmp/anas-lab/first/publication.json \
  --out /var/tmp/anas-lab/withdraw
```

`--previous` 不是状态库自动恢复：未执行的计划不能当已应用记录传入；旧记录必须始终对应当前
实验那唯一一条 `INCUS_LAB` 路由。输入损坏时先停止当前尝试，再用有效旧记录单独 `--withdraw`。

发布步骤是域名预占、槽位检查、精确路由、限时过滤、HTTP 后端确认、现有 Traefik renderer 发布。
撤销步骤是移除 HTTP 路由、删除 tuple、清理并确认 conntrack、删除精确 /32 路由、确认地址可退役。
**撤销后保留两张空许可的拒绝表**，不能删掉 drop 后误让默认路由恢复后端访问。整个实验来源和
后端停止/断开后，才按 `teardown` 删除有归属证明的表。

同拓扑更新也先撤销旧发布，然后以一个 nft 事务替换过滤规则。Docker 容器/端点身份、bridge、子网、源 IP、veth 或源接口
变化时，或 consumer/resource/project/interface 作用域变化时，仅产生 `withdraw`，需结束旧实验后开新实验。guest 停止、身份/分配无效或采集失败时，若提供
有效 `--previous`，也只产生撤销计划，不输出新许可或 Traefik 配置；无旧记录时直接报错。
成功写出计划的退出码 0 不表示已发布或已撤销，必须读取 `lifecycle.json.action`。

## 专用 Linux 宿主实验步骤

1. 使用可销毁的 Docker/Incus 宿主，记录 daemon/kernel/Docker/nft 版本、镜像精确 fingerprint、架构、interface 和测试 revision。不要在生产网络执行本原型。备份 `nft list ruleset`、Docker 网络检查结果和路由，确认 `anas_incus_lab`、`anas_incus_lab_l2` 表不存在。
2. 准备一个真实租约 guest；分别测试非特权 container 与 VM。服务监听 guest NIC 或 `0.0.0.0:7000`。保留该 IP 的分配，整个授权有效期内禁止回收给其他实例。IPv6 后端不开放。
3. Traefik 单独接入 Docker 入站 bridge，不加入 guest 网络。确认两个网段与全部 LAN/VPN/Docker/Incus 网段不重叠。根据 `docker inspect`、宿主 veth 对端信息、`incus query` 的实例 UUID/状态/NIC/MAC 和网络租约采集观测；不能只证明 IP 在子网内。生成器只检查观测内部一致性，并不能证明采集者可信。
4. 生成文件后先运行 `nft --check --file <输出目录>/firewall.nft`。检查现有规则的优先级；本表的 accept **不能覆盖**后续 Docker/Incus base chain 的 drop。不自动插入全局 ACCEPT、不关闭 Docker/Incus 防火墙；若窄规则仍被后续拒绝，记录阻塞并继续验证规则归属设计。
5. 在实验宿主加载审核后的两张表；在 Traefik namespace 执行 `AddRoute`，核验 `ip route get <guest-ip>` 的 interface 与源地址。先验证 HTTP 后端可达，再在隔离的实验 Traefik 中通过现有 `anas-entrypoint.sh` 渲染 `traefik.env`；不要把这些字段交给消费者写入生产动态目录。
6. 许可只有 30 秒，超时后重新采集身份与状态再重建实验；不由旧请求自动续期。测试正常 HTTP、未获批端口、其他 Docker/LAN 来源、同 bridge 冒用源 IP/MAC、guest 主动反向流量及 IPv6 绕行。保留返回码、抓包和计数器。
7. 撤销先移除实验 Traefik 路由，再执行 `Revoke` 和 `Conntrack`。用持续传输确认旧连接停止；只有确认网络许可已删除且连接清理完成后，才允许把 IP 分配给第二个实例并验证旧域名不可访问。可用 `--previous`/`--withdraw` 生成操作顺序，但没有执行器或自动对账；仍须另行实现并验收。
8. 撤销后保留默认拒绝表，先停止/断开实验来源和 guest，再清理本轮的 `anas_incus_lab` 与 `anas_incus_lab_l2` 两张表、实验路由和网络；不得 `flush ruleset` 或删除外部资源。检查路由和表均已撤销。部分失败时停止 Traefik 探针，按相同顺序清理，不把生成成功视为网络可用。

## 验收边界

新增的 `--mediation` 可读取实验请求目录并套用冻结授权；宿主动作通道、生产目录注册/只读身份和运行时 watcher 尚未实现，因此不是生产发布功能。HTTP 使用 TCP 承载不代表交付 TCP 端口发布；TLS TCP、普通 TCP、UDP、LAN 均未实施。公网 HTTPS/IPv6 入口复用既有 Traefik，但本原型未验证公网链路。

本地开发机无可用 Docker daemon/Incus。2026-09-11 已在操作者指定的 Ubuntu 26.04 宿主执行下述
独立 namespace 实验，通过 nft 加载、路由及实际 HTTP 数据流检查；该宿主未具备 Incus/KVM 验收
条件。真实 guest、Docker/Incus 规则顺序与 KVM 验证仍未执行，不据此改变 Module 的 `developing` 状态。

来源约束使用 bridge `input`：`ibrname` 只适用于 bridge input/forward。按 [nftables 官方排错参考](https://wiki.nftables.org/wiki-nftables/index.php/Troubleshooting) 核验，已在下述隔离 namespace 中实际加载；与 Docker/Incus 规则共存仍待执行。实验过滤会拒绝 guest bridge 的其他入站流量，不作为通用 guest 出网防火墙安装。

## Linux namespace 数据面验证

`test-env/scripts/server-incus-http-netns.py` 是手工远端实验入口，依赖 Linux、root、Python 3、
`unshare`、`nsenter`、`ip`、`sysctl` 和 `nft`。它不调用 Docker 或 Incus。必须先为本轮明确指定
测试宿主，再复制该脚本及上面的夹具产物到本轮独立临时目录，在该宿主执行：

```bash
sudo python3 /tmp/<本轮目录>/server-incus-http-netns.py --artifacts /tmp/<本轮目录>
```

脚本自行 `unshare --net`，拒绝带既有链路、路由或规则的 namespace；bridge/veth、所有监听端口、
HTTP 探针和 nft 规则均位于新建 namespace 及其子 namespace，不建立全局命名 netns。所有子进程
结束时清理，并有 90 秒生存上限；正常退出还比较宿主 nft 的无计数器摘要，发现变化即失败。
M2 `remote-test preflight` 的 helper/最小权限验收是另一条路径，此脚本不声称通过该门禁。

夹具使用 HTTP 进程代表来源和后端，先验证六组可达基线，再加载真实生成规则，验证批准端口可达、
未批准端口/其他 bridge 来源/同 bridge 来源/IP 与 MAC 冒用/guest 反向发起/IPv6 后端均拒绝。
实际流式 HTTP 连接在 tuple 撤销后必须停止，新请求被拒绝；重建固定夹具许可后等 31 秒验证过期。
这不提供 TCP/UDP 发布能力，也不测试公网 Traefik、受管 IP 重分配或 conntrack 条目删除。

2026-09-11 的 20 项 namespace 检查与宿主规则不变检查均通过，证据见
[实验记录](../../../dev-docs/reviews/2026-09-11-incus-http-netns-validation.md)。实际 Incus 两档 guest、
实例身份、跨租约围栏与完整撤销仍按计划单独验收。Core 的独立 `lease_secret` 已落地，但该实验不
读取或生成真实租约密钥。

## 授权与请求中介模式（新代码，测试暂缓）

`--mediation <管理员文件>` 可与 `--input` 或 `--capture` 组合；请求 action 为 revoke 时，改与
`--withdraw --previous` 组合。本模式也只生成计划，不执行渲染、联网或网络写入。指定 capture
时仍执行前述只读采集。消费者请求文件只含 action、instance_id、workload_id、guest_port 与可选
label，不能指定租约、主机名、IP/URL、auth、middleware 或 entrypoint。

`mediation.example.json` 是合成管理员夹具：`authorization` 形状与 deployment resource 的
`compute_ingress` 相同，`active_deployment` 是实验操作者断言，`request_directory` 与 `request_file`
将一个目录绑定到该租约。**夹具不是活动部署证明**，不得直接用于生产或从消费者目录加载授权。
准备实验时从实际测试部署取得匹配的冻结授权；父目录与挂载由实验操作者控制，只把单租约请求
目录交给对应消费者，绝不交出授权文件、其他租约目录、密钥文件或 Traefik 动态目录的写权限。
离线模式没有 Core 状态校验；下节 workspace 模式已接入读取与目录登记，自动挂载仍未实现。

示例采用 named，prefix 为 incus，label 为 http，目标为 `incus-http.example.test`。
其中 `auth: none`（默认）不提供访问控制；SNI、Referer 与日志可泄露 URL，不得发布敏感或可写
服务。`request.example.json` 展示原子 rename 发布的完整请求形状。Linux amd64/arm64 下，读取器
先用 O_PATH 固定单层文件名的 inode，排除符号链接、硬链接、设备/FIFO，再经受信 `/proc/self/fd`
读取同一普通文件；限 4 KiB，拒绝读中变化及重复/未知/大小写别名/null 字段。macOS 模式明确拒绝。

```bash
# 仅生成；路径及文件由实验操作者预先准备。本轮未执行此命令。
incus-network-prototype --input /var/tmp/anas-lab/observation.json \
  --mediation /var/tmp/anas-lab/mediation.json --out /var/tmp/anas-lab/mediated
# request 改为 revoke，且 previous 确实对应当前已应用的那条实验路由后：
incus-network-prototype --withdraw --previous /var/tmp/anas-lab/mediated/publication.json \
  --mediation /var/tmp/anas-lab/revoke.json --out /var/tmp/anas-lab/revoked
```

fixed/named 不需要命名密钥文件；random 要在管理员配置中给 `lease_secret_file`，指向包含
规范 base64 字符串的 JSON 文件，值来自该授权的独立密钥引用。它必须在消费者请求目录之外、
仅对实验操作者可读。工具不生成密钥、不记录其路径或值；这也不是生产 Secret Store 读取接口。
random 使用 HMAC-SHA256 前 128 位，不能用旧证书 bundle 代替密钥。

发布时观测身份必须匹配授权，Host/allowed_ports/auth 由授权覆盖，guest_port 来自通过授权的
请求。`publication.json.mediation` 保存拟发布的实例 UUID、任务、域名和预占编号，不保存密钥。
ForwardAuth 只使用冻结 middleware，并增加存在性/拒绝未授权请求的验证步骤；这一步仍需操作者
在真正加载实验路由前完成，工具不宣称已经检验 Provider。输出复用 `ANAS_TRAEFIK_ROUTE__INCUS_LAB__*`
及现有 entrypoint renderer，实验入口保持 HTTP/TLS=false，不代表生产 HTTPS 认证已验收。

单次规划器使用内存预占，跨进程不会自动恢复旧请求；本 CLI 不提供持久全局锁、后台 reconcile
或生产多路由登记。`--previous` 对应旧实验的清理总在新发布之前，计划生成时的预占不能证明
实际域名/IP 已保留。当前观测拒绝且有同租约 previous 时只生成撤销；非法请求/授权在采集前报错。
mediated revoke 核对旧记录的实例/任务/端口/label，不要求实例 Running；授权已撤销或密钥丢失时，
操作者仍可不带 mediation 单独生成旧记录的 `--withdraw`。

编译、单元、文件攻击场景和真实服务器验证均留到后续测试阶段，见 [待测清单](e2e-plan.md)。

## 从 Core 活动状态登记和读取（新增，未测试）

workspace 模式不使用离线夹具的 active_deployment、authorization、request_directory 或密钥文件。
配置形状见 `mediation-workspace.example.json`；workspace 和登记根均由管理员预先选择，不能由
消费者请求提供。读取器在 Core 共享运行锁下验证活动状态及冻结授权，登记绑定 workspace 路径、
部署、激活时间和清单摘要。切换/迁移后需在新目录重新登记；旧目录不是自动授权或续期来源。

```bash
# 会创建新的私有登记根、空租约目录和 registry.json；不挂载、不运行采集或网络操作。
# 正常部署的 ingress 启动拦截仍保留。正例只用独立元数据夹具，不编辑实际 active 状态绕过它。
incus-network-prototype --register-requests /var/tmp/anas-lab/workspace \
  --out /var/tmp/anas-lab/request-registry
# 管理员按 registry.json 选择本租约目录放入原子写好的 http.json 后，仅生成计划：
incus-network-prototype --input /var/tmp/anas-lab/observation.json \
  --mediation /var/tmp/anas-lab/mediation-workspace.json --out /var/tmp/anas-lab/core-mediated
```

根目录及 registry.json 不交给消费者；后续只挂载该租约子目录。登记根/租约目录为 0700，登记
文件为 0400，无 Secret。登记拒绝覆盖旧目标；登记中的代次/目录映射必须与最新 Core 授权完全
一致，且目录设备/inode 必须与登记时相同，不能用同名目录换位或替换。该入口只接受 example.test 授权，所以不能当生产开启命令。

random 模式经 Core 适配器从原 Secret Store 返回对应租约的独立密钥，不允许提供另一文件覆盖。
它仍是有工作区读取权限的管理员实验进程，不代表可以把 Store 挂入生产中介。固定/命名模式不读
命名密钥。授权读取不读取 Store，只有 random 的单条密钥解析会进入现有 Store 解析器。
采集后再核对 Core；失去授权时只生成此前同租约发布的撤销。Core 已停用、登记失效或无法读取
导致加载失败时，管理员仍可使用不带 mediation 的 --withdraw 清理已记录实验。

本次没有执行以上命令、创建真实登记、读取实际 Secret Store或跑测试。权限、锁、登记失败、
切换/备份、旧请求重放和真实挂载的后续验证见 [待测清单](e2e-plan.md)。没有后台消费、全局路由
锁或生命周期 watcher；真正执行前仍需再次确认活动授权及观测，不能根据旧磁盘请求自动恢复访问。

## 探测身份与 fixture 准备（新增，未运行）

`--capture-probe` 和 `--prepare-fixtures` 是互斥的管理员实验入口，均要求新输出目录；不能与
其他模式混用。代码尚未编译/测试，下列示例只记录后续流程，本轮没有执行。正常生产 ingress
拦截不变，正例仍用独立元数据夹具，不能编辑实际 active 状态绕过保护。

`capture-probe.example.json` 的容器/网络 ID 是占位值，实验时替换为管理员选定的完整 64 位 ID。
采集进程必须已位于该 Traefik 的 netns，且 procfs 能看到 Docker 返回的 PID；程序不进入 namespace，
不启动进程。它只读独立实验 Docker socket（继续拒绝默认系统 socket），核对指定 bridge 的分配，
在同一 OS 线程上从真实 procfs/nsfs 和未绑定 socket 采集身份/cookie，前后比较完整选定映射。

```bash
# 预置于选定实验 Traefik netns 后运行；本轮未执行。
incus-network-prototype --capture-probe /var/tmp/anas-lab/capture-probe.json \
  --out /var/tmp/anas-lab/probe-capture
```

输出 `probe-identity.json`，含采集时间、容器/网络/endpoint ID 和 `identity`（PID、启动 tick、
boot ID、namespace device/inode/cookie、源 IPv4），不含 socket 路径、证书或密钥。它是管理员
观测记录，不是认证凭据或生产授权；运行时仍核对实际进程及每个 socket。生产启动器装配待办，
不得把实验 Docker 管理 socket 挂入中介来使用这条路径。

`prepare-fixtures.example.json` 指定现有活动实验 workspace、请求登记根、Core 交付的私有读取
配置、同一执行状态目录和受信 renderer。读取配置含敏感值，必须由可信准备方供给，不能用
消费者输入构造。状态目录预先建为当前用户私有；已有未撤销 publication 时先完成正常清理。

```bash
# 只查询、持锁并准备文件，不执行 Probe、HostActions 或动态路由发布。
incus-network-prototype --prepare-fixtures /var/tmp/anas-lab/prepare-fixtures.json \
  --out /var/tmp/anas-lab/http-fixtures
```

完整流程限 90 秒，登记阶段另限 30 秒。新目录中 `response-*.bin` 是每目标独有的响应，
`fixture-plan.json` 描述对应目标与固定 GET 路径 `/anas-incus-http-fixture`，状态为 `prepared-only`。
最后生成的 `fixture-registry.json` 为 0400、单硬链接、1 MiB 内的规范 JSON；不含命名密钥、API
凭据或可重放 token。响应/计划为 0600，父目录 0700。如果最后登记失败，私有准备文件可能保留供
检查，命令明确报错；它们不授权网络发布。不能覆盖旧输出目录重试。

管理员后续按计划把响应放入对应 guest，通过指定端口和路径原样返回这些字节。工具不连接或修改
guest、不安装 HTTP 服务，也不从第一次返回结果学习摘要。只把该 guest 的响应交给它，不共享
其他目标响应或登记根。这一步完成与否仍须真实 Probe/E2E 验证。

运行装配可使用 `NewRegisteredFixtureHTTPProbe` 读取登记。登记省去易随中介会话变化的 reservation，
其余部署、请求、Host/auth、UUID/incarnation、MAC/IP/端口全部固定。新 token 只由当前 Planner
产生，探测前后重查当前 Core、请求、独立实例事实及登记文件；中介重启不会重放旧 token，guest
重启或目标变化必须重新准备。文件不能代替执行回执、地址保留或当前授权。

上述命令都未运行，真实只读身份、宿主动作、生产启动供给、guest 内服务安装、孤立工件恢复与
服务器验收仍待完成。测试恢复后按 [待测清单](e2e-plan.md) 执行，不将准备文件当成网络通过记录。

## 外部工件盘点与恢复接线（代码未运行）

运行包新增 `Executor.Recover` 和必需的 `HTTPArtifactInventory`。Controller 在同一状态锁下先
撤销未完成回执，再要求完整文件/API/宿主作用域清空；开通/续租前后和地址释放前也检查。此处没有
新增 CLI 模式，现有准备命令不调用恢复、不执行网络变更，也不证明外部已经清空。

安装方给 `route_directory` 指定专属受信目录，不能与 auth 等其他配置混放，并保证实际 Traefik
file provider 观察该目录。盘点最多 4096 项、30 秒，文件必须匹配回执的完整输出，API 前后重读
文件和目录身份；只有完整匹配的已加载 HTTP 对象及对应 ForwardAuth 使用关系可以排除。未知
文件、孤立已加载对象、其他服务/provider/协议对 `anas-compute-` 的引用均拦截。保留前缀出现在
无关配置字符串中也可能被拒绝，不应使用它命名其他对象或填写无关字段。

撤销只额外清理名称规范且完整字节匹配当前回执的临时文件。部分写入、修改内容、模板版本变化、
未知文件和已退役 token 都不能据名字自动删除或重建授权。未知工件存在时保留地址与 retiring
回执，继续关闭可核验的路由/许可/连接。缺失/损坏状态须保留现场并结合独立备份与宿主证据处理；
不要删除状态/锁后重跑，也不要从 owner 注释重建许可。

真实宿主盘点必须核验地址保留、`/32`、HTTP 许可、连接、安装 namespace/接口身份及拒绝基线。
该适配器及受限管理员恢复动作尚未实现；不得用空实现替代。以上代码未编译/运行，文件/API 检查
不代表真实网络清空。后续故障注入和宿主验收已记录到 [待测清单](e2e-plan.md)。
