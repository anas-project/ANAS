# Forgejo Runner 固定 AppArmor 加载范围与联合验收

后续第八轮网络超时的实际日志、第九轮Core停止以及新计数/证据回归，接续记录在
[停止事务与出站前置条件](2026-09-25-forgejo-stop-forwarding-continuation.md)。下文保留
各轮当时的工件身份与终态，不将随后修复归因于此前冻结运行。

状态：新不可变镜像与工作流已完整验收；后续联合停止链路单独记录。
日期：2026-09-25。

继续实际 `/Users/whl/Documents/anas`，保留全部已有暂存和未暂存修改，不提交或推送。
接续[构建 chroot 环境修复](2026-09-25-incus-build-chroot-environment.md)和
[Core 停止前清理](2026-09-24-forgejo-core-stop-barrier.md)。指定测试入口仍为
`ssh whl@ln.hlong.wang -p 2200`，不联网搜索、不操作物理宿主既有 Docker 容器。

## 已核对的失败边界

上一轮 `policy-tmpdir-r1` 的真实构建、导出与重复复用已经成功，但镜像原生准入未通过。
它不能进入后续停止验收。官方 AppArmor 包不仅提供 parser，还安装并自动加载了
`unprivileged_userns` fallback；这超出了原来只给固定 Podman 程序补充权限的范围。

独立 `diagnose-tmpdir-policy-loader-v3` 对照报告区分了 namespace 创建与后续 UID 映射：
原镜像的普通 `unshare --user` 退出0；加上 root-user 映射时虽退出1，但错误来自
`/proc/self/uid_map`，不能当作 namespace 创建被拒绝。新诊断实例在第一次启动前配置
固定加载器后，两种普通 unshare 都以 `Permission denied` 退出1，fallback 不再加载，
固定 Podman profile 仍存在。

这只是因果对照。该诊断随后发生 `TimeoutExpired`，整体退出1；没有把部分对照结果
称为 rootless engine 或完整原生通过。它已正常关机、QEMU退出0、无强制退出或清理错误，
原镜像 SHA-256 保持
`facabecc967a2e60dbc039ae379ad1f2f06fe5c27c2f5d00939daf1f3970d143`，物理宿主各项对照相同。

## 当前交付与实验身份

工作树已有 `anas-apparmor-loader.conf`：guest AppArmor 服务的 boot/reload 只执行固定
Podman policy 的 parser；不以 `/bin/true` 忽略加载、不停止 AppArmor、不修改 sysctl，
也不卸载外层 Incus 约束。原始包的停止行为不被覆盖。固定策略与 engine user manager
之间仍有启动依赖，普通程序拒绝、实际 rootless API、OCI create/exec 限额和工作流各有
独立必需门禁，任一缺失都不能标记通过。

本轮重新核对本机 `policy-v4` 的135个源码输入全部相同，相关配方、keyring、chroot
环境回归通过。未修改或重试旧失败 revision。新环境为：

| 字段 | 值 |
| --- | --- |
| 实验目录 | `/data/anas-incus-20260924.Hd6AM0/runner-policy-loader-r1` |
| VM 身份 | `anas-runner-bake-27944d` |
| 回环 SSH 端口 | `22234` |
| 环境 | Ubuntu 26.04 amd64，KVM，2 CPU / 4096 MiB |
| 基础镜像摘要 | `4908fb59ccd4e87ae4e8e973b7ef56f535448eacb24a87fd787270c0048987bc` |
| revision | `policy-loader-r1` |
| 镜像工具摘要 | `822f8be5513db0a06f8ac3f4f4496445c503b01a89ad7a03ca529fdd088803d1` |
| 原生 computeclient 测试摘要 | `fe4673faf3764fe0f66c61699b68746eaef47304e46a2c84472ac2f606aa3faf` |

QEMU 使用临时UID1000/GID108、无附加组/capability、no-new-privileges；只有新写盘，
不传入业务 socket、目录或设备。独立 owner 对准备、构建、原生测试与整台VM分别限时，
结束时归档公开结果、校验导出三文件及fingerprint、核对精确QMP身份、正常关机和实际wait，
最后检查端口及物理宿主基线。失败流仍只保留为 `.partial`，不冒充有效镜像。

源码来自已有 dirty checkout，不是签名正式发布。新镜像的完整终态取得前，不启动依赖
它的停止验收；已准备但未运行的 `forgejo-stop-r8` 不能计入成功或失败统计。

## 原生事件完整性回归

本轮实际复现原镜像监督器的集合判定缺口：只有pass没有run、重复父/子项pass、重复包
终态和其他包的同名事件都会被接受。现在将判定提取为纯函数，逐项要求一次run、一次
pass，并核对包身份、父子完成顺序和唯一最终包pass；skip/fail、缺项、含糊退出码及
畸形事件均拒绝。11项离线监督器回归通过，既有CI入口已覆盖该文件，不增加新执行权限。

