---
doc_type: review
status: current
created: 2026-09-22
updated: 2026-09-22
---

# Runner 根目录、主组与真实 one-job 接续

接续操作者“继续，直到完成当前任务”的请求。仓库基线仍为
`claude/forgejo-docs-audit-20260920` / `3f5242e` 加累积工作树；不切换分支、提交或推送。
指定目标为 `whl@ln.hlong.wang:2200`，不修改、重启或使用已有 Docker。当前任务是修复
Runner 引擎，验证真实 one-job 和回收；本文随本轮实际终态补充，不把诊断对照当成发布验收。

## 环境与已保存证据

先只读验证前轮停机的 `anas-runner-bake-k9kuyu`：磁盘无 corrupt 标记、基础镜像绑定正确、
文件归 whl、外层目录 0700、QMP/pidfile 不存在、回环端口空闲。重新启动只使用既有独立
QEMU 用户态网络、1 vCPU/2 GiB，没有 TAP/宿主 bridge。该 VM 两次正常退出，原始失败
和 lab-r5 归档已复制回物理目录，未重建或覆盖 lab-r4。

本机日志根为 `/tmp/anas-onejob-completion-20260922.w9zeI4`；本轮物理 Docker 前置记录为
其中的 `host-before.txt`，不是沿用前轮基线。Ubuntu 实验根为
`/home/whl/anas-engine-debug-20260922.K9kUYU`。

另建立计划内的 Debian 13 实验 VM，根为
`/home/whl/anas-onejob-debian-20260922.UefkeK`，身份 `anas-runner-bake-uefkek`，回环 SSH
22131。基础 OS 镜像经官方 HTTPS 获取并与同源 SHA-512 校验，不宣称独立镜像签名验证。
运行 Debian 13 / kernel 6.12.107+deb13-cloud-amd64 / Incus 6.0.4。为解决下载停顿，只在 VM
记录并切换 Debian 镜像地址，APT 签名校验仍启用；没有修改物理宿主软件源、软件包或内核。
测试 VM 的 /run 容量上限为 512 MiB，外层 RAM 仍为 2 GiB；这不是业务宿主的 remount。

lab-r5 完整构建成功并导出，fingerprint 为
`75ec84539232bd9e1d24ac55cd8b6f7a68a65ce1a4a9ead283afe8bf509d58c5`；它首先修正本地
Podman Unix socket 服务不必要的 network-online 等待。完整归档保存在原 Ubuntu 物理根的
`reports/completion-r5-archive.tar`，SHA-256
`ef14e67d46e4a9e9c21f38aec0f34c721e2a384a0be7fcdcf8fdc8eeb24fc67d`，267,100,160 字节。

Debian 首次 Python data-filter 提取把 .format/object 的 0400 变成 0600，严格归档校验
正确拒绝；失败副本保留。第二次先校验原归档 SHA、路径、类型、大小与所有权，再在新目录
保留原始模式提取，现有 export 工具成功恢复完全相同的 fingerprint，没有放宽归档校验。

## 从观察到已确认缺陷

Ubuntu 中的早期观察为 systemd 服务停在 activating、父进程等待 `(sd-mkuserns)`，并伴随
AppArmor 叠加标签的 ptrace/signal 拒绝。拟议的实验 sysctl 对照被工具拦截，未执行；独立
读回仍为默认 1。不能把该线索先验认定为唯一根因，也没有禁用 AppArmor 或修改 Incus 围栏。

Debian 默认安全配置下，同一原样 lab-r5 可启动到完整 systemd，但网络与引擎服务出现
200/CHDIR。固定实例的 stat 及 squashfs 原始元数据共同确认镜像根目录 `/` 为 **0700**。
非 root 服务账号无法遍历；networkd 与 Podman 的 journal 均实际记录 Permission denied。
默认配方现在在构建 chroot 的 post-files 中显式 `chmod 0755 /`。这只定义 guest OS 根目录
权限，不改变私有 archive、对象文件或用户 home 的权限。四个目标的源回归先失败后修复。

只修复可销毁 guest 的根目录后，引擎进入 newuidmap 阶段，仍退出 125。实际错误明确显示
`gid:1003 pw_gid:1002 st_gid:1003`：service 用 actions-engine GID 1003，但 passwd 记录
runner-engine 主组 1002。配方与 provision.sh 统一用 `--gid actions-engine --no-user-group`；
移除不再需要的私有 supplementary group。两个 UID 与 home 0700 保持分离，共享组本来就用于
固定本地 socket；不通过放宽 nesting、设备、AppArmor 或宿主 socket 访问来修复。

## 实现与原生入口

生产 controller 循环被原样抽为 runControllerLoop，信号清理仍使用独立预算。新增真实进程
fixture 使用相同循环、API 客户端、共享 compute 和 FileStateStore。测试专用适配器只把
公开 TLS 测试 CA 放入新 guest，不改变 image、engine unit 或 Incus 隔离配置。当前测试代码
不自动代表真实 one-job 已通过，实际执行结果在本轮终态中另行记录。

`replay-incus-image-native.py` 在空 test daemon 上复用已验证的私有 lease/supply，避免重复制
大文件挤占 /run；case 闭集为 immutable image smoke 与已知 lab-r5 的 root/group 诊断。
后者会修改一个可销毁 guest，明确不是不可变镜像准入，不会变更镜像归档或生产入口。

## 当前执行状态

根目录和主组源回归、控制循环清理及离线 fixture 准入已验证。root-only 对照实际失败并
保留了 newuidmap 证据；root/group 组合对照、修正后新 revision 的完整烘焙、真实 one-job
及最终物理后置基线尚待本轮后续终态。Incus 仍为 developing、30/75，production gate 不变。
