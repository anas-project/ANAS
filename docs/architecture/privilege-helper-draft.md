# 特权操作与 helper（草案）

> **状态：§3–§4 的网络部分已实现**（`cmd/anas-helper`），sudoers 与生成脚本已删除。
> §5 的流式 `btrfs send` 和 §4 里的 `subvolume delete` 仍是草案。
>
> 现状原本是一处隐式 `sudo`（macvlan 桥）加若干处显式的"权限不够就报错"。本文说明
> 把前者收进一个受限 helper 的做法，以及为什么**不是**所有特权操作都该跟着进去。

## 1. 现状清单

| 操作 | 实际需要 | 现在怎么做 |
| --- | --- | --- |
| 建/删 `anas_bridge`、装路由 | `CAP_NET_ADMIN` | `anas-helper`（root 拥有、`setcap cap_net_admin+ep`）。**已实现**；此前是 `sudo sh <base>/anas_service.sh` |
| `btrfs send`（backup `send` / `send-file`） | `CAP_SYS_ADMIN` | 读 `/proc/self/status` 的 `CapEff` 探测，不够就报 `insufficient_privilege` |
| `btrfs receive`（restore `send-file`） | 两端都要 `CAP_SYS_ADMIN` | 显式报错，要求以 root 运行 |
| `btrfs subvolume delete`（`delete`、`prune`、apply 后的保留策略、清理中断的 create） | `CAP_SYS_ADMIN`，除非挂载带 `user_subvol_rm_allowed` | 显式报错 + remount 建议 |
| `rsync -aHAX --numeric-ids`（backup `copy`） | 读其他 UID 的数据、恢复属主 | 探测期报"部分数据这个用户读不了" |
| `btrfs subvolume create` / `snapshot` | **不需要特权** | — |
| docker / docker compose | docker 组或 socket 访问权，不是 root | — |
| `cp --reflink`、`ip -4 route show`、go、git | 不需要 | — |

两条容易记反的事实：

- **创建和快照 subvolume 不需要特权，删除需要。** 后果是一个工作区可能能建快照却回收
  不掉空间，只涨不落。`describeSubvolumeDeleteFailure` 就是为这个不对称存在的。
- `btrfs subvolume show` 需要 `CAP_SYS_ADMIN`，所以它不能用作前置检查——
  `btrfsSubvolumeShow` 改用"在 Btrfs 上 + inode 号为 256"来判断，无需任何特权。

## 2. 分界原则：产物是不是特权产物

`internal/runner/backup.go` 里已经写下了这条立场，本设计沿用它而不是绕开它：

> anas 从不自己提权。shell 到 sudo 会把 root 拥有的文件留在 `.anas/state/` 里，并让
> 特权路径变成隐式的。

由此得到判据——**一次提权是否产生了用户事后要面对的特权产物**：

- **不产生**：宿主级、部署自有、幂等、不落盘的操作。macvlan 桥是典型：它不生成文件，
  不碰用户拥有的任何东西，删掉再建一次没有区别。这类隐式提权是安全且不可见的，
  用户不需要知道它发生过。
- **产生**：备份与恢复。以 root 跑 rsync 恢复属主，产物**必须**是一棵多 UID、root
  拥有的树；`btrfs receive` 按定义就是以 root 创建 subvolume 并设属主。把这类提权
  藏起来，用户会在自己的工作区里发现一堆动不了的文件。这类必须保持显式，由操作者
  选择。

代码里已经预设了正确的授权机制：`detectSysAdmin` 特意读 `CapEff` 而不是只看 uid，
注释点名 systemd 的 `AmbientCapabilities` 是"只授这一个特权给备份 timer"的方式。

## 3. helper 的形态

一个小二进制 `anas-helper`，只做几件具名的事，**不接受任意脚本或命令**。已实现：

```
anas-helper bridge up   --parent <iface> --name <bridge> --address <cidr> [--route <cidr>]...
anas-helper bridge down --name <bridge>
```

计划中（见 §4）：

```
anas-helper subvolume del --path <path>
anas-helper btrfs send    --subvolume <path> [--parent <path>]   # 输出到 stdout，见 §5
```

参数在 helper 内部校验：接口名必须匹配 `^anas[a-z0-9_]*$`，路径必须落在工作区内，
地址必须是带前缀长度的合法 IPv4 CIDR（`ip addr add 192.168.1.50 dev x` 是合法的且
意为 `/32`，用在桥地址上会静默产生一个到不了容器的接口）。这是它和今天那个方案的**实质区别**——今天 sudoers 授权 root
执行一个**位于用户可写目录、内容由 anas 自己生成**的脚本，[runbook](../operations/runbooks/privileged-helper.md)
自己标注了这是弱点。对运行 anas 的用户来说，那已经约等于 root。

### 3.1 授权机制

两种，取决于调用场景：

| 机制 | 适用 | 关键性质 |
| --- | --- | --- |
| file capability（`setcap cap_net_admin+ep`） | 交互式 `anas apply` | **不传给子进程**。execve 后 ambient 集是空的，所以 helper 要么用 netlink 在进程内做完，要么自己把能力抬进 ambient 集 |
| systemd `AmbientCapabilities=` | 开机 unit、备份 timer | **会传给子进程**，所以 shell 调 `ip`/`btrfs` 依然可用 |

**这个区别决定 helper 怎么写。** 实现选择的是第三条路：helper 自己把 `CAP_NET_ADMIN`
抬进 ambient 集（`capset` 加 inheritable，再 `prctl(PR_CAP_AMBIENT_RAISE)`），然后
exec `ip`。这样两个场景共用一个实现，而且不必引入 netlink 绑定——本仓库只有四个直接
依赖，为此再加一个不划算。capget/capset 的结构体是内核 UAPI，直接声明在
`cmd/anas-helper/apply_linux.go` 里。

