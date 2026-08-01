# anas 使用指南

*[English version](usage.md)*

这是面向任务的指南：怎么建起一套部署、怎么日常运行、怎么改、出问题怎么救回来。
每条命令产出的确切 JSON 与退出码见 [contracts/](contracts/README.md)。

---

## 一、workspace

一套部署拥有的全部内容都在一个目录里：

```text
<workspace>/
  config.yml          期望状态 —— 唯一需要你手工编辑的文件
  config.lock.yml     解析出的 cask 版本与能力绑定
  data/               业务数据
  snapshots/          时间点副本
  .anas/              运行时状态（0700，里面的东西不要手工改）
```

**恢复这套部署不需要这个目录之外的任何东西。** 这是布局存在的理由，也是 `data/`
没有可配置位置的原因：一旦可配置，自足性就变成了"只要没人挪过它才成立"。数据要放
大盘，就把整个 workspace 放过去。

但"自足"不等于"打个包就行"。**请用 `anas backup`**（第七节），不要直接复制目录：

- `snapshots/` 是 `data/` 的副本。btrfs 上它们共享 extent、几乎不占额外空间，但
  `cp` 和 `tar` 是逐文件读出再写入的，所以 1.3 GB 数据的五个快照会**实打实展开成
  约 6.5 GB 归档**——而这些数据已经在 `data/` 里有一份了。
- `.anas/` 绝大部分可重建。线上实测 402 MB，其中真正不可重建的只有 **52 KB**
  （`state/` 和 `secrets.generated.yml`），活动制品再加 42 MB，剩下约 360 MB 是历史
  deployment 和构建缓存。
- 服务运行中复制出来的副本，最好也只是崩溃一致的。`anas backup` 会在拍快照的那一刻
  把它们停下来。

`anas backup` 已经知道该收哪些、不该收哪些。"单目录"是**使这件事成为可能的性质**，
不是"请手工打包这个目录"的指示。

### 命令怎么找到 workspace

按顺序：`-w` 参数、`$ANAS_WORKSPACE`、当前目录——但**当前目录必须已经含有
`.anas/`** 才算数。

任何命令都不会凭空创建 workspace。一次打错的 `cd` 不该悄悄长出第二套平行部署，
所以 `anas init` 是唯一会创建它的命令。

**`rollback`、`snapshot restore`、`backup restore` 只接受 `-w`。** 它们是会替换
东西的操作，而残留在 shell 配置里的环境变量，是把破坏性命令指向错误部署的最容易
的途径。

---

## 二、第一次运行

```bash
anas init /srv/anas
```

建立上面的目录结构并写出 `config.yml` 骨架。在 btrfs 上还会把 `data/` 建成
subvolume——快照就是从它拍的。不在 btrfs 上时会说明哪些能力将不可用，并等你确认。

`init` 会打印一行 `export ANAS_WORKSPACE=…`，但**不会写进任何文件**。要写进
shell 配置得显式要求：

```bash
anas init /srv/anas --shell-init write     # 幂等，带标记块
anas init /srv/anas --shell-init remove    # 撤销
```

默认不写是因为 `ANAS_WORKSPACE` 是机器级全局的：一旦进了配置文件，无论 `cd` 到
哪都以它为准，正好把目录解析要避免的"在错地方跑对命令"重新引了回来。而且它对
cron 和 systemd 单元不生效。

### cask 定义从哪来

cask 是**程序的一部分，不是部署的一部分**——和 `anas` 二进制同类，因此刻意不复制进
每个 workspace。把它们装在二进制旁边：

```text
/opt/anas/
  bin/anas
  casks/mods/…
```

这个布局下**什么都不用配**：`anas` 会在自己可执行文件旁边找到 casks，无论从哪个目录
运行。直接在源码检出里运行也是同理。

如果二进制装在别处（比如 `/usr/local/bin/anas` 而 casks 在另一处），指一次即可：

```bash
export ANAS_ROOT=/opt/anas
```

而且只有一部分命令需要它们：

| 需要 casks | 不需要 |
| --- | --- |
| `plan` `lock` `render` `build` `apply` | `init` `status` `start` `stop` `restart` |
| `config set` `config explain` `config plan` | `deployments` `rollback` `snapshot *` `backup *` |

这条分界是**改东西 vs 跑东西**。渲染好的 deployment 自带了启动所需的一切——这正是
把 workspace 恢复到一台裸机上、casks 根本不在场也能起来的原因。渲染一个**新的**才
需要那些定义。

找不到时会报 `could not locate project root containing casks/mods` 或
`could not locate cask bundle directory`。`ANAS_ROOT` 两者通吃；`--root`、
`--cask-root`、`ANAS_CASK_ROOT` 是单次调用的形式。

