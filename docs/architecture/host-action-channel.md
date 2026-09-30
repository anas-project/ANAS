# 宿主特权动作通道（设计）

> 状态：**当前实现与待验收设计。主要发行版已通过真实 CLI/HTTPS、共享 job、systemd 确认执行及五分钟自然过期门禁（2026-09-23，身份链简化之前的实现）；完整发行版/升级/交互和非 systemd 验收未完成**。更新：2026-09-30。当前实现以 §14（身份链简化）为准，§13 中未被 §14 取代的部分仍有效，§7—§12 是历史切片。本文规定安装期那一次 root 之后，Web 控制台与 CLI 如何执行需要特权的
> 宿主操作，而不再向用户索要第二次密码。
>
> 它不取代[特权操作与 helper](privilege-helper-draft.md)，而是沿用那份文档的规则并补上它没有
>覆盖的一类：需要**完整 root**、且会留下持久特权产物的操作（安装软件包、启用系统服务）。

## 1. 为什么需要它

### 转发许可动作评审（2026-09-25）

新增编译动作 `incus.forwarding.permission.plan` 与 `incus.forwarding.permission`，
需求为 INCUS-R-101—R-105，沿用原 job、工作区授权、一次性确认、release 摘要与审计。
参数只含资源、enable/disable/retire 和明确 IPv4/TCP 目的地，不含命令、路径、来源接口或
调用方提供的实例身份。执行仍在短命 hostd 进程中，没有消费者可调用的第三个特权入口。

| 评审问题 | 当前决定与尚未关闭的边界 |
| --- | --- |
| 能否不用 root | 读取 root-only 宿主/投递证据并修改宿主 nft、兼容集合和精确 conntrack 需要既有 hostd；不把管理凭据或网络 capability 下发给消费者 |
| 权限是否最小 | 只操作有原始回执的租约级对象与明确目标；原 FORWARD 规则、Docker 链和更早显式拒绝不重写，不修改全局策略 |
| 留下什么产物 | 原宿主 state.json 中的 forwarding_scopes 及内核拒绝表、带期限集合、精确兼容链；不新增用户可写 root 脚本或 job 数据库 |
| 如何撤销 | 同动作 disable 先关闭许可并验证精确连接撤销；retire 另经确认，在部署停止、租约凭据撤销、实例/操作及物理端口为空后移除原回执对象，保留授权和失败历史；完整宿主卸载联合验收仍待完成，残留继续阻止依赖拆除 |
| 半失败如何处理 | 每步先保存 pending；许可可能生效后的读回、最终保存、取消或会话关闭失败，在原状态锁下执行一次独立有界撤回，不重试安装/续期；原失败与依赖阻止保留，撤回失败保留待完成步骤，不自动认领同名对象，不凭错误返回宣称没有效果 |

生产 enable 被编译侧生命周期门禁阻止，调用者不能确认掉该阻止项。此登记是受约束的
产品接线，不是自动续期、完整停止/重启撤销或默认 Docker 业务验收完成的声明。完整
运行 owner 接通并独立验收之前，不把候选内核执行器开放为生产功能。2026-09-26 的
显式退役接续不新增特权入口：它复用上述 plan/apply、跨工作区防护与原状态锁；不接受
调用者提供的“已停止/已撤权/为空”声明，不删除失败记录或把过期当作连接清理证明。

Web 首次恢复既有 owner 会话期间保留维护页等深链接，待会话恢复完成后才依据实际可访问
页面决定是否重定向。system 信息先返回工作区并不等于最终认证结果；恢复失败或权限丧失
仍退回总览。页面渲染和 API 的认证条件不变，此导航修复不授予任何宿主执行权限。
对应真实嵌入 UI 的 8 项浏览器门禁已经通过：实际五分钟过期后更新计划并清除旧勾选，不自动
确认或执行；只有重新勾选才生成新确认并执行。该独立 VM 运行同时通过正常关机、实际退出
状态与物理宿主对照，不从单元测试或 API 过期测试推导浏览器交互成功。

**当前身份结论：不迁移 anasd 的非 root 身份。** 现有控制台需求明确需要读取 root-only TLS
私钥和访问宿主工作区。当前实现保留 root/root，使用固定安装策略、内核 peer 身份及 PID 1
独立报告的实际 `anasd.service` 进程进行准入；不是简单允许任意 UID 0。早期“非 root 迁移”判断
不再适用，也不需要为接通通道而放宽 TLS 权限、加入 Docker 组或递归改属主。


ANAS 是面向普通用户的 NAS 产品。按 [Core 实现标准](core-implementation-standard.md) §4，命令行
安装完成之后，日常操作应当都在 Web 端完成。但有些操作需要 root——例如装 Incus daemon。

三条路里只有一条成立：

| 做法 | 结论 |
| --- | --- |
| Web 端向用户要 root 密码 | **禁止**。密码会经网络到达服务、驻留内存、可能进日志；它授予整台宿主的控制权；而且一个索要系统密码的网页表单与钓鱼在形态上无法区分 |
| 每次都让用户回终端 `sudo` | 对目标用户不可接受，且与 §4 直接冲突 |
| **安装期一次授权，之后经受限通道执行具名动作** | 本文方案 |

关键性质是：**用户给出的是一次性的安装授权，不是一个可以随意提权的通道**。通道能做什么，在
安装的那一刻就已经固定。

## 2. 不能做成什么

[特权操作与 helper](privilege-helper-draft.md) §3.1 已经点名了被替换掉的旧做法的弱点：

> sudoers 授权 root 执行一个**位于用户可写目录、内容由 anas 自己生成**的脚本

对运行 anas 的用户来说，那约等于 root。因此「白名单里放脚本」这个方向必须排除——只要脚本文件
是 anas 能写的，白名单就是装饰。

由此得到本设计的第一条不变量：

> **通道只接受动作 id 与类型化参数，永不接受命令、argv、路径或脚本。**

动作实现**编译进二进制**。新增一个动作意味着发布一个新版本的二进制，而不是往某个目录里放一个
文件。这与 §3「一个 capability 一个二进制，每个只做几件具名的事，不接受任意脚本或命令」是同
一条规则。

## 3. 形态：`anas-hostd`

一个 root 拥有、**socket 激活**的动作执行器。

```text
Web 控制台 / CLI
      │  连接 /run/anas/hostd.sock（属组 anas，0660）
      ▼
systemd socket 激活 → anas-hostd 以 root 启动
      │  读入一条 anas.action/v1 请求，创建 job
      │  按 id 分派到编译进来的动作，执行
      │  向 job 的事件日志追加 progress / result
      ▼  进程退出（job 记录留在服务端）
```

**socket 激活而不是常驻 daemon**：不留一个一直跑着的 root 进程。每次请求起一次、做完就退，攻击
面只在请求处理期间存在。

### 3.1 重新设计：两个入口，不是三个二进制

[特权操作与 helper](privilege-helper-draft.md) 在还没有通用通道时写下了「一个 capability 一个
二进制」，并据此规划了第三个二进制 `anas-btrfs-helper`（`CAP_SYS_ADMIN`）。**有了本通道之后，
那个规划应当收回。**

