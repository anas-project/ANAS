# Incus 宿主供给原生生命周期验收

此入口在全新、明确指定的 QEMU VM 中运行生产 `incusprovision.NewLocalBackend`，实际安装
官方 Incus 包、配置回环 daemon、btrfs 池、Docker 控制桥、防火墙和非 root relay，随后登记
管理连接并卸载本次所有的资源。不是 mock、仅 API 协议测试、手工预先初始化 daemon 后的
验收，也不是物理宿主上的安装脚本。

## 安全前置条件

必须得到操作者对目标测试服务器的明确授权。另建可销毁 VM，使用新的系统盘和 SSH 身份，
cloud-init `instance-id` 必须是独有的 `anas-incus-host-xxxxxx`（末六位为小写字母/数字）。
不映射物理宿主 Docker socket、块设备、业务目录、卷或网络桥。物理宿主的容器、网络、卷、
服务进程、配置、nft 和路由应在实验前后独立只读盘点。

首次执行只允许声明式 recipes 当前适配的 Debian 13、Ubuntu 24.04 LTS 或 Ubuntu 26.04
LTS。记录实际内核、架构与软件包版本；一台机器通过不能代替另外两份 recipe、ARM64 或 VM
计算档。VM 内必须有 systemd，默认容器档不依赖嵌套 KVM。

测试准备阶段可以从 VM 自身的官方仓库安装 Docker，但不得预装 Incus 或初始化其 daemon、
池、管理证书和 HTTPS。必须阻止默认 Docker 服务意外启动，仅启用数据根为
`/var/lib/anas-host-provision-test` 的实验 daemon，且起始容器库存为空。

**该实验 daemon 在 VM 内占用 `/run/docker.sock`。** 生产后端固定该路径，不接受测试注入
任意宿主 endpoint。默认 socket 路径本身不是授权：Python 入口与 Go 测试都先检查 root、
精确 cloud-init 身份、QEMU DMI，再检查专用数据根与实际 daemon ID；其他实验 VM 或任何
业务 daemon 都拒绝。这与同目录下“独立 socket 构建测试”的准入方式不同，不能拿它解除
物理宿主 Docker 隔离 guard。

## 构建、交付与运行

在匹配的源码 checkout 中构建 Linux 验收程序、真实 relay 和 JSON 测试转换器。以下为 amd64
示例；换架构时三个二进制必须同时匹配，不能静默降级：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOPROXY=off \
  go test -c -o /tmp/incusprovision-native.test ./internal/incusprovision
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOPROXY=off \
  go build -o /tmp/anas-incus-control-relay ./modules/incus/control-relay
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOPROXY=off \
  go build -o /tmp/test2json cmd/test2json
```

把二进制与 `packaging/systemd/anas-incus-control-relay.service` 交付到 VM。测试程序和转换器
安装到 root 私有 `/opt/anas-host-native/bin`；真实 relay 和固定 unit 放到它们的生产路径
`/usr/local/lib/anas/anas-incus-control-relay`、`/etc/systemd/system/anas-incus-control-relay.service`。
只执行 `daemon-reload`，不预先创建 relay 用户、配置或启用 relay；这些效果由生产后端执行。
这一步是显式受信的实验交付，不证明正式 release 签名、安装器或宿主审批通道已验收。

建立 root-only 报告父目录 `/opt/anas-host-native/reports`。不要把某些发行版上允许日志组写入
的 `/var/log` 用作此 root 操作的证据祖先目录，也不要改变系统日志目录权限。交付入口后执行：

```sh
sudo python3 /opt/anas-host-native/src/server-incus-host-provision-e2e.py \
  --vm-id anas-incus-host-abc123 \
  --tests /opt/anas-host-native/bin/incusprovision-native.test \
  --test2json /opt/anas-host-native/bin/test2json \
  --report-root /opt/anas-host-native/reports/run-a