`ip` 从一组绝对路径里查找，不走 `PATH`：它即将带着 `CAP_NET_ADMIN` 运行，而 `PATH`
属于调用方——正是这个设计不想扩大权限的那一方。

不采用 docker 那种"root 守护进程 + 用户组"的模型：`docker` 组等价于 root（`docker run
-v /:/host --privileged` 即可拿下整机），而 anas 真正需要的只有 `CAP_NET_ADMIN` 一个
能力。用等价于 root 的组去换它不成比例，也与本仓库一贯收紧特权的方向相反。

### 3.2 安装与升级

`install.sh` 已经有 sudo 分支往 `/usr/local/bin` 装二进制，多两步（**已实现**）：

```sh
install -m 0755 anas-helper /usr/local/lib/anas/anas-helper
setcap cap_net_admin+ep /usr/local/lib/anas/anas-helper
```

发布归档里同时打包 `anas` 和 `anas-helper`（`scripts/ci/build-anas-release.sh`），
安装器在 helper 存在时才装它，所以旧版本归档也能被新安装器处理。

**升级必须重新 setcap**：替换二进制会丢掉 xattr，而失去能力后的失败模式是"桥建不
起来"，离原因很远。安装器负责这件事；`setcap` 不可用时它会装好二进制并打印那一行
让人手工执行。

某些文件系统（部分 NFS、noxattr 挂载）不支持 file capability。那种环境退回 systemd
unit 方案或保留 sudoers。

## 4. 收编范围

| 操作 | 进 helper | 理由 |
| --- | --- | --- |
| 桥、路由（`CAP_NET_ADMIN`） | ✅ | 不产生产物，幂等，宿主级 |
| `btrfs subvolume delete` | ✅ | 不产生文件，只回收空间；性质接近网络类。而且现在的失败模式很糟——快照能建不能删，空间只涨不落 |
| `btrfs send`（流式，见 §5） | ✅ | 产物由非特权父进程写，属主天然正确 |
| `btrfs receive` | ❌ | 按定义以 root 创建 subvolume 并设属主，产物就是特权产物 |
| `rsync` 恢复属主（copy 模式） | ❌ | 同上：产物必须是多 UID、root 拥有的树 |

后两项保持现在的显式门禁：探测、报 `insufficient_privilege`、由操作者决定以 root
运行或授予 `CAP_SYS_ADMIN`。

## 5. 流式 `btrfs send`

`btrfs send` 需要 `CAP_SYS_ADMIN`，但它的**输出**是一段字节流，不必由特权进程落盘：

```
helper（持 CAP_SYS_ADMIN）  ──stdout──>  anas（普通用户）──> 写入备份文件
```

这样三件事同时成立：产物属主正确、主进程不需要 `CAP_SYS_ADMIN`、backup.go 那条
"提权不得产生 root 文件"的原则一点没破。

`send-file` 模式直接适用。`send`（直接管到 `receive`）不适用，因为接收端本身就是特权
操作——那条路径继续走显式授权。

## 6. 开机 unit

同一个 helper 顺带解决另一个缺口：**`anas_bridge` 活不过重启**。它是 `ip link add`
建的宿主接口，重启就没了，而仓库里没有任何开机集成会重建它。重启后容器靠 restart
policy 起来、在局域网上可访问，但宿主↔容器不通，容器因此解析不了 DC 的 DNS，
winbind 和 Kerberos 会退化。

一个 oneshot unit（`After=network-online.target`）在开机时调用同一个 helper 重建桥和
路由即可。地址从制品的 `.global.env` 读，与部署保持一致。

## 7. 不变量

写成硬约束，因为"anas 配置网络"和"anas 可能把机器搞断网"之间只隔着这一条：

**anas 只创建和修改它自己命名的接口（`anas*`），永不触碰宿主自身的地址、默认路由和
resolver 配置。** 已经是 helper 参数校验里的硬性检查，两个方向都是，并有单元测试
逐个断言它拒绝 `eth0`、`lo`、`docker0` 一类的名字——不再是留在代码习惯里的约定。

## 8. 迁移

1. **已完成**：`ensureMacvlan` 改为调用 helper，生成脚本的整段逻辑删除，`base` 参数
   随之从 `ensureMacvlan`/`removeMacvlan` 上去掉。
2. **已完成**：runbook 重写为 helper 的安装与用法，并说明旧的 `/etc/sudoers.d/anas`
   和遗留的 `anas_service.sh` 可以删除。
3. **未做**：`NETWORK_NAMESPACE_PATH` 那条隔离测试路径仍用 `sudo nsenter`。让 helper
   接受 `--netns` 并自己 setns 需要 `CAP_SYS_ADMIN`，那只会把特权换个地方要，所以
   保留现状——它只影响测试环境。

## 9. 待验证

1. **file capability 与 ambient 抬升在真实 Linux 宿主上的端到端行为**——这是最关键的
   一条，本机没有 Linux 宿主，只验证了交叉编译和单元测试。要确认的是：`setcap` 之后
   非 root 用户执行 helper 能建出接口，且 `ip` 确实继承到了能力。
2. file capability 在目标发行版的 `/usr/local/lib` 下是否可靠（xattr 支持、各发行版
   打包工具是否保留）。
3. 流式 send 的吞吐是否受管道影响（预计不受，`btrfs send` 本来就写 stdout）。
4. `subvolume delete` 收进 helper 后，`user_subvol_rm_allowed` 那条提示是否还需要保留
   （helper 可用时不需要，但 helper 未安装时仍要）。