理由是 `CAP_SYS_ADMIN` 实践上等价于 root——它能 mount、setns、加载 BPF。为它单开一个文件，
换来的「比 root 小」只是名义上的，却付出了两份代价：那条路径没有审计，而且多一个需要单独授权、
单独升级、单独 setcap 的文件。

按今天的需求从头设计，分界不是「有几种 capability」，而是**这条路径是不是日常热路径**：

| 入口 | 权限 | 谁调用 | 频率 | 审计 |
| --- | --- | --- | --- | --- |
| `anas-helper`（直接 exec） | `CAP_NET_ADMIN` | `anas apply` 自己 | 每次部署 | 无需——无产物、幂等、单一能力 |
| `anas-hostd`（socket 通道） | 完整 root | Web 控制台 / CLI | 罕见 | 每次 |

`anas-helper` 保持独立，是因为它是**唯一真正窄的能力**：建桥不产生任何用户事后要面对的产物，
删掉重建没有区别，而且每次 `apply` 都要走。让这条最频繁的路径永远碰不到 root-capable 的文件，
是这次拆分唯一真正买到的东西。

其余全部——btrfs subvolume 删除、`btrfs send`、备份 rsync 恢复属主、装包启服务——归 `anas-hostd`。
它们的共同点是：**罕见、留下用户事后要面对的产物、且需要审计**。把它们放进一条有审计、有二段
确认、有对称撤销动作的通道里，比分给若干个各自 setcap 的文件更安全，也更少活动部件。

`anas-helper` 因此**保持现名**：它不再是「网络那个 helper 之一」，而是「那个不需要 root 的
helper」，名字宽泛并不误导。

### 3.1.1 随 anas 一起升级

`anas-hostd` 与 `anas` 同版本发布、一起升级。动作实现编译在里面，所以「通道能做什么」这个问题的
答案永远等于「装着的这个版本是什么」——不存在一个可以单独更新、或者被写入新脚本的动作目录。

### 3.2 协议：与 Module Command 同一套

线格式与 job 语义由[统一动作 ABI `anas.action/v1`](action-abi.md) 定义，本通道不另起一套：

- stdin 一条 JSON 请求：动作 id、规范化参数、调用 id；
- stdout 严格 JSON Lines：零到多条 `progress`/`warning`，最后恰好一条 `result`；
- 每次调用创建一个 job，执行不绑在连接上，断连可重连重放；
- 原始 stderr 丢弃；取消是协作式的，杀进程组后必须标记 `outcome: unknown`。

**共用的是线格式与 job 语义，不是代码路径与注册表。** 两者信任边界不同——模块命令跑在部署里、
由 manifest 声明；本通道跑在 root、动作编译进二进制。让信任域不同的两段逻辑共用一个进程，是这类
设计最常见的失手方式。

### 3.3 授权与审计

- **调用方身份**：`SO_PEERCRED` 读取对端 uid/gid，必须是 root/root：anasd 以 root 运行，socket 为 root:root
  0600，peer 校验与文件权限等价。不再证明是哪一个 root 进程（§14）。
- **参数校验在动作内部**，与 `anas-helper` 的 `parseBridge` 同样的做法：枚举、模式、范围，拒绝
  一切不认识的字段。
- **每次调用都进 journal**：动作 id、规范化后的参数、调用方 uid、结果。参数中被标注为敏感的字段
  不落日志。
- **破坏性动作需要二段确认**，见 §3.4。

### 3.4 二段确认：把「用户看到的」和「实际执行的」绑在一起

这就是 `anas plan` / `anas apply` 那一套，搬到宿主动作上。

**它防的不是未授权调用**——控制台本来就已通过认证，被攻陷的控制台当然能发请求。它防的是另一件
事：**用户点「确认」时看到的那段说明，和真正执行的那个操作，可能不是同一个东西。**

没有二段确认时：

```text
控制台自己算出「将会删除 3 个实例」并显示给用户
用户点确认
控制台发 incus.uninstall
hostd 自己再算一遍，可能算出 5 个，然后删掉
```

两次计算由不同的代码在不同时刻做出。中间实例数变了、控制台缓存过期了、或者控制台有 bug 显示 A
却发送 B——用户批准的都不是他看到的那件事。

有二段确认时：

```text
1. 控制台 → plan incus.uninstall
2. hostd 算出确切影响，返回摘要 + 绑定该摘要的一次性 token
3. 控制台显示 hostd 返回的那段摘要（不是自己算的）
4. 用户确认 → 控制台 → apply，带上 token
5. hostd 重新计算；影响与 token 绑定的摘要不一致就拒绝执行
```

保证变成：**摘要由将要执行它的那段代码产生，且从展示到执行之间没有漂移。**

只有破坏性动作需要它。`incus.status` 这类只读动作、`incus.install` 这类幂等的建立性动作，一段
就够。

#### 这个 token 在哪一层：ABI，不是 HTTP

确认 token 定义在[统一动作 ABI](action-abi.md) 里，而 ABI 在两个入口**之下**。因此 CLI 与
Web 用的是同一个机制。不要把它与控制台的会话凭据混为一谈：

| | 会话 token | 确认 token |
| --- | --- | --- |
| 层 | HTTP/HTTPS，控制台会话 | 动作 ABI，`anas-hostd` 的 socket 协议 |
| 回答的问题 | **你是谁** | **这是不是刚才给你看过的那件事** |
| 存放 | 浏览器 Cookie | root 拥有的 `/run/anas/`，且只存摘要 |
| CLI 有没有 | 没有——CLI 走 socket 不走 HTTP | **有** |

CLI 上的两段是这样的：

```text
anas host plan incus.uninstall     → 打印摘要 + token
（用户读，确认）
anas host apply --token=...        → 执行
```

可以做成一条命令内部完成 plan → 提示 → apply，但**两个 job 依然分别记录并互相引用**，审计里看到
的与 Web 端完全一致。

#### token 的生命周期与存储

这条与 socket 激活直接冲突：每次连接都是新起、做完就退的进程，**内存里留不住东西**，而 token
必须跨越 `plan` 和 `apply` 两次独立连接。因此必须落到进程之外。

**存哪里**：root 拥有的 `/run/anas/`（tmpfs，重启即清，权限清楚）。存的是 token 的**摘要**、
被批准动作的摘要、到期时间——不是 token 本身，这样即使文件被读到也不能直接拿去用。`apply`
校验通过后**一次性消费**，防重放。

**有效期 5 分钟。** 定这个数之前要先看清它在防什么：`apply` 会**重新计算**影响，与 token 绑定的
摘要不一致就拒绝——所以**过期时间不是在防「世界变了」，那已经被重算覆盖了**。它只在防 token 被
窃取；再加上一次性消费，被窃的 token 只能用一次，而且要赢过合法用户的竞速。压力因此小得多，
可以取「人类交互的下限」而不是「保险起见放长」。

**对话框停留过久时，控制台静默重新 `plan` 换新 token。** 这条比数值本身重要：它把「安全上要短」
和「用户可能要去核对一下再确认」这两个相反的要求解耦了，于是 5 分钟不会变成一个需要反复权衡的
参数。