### 启动

编辑 `<workspace>/config.yml`，然后：

```bash
anas apply --build --update-lock -w /srv/anas
```

`--build` 构建镜像；`--update-lock` 把解析出的 cask 版本写进 `config.lock.yml`。
首次之后两者都不需要，除非版本有变。

---

## 三、日常运行

```bash
anas status                 # 当前活动的是哪个，验证是否通过
anas start
anas stop
anas restart
anas deployments list
anas deployments inspect <id>
```

以上都接受 `-w`，或者直接在 workspace 目录里运行；也都**不需要 cask 树**——它们读的
是已经渲染好的 deployment。

---

## 四、修改配置

直接编辑 `config.yml`，或者用 `config` 系列命令——它们要读 cask 定义，因此需要设好
`ANAS_ROOT`（见第二节）：

```bash
anas config set core.timezone Europe/Berlin -w /srv/anas
anas config explain nextcloud.domain_prefix        # 改它的代价是什么
anas config plan -w /srv/anas                      # 当前待应用的改动会做什么
```

然后应用：

```bash
anas apply -w /srv/anas
```

每次 apply 产出一个**新的不可变 deployment**，上一个仍留在磁盘上——这正是回滚
得以可能的原因。

有些设置无法靠普通重启生效，因为它们改的是服务**内部**的状态：存在 LDAP 目录里
的口令、需要迁移的数据库。`apply` 会以退出码 4 拒绝并指名是哪一项。安排好迁移之后
用 `--allow-risky` 继续。

---

## 五、出问题的时候

这是最值得认真区分的一处。

| 情形 | 命令 | 数据会怎样 |
| --- | --- | --- |
| 配置改坏了服务，数据是好的 | `anas rollback` | **不动** |
| 数据本身出了问题——升级失败、迁移搞砸 | `anas snapshot restore <id>` | **回退** |

```bash
anas rollback -w /srv/anas              # 回到上一个 deployment
anas rollback <deployment-id> -w /srv/anas
```

`rollback` 只切换制品，**永不触碰数据**。这一点很重要，因为最常见的情况就是
"我把配置改坏了"——用回退数据来回答它，等于把上次 apply 之后写入的一切都扔掉。

回退数据只有 `snapshot restore` 一条路，它把配置、锁、密钥、状态、制品、数据
一并放回同一个时间点。

### 被拒绝的回滚

如果一次回滚要跨过某个"改写了磁盘数据格式"的 cask 版本，它不可能成功：旧代码读
不了新数据。`anas` 直接拒绝，**不提供 `--allow-risky` 绕过**，并指向应该恢复的
快照。

如果 cask 对自己的数据格式什么都没声明，版本变化一律按"未知"阻断，可用
`--allow-risky` 覆盖。

---

## 六、快照

快照是**本机的、瞬时的，为撤销一次变更而存在**。需要 btrfs。

```bash
anas snapshot create --label "升级前"
anas snapshot list
anas snapshot show <id>
anas snapshot path <id>          # 打印只读数据路径，用于捞单个文件
anas snapshot restore <id> -w /srv/anas
anas snapshot verify             # 记录在案的快照是否还在
```

`verify` 值得挂进 cron。备份最常见的失效方式，是有人手工删掉了底层子卷，而在真正
需要它的那天之前没有任何人发现。

### 自动快照

`apply` 会在无法撤销的变更之前自己拍一张：

- cask 升级跨过了该 cask 声明为"改写数据格式"的版本
- 某项设置的 effect 是 `data_migrate` 或 `credential_rotate`

例行 apply **不会**拍快照。拍了会让保留槽位被普通配置编辑填满，把真正要紧的那张
挤掉。

```bash
anas apply --snapshot        # 强制拍一张
anas apply --no-snapshot -y  # 跳过；必须带 -y，因为这是在放弃唯一的退路
```

### 保留策略

`config.yml` 里的 `rollback.snapshot.keep_auto`，默认 5。手动创建的和 pin 住的
既不计入数量，也不会被回收。

```bash
anas snapshot pin <id>
anas snapshot prune --dry-run    # 永远先看一眼
anas snapshot prune --keep 3
```

---

## 七、备份与灾难恢复

备份是**把快照送到别处**，这样它能在磁盘损坏后幸存。

规划之前先问清楚这台机器实际能做什么：

```bash
anas backup capabilities --to /mnt/backup
```

它会报告四种模式各自能否运行，不能运行的给出枚举化的原因——"文件系统不对"和
"读不到数据"的补救办法完全不同。

