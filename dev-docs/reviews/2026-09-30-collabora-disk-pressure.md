# Collabora 文档编辑失败诊断

检查时间：2026-09-30 19:38–19:43（Asia/Singapore）。目标：`whl@ln.hlong.wang:2200`。

## 已观察到的故障

- `anas_collabora` 持续运行，Docker health 为 healthy，OOMKilled=false；公开 `/hosting/discovery` 返回 HTTP 200。
- 19:30 左右的文档打开日志明确出现 `Low disk space`、`Out of storage`、`Disk-Full error while starting session`，随后终止文档连接。这是本次文档打开失败的直接原因。
- 根分区 98 GB，已用 91 GB，可用 2.1 GB，使用率 98%。Collabora `/opt/cool/child-roots` 位于根分区 Docker overlay，目录约 765 MB。没有外部挂载。
- `/data` 为独立 3.7 TB 分区，可用 3.4 TB。
- `/home/whl` 占用 64 GB，其中 `anas-incus-followup-20260923.4p9ob_nk` 占用 45 GB，内含多代 host-v* 测试目录。仅确认占用，没有核实其活动状态或删除安全性。
- Docker 使用 17 GB；镜像报告可回收 10.63 GB，但这些镜像可能用于回滚，不能直接全部清理。journald 约 3.8 GB。Collabora Docker 日志已有 100 MB × 3 轮转限制。
- 宿主 swap 几乎用满，但可用内存约 5.2 GB；没有证据将本次失败归因于 OOM。

## anas 层面的缺口

1. `modules/collabora/docker-compose.yml` 没有为临时文档存储声明宿主目录，因此默认依赖 Docker 根分区。
2. 当前 `coolwsd --probe` 在低磁盘空间导致文档不可用时仍通过；发现接口成功也不能证明编辑可用。
3. 宿主测试产物大量留在系统盘。应为远程测试规定工作目录、生命周期与清理机制，避免长期运行的服务受到测试残留影响。
4. 配置 `mount_jail_tree=true`，Compose 仅增加 MKNOD；日志重复报告 coolmount 缺少 CAP_SYS_ADMIN。这是独立配置问题，尚不能证明是本次直接原因。不要未经验证扩大容器权限。

## 建议修复顺序（尚未实施）

1. 先核实 45 GB 测试目录是否仍被进程、Incus、挂载或后续测试引用；保留报告和必要回滚材料，将可归档产物迁至 `/data`，迁移验证后回收根分区空间。目标根分区至少留出 10 GB，作为运维余量，并非已核实的上游阈值。不要直接删除整目录或执行全量 Docker prune。
2. 在 Collabora Module 中把临时 jail/document 存储纳入 anas 的工作区路径管理并放到数据分区。单独绑定 child-roots 前须验证同文件系统约束、目录权限、镜像启动清理和 jail 创建行为；不能假定一个 bind mount 即可安全上线。避免使用当前 swap 压力下的 tmpfs。
3. 增加模块存储预检与运行时空间告警，检查实际临时目录所在文件系统的字节和 inode；状态报告应明确显示存储不足。空间告警不应触发反复重启。
4. 明确选择并测试 jail 挂载策略：评估显式 `mount_jail_tree=false`，或按上游要求采用受限权限和安全配置。默认不使用 privileged。
5. 远程测试统一使用 `/data` 下的隔离目录；对完成的测试产物实施保留与清理规则。

## 验收与边界

恢复空间后，用 Nextcloud 真实打开、编辑、保存、关闭再重开文档验证；同时覆盖低空间拒绝、状态识别和恢复。挂载方案必须检查持久文件保存与容器重建。此次仅进行只读诊断，未重启、清理、修改部署或实现修复，未做真实文档往返验收。

上游配置对 child_root_path 的同文件系统约束及 mount_jail_tree 的说明见 [Collabora 配置源文件](https://github.com/CollaboraOnline/online.mirror/blob/main/coolwsd.xml.in)。以目标镜像的实际行为为最终验收依据。