**过期后 `apply` 必须重新展示，不得直接执行。** 用户已经点过确认，只报一句「token 过期」会让人
困惑；但沿用旧摘要直接执行又恰好绕过了这个机制存在的理由。正确行为是自动重新 `plan`，**把新摘要
再展示一次**，由用户重新确认。

### 3.5 长时动作：进度、回显与断连

`btrfs send` 一个大 subvolume 可以跑很久，装包也可能几分钟。三件事分开看：

**进度回显已经在协议里**：stdout 是严格 JSON Lines，`result` 之前可以有任意多条 `progress` /
`warning`，控制台直接渲染即可。事件带序号并追加进 job 的日志，因此「重连后看到之前的进度」不是
额外功能，就是重放这条日志。

**长请求不等于常驻进程**：socket 激活反对的是「没人用也一直跑着」，不是「一条请求跑得久」。
处理进程活多久由请求决定，跑完照样退出，性质不变。

**两处都已由[统一动作 ABI](action-abi.md) 解决**：

1. **调用方断连不中止执行。** 每次调用创建一个 job，执行进程是服务端的子进程而不是连接的
   子进程；断开只是停止订阅，`attach(job, from_seq)` 可以重连并重放。要停止只有显式 `cancel`
   这一条路。见该文 §4、§5。
2. **`btrfs send` 不需要第二个数据 fd。** 目的地由**动作自己打开**——调用方给的是经校验的目的地
   描述，不是「把字节流给我」。数据根本不经过通道。见该文 §10。

### 3.6 可移植性：非 systemd 发行版的等价物

**systemd socket 激活做的事**：systemd 自己持有监听 socket，服务不在跑的时候 socket 依然存在；
有人连上来，systemd 才以 root 启动 `anas-hostd`，并把**已经打开的 fd** 传给它。三个性质合起来
才是本设计想要的：

1. 平时没有常驻 root 进程；
2. socket 的属主、属组、权限由 unit 文件声明，不靠程序自己 chmod（省掉一个竞态窗口）；
3. 服务处理完一条请求就退出。

**非 systemd 发行版（如用 OpenRC 的 Alpine）没有内建等价物**，候选各有代价：

| 候选 | 代价 |
| --- | --- |
| 常驻 root supervisor | 依赖最少、最贴近 OpenRC，但恰好丢掉性质 1：多一个 24 小时在跑的 root 进程 |
| `inetd` / `xinetd`（Alpine 的 busybox 自带） | 模型对得上，实际也不算新依赖，但声明方式与权限模型又是一套 |
| setuid root 二进制，不用 socket | 丢掉 fd 传递，PATH 与环境净化变成自己的问题，攻击面明显更大 |

**结论：常驻 supervisor，但只让它 accept。** 真正要守住的性质不是「用 systemd」，而是
**做事的那个进程是短命的**。据此把常驻部分压到最小：

```text
OpenRC 服务 = 一个只做 accept 的极小启动器（常驻，root）
                 │ 每来一条连接
                 ▼
              fork/exec anas-hostd 处理一条请求，退出
```

常驻代码只有 accept 循环那几十行，动作逻辑全部仍在短命进程里。与 systemd 的差别就只剩「谁持有
socket」——systemd 上是 systemd，OpenRC 上是这个启动器——而**每条请求起一个新进程、做完就退**
这条性质在两种 init 上完全一致，`anas-hostd` 本身不需要为此有两套写法。

setuid 方案排除：它同时丢掉 fd 传递和环境净化，是三者里唯一在安全性上明确变差的。

## 4. CLI 与 Web 走同一条通道

CLI 不再需要自己的特权路径。两者都连同一个 socket、发同样的请求、留同样的审计记录。

这消除了一类长期存在的不一致：某些操作只能在命令行完成、Web 端做不了。在本设计下，**能力差异
只可能来自动作清单，不会来自入口**。

安装期那一次 `sudo` 仍然是必需的，且仍然只发生一次：它安装 `anas-hostd`、注册 socket unit、
设置属组。此后不再有第二次密码提示。

## 5. 初始动作清单

保持最小。首批只覆盖 Incus 宿主供给：

```text
incus.install      # 按发行版表安装 incus 包
incus.configure    # 启用服务、监听回环、初始化存储池
incus.enroll       # 生成 ANAS 管理证书并加入信任库
incus.status       # 只读：装没装、版本、监听地址、存储池
incus.uninstall    # 移除 ANAS 建立的信任条目与 project/network/profile
```

### 5.1 新增动作的评审

清单会**默认增长**——每次有人需要一个新的宿主操作，最省事的做法就是往里加一条。没有显式闸门，
几年后这个清单就等于一个 root shell。因此新增动作按**需求级变更**处理，不是代码级变更：它拿一个
需求 ID、进需求矩阵、写明验证方式，走与 Core 变更同一套[评审门禁](core-implementation-standard.md)。

每条提案必须回答五问：

1. **能不能不用 root 做到？** 能，就不该进这个清单。这一问最容易被跳过，也最有效；
2. 它需要什么权限，为什么这是最小的；
3. 它留下什么用户事后要面对的特权产物；
4. 有没有**对称的撤销动作**——`incus.install` 之所以可接受，正是因为 `incus.uninstall` 存在；
5. 失败到一半时留下什么状态，重跑是否收敛。

两条持续性要求：

- **定期复审并删除从未被调用的动作。** 没人用的特权代码是纯负债：不产生价值，攻击面一分不少；
- **清单必须可打印**（`anas host actions`）。运维不读源码就能审计自己装的这个二进制能做什么——
  这条把「动作清单就是安全边界」从文档里的承诺变成当场可验证的事实。

## 6. 待决

- **事件日志的持久化与保留期**：见[统一动作 ABI](action-abi.md) §12；

## 7. 当前编码边界（2026-09-19）

`internal/hostaction` 与 Module 注册表分离。`cmd/anas-hostd` 和候选单元已编码并进入发布打包，
但安装器和正式非 root 执行者仍未接入；下述较早切片的缺口以 §11 当前边界为准。
目前唯一编译处理器是 `incus.status` 的 **installation-preflight 子集**，只读固定的系统标识，
返回声明式发行版匹配、隔离档与未完成门禁。它不查询 daemon 的运行版本、监听、存储池、
证书或资源归属，也不声明 compute ready。完整状态接口仍待后续供给。这个子集不需要 root，
因此独立诊断工具无需提权，没有为它增加第三个特权入口。

动作输入复用 `anas.action/v1` 严格帧。`Receive` 只接受已连接的 Unix stream socket，
先从 `SO_PEERCRED` 取得 UID/GID/PID，再按受信安装策略决定是否读取请求；没有参数可填写身份。
UID/GID 不能为 root/零值，只允许固定服务账号及其 primary GID，或指定组作为真实 primary GID。
它不通过 NSS 或客户端组列表补充 supplementary group 资格。其他平台拒绝，读取有三秒上限。
这一段只验证连接身份，不证明 socket 已由受信 systemd unit 建立，也不代替应用角色授权。