| 模式 | 需要 | 说明 |
| --- | --- | --- |
| `snapshot` | 目标与源在同一个 btrfs 上 | 最快，但副本就在你要防的那块盘上——因此永远不会被推荐 |
| `send` | 两端都是 btrfs，**需要 root** | 支持 `--parent` 增量 |
| `send-file` | 源是 btrfs，**需要 root** | 产出流文件，只能还原到 btrfs |
| `copy` | 目标可写，**且数据全部可读** | 到处都能用；非 btrfs 上唯一可用的模式 |

```bash
anas backup plan --to /mnt/backup          # 只说会做什么，不做
anas backup create --to /mnt/backup
anas backup list   --to /mnt/backup
anas backup verify --to /mnt/backup        # 这个也挂 cron
```

容器只在**拍快照期间**停机，随后立即启动，再进行传输。所以停机时长由拍快照的
耗时决定（秒级），而不是由数据量决定。备份中途失败时服务会被重新启动，这个补偿
在进程被强杀之后依然生效。

### 恢复到一台新机器

```bash
anas init /srv/anas                 # workspace 必须先存在
anas backup restore --from /mnt/backup -w /srv/anas
anas start -w /srv/anas
```

恢复出的 workspace 自带制品，因此**不需要 cask 源码树**就能启动。但仍需从
registry 拉取上游基础镜像。

---

## 八、哪些操作需要 root，以及为什么

`anas` 以普通用户身份运行。有三类操作不行，它会如实报告而不是自行提权：

| 操作 | 原因 |
| --- | --- |
| 回收快照（`delete`、`prune`） | `btrfs subvolume delete` 需要 `CAP_SYS_ADMIN`，除非文件系统以 `user_subvol_rm_allowed` 挂载 |
| `send` / `send-file` 备份 | `btrfs send` 需要 root |
| 对已经跑过的部署做 `copy` 备份 | 容器以 root 身份写数据，非特权读不到 |

第一条和第三条的根因是同一个：**容器数据归 root**。所以回收或读取它的任何一份
副本都需要特权，跟用不用快照无关。

实际做法是用 systemd timer 以 root 跑那些定时操作——它们本来就是要定时的：

```ini
# anas-prune.service / anas-backup.service，配对的 .timer 单元触发
ExecStart=/usr/local/bin/anas snapshot prune -w /srv/anas -y
ExecStart=/usr/local/bin/anas backup create -w /srv/anas --to /mnt/backup -y
```

以 `user_subvol_rm_allowed` 挂载也可行，但它作用于**整个文件系统**且无法收窄到
单个子卷，因此它被写成可选项而不是要求。

---

## 九、没有 btrfs 的情况

除了依赖快照的部分，其余都能用：

- 没有 `snapshot` 系列命令，也没有升级前的自动快照
- 没有"数据可回退"这层保护——但 `rollback` 本身照常工作
- 备份只有 `copy` 模式，而服务跑起来之后它需要特权

`anas init` 会在你还来得及换一块盘的时候把这些讲清楚。

---

## 十、脚本调用

每条命令都接受 `--json`：

- stdout 上**恰好一个** JSON 文档，不需要过滤即可解析
- 进度、警告、日志走 stderr，格式是 JSON Lines
- 退出码取自固定的表

| 码 | 含义 |
| --- | --- |
| 0 | 成功 |
| 1 | 工作已经开始并失败了 |
| 2 | 命令行写错了 |
| 3 | 需要确认但无法取得确认 |
| 4 | 机器不在能执行这件事的状态 |

`code` 和 `reason` 字段是固定枚举。`message` 是给人看的，**不要解析它**。不带
`--json` 时的输出是散文，不构成契约。

非交互调用方应在需要确认的地方带 `-y`；不带且没有终端时，命令会立刻以退出码 3
失败，而不是阻塞等待一个没人会给的输入。

完整细节见 [contracts/README.md](contracts/README.md)。

---

## 十一、常见提示

**`… is not a workspace: no .anas/ directory`** —— 你不在 workspace 里。加 `-w`、
`cd` 进去，或者 `anas init`。

**`could not locate project root containing casks/mods`** / **`could not locate
cask bundle directory`** —— 这条命令需要 cask 定义。设置 `ANAS_ROOT`，见第二节。

**`anas rollback requires an explicit -w`** —— 设计如此，见第一节。

**`no backup mode can run against …`** —— 跑 `anas backup capabilities --to …`，
它会逐个模式说明原因。

**`cannot delete Btrfs subvolume …: needs CAP_SYS_ADMIN`** —— 快照回收，见第八节。

**`crosses data-breaking version …`** —— 你要的这次回滚不可能成功，改用恢复快照。
见第五节。
