# 特权 helper（anas-helper）

当某个已启用的 module 声明 `features.host_lan: required`（当前是 `samba_fs`）时，
runner 需要在宿主机创建 macvlan 桥接口，使宿主机可以访问 macvlan 网络中的容器。
这是 anas 唯一一件普通用户做不了的事。

它由一个独立的二进制 `anas-helper` 完成，安装在 root 拥有的目录下，并只被授予
`CAP_NET_ADMIN` 一个能力。

## 安装

`install.sh` 会自动完成：

```sh
sudo install -d -m 0755 /usr/local/lib/anas
sudo install -m 0755 anas-helper /usr/local/lib/anas/anas-helper
sudo setcap cap_net_admin+ep /usr/local/lib/anas/anas-helper
```

`setcap` 来自 `libcap2-bin`（Debian/Ubuntu）或 `libcap`（RHEL 系）。缺少它时安装器
会装好二进制并提示手工执行那一行。

**每次升级都要重新 setcap。** 替换文件会连同 xattr 一起丢掉能力，而随之而来的报错
（`needs CAP_NET_ADMIN`）离原因很远。安装器负责这件事；手工替换二进制的话要自己记得。

runner 按这个顺序查找 helper：anas 二进制旁边、`/usr/local/lib/anas/`、最后才是
`PATH`。前两者是部署应有的形态，`PATH` 只是开发和测试时的便利——helper 是持有能力的
那一个，而 `PATH` 属于调用 anas 的人。

## 它能做什么

```
anas-helper bridge up   --parent <iface> --name <bridge> --address <cidr> [--route <cidr>]...
anas-helper bridge down --name <bridge>
```

- `up`：接口不存在则创建 macvlan 子接口，把地址设成 `--address` 并删掉该接口上其他
  残留地址，拉起接口，然后为每个 `--route` 装一条经该接口的路由。
- `down`：删除该接口。

**它只能操作名字匹配 `anas*` 的接口，两个方向都是。** 宿主自己的网卡、地址、默认
路由和 resolver 配置在这里根本不可寻址，无论传入什么参数。这是"anas 配置网络"和
"anas 可能把机器搞断网"之间的那条线。

`--address` 和 `--route` 必须写出前缀长度：`ip addr add 192.168.1.50 dev x` 是合法的
且意为 `/32`，用在桥地址上会静默产生一个到不了容器的接口。

## 为什么不是 sudoers

早先的做法是一条 sudoers 规则，授权 root 执行一个**由 anas 自己写进用户可写目录的
shell 脚本**。对运行 anas 的用户来说，那已经约等于完整的 root——脚本内容是他能改的。
现在的 helper 是 root 拥有的固定二进制，接受具名操作而不是脚本，并自己校验每个参数。

也不采用"root 守护进程 + 用户组"（docker 的模型）：`docker` 组等价于 root，而这里
真正需要的只有一个能力。

**升级提示：** 从旧版本升上来后，`/etc/sudoers.d/anas` 那条规则不再被使用，可以删除。
运行时 base 目录下遗留的 `anas_service.sh` 也不再生成或执行，可一并删除。

## 权限不足时

helper 会明确报出来，并给出三条路：`setcap`、systemd unit 的
`AmbientCapabilities=CAP_NET_ADMIN`、或以 root 运行。

## 网络命名空间隔离环境

设置了 `NETWORK_NAMESPACE_PATH` 时（隔离测试环境），进入命名空间本身就是特权操作，
这条路径仍然使用 `sudo nsenter`。普通部署不涉及。

## 其他需要特权的操作

只有网络这一项被收进 helper。备份与恢复里的
`btrfs send`、`btrfs receive`、`btrfs subvolume delete` 和保留属主的 `rsync` 仍然是
**显式**的：anas 探测自己有没有 `CAP_SYS_ADMIN`，没有就报 `insufficient_privilege`
并说明原因，由操作者决定以 root 运行或授予能力。

理由是这些操作会产生用户事后要面对的**特权产物**（root 拥有的文件和 subvolume），
而桥不会。详见 [特权操作与 helper](../../architecture/privilege-helper-draft.md)。