编译清单只能查询，不能由 manifest、JSON 或插件添加处理器；计划中的 install/configure/enroll/
uninstall/image-prune 全部拒绝。`incus.status` 参数暂只接受 `{}`，不接受命令、路径、URL 或认证覆盖。
规范化调用与 peer 保存在不可由调用方构造字段的内部对象中；内存一次性使用保护不代替共享 job 的
持久化幂等、并发与崩溃恢复。

执行审计复用 `audit.Writer`：开始记录成功后才探测；结束记录成功后才返回成功候选帧。两条记录
绑定同一 job/invocation 与内核 peer，不保存原始失败输入、探测错误或 endpoint。结束审计失败返回
unknown。该候选终态没有自选 seq，仍须交给共用 recorder 核验真正的 EOF/进程退出，再写唯一 job store。
没有让 root 进程直接打开用户可写的 workspace 日志或另建一份 job 存储。

激活 fd/安装配置校验、被拒连接审计和共享 job 的执行侧绑定现已编码，边界见 §8。
跨进程绑定传输已补入 §9；私有 listener 装配、生产退出状态监督及二段确认仍未交付，也没有安装 root 二进制、systemd/OpenRC 单元或
CLI/Web 执行路由。`anas host actions` 只提供本机编译清单，本节不能作为启用命令。
发行版预检细节见 [Incus 宿主供给设计](incus-host-provisioning.md) §2.1；验证和剩余事项由
[宿主通道计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/host-action-channel.md)跟踪。

## 8. 激活身份与共享 job 交接（2026-09-19，内部实现）

### 8.1 固定安装身份与已接受连接

