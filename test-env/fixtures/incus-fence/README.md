# 独立 Incus 租约围栏测试

此入口只用于**可销毁的独立 QEMU VM**，不是业务宿主的安装脚本。需要操作者明确授权物理测试服务器；
VM 不得接入已有业务 Docker、存储设备或网络桥。它用生产 Provider 二进制和受限证书直接请求真实 daemon，
覆盖 project 围栏完整性、隔离档实例类型上限、租约归属与只在较新 daemon 上存在的限制键。

## 前置条件

使用全新 cloud-init `instance-id`，形如 `anas-incus-fence-abc123`。VM 内没有 Docker，Incus 初始只有
default project，没有实例、镜像、存储池和信任证书。按要验证的代际安装 daemon，并装上
`btrfs-progs`、`nftables`、`dnsmasq-base` 与 `openssl`（`--no-install-recommends` 不会带上它们）：

| 代际 | 来源 |
| --- | --- |
| `6.0` | Ubuntu 26.04 / 24.04 或 Debian 13 官方仓库 |
| `7.0` | Debian 13 `trixie-backports`（`apt-get install -t trixie-backports incus incus-client`） |
| `7.5` | Zabbly `stable` 仓库；先核对 `key.asc` 指纹为 `4EFC 5906 96CB 15B8 7C73 A3AD 82CC 8797 C838 DCFD` |

VM 只经 QEMU user-mode NAT 出网时只有 IPv4。若到 `deb.debian.org` 的 IPv4 很慢，改用
[中国大陆构建说明](../../../docs/developer/china-mainland-build-and-distribution.md) 中的 APT 镜像，并保留
`Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg`；不要拉取源码索引。

在 ANAS checkout 中构建 Linux amd64 Provider，并准备一个以 `.qcow2` 结尾的小 qcow2 文件作 VM 镜像夹具
（从不启动，只让 CLI 以 VM 类型导入）：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o provider ./modules/incus/provisioner
```

```bash
qemu-img create -f qcow2 rootfs.qcow2 64M
```

## 执行

```bash
sudo python3 server-incus-fence-e2e.py --vm-id anas-incus-fence-abc123 --generation 7.5 --provider /opt/anas-fence-inputs/provider --vm-rootfs /opt/anas-fence-inputs/rootfs.qcow2 --report-root /opt/anas-fence-report
```

`--generation` 必须与 daemon 通过 API extension 声明的限制键一致，否则第一项检查失败。入口创建 6 GiB
btrfs loop 池、一个 dir 池、固定名称的 project、证书与测试镜像，打开 `127.0.0.1:8443`。必需检查：
6.0 为 16 项，7.0 为 18 项，7.5 为 19 项；每项写入报告根的 `checks.jsonl`，汇总在 `summary.json`。
Provider 的 stderr 保存在 `provider-stderr/`，不含证书或私钥。

## 清理与证据

正常与失败路径都只删除固定名称的实例、镜像、profile、project、bridge、证书与两个池；遇到未知对象
拒绝删除。结束时要求只剩 default project，且没有池、实例和信任证书，这本身是必需检查。收集报告后
正常关闭 VM，再删除本轮可写盘和临时 SSH/cloud-init 文件；物理宿主的 Docker、nft 和路由应在前后
独立比较。2026-09-26 的三档结果见
[实机核验记录](../../../dev-docs/reviews/2026-09-26-incus-fence-native-validation.md)。
