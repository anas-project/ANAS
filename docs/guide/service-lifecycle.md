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
