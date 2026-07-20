# macvlan 特权操作与最小 sudoers 授权

当某个已启用的 cask 声明 `features.host_lan: required`（当前是 `samba_fs`）
时，runner 需要在宿主机创建 macvlan 桥接口，使宿主机可以访问 macvlan
网络中的容器。该操作需要 root 权限，runner 通过
`sudo [nsenter --net=<path>] sh <base>/anas_service.sh [add|del]` 执行。

## 脚本行为

`anas_service.sh` 由 runner 生成到运行时 base 目录（默认 `~/.anas`，
0700），内容只做三件事：

- `add`：创建 `anas_bridge` macvlan 接口、绑定桥 IP、拉起接口；
- `del`：删除 `anas_bridge` 接口；
- 不读取环境变量，参数在生成时已固化（fmt %q 引用，无注入面）。

Docker macvlan 网络本身通过 `docker network create` 创建，不需要 sudo
（要求当前用户在 `docker` 组或以 root 运行 runner）。

## 最小 sudoers 授权

为运行 anas 的用户（示例 `nas`）授权固定路径的脚本执行，避免开放全量
sudo：

```text
# /etc/sudoers.d/anas
nas ALL=(root) NOPASSWD: /usr/bin/sh /home/nas/.anas/anas_service.sh *
nas ALL=(root) NOPASSWD: /usr/bin/nsenter --net=* sh /home/nas/.anas/anas_service.sh *
```

注意：

- 路径必须与实际 base 目录一致；使用 `-b` 自定义 base 时同步修改。
- base 目录是 0700 且属于运行用户，root 执行前应确认目录属主可信；更严格
  的方案是把脚本安装到 root 所有的路径（例如
  `/usr/local/lib/anas/anas_service.sh`），由管理员审查后固定内容，
  并在 sudoers 中只授权该路径。
- 仅在部署包含 `host_lan: required` cask 的主机上需要此授权；纯 Web
  服务栈不需要任何 sudo。
- `nsenter` 分支仅在设置了 `NETWORK_NAMESPACE_PATH`（在网络命名空间隔离
  环境中运行，例如测试容器）时使用，普通部署可省略该行。