`OpenSystemdActivation` 只处理一次 `Accept=yes` 激活：固定 fd 3，精确校验 `LISTEN_PID`、
`LISTEN_FDS=1` 和 `LISTEN_FDNAMES=connection`，随后清除这些环境标记。名称及 fd 语义已对照
[systemd v257 的 sd_listen_fds 文档](https://github.com/systemd/systemd/blob/v257/man/sd_listen_fds.xml)。
标记不是认证：还须核对 AF_UNIX/SOCK_STREAM、已连接且非监听、固定本地 socket 名和内核 peer。
匿名 socket、socketpair、其他地址、TCP、普通文件与多 fd 均拒绝。

安装输入固定为 `/etc/anas/hostd.json`：`schema` 为 `anas.host-action-installation/v1`，
字段只有 `release: {version, commit}`、非零 `service_uid/service_gid` 和显式 `group_gid`。
`group_gid=0` 关闭额外组准入；socket 属组此时取 service_gid。规范 JSON 限 4 KiB，拒绝重复、
未知、大小写别名、null、缺失字段及非规范拼写。version/commit 必须与执行二进制一致，开发构建
不能开启激活路径。不存在命令、路径、可执行文件、插件目录、密码或 environment 字段。

Linux 从 `/` 用持有的目录描述符逐级 NOFOLLOW 打开固定路径：祖先须 root 所有且无组/其他写权，
策略为 0600 单链接普通文件；`/run/anas/hostd.sock` 为 root 所有、指定属组、0660 单链接 socket。
前后复核目录链、策略字节/元数据和 socket 节点；内容、权限、链接或目录项替换都失败。消费的是
启动器已接受的连接，不新建监听，不自动修权限，不读取 caller 指定路径，也不执行用户可写产物。
路径检查不证明 systemd unit 的其余安全选项已安装；完整激活与升级仍须实机验证。

### 8.2 job 执行者侧绑定

`Activation.Serve` 强制注入 job 绑定与既有审计 writer，处理一条请求后关闭连接。
底层只读执行函数现为私有，不能从公开执行入口绕过绑定。拒绝的 peer/输入只记录固定原因及
实际内核身份，不把未解析的请求自报 action/job id 记为有效动作。拒绝审计失败同样拒绝执行。

`jobexecutor.HostJobBinding` 位于**非 root job 所有者一侧**，复用同一个 `consolejobs.Store` 与
`ExecutionLease`。它只接收已开始的 `incus.status` 只读 job，匹配实际 invocation、创建者、
workspace、开始时间、冻结 version/commit 和空参数。排队、终态、已取消、已有执行事件、其他
动作、变更型 job 或另一 store 的执行租约均拒绝。实际调用前重新授权已持久化 actor，并在授权
回调后重读状态，防止等待期间取消或变更被忽略。对象只允许一次同步执行，不允许晚到回调执行。

`RetainActionExecution` 确认 store 与 lease 属于同一目录，并为监督期保留原执行租约。绑定结束
不自动释放所有权；只有监督者确认进程和 I/O 全部结束后才 `Close`。绑定不创建第二份 job，
不写成功终态、不恢复失联执行，也不充当持久化幂等或跨进程授权凭据。

root 执行端通过独立认证的 broker 使用这条绑定，不能直接以 root 打开用户可写 job 目录，
也不能把传入的一份 JSON 当作实际 running job。§9 已补绑定传输和进程存活保护，生产装配及完整退出状态监督仍未实现。订阅断连
不得成为执行取消；socket EOF 不等于 exit 0。只有共用 recorder 拿到独立实际退出证据后才允许
提交最终 outcome。当前测试中的受控流/退出值不代表真实 systemd 子进程已验收。

发送成功候选帧之后的描述符清理失败仍返回错误，不能让启动器因函数返回 nil 而报告正常退出。
清理完成性继续由监督者负责，不能仅凭已写出的候选帧判断。

### 8.3 公开清单与部署阻塞

`anas host actions [--json]` 已接入 CLI；只打印当前客户端的编译清单和 version/commit，明确
`source: compiled-client`、`installation_verified: false`。它不连接 socket，不证明服务端清单，
也不开放 install/configure/enroll/uninstall/prune。具体输出见[命令契约](/reference/contracts/commands#host-actions)。

复核发现现有 `packaging/systemd/anasd.service` 仍以 root/root 运行，与上述非 root peer 政策
不兼容。不得为接通而静默允许 root/任意 UID；正式接入需单独确定非 root 执行所有者、目录归属、
Docker 权限及迁移流程。本轮不改服务身份或文件属主，不安装新 root 服务。后续安装动作、对称
卸载、二段确认、Linux 原生以及真实 Incus/KVM 验收继续阻塞。

## 9. 跨进程 job 绑定传输（2026-09-19，内部接线）

### 9.1 两端身份与固定连接

`Activation.ServeBrokered` 接入原有激活验证及 `executeBound`，root 端只连接固定
`/run/anas-job-broker/socket`。连接时以描述符检查 `/`、`/run` 的 root 归属与无共享写权限；
`anas-job-broker` 是服务账号所有的 0700 目录，socket 为同一 UID/GID 所有的 0600 单链接节点。
这个目录只作为非 root 所有者的通信端点，不向 root 提供可执行文件、脚本或 job 数据库。
调用方不能提供地址、路径、命令或新的处理器。§10 已实现 listener 的创建和停机清理；
私有目录的安装与实际非 root 服务装配仍未完成。

root 端要求 broker 的内核 PID/UID/GID **与最初请求激活连接的进程完全相同**，并且是安装策略的
service_uid/service_gid。仅组成员资格不能替代实际执行所有者；CLI/Web 的执行请求须由共同的
非 root job 所有者发起，不能用直连 socket 的方式跳过 job。broker 重启后即使 UID/GID 相同，
旧请求也不能转交新 PID。激活侧同时从**原请求 socket**取得并保留进程句柄，拨号前、运行
回调前后及握手返回时检查原进程仍存活；只比较第二条连接的同名 PID 不足以抵抗原 PID 被复用。
broker listener 必须由所有者进程自己创建，不能直接继承 PID 1 创建的
监听 socket：[`SO_PEERCRED`](https://man7.org/linux/man-pages/man7/unix.7.html) 返回的是 connect/listen
时的凭据，而不是事后接管 fd 的进程身份。

所有者侧 `AcceptJobBrokerObserved` 在读取任何载荷前要求当前非 root 身份及对端 root UID/GID，
并取得连接关联的 `SO_PEERPIDFD`。root 端也为 broker 保留同类句柄；不支持该选项或句柄检查失败
直接拒绝，不回退成 `pidfd_open(传入的 PID)`。句柄必须带 CLOEXEC；接收不到可靠身份不能返回
空的“已验证”对象。实际内核与权限支持仍须按发行版验收，常量能编译不等于平台可用。

### 9.2 同一 job 的握手与复核

`HostJobBinding.ServeBroker` 只服务已经创建并开始执行的同一份 job，沿用原 Store、lease、
冻结 release、actor 和参数检查。私有握手顺序为 `claim → bound → finished → validated`，
包含原 `anas.action/v1` 请求、release、原连接身份与 128 位随机会话 nonce。每帧最多 8 KiB，
规范 JSON 加 LF，拒绝重复/未知字段、null、大小写别名、错误阶段、错 nonce、尾随帧和缺少最终 EOF。
普通 I/O 截止为三秒，一轮绑定上下文最多十五秒；只适用于当前短时预检，不承诺长时安装动作。

`bound` 只有在真实 running job、当前权限及冻结请求核验后才发出。发送前先记录该会话可能已
授权，部分写入也按可能生效处理。`finished` 之后再次读取 job、复核权限，再重读控制状态，
全部通过才回 `validated`。执行期间角色撤销、取消、版本变化或审计/存储失败不能得到成功确认。
结束确认之前的断连和错误不触发自动重试；同一 binding 对象只允许一次执行。

这不是另一套 job/事件日志，也不是破坏性操作的五分钟确认 token。nonce 只绑定当前连接的消息，
不能用于重建 running job、恢复失联执行或代替共享 Store 的幂等策略。拒绝只审计固定原因及
内核身份，不记录原始请求或把自报 job id 当成已授权身份。

### 9.3 进程结束与业务完成分开

`BrokerSession.Close` 在 grant 可能被观察到之后要求对应进程已结束；仅握手完成、EOF 或对端
关闭 socket 不够。`HostJobBinding.Close` 传播这个阻断并保留原执行租约，不在失败后释放所有权。
`WaitBrokerExecutor` 只轮询连接关联的进程句柄，不发送信号，不按数字 PID 找进程；等待结束也
不意味着得到了退出码。`Close` 不持有 job 锁去等待 session 锁，避免与 session 回调取得 job 锁
形成反向锁序。

[`pidfd_open(2)`](https://man7.org/linux/man-pages/man2/pidfd_open.2.html) 区分了进程退出可读通知与
子进程 wait：仅观察到退出，不能推导 exit 0。现有只读预检不创建子进程；未来安装器会创建包管理器
等后代，必须另补 cgroup/监督者证明，不能复用本段当成完整进程树清理。job 的成功终态仍须由
共用 recorder 根据实际输出 EOF、独立真实退出状态及审计提交；目前没有生产适配器提供这一整条证据。

原生回归入口曾为 `bash test-env/scripts/test-host-job-broker-native.sh`（2026-09-30 随 broker 删除，改为 §14 的
`test-host-action-native.sh`）。它只在非 root Linux 上
运行隔离 socket/子进程 fixture，要求关键用例真实执行；内核不支持、用例跳过或缺失均不能通过。
本机 macOS 的协议测试及双架构交叉编译与该原生门禁分开记录。这个脚本不安装服务、不执行 sudo、
不修改宿主防火墙，也不代表 systemd、真实 root 对端或 Incus/KVM 验收。

## 10. 所有者私有监听与任务分派（2026-09-19，内部实现）

### 10.1 只创建自己的通信端点

`OpenJobBrokerListener` 在非 root 执行所有者进程中创建固定 `/run/anas-job-broker/socket`。
安装方须先提供服务 UID/GID 所有的 0700 目录；代码不创建、chown 或修复安装目录。
沿用既有目录描述符校验，`/` 与 `/run` 要求 root 所有且不可共享写，私有目录加非阻塞排他
flock，锁持有到该 listener 完全关闭。已有 socket、普通文件、符号链接或不安全目录均拒绝；
即使取得了锁，也不把崩溃残留解释为可以删除或接管的对象。

新 socket 在已校验私有目录中创建，再通过固定父目录的 NOFOLLOW 操作收紧到 0600；不改变 Go
进程的全局 umask。Linux 的目录权限、socket 权限及连接身份是不同检查，依据见
[unix(7)](https://man7.org/linux/man-pages/man7/unix.7.html)。socket 的初始权限收紧窗口由私有父目录
隔离其他账号；这不是针对恶意 root 或同 UID 进程的沙箱。权限操作不受支持就失败，不降级为宽松模式。

显式关闭 Go 的[自动 unlink](https://pkg.go.dev/net#UnixListener.SetUnlinkOnClose)，
停机只在目录链和完整 socket 身份仍匹配时，以持有的父目录描述符删除自己的节点。不递归删除
目录、不删除被替换或被移动的对象。启动中途失败可保留不确定节点供管理恢复；清理失败保持错误，
再次 Close 不把它改成成功。Accept 空闲时也周期复核身份，并响应所有者取消。

### 10.2 先由监督者注册，再由连接查找

`jobexecutor.HostJobBroker` 复用同一个 Store、ExecutionLease、release 和当前权限检查。
监督者通过 `Register` 绑定已经开始的只读预检 job，再发起宿主激活；socket 只查找匹配的
job/invocation，不创建、启动、重建或注册 job。注册表只是当前监督对象的有界索引，不存第二份
任务状态或幂等记录。每次调用仍由原 `HostJobBinding` 校验完整请求、当前角色和控制状态。

单个 owner 最多保留 32 个绑定、同时处理 8 条连接；等待空位发生在 accept 之前，不无限启动
goroutine。重复注册和并发抢占同一绑定失败，不能替换仍保留进程句柄的对象。`Ready` 只在实际
listener 打开并验证后通知；启动失败不就绪，也不能在同一对象上隐式重启。Run 的 context 来自
owner，不从 Register、浏览器或 CLI 请求继承；原请求返回或取消不停止已注册的执行。

### 10.3 停机、退休与未确认执行

停止时先关准入、取消 owner 的握手上下文，再等待所有连接处理结束，关闭 listener；Run 自身
保留执行租约直至这些处理结束。每个 job 的保留是独立的：成功握手也不自动关闭进程句柄，仍由
监督者 `WaitBrokerExecutor` 观察退出。授权可能生效后的握手失败或审计失败会停止进一步准入；
`Close` 在远端未退出时仍拒绝交出该 job 的执行租约。

`Retire` 必须同时看到共享 Store 中的实际终态和远端清理确认；只关闭连接、只关闭 binding 或
只过了某个 TTL，均不能让 running job 被删除后重新绑定。服务层不分配事件序号、不制造退出码
或成功终态、不按 socket EOF 推断执行成功。非 root 服务迁移、root 可执行程序、独立退出码与
完整后代清理、公共执行入口及宿主写动作仍未交付，不能用本节内部 API 当成已启用产品功能。

原生门禁已扩展到 listener 和共享 Store 分派用例，并接入 CI 的 Go job。门禁要求指定包和
用例实际通过，skip、无匹配测试、错误平台或缺内核能力都不能算验收。工作流接线不等于本轮已
运行 GitHub CI；实际执行范围记录在
[本轮核对](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-19-host-job-broker-listener.md)。

## 11. 独立退出状态、共享终态与可执行程序（2026-09-19，待实机验收）

本节取代前面切片中“没有退出状态适配器/root 程序”的当前状态描述；前面所述非 root 服务迁移、
安装器和公共执行入口的缺口仍然存在。代码不是已启用的宿主供给：只读预检仍是唯一动作。

### 11.1 授权前绑定服务管理器证据

生产 `BrokerSession` 在发出 `bound` 前，通过固定 `/run/dbus/system_bus_socket` 建立 D-Bus
连接，不读取调用方地址或 `DBUS_*` 环境。当前适配器要求该 socket 的内核对端为 root，随后
核验 `org.freedesktop.systemd1` 的唯一总线身份确由 UID 0、PID 1 持有，之后始终使用该唯一身份。
不满足这个本地总线模型时拒绝，不能默默切换到会话总线或另一个管理器；发行版实测仍待完成。

通过活着的原 socket pidfd 夹住 `GetUnitByPID` 查询，核对单元的 MainPID、ExecMainPID 和单调
开始时间，固定 `InvocationID`，并在执行前持有 `Unit.Ref` 防止单元记录过早回收。只接受
`anas-hostd@*.service`、固定模板路径、root/root、Type=exec、Restart=no、无重启、无 drop-in、
无 transient/DynamicUser/Delegate 及 KillMode=control-group。单元名不能由请求提供。

实际方法、属性和类型以 [systemd D-Bus 接口文档](https://man7.org/linux/man-pages/man5/org.freedesktop.systemd1.5.html)
为依据。客户端使用 `github.com/godbus/dbus/v5 v5.2.2`，不另造 D-Bus 协议实现，不执行或解析
`systemctl`，也不授予 StartUnit/SetProperties 等控制调用。只读取所需属性，不读取环境变量集合。

### 11.2 进程退出、清理和业务终态分别确认

进程 pidfd 结束之后，观察器继续等待**同一个 invocation**进入 inactive/dead 或 failed/failed，
主进程和控制进程均为 0，并取得实际 `ExecMainCode/ExecMainStatus/Result` 与退出时间。
再用 `GetUnitProcesses` 确认该单元没有残留进程，之后重读身份、时间和退出字段以拒绝重启漂移。
查询失败不是空进程集；缺字段、零值、失去管理器身份或重新加载不能变成成功证据。

正常退出 0 与 success 才能匹配成功帧；正常非零退出与 exit-code 才能匹配失败帧。信号、OOM、
timeout/watchdog、流损坏、错误回执、关闭失败或取消均不能从候选成功帧推导成功。这是当前不派生
子进程的预检通道；没有声称它能约束恶意 root 逃离 cgroup 或替代未来包管理器的完整恢复验收。

`HostJobBroker.ExecutePreflight` 把已运行 job 的注册、固定激活请求、broker 握手、实际输出 EOF、
独立退出观察、句柄关闭和共享 `ActionRecorder` 终态串起来。成功记录之前再检查当前角色与任务，
Store 的 pre-commit 检查拒绝等待期间已持久化的取消。终态仍在唯一 job journal 中原子提交并重放，
没有另建状态库。仅声明式 `incushost.Preflight` 可重新计算出的输出和固定错误文本可进入 recorder。

退出或清理无法确认时保留 binding/lease，并以既有 `execution_containment_lost` 记录 unknown；
该 Store 的后续准入和启动继续被阻断。不从 socket EOF 或一份自报 JSON 合成退出码，也不通过
重新构造 broker 来清除阻断。执行者 context 和结果写入独立于 CLI/Web 订阅的连接寿命。

### 11.3 root 程序和发布产物，不自动安装

`cmd/anas-hostd` 新增 `--serve`、`--actions`、`--version`；后两者只报告当前二进制，明确
`installation_verified: false`。服务模式复用固定激活校验和 broker；root 审计使用
`/var/lib/anas-hostd/audit` 下的既有 `audit.Writer`，逐级核对 root 所有祖先，不读取用户 job 路径。
已发出失败帧也必须返回非零进程退出；描述符或审计关闭失败覆盖成功返回。

发布脚本用同一 version/commit/date 编译 `anas-hostd`，并打包候选 `anas-hostd.socket` 与
`anas-hostd@.service`。模板限制 AF_UNIX、无自动重启、单次最长 30 秒、严格只读系统和私有临时目录。
当前唯一保留的 capability 是 `CAP_DAC_OVERRIDE`，用于连接服务账号的 0700/0600 broker 端点；
空 capability 集合会使 root 也无法通过该文件权限检查。它不是任意读写授权：固定路径与编译期
动作限制仍执行，且不得借此增加命令/路径参数；不具备包安装、mount 或网络管理能力。
能力含义见 [capabilities(7)](https://man7.org/linux/man-pages/man7/capabilities.7.html)。

候选单元还需安装策略、`anas` 组、非 root owner 的目录和服务迁移；`install.sh` 当前不安装或
启用它们。没有修改既有 anasd 身份和数据属主，也没有运行 systemd 或真实 root 执行链。
测试、交叉编译和打包检查不代替这项验收，更不代表 Incus 安装、配置、卸载和生产 ingress 已完成。

## 12. 共享队列与可选 HTTP 入队（2026-09-19，代码已接线）

`HostActionService` 属于现有 anasd 的执行所有者，沿用同一个 `consolejobs.Store`、进程级
`ExecutionLease`、`HostJobBroker` 和审计 writer。它不是新数据库或新特权进程。HTTP 适配器
只把已认证 actor、注册 workspace 与幂等键交给 `InvokePreflight`；动作固定为 `incus.status`，
参数固定为空。冻结 release 随原 job 保存，socket 原请求和 broker 继续使用现有 ABI。

服务等私有 listener 就绪后才公开准入。共享 Store 完成 action 级 retry/coalesce；错误不披露
其他 workspace 的冲突 job ID。worker 在领取前核对当前 actor 和冻结请求，失权/旧版本只终结
未启动 job；一旦 Start 已提交则未知执行不能重新排队。成功终态仍由 §11 的 recorder 与退出
证据决定。创建/合流/开始/终态使用 `host_job_transition` 审计，不记录原始参数或凭据。

HTTP 生命周期只覆盖入队；请求或 SSE 断连不取消 owner。显式取消只允许 queued 状态，运行中的
预检没有协作取消协议，不能误走旧 executor 的取消接口。worker 与 queued cancel 竞争时只接受
Store 中同 invocation、从未开始的终态作为取消已胜出；并不重试任何不确定的 running job。
正常停机可能同时收到 runtime 结束与 ctx 取消，只有异常结束才停止准入并报告失败。服务关闭
等待 runtime 收尾，原租约关闭错误传播至 daemon 返回值。启动恢复对宿主 running job 复用
现有 daemon_restarted 持久阻断，即使开关已关闭也不会漏记或自动清除。

`consoleauth.CheckJobOwner` 只给已认证并持久化的任务重新验证 actor，不能用一个 actor 字符串
登录 HTTP。当前 capability 必须 full；本地 owner 只要仍存在就不因浏览器退出/换密码撤销任务。
代理只使用当前安装配置的 issuer/group 与尚未过期的本地代理身份记录，不延长 idle/绝对有效期，
也不是对远端 IdP 的即时角色查询。实际调用和成功提交前均重读本地权限。回调可在 jobs.lock 内
执行，不能反向进入 job Store。

公开接口、默认关闭条件及响应见[服务配置参考](/reference/anasd-service-configuration)。只有显式
准备的非 root Linux release daemon 能启动该队列；默认 root 服务不变，不隐式修改 uid/gid、
目录属主或 Docker 组。`LoadService` 的 0640 配置支持只改变服务配置的安全读取，不扩展 TLS
私钥权限。现有 TLS loader 的 root-owned 私钥政策、实时证书更新、状态所有权和宿主安装必须
共同迁移；当前尚未提供这套可部署流程，不能称为生产已启用。CLI 专用 invoke 和 UI 按钮也未交付。

## 13. 当前装配与验收边界（2026-09-23）

安装策略为 root-owned `0600` 的 `/etc/anas/hostd.json`，使用未发布的 v2 schema，绑定
version/commit、`systemd-root-service` 和固定服务单元。socket 为 root/root `0600`，每次连接
激活一个 `anas-hostd`；执行者通过固定私有 broker 复核共享 job、持有原执行租约，并由 systemd
独立退出状态和空进程集决定是否允许写入终态。普通 root 进程不能只凭自报 PID/单元名准入。

服务身份、invocation、退出码与空进程集全部从 `/run/systemd/private` 读取，连接先以
`SO_PEERCRED` 核对 PID 1/root/root。认证后以 Linux UNIX socket 的实际发送队列排空
作为文本认证与二进制请求的交接边界，最多等待三秒且服从更短的取消/期限；不靠固定延时、
重新发送方法或放宽身份检查收敛。主要测试环境已复现直接紧随 `BEGIN` 的首个请求滞留，
独立原生 socket 回归覆盖未排空、实际排空、取消、超时及连接失效。

短命 executor 的 unit 引用保留与上述证据读取分开：`Unit.Ref/Unref` 要求消息总线客户端
身份，不能在直接 PID 1 连接上冒充成功。固定 `/run/dbus/system_bus_socket` 上的辅助
连接只持有对象引用，避免 unit 被回收，**不提供任何授权、PID、invocation 或退出证据**。
取得引用之后仍从原来经内核核验的 PID 1 连接绑定活进程；完成时仍要求同一 invocation 的
真实退出状态及空进程集，再释放引用。总线拒绝、丢失或错误应答不能代替缺失的 PID 1 证据，
也不能把不确定退出升级为成功。两条连接都不读取 `DBUS_*` 环境或调用方地址；没有增加
特权动作、可写 handler 或第三个 root 程序。

编译动作包括 `incus.status`、install/configure/enroll/uninstall 各自的 plan 与执行动作，
以及 `incus.ingress.observer.plan` / `incus.ingress.observer` 的观察配置交付与撤销；后者同样要求
一次性确认，作用域内容由宿主推导，不能由调用方提交。实现与状态机边界见 Incus 宿主供给 §7.9。
当前建立性动作也要求计划绑定和确认，比原设计“一段即可”的最低要求更保守。确认元数据位于
root-only `/run/anas/confirmations`，原 token 不进入 job/审计，消费与执行 Claim 分开；
五分钟有效期从原 plan 时间起算，同一个过期计划不能通过重新签发延长。成功终态仍需独立退出
证据，连接关闭、进度事件或后端自报成功都不能替代它。

CLI 使用受验证 HTTPS 控制台会话和同一队列，不自行持有第二份 job store 或直接打开 root socket。
控制台通过公开能力区分预检与供给入口：只装配预检不得显示供给能力。页面显示服务端影响步骤，
不要求用户填写参数摘要、计划 ID 或 token；过期后重新计划并重新确认，未知 apply 不自动重试。

长动作预算来自编译期清单，未认证读入仍单独限时。公开进度只包含固定阶段名，不复制包管理器
stdout/stderr。`anas-hostd` 必须有包管理、账户创建、网络配置所需的真实 root 权限；单元保留
`NoNewPrivileges`、`ProtectSystem` 和 `ProtectHome`，显式开放 `/etc`、`/usr`、`/var`、`/run`
及私有临时目录。Debian 官方依赖的 initramfs 触发器还需更新 `/boot` 的 initrd，
所以同版本固定单元以 `-/boot` 提供可选路径例外；不存在时不因此令单元失效，存在时只允许
这个固定系统树，不开放整个根目录，也不跳过包触发器。控制台没有该写路径。
这不是低权限沙箱；安全边界是不可由请求扩展的动作、类型参数、安装身份、
一次性批准、审计、资源归属和退出监督，不能把写路径表宣传为抵抗恶意 root。

固定 root executor 显式保留 `AmbientCapabilities=CAP_SETUID`：主要测试环境中完整服务
保护组合会在 exec 前丢失该 root 能力，导致 APT 的 `seteuid/setresuid` 失败，连官方索引
更新也无法完成。仅补齐这个已授权 root 安装动作需要的能力，保留 `NoNewPrivileges`、
文件系统保护与 socket family 限制，不把能力增加到 anasd，也不配置关闭
APT 自身的沙箱。私有缓存权限可能使 APT 自行回退为 root 下载；安装成功不能据此声称
所有下载都由 `_apt` 执行，独立沙箱下载证明仍须核验实际用户及目录权限。

安装动作的编译执行预算为 2700 秒，供给后端对 APT 每步、Incus 启动、服务查询分别限制，
不再把 30 秒只读查询预算用于完整服务启用。其余动作沿用原预算。broker 的完成等待从已认证
动作派生，另外保留有界审计和协议交换时间；发行包外层 `RuntimeMaxSec=2730s` 与编译清单
做一致性检查，避免只改后端期限而仍由旧单元提前终止安装。这不延长未使用确认的五分钟有效期，
不取消调用方更短的 deadline，也不保证系统管理器接受的作业会随 CLI 取消。超时后仍需保留
不确定副作用的 intent/receipt 和独立退出监督；服务后来 active 不能充作原操作已确认成功。

发行归档包含 hostd 与固定单元；2026-09-30 起不再有控制转发程序，Incus 由 configure 改为直接监听
控制网桥网关（宿主供给设计 §3.9），升级时安装器删除旧版留下的转发单元与程序。升级/卸载在覆盖前
检查活动动作、停止 socket 再复查；未排空时拒绝，
不杀掉进行中的包安装来制造“完成”。确认目录在同一次开机内跨 daemon 重启保留；崩溃遗留
的执行仍需恢复裁决，不因移除临时目录而获得重放权限。

**这些源码和本机夹具不是发布批准。** 实际 systemd 私有 D-Bus、原生身份、发行版包安装、控制
bridge、双栈与 guest 生命周期尚需真实宿主验证。非 systemd 启动、生产 HTTP ingress 的 namespace/
地址生命周期证明和破坏性镜像 prune 仍存在实现或接线缺口，当前不开放这些路径。

### 13.1 受限 HTTP 观察动作（2026-09-21）

编译清单新增 `incus.ingress.observe_http`，只读、root 执行、不要求破坏性确认，30 秒预算，
reject 并发。v3 参数只含逐次调用绑定与已登记 scope/租约/实例/workload/端口；没有自由命令或
endpoint。实际实现由 `internal/incusprovision/ingress_observation*.go` 提供，不接受插件注册。

新动作与供给动作共用审计、执行绑定和终态监督。公有结果投影再次限制为单租约身份，拒绝新增
字段、旧 schema、未知错误原文和 provisional success。宿主审计记录规范参数摘要，不记录
管理证书、私钥、完整 API 结果或工作区文件。`HostObservationInvoker` 共用原队列，每次新建 job，
等待结束前仍检查权限、workspace、invocation 与观察 nonce；调用方不再等待不会取消已归属的执行。

数据源授权来自固定 root scope 与当前已登记工作区，root 持有的管理凭据不交给中介。
该替代路径不是一张新的 daemon 只读证书；scope 自动安装与服务启动尚未交付。
HOSTACT-R-012 五问及验收边界见
[宿主通道计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/host-action-channel.md)
和 [Incus 设计 §7.8](incus-host-provisioning.md)。没有增加第三个特权入口或后台 root 观察服务。

### 13.2 配置执行前排空与正常停机（2026-09-21）

观察配置的确认交付见 [Incus 设计 §7.9](incus-host-provisioning.md)，共享执行器的排空屏障见
该文 §7.11。配置任务在 queued 阶段等待旧中介，不占用 root 执行位置或阻塞清理依赖；真实
broker 授权回调再次检查绑定的排空结果。原确认/审计/退出监督均保留，没有注册新 root 动作。

正常服务取消先拒绝配置写入，保留只读队列、broker 与租约至中介排空。失败需可信所有者显式
重试，不自动放开新启动或续期配置；生产 launcher 和异常跨进程恢复仍未验收。源码接线不代表
现在已经启动了中介或开放 ingress。

## 14. 身份链简化（2026-09-30，当前实现）

依据[设计简化评审](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-28-incus-design-simplification-review.md)
第 4 项：anasd 以 root/root 运行，本身已等价于 root，对一个 root 调用方做进程身份证明不构成安全边界。本节取代
§8.1 的服务身份字段、§9—§11 的回连 broker 与 systemd 退出观察，以及 §13 中私有总线、broker 与单元引用的段落。
动作清单、类型化参数、二段确认、审计以及系统写路径与 anasd 沙箱的分离都不变。

```text
anasd（root，任务已在共享 job store 中开始）
   │ 连接 /run/anas/hostd.sock（root:root 0600），写一条 anas.action/v1 请求并关闭写端
   ▼
systemd Accept=yes → anas-hostd@N.service（root）
   │ 校验激活 fd、安装策略与 socket 节点；SO_PEERCRED 必须是 root/root
   │ 在 /var/lib/anas-hostd/invocations 建立本次调用的记录（同一调用 id 只能开始一次）
   │ 审计、领取确认、执行编译动作
   │ 先把终态写进调用记录，再发出终态帧，退出码与终态一致
   ▼
anasd 用共享 recorder 记录事件流；流在终态前中断时，查询只读动作 host.invocation.status
```

**安装策略 v3。** `/etc/anas/hostd.json` 只剩 `schema` 与 `release`（版本与提交），不再写服务模式、单元名或 socket
属组。socket 由单元固定为 root:root 0600，对端只接受 root/root，不再经 PID 1 私有总线核对调用方属于哪个单元。

**调用记录取代退出证据。** 每次调用在 hostd 自己的 root-only 目录里留下两个文件：

- `<invocation>.lock`：独占创建，同一调用 id 重放会被拒绝；执行期间持有 flock，hostd 被杀时由内核释放；
- `<invocation>.json`：原子替换，先记开始，再记终态。

终态先落盘、后发帧，所以 anasd 收到的终态帧就是这份记录。`host.invocation.status` 是编译进清单的只读动作，参数恒为
`{}`，查询对象就是请求自身的 job 与调用 id：

| 状态 | 含义 | anasd 的处理 |
| --- | --- | --- |
| `absent` | 从未开始 | hostd 读取请求的时限过后仍不存在，任务记为失败（`host_action_not_started`），没有执行 |
| `running` | 锁仍被持有 | 继续轮询，最长到该动作的编译预算 |
| `finished` | 已记录终态 | 以记录的终态结束任务 |
| `lost` | 已开始、没有终态、锁已释放 | 记为未知并保留执行阻断，与原先相同 |

记录保留 30 天；锁未被持有的过期记录在下一次调用开始时清理，每次最多 64 条。

**删除的机制。** 固定私有 broker `/run/anas-job-broker` 与其握手协议、原请求进程的 pidfd 夹持、PID 1 私有总线上的
调用方单元核验、`Unit.Ref` 引用、`GetUnitProcesses` 空进程集与退出码观察，以及 `github.com/godbus/dbus/v5` 依赖。
`anasd.service` 的 `RuntimeDirectory` 只保留 `anas/confirmations`；安装器不再创建 broker 目录，卸载时仍移除早期版本
留下的空目录。同日删除了转发许可（`incus.forwarding.*`）、观察配置与 HTTP 观察（`incus.ingress.observer*`、
`incus.ingress.observe_http`）三组动作：§1 的转发许可评审与 §13.1、§13.2 只作历史记录。

**不变的部分。** 动作 id 与类型化参数、编译清单与 `anas host actions`、二段确认与五分钟 token、确认只由 hostd 领取、
审计、单元的 `RuntimeMaxSec` 与 `KillMode=control-group`，以及非 systemd 的待决事项。

**限制。** anasd 重启时正在运行的宿主任务仍按原规则记为 `daemon_restarted` 并阻断宿主队列，还没有在启动时用调用
记录自动结清。原生门禁改为 `test-env/scripts/test-host-action-native.sh`：调用记录、激活与对端核验的 Linux 用例必须
实际运行。新实现还没有在 Linux/systemd 实机上重跑完整审批门禁。
