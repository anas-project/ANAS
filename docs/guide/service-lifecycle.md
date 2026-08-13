# 服务生命周期

## 部署

`apply` 是正常部署入口。每次成功应用都会生成一个新的不可变 deployment，完成物化后再切换活动状态：

```bash
anas apply -w /srv/anas
anas status -w /srv/anas
```

首次部署或需要更新锁定决策时使用 `--build --update-lock`。

## 日常操作

```bash
anas start -w /srv/anas
anas stop -w /srv/anas
anas restart -w /srv/anas
anas deployments list -w /srv/anas
anas deployments inspect <id> -w /srv/anas
```

对具名 Module 执行生命周期命令时，Runner 会自动扩展依赖或被依赖关系，并按照安全顺序处理整个链。

## 回滚

deployment 回滚解决“发布制品或配置有问题”，数据快照恢复解决“持久数据已经被改变”。两者不是同一个操作：

```bash
anas rollback <deployment-id> -w /srv/anas
anas snapshot restore <snapshot-id> -w /srv/anas
```

这类替换操作只接受显式 `-w`，以降低命令指向错误 workspace 的风险。执行前先阅读[备份与恢复](backup-and-restore.md)。
