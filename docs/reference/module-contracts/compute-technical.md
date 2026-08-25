> 本页由 Contract 技术文档生成，请勿直接编辑。

# compute Contract 技术说明

## 生命周期

`compute` 1.x 只定义一次性 VM 的 `create`、`inspect`、`start`、`exec_stdin`、`stop`、`delete` 和
`list_managed`。调用方只传稳定 instance identity、固定镜像 fingerprint 和数值资源上限；Provider
拥有 profile、network、storage pool、证书和 project policy，不接受调用方传入任意 Incus device、
raw config、挂载或宿主 socket。

`exec_stdin` 是唯一允许把一次性 Secret 送入 guest 的操作。Secret 是不可重放的流，不能出现在请求
元数据、命令参数、cloud-init、镜像、磁盘状态或日志；guest 只能把它写入 tmpfs。

## Incus Provider 边界

首个 interface 是 `incus_vm`。Provider 必须连接独立 KVM 宿主，固定使用 restricted project
`anas-forgejo-runners`，在任何变更前验证 `restricted=true` 以及 instance、CPU、memory、disk 配额。
Provider 只能创建 `anas-` 前缀的 VM，且 `list_managed`/janitor 不能枚举或删除其他 project 和其他
前缀的实例。

当前仓库实现面向 Forgejo one-job Runner 的首个 Provider；真实 Incus/KVM、网络 egress 和 project
credential 仍需在独立宿主完成 E2E，不能由单元测试替代。