当前正在运行的镜像实验仍使用其原先冻结的监督器，不中途替换输入或重写来源清单。
最终需把保留的原始JSONL再交给这个更严格的判定器独立复核；这项测试脚本变更不改变
镜像、构建器、生产controller或Go原生测试程序，也不能修复原有失败或缺失的事件。

## 新镜像完整终态与独立复核

本轮完整入口退出0。build/export/repeat均成功，重复返回existing=true且release完全
相同。实际image主测试与九个子项共十项均有唯一run/pass，随后五种真实Forgejo工作流
全部完成：正常success、显式failure、SIGKILL保留状态恢复success、SIGTERM取消后回收，
以及未授权仓库保持waiting且无registration/instance。

新镜像fingerprint为
`16e440ee8ad764f7faac9bcdaed33388466cfcefd102ffbd0ed1d67982c1d5b1`，导出归档SHA-256为
`628ef2b669477823a05bffdc8c8c19e6f963b4a9c94a6328b91afc6f3e34a8e4`。
公开证据为该轮 `reports/public-evidence.tar.gz`，SHA-256：
`32d942842cbc67139973dcfdf7f8809015e6de9cacdf7eb2be0551ddee2eb369`。

已再次读取原始JSONL并应用本轮更严格的事件判定器，十项run/pass、唯一package终态均
通过；六个bootstrap/chroot/keyring原生回归和五个workflow终态也逐项核对。远端所有
交付工件及上游二进制符合源清单；导出三成员、逐分片大小/SHA和合并fingerprint与首次
构建release一致。没有把新的监督器标记为当时运行工件，也没有重写原始结果。

普通userns创建在新guest中被拒绝；两份实际loader启动记录与boot/reload命令符合固定
策略，host限制仍为1。真实rootless OCI create/exec的CPU、内存、PID及no-new-privileges
检查通过。TLS保持验证，部署CA只通过生产controller stdin进入单次Runner，没有手工
安装guest信任或改外层Incus策略。上述工作流时长不是容量规划基线。

独立owner正常关机，QEMU退出0，无强制退出与清理错误；原有24个容器、17个网络、卷、
Docker身份/配置/unit、nft、IPv4/IPv6路由与named netns全部与开始相同。
本机独立结论位于 `/tmp/anas-continue-20260925.i6RxDA/policy-verification.json`。
通过的是明确实验revision，不是正式签名目录发布；完整业务栈与其他平台仍单独验收。

后续停止夹具改为上述已验证的新fingerprint及导出摘要，不使用旧trust-r2、诊断写层或
曾失败的policy-tmpdir-r1。该变更只选择验收输入，不改变生产运行约束；新Core/Hook等
工件将重新构建并绑定源输入，再启动全新 `forgejo-stop-r8`。

## 联合停止链路的新运行

`forgejo-stop-r8` 使用新的 `0.0.0-native.20260925.501` 实验工件，784个编译前后源码
输入一致；镜像和Forgejo上游二进制均按已有完整验收清单再核对。VM身份
`anas-incus-host-62b152`、回环端口22228，Ubuntu26.04 amd64、KVM双核/4096MiB，
全新写盘、没有复用前七轮失败运行状态。仅实验目录内的空输入目录被填入文件。

该入口将生产Core stop方法、旧冻结Hook、controller、账号helper和实际正在运行的
guest工作流组合验证。准备workspace、SQLite和TLS传输镜像仍是明确夹具；即使本轮
通过，也不冒充公开CLI完整IAM/PostgreSQL/Forgejo部署。运行阶段、失败反例和最后的
宿主对照以随后终态记录，不把镜像层成功提前记成停止链路成功。

第八轮终态：前两阶段（真实宿主审批/工件与Forgejo管理账号）通过，但
`actual_workflow_running` 以 `payload_not_running` 失败，完整入口退出1，未执行Core
停止和密码停用。公开归档SHA-256为
`3b2f736d703dd6423b92e7535c0dce68526e88d39a30d45f32c8cd77848feca0`。
VM正常关机、QEMU0、无强制退出和清理错误，物理宿主各项基线一致。

该失败不能推翻已独立通过的镜像层十项/工作流五场景，也不能用它们替代第八轮的失败。
后续诊断在独立 `inspect-forgejo-stop-r8` 写层中进行；只读原测试数据库的任务状态/计数
和controller非秘密状态，不读取账号口令/token列，不修改任务、修复状态或运行旧测试。
原失败盘保持只读backing，hash与诊断VM收尾分别核对；诊断不作为产品验收。
