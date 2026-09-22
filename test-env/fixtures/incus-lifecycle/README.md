# 独立 Incus 容器生命周期测试

此入口只用于**可销毁的独立 QEMU VM**，不是业务宿主的安装脚本，也不是正式 Runner 镜像发布。
需要操作者明确授权物理测试服务器；VM 不得接入已有业务 Docker、存储设备或网络桥。

## 前置条件

使用独立、可删除的系统盘和新的 cloud-init `instance-id`，形如 `anas-incus-lifecycle-abc123`。
在 VM 内安装匹配发行版的官方 `incus`、`incus-client`、`btrfs-progs`、`nftables` 和
`dnsmasq-base`。`--no-install-recommends` 不会自动安装最后一项。VM 必须没有 Docker，
Incus 初始仅有 default project，且没有实例、镜像和存储池。宿主不安装这些测试依赖。

从对应 ANAS checkout 编译 Linux amd64 的 `internal/computeclient` 测试二进制、
`modules/incus/provisioner`、`test-env/helpers/incus-guest-fixture` 与 `cmd/test2json`。
把源码快照、摘要和二进制交付到 VM 的私有目录；不要复制生产凭据或使用假镜像摘要。

## 执行与副作用

下面路径必须替换成该 VM 内的真实私有路径，实例 ID 必须与创建 VM 时的一致：

```bash
sudo python3 server-incus-lifecycle-e2e.py \
  --vm-id anas-incus-lifecycle-abc123 \
  --provider /home/anas-test/verification/bin/provider \
  --tests /home/anas-test/verification/bin/computeclient.test \
  --guest-binary /home/anas-test/verification/bin/guest-fixture \
  --test2json /home/anas-test/verification/bin/test2json \
  --report-root /home/anas-test/verification/reports/new-attempt
```

入口验证 root、QEMU、cloud-init 身份、无 Docker 和空库存后，使用 **VM 自身**的 Incus
服务，在 VM 内创建 12 GiB btrfs loop 池、两份测试 project/证书/bridge/profile 及容器。
镜像只包含限定用途的原生检查程序；它的 SHA-256 来自真实归档字节，不进入生产 catalog。
根盘最多实际写入约 4 GiB；测试前要求池至少有 8 GiB 可用空间，完成后再次检查剩余空间。

Go 原生门禁必须包含全部 13 个指定测试通过事件（一个父用例、七个子项、五个嵌套反例），
不能以空匹配、skip 或仅退出码代替。输出只包含测试状态、字节数和阶段耗时，不输出输入
secret。真实 stdin 只传入 guest 进程内存；取消之后使用独立上下文删除并读回，不把 CLI
退出等同于 guest 已销毁。不会调用业务 Docker 或打开其 socket。

## 清理与证据

正常与失败路径均只清理固定的测试对象，未知名称拒绝自动删除。测试 runtime、TLS 文件
保存在 VM 私有 `/run/anas-incus-lifecycle`，报告根保存失败记录、逐事件 JSONL 和镜像摘要。
失败后先保存报告和实际清理结果，不能把未知副作用计为已清理。重跑必须使用新的报告目录，
并由实验所有者先核实旧资源；不自动覆盖旧私有配置或旧证据。

收集报告后，停止并核对该 VM 的进程、端口和挂载，再删除**本轮**可写系统盘及临时 SSH/
cloud-init 文件。不得按通用前缀递归清理服务器，不能删除其他实验或业务资源。
物理宿主的 Docker 容器/网络/卷、服务身份、配置和 nft 规则应在测试前后独立比较。

结果只覆盖已记录的 daemon/架构及容器夹具；ZFS、产品 distrobuilder 镜像、VM 档、真实
Forgejo one-job、controller crash、双栈和生产 ingress 仍需各自验收。
