# 特权操作与 helper（草案）

> **状态：草案，未实现。** 现状是一处隐式 `sudo`（macvlan 桥）加若干处显式的
> "权限不够就报错"。本文提出把前者收进一个受限 helper，并说明为什么**不是**所有
> 特权操作都该跟着进去。

## 1. 现状清单

| 操作 | 实际需要 | 现在怎么做 |
| --- | --- | --- |
| 建/删 `anas_bridge`、装路由 | `CAP_NET_ADMIN` | `sudo sh <base>/anas_service.sh`（`internal/runner/network.go`）——**全仓库唯一一处隐式提权** |
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

一个小二进制 `anas-net`（暂名），只做几件具名的事，**不接受任意脚本或命令**：

```
anas-net bridge add    --parent <iface> --name <bridge> --address <cidr>
anas-net bridge del    --name <bridge>
anas-net route add     --dev <bridge> --to <addr>/32
anas-net subvolume del --path <path>          # 见 §4
anas-net btrfs send    --subvolume <path> [--parent <path>]   # 输出到 stdout，见 §5
```

参数在 helper 内部校验：接口名必须匹配 anas 自己的命名前缀，路径必须落在工作区内，
地址必须是合法 CIDR。这是它和今天那个方案的**实质区别**——今天 sudoers 授权 root
执行一个**位于用户可写目录、内容由 anas 自己生成**的脚本，[runbook](../operations/runbooks/macvlan-sudoers.md)
自己标注了这是弱点。对运行 anas 的用户来说，那已经约等于 root。

### 3.1 授权机制

两种，取决于调用场景：

| 机制 | 适用 | 关键性质 |
| --- | --- | --- |
| file capability（`setcap cap_net_admin+ep`） | 交互式 `anas apply` | **不传给子进程**。execve 后 ambient 集是空的，所以 helper 不能再 shell 出去调 `ip`——必须用 Go 的 netlink 在进程内做完 |
| systemd `AmbientCapabilities=` | 开机 unit、备份 timer | **会传给子进程**，所以 shell 调 `ip`/`btrfs` 依然可用 |

**这个区别决定 helper 怎么写。** 为了两个场景共用一个实现，helper 应当在进程内做
netlink，不依赖 ambient 传递。

不采用 docker 那种"root 守护进程 + 用户组"的模型：`docker` 组等价于 root（`docker run
-v /:/host --privileged` 即可拿下整机），而 anas 真正需要的只有 `CAP_NET_ADMIN` 一个
能力。用等价于 root 的组去换它不成比例，也与本仓库一贯收紧特权的方向相反。

### 3.2 安装与升级

`install.sh` 已经有 sudo 分支往 `/usr/local/bin` 装二进制，多两步：

```sh
install -m 0755 anas-net /usr/local/lib/anas/anas-net
setcap cap_net_admin+ep /usr/local/lib/anas/anas-net
```

**升级必须重新 setcap**：替换二进制会丢掉 xattr，而失去能力后的失败模式是"桥建不
起来"，离原因很远。安装器要负责这件事，并且 anas 启动时应当能自检（读
`/proc/self/status` 之外，也可以直接尝试并给出准确的错误）。

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

**anas 只创建和修改它自己命名的接口（`anas_bridge`），永不触碰宿主自身的地址、
路由表默认路由和 resolver 配置。** 今天的脚本已经守着这条线；helper 应当把它变成
参数校验里的硬性检查，而不是留在代码习惯里。

## 8. 迁移

1. helper 落地并由安装器 setcap 之后，`ensureMacvlan` 从 `sudo sh <script>` 改为直接
   调用 helper；生成脚本的整段逻辑删除。
2. sudoers 文件与 [runbook](../operations/runbooks/macvlan-sudoers.md) 一并删除，文档
   改为说明 helper 的安装与自检。
3. `NETWORK_NAMESPACE_PATH` 那条隔离测试路径需要保留一种进命名空间的方式；helper 可
   以接受 `--netns <path>` 并自己 setns，从而不再需要 `sudo nsenter`。

## 9. 待验证

1. file capability 在目标发行版的 `/usr/local/lib` 下是否可靠（xattr 支持、以及各发行版
   打包工具是否保留）。
2. Go 的 netlink 实现能否覆盖脚本现有的全部操作：macvlan 接口创建、地址替换、清理
   残留地址、`/32` 路由。
3. helper 自己 `setns` 进网络命名空间是否需要 `CAP_SYS_ADMIN`（很可能需要，那么隔离
   测试环境仍要额外授权，这只影响测试环境）。
4. 流式 send 的吞吐是否受管道影响（预计不受，`btrfs send` 本来就写 stdout）。
5. `subvolume delete` 收进 helper 后，`user_subvol_rm_allowed` 那条提示是否还需要保留
   （helper 可用时不需要，但 helper 未安装时仍要）。