```

报告目录及 `/run/anas-incus-host-lifecycle` 必须不存在。所有输入均要求 canonical 路径、
root-owned 只读祖先、普通单链接文件、执行权限和有界大小；测试程序再次比对自身及 relay
摘要。入口生成 root-only 身份标记，不读取生产 Docker 登录配置、root 密码或任意代理。

## 必需证据与失败处理

门禁要求一个父测试与十个子项共 **11 项**均出现 run/pass，非零退出、缺项、重复、其他测试、
skip 或 fail 均不通过。子项覆盖未确认/过期计划拒绝、无宿主效果的 skip、官方包安装、实际
配置、私有管理连接、重复安装/配置/登记、默认保留包的卸载、显式只移除本次新增包，以及
重复卸载。原 Docker 网络 ID 和空容器库存须保持，daemon ID 全程固定。

新增的 `uninstall_preflight_preserves_retained_storage` 只在已核实身份的实验 VM 内创建
一个有精确测试所有权标记的 custom volume。卸载必须在删除 bundle、信任、relay、规则或
网络之前拒绝，管理连接仍可通过真实 endpoint 使用，所有权、intent 和 receipt 均保持。
该卷再次读回所有权后才按精确名称删除，并确认缺席；失败则保留证据，不清空整池或删除
其他对象。旧的 10 项报告不满足此门禁。

监督器通过管道分别限制 stdout/stderr 日志，每份最多 32 MiB；超限、超时、日志不完整
或缺少必需事件一律失败。不得通过对子进程设置 `RLIMIT_FSIZE` 来限制日志，因为该上限
会同时截断 APT 包索引、包数据和 Incus 存储文件。监督器在发出本次私有进程组的结束
信号之前保留子进程身份，再回收子进程；不按命令名称查杀其他进程。

2026-09-23 第一轮真实安装发现了上述旧监督器问题：APT 的 universe 索引停在
33,554,432 字节，安装没有完成。失败 intent 和 VM 磁盘保留，未清空状态继续冒充通过。
修复后的监督器已通过 macOS 和 Linux 的大数据文件、日志超限及输出关闭后超时回归；
后续实际还发现并修复服务启动期限、relay 公开配置在 root 私有目录中的访问、Ubuntu 26.04
实际 daemon 的拆包归属，以及网络枚举顺序引起的错误计划漂移。第五轮全新 Ubuntu 26.04
amd64 VM 的完整 11 项已全部通过；失败证据分别保留，不能用监督器单测或某一阶段通过
替代完整结果。实际摘要、工件身份和收尾见
[本轮记录](../../../dev-docs/reviews/2026-09-23-incus-service-execution-budgets.md)。

跳过后的 `Disabled` 状态必须在管理连接验证并持久化之后才解除；失败的 trust/endpoint
不得恢复功能，成功登记仍保持 `compute_ready=false`，由其他运行门禁独立决定可用性。
管理私钥、TLS bundle、完整 root 状态不进入测试输出或报告。

测试使用真实 fresh plan digest 和后端 Binding，但不声称覆盖宿主动作审批、peer 身份、Web
按钮、CLI 作业路由或消费者所在 bridge 的连通性。它也不证明 Provider 租约、guest 生命周期、
完整生产 ingress 或正式镜像发布。分别沿用相应专用验收。
实际安装的 CLI/HTTPS、共享 job、一次性确认及 systemd hostd 通道使用独立的
[host-action 验收入口](../incus-host-action/README.md)，不能从这里的通过结果推导。

命令与输出均有上限。失败后保留保护状态和 effect receipts，不绕过 pending-intent 防护，
不把“重跑成功”覆盖失败记录，也不通过删除整台宿主资源来制造清理通过。必须先核对并归档
本次不含秘密的报告，再由实验所有者正常关闭精确 VM，确认进程、socket、回环端口消失后
按固定清单删除本次临时盘和身份。不要将含管理私钥的 `/var/lib/anas/incus-host` 打包提交。

离线 guard 与状态回归：

```sh
go test ./internal/incusprovision
python3 -m unittest discover -s test-env/scripts -p 'test_incus_host_provision_e2e.py'
```
