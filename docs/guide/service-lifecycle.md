# 服务生命周期

## 部署

`apply` 是正常部署入口。每次成功应用都会生成一个新的不可变 deployment，完成物化后再切换活动状态：

```bash
anas module update -w /srv/anas  # 首次部署或明确更新 Module release
anas apply -w /srv/anas
anas status -w /srv/anas
```

`module update` 解析远程 Module、能力绑定和宿主机策略并更新 lock；普通 `apply` 使用既有
lock，不会升级 Module。只有源码开发使用本地 Module 覆盖时才按需添加
`--build --update-lock`。

## 日常操作

```bash
anas start -w /srv/anas
anas stop -w /srv/anas
anas restart -w /srv/anas
anas deployments list -w /srv/anas
anas deployments inspect <id> -w /srv/anas
```

对具名 Module 执行生命周期命令时，Runner 会自动扩展依赖或被依赖关系，并按照安全顺序处理整个链。

声明 `before_stop` 的 Module 会先执行原部署的清理钩子，再停止容器和移除网络。Forgejo
使用它先停止原 Actions controller，等待运行作业及 Runner 注册的清理完成，让 API 与
compute 连接在清理期间继续可用。清理钩子只接收原部署的私有投影，不获取候选部署的
新 Secret Store 凭据。清理失败会阻止继续拆除依赖，也不会通过 apply 恢复
自动重启失败的 controller。备份遇到未确认清理时保留事务标记并暂停自动恢复；应先核对
失败模块与残留资源，不能通过删除标记或重写状态把失败改成成功。
备份的停止记录必须在数据与目录同步成功后才允许清理；存储同步失败会阻止继续执行，
不会把未持久化的记录当作安全恢复依据。

完整级管理控制台也可执行 `start`、`stop` 和 `restart`。选择具名 Module 后，浏览器先向服务端请求
预览；页面展示当前活动 deployment 中由 Runner 展开的完整有序链，只有确认这条实际链后才创建持久
任务。空选择表示整个活动 deployment；如果 deployment、摘要或依赖图在确认前变化，服务端拒绝旧
预览并要求重新生成。控制台中的运行态和健康来自实时 Compose 探测，不从 deployment 状态文件推断。

运行中的生命周期任务只在服务端声明的安全阶段接受取消。取消会终止整个外部命令进程组，并在任务
终态后执行补偿检查；已经进入不安全阶段的任务会拒绝取消，而不是伪装成已停止。

## Module 管理

完整级管理控制台的 Module 管理页把四类状态放在一起展示：期望配置中的选择状态、不可变 Module
视图中的安装版本、活动 deployment 冻结的版本与入口地址，以及实时 Compose 运行态、健康和容器数。
管理入口来自活动 deployment 物化时冻结的公开 HTTP(S) 地址；页面不会从当前配置重新推导地址，也不
暴露宿主机路径。

“启用/禁用”只通过强配置 ETag 修改期望配置，不会隐式 apply；配置在任务执行前已变化时，旧操作会被
拒绝。目录更新和按 lock 同步同样创建持久、幂等、每 workspace 串行的任务。更新完成后仍需单独生成
plan 并确认 apply，运行环境才会改变。CLI 的对应流程保持不变：

```bash
anas module list -w /srv/anas
anas module update -w /srv/anas
anas module sync -w /srv/anas
```

## 回滚

deployment 回滚解决“发布制品或配置有问题”，数据快照恢复解决“持久数据已经被改变”。两者不是同一个操作：

```bash
anas rollback <deployment-id> -w /srv/anas
anas snapshot restore <snapshot-id> -w /srv/anas
```

这类替换操作只接受显式 `-w`，以降低命令指向错误 workspace 的风险。执行前先阅读[备份与恢复](backup-and-restore.md)。

## 临时存储与路径切换

Module 通过 `temporary_directories` 显式声明可丢弃目录。默认根为 `<workspace>/tmp`；可用 `global.temp_path` 指定绝对路径。Runner 在启动前登记租约、按声明设置权限，并核验 Docker 实际绑定。空间或文件系统身份无法核验时拒绝分配；已有临时内容保留。

解析后的根路径改变时，`plan` 的 `temp_switch` 展示全部停止与启动范围以及会话中断影响。`apply` 先预检目标，再按旧依赖逆序停止全部 Module，并按目标依赖正序重建启动。旧内容不会复制；正常停止、现有启动链或挂载核验失败时保留旧树并尝试恢复。成功后清理本 workspace 已释放目录，清理失败会保留新服务运行并登记重试。

已提交切换的旧目录清理失败会记录为 `cleanup_deferred` 并报告 `temp_cleanup_pending`，后续停启、应用配置和再次切换仍可执行。旧目录租约继续保留，可在故障解除后显式 GC；未提交切换、未确认的停止钩子或目标挂载核验失败仍阻止自动恢复。普通定向启动只预检选中模块及其依赖范围；定向重启在停止前完成相同范围的临时存储预检。

```bash
anas temp status -w /srv/anas
anas temp gc --dry-run -w /srv/anas --json
anas temp gc -w /srv/anas
```

GC 强制显式 `-w`；运行或已停止容器仍有绑定时不会删除，Docker 查询失败时也会保留。中断的切换需先通过 `apply` 或 `start` 对账；GC 不会隐式启动服务。运行状态中的 `temp_storage` 会显示不足或不可用，并覆盖成功的健康探针；条件恢复后刷新状态，不触发重启。Btrfs 没有固定 inode 池，inode 余量显示“不适用”。

控制台的“刷新运行状态”重新查询当前 workspace；查询恢复后清除此前的请求错误。切换 workspace 时，较早查询的结果不会覆盖当前页面。

历史回滚的临时根不同时，使用历史冻结配置、lock 和 Module 制品生成新的 deployment，再复用上述切换流程。历史制品和历史临时内容不会恢复或改写。

备份恢复到另一 workspace 或克隆后的外来冻结制品只用于诊断与配置恢复，不授予源 project 的运行权限。先执行不带 `--deployment` 的 `anas apply`，生成目标制品及新租约；此前的生命周期、历史回滚与对应预览拒绝操作源制品。受管临时部署必须保留有效的冻结 `DATA_PATH` 绑定；无绑定或绑定无效时不能推断授权。同 workspace 快照恢复和有效本地租约保持原授权。
