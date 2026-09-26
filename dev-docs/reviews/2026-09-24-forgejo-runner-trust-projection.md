# Forgejo Runner 内部 CA 的真实交付接续

状态：Runner 信任投影的新镜像与真实工作流闭环已验收；完整业务部署及其他平台仍单独验收。日期：2026-09-24。

从原有 staged/unstaged 工作树接续，HEAD 为
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`，不提交、不推送、不重新暂存。
已读取要求/计划索引、Incus 要求/计划/宿主供给架构，独立核对第三十七轮 Core 合成
双消费者的公开归档和宿主收尾。测试入口继续是 `ssh whl@ln.hlong.wang -p 2200`，
不联网搜索、不修改物理宿主现有 Docker。

## 发现与范围

实际 Forgejo 关闭 Actions 的公开 init/import/render 已在 Mac 上成功，使用仓库原样
Samba/Postgres/Casdoor/Forgejo/Traefik 模块；它不是启动成功的证据。进一步检查真实
controller→guest 路径发现只传 40 字节 token，没有部署内部 CA。旧 one-job 原生入口
用 `nativePublicTrustCompute` 在 guest 启动后手工 file-push CA 并更新系统根，这掩盖了
默认内部证书部署的信任交付缺口；旧通过结果不能当作默认产品路径已完成。

本轮去除该测试替身，用生产 controller 在既有 stdin 通道投影校验后的公开 CA。
Actions controller/preflight 仅挂载固定公开 CA，最大 32 KiB、root 所有单链接只读，
校验当前有效 CA/signing 用途与严格 PEM。启用时无效输入阻止启动；独立公共根部署无
文件时不引入新 CA。投影后的 token+CA 只在 stdin，参数仅带非秘密大小/摘要，状态不存
payload。原 namespace、进程身份、mTLS/pin、工作流镜像授权与资源补偿不变。

guest 固定输入 helper 在 engine 准入后以 runner-agent 读入有界帧，排他创建 0600
临时文件，完整长度/摘要/EOF 都要成立。组合 bundle 仅用于本次 one-job SSL_CERT_FILE，
不更改 guest 公共根、engine、工作流镜像或 Incus 服务。失败/结束清理临时 token 与 CA。
旧 starter 拒绝新增参数，需要新不可变配方；不能重写 lab-r11 或自动在 apply 烘焙。

这只解决 Runner 进程连接 Forgejo 的部署信任，不声称工作流 OCI 镜像内的 checkout
或私有 registry CA 已交付，也不等于完整 Core/Forgejo/AI Agent 产品部署。

## 回归与独立实验

先新增 controller CA 校验、文件身份、stdin/argv/state 分离和无效输入无副作用回归，
再完成实现。shell 入口在临时目录执行原控制流与真实 coreutils，验证截断、摘要错误、
多余字节、符号链接、非规范头部和失败清理；不碰本机 /run/systemd。四个 image targets
都要求原样冻结新 helper。Compose 检查保证两个服务仍非 root/只读/空 capability，仅
公开单文件 CA 挂载。现有 engine 入场控制和 token-only 兼容路径保留。

本轮新隔离 VM：`anas-runner-bake-f4126f`，Debian 13 amd64，KVM 单核/2048 MiB，
22186。实验目录 `/data/anas-incus-20260924.Hd6AM0/runner-trust-r1`，避免空间紧张的
物理根分区。基础 qcow2 摘要
`5754395abffb1d384d50f6d0945d46d1beb7be42a7e786e4fc4a6f27270ab16f`，只读 backing
加新写盘，不复用失败状态。QEMU 临时 UID1000/GID108、无附加组/capability，PID/QMP
受独立限时 owner 管理；每步失败也执行归档、精确身份关机、实际 wait 及物理宿主对照。

编译清单绑定 123 个仓库输入。`trust-v2` 仅更新测试 CA 的显式 signing key usage，
与 `trust-v1` 的 controller/Provider/recipe 工件相同。测试版本不冒充正式签名发布；
新 build 使用独立 `trust-r1`。配方仅在实验输入中记录官方 Debian→TUNA 的传输覆盖，
仓库默认 URL 与签名校验不变。真实 bake/boot/one-job 终态及收尾将在取得后追加。

第一轮只停在基础环境下载，未运行 build 或产品测试。读取当前下载进度后，确认没有
dpkg 子进程及产品目录，仅终止这一明确 VM 内的 apt 下载，外层正常归档、关机、wait=0，
全部宿主对照一致；原盘保留。它不是产品通过或产品供给失败的证据。

第二轮改用新目录 `runner-trust-r2`、端口 22187 和新 cloud-init 身份，从同一校验过的
Debian 基础镜像重新准备。只在该 VM 的 `/run/anas-native.sources` 指向已记录的 TUNA
Debian/security 镜像，继续使用 Debian 官方 keyring 验签；强制 IPv4 与省略翻译索引
仅改变下载传输，不改产品安装表或物理宿主的 apt 配置。原有产品/镜像输入不变，烘焙
修订名为独立 `trust-r2`。

## 新镜像与运行中信任观察

第二轮实际身份为 `anas-runner-bake-40e17a`。官方 Debian 包安装的 distrobuilder 为 3.2，
执行前密封并测量的 ELF SHA-256 为
`cc942a240ea5a5795422d0995dedaa0f5c75da6298da8723e024be4a47e6496d`。
复用的固定 Runner 13.2.0 输入摘要为
`fadaec897f5e6641c363f87ecaf75f866f99c81b7097ec6b433e4c48123fcad1`，Forgejo 15.0.7
输入摘要为 `cb75c2780d13a8a8b91390e42615354219aac58fd86a9d20baa40f5281c8e9a3`。

生产 artifact CLI 实际完成新 build、export 与第二次 build，三项退出码均为 0；第二次
明确 existing=true、release 相同，没有再次调用烘焙。新 fingerprint 为
`63c2313fba50f8ab861413322587334a78ba59ccccda7d776b539de1e70874c5`。
固定输入导出归档保存为远端 `runner-trust-r2/reports/runner-export.tar.gz`，大小
244784046 字节，SHA-256 为
`923dfc9db6749be224bfce2edb28c3ab08348319fb380ab525037983a005dcd5`。
它只包含新 artifact.json、metadata/rootfs，不是签名发行，也没有修改生产 catalog。

新镜像的原生启动/引擎门禁退出 0。真实工作流的正常、显式失败及各自资源回收也已取得
通过事件。另以独立只读观察核对当时运行的测试实例：`volatile.base_image` 等于上述
fingerprint，guest 系统 CA 字节的 SHA-256 与导出 rootfs 内的原始文件一致；单次
runner-ca-bundle.pem 的 SHA-256 恰好等于原始系统根加本次测试 CA 的组合。
观察没有 file-push、包安装、修改证书库或修复测试状态；只覆盖这一运行实例的这一时刻，
完整取消/恢复/未授权反例及宿主收尾仍以随后终态为准。只读结果保存在
`runner-trust-r2/reports/live-trust-observation.json`，不含证书内容、私钥或 token。

## 完整终态与独立复核

第二轮完整入口退出 **0**。新镜像的父测试及八个子项共 **9 个必需 pass** 均实际执行，
无 fail/skip/重复/漏项，包含非特权/设备围栏、真实 Runner、配置可读、engine 目录归属、
rootless API、真实 user session 及 OCI create/exec 的实际限额。随后完整 Forgejo 工作流
矩阵通过，下表时间来自真实 case 报告，不是容量规划基线：

| 场景 | 实际终态 | 秒 | 清理读回 |
| --- | --- | ---: | --- |
| 正常 | success | 120.924 | 实例、根盘、注册、controller state 均空 |
| 显式失败 | failure | 89.292 | 同上 |
| controller SIGKILL 后保留状态恢复 | success | 126.905 | 同上 |
| controller SIGTERM 取消 | controller-cancelled | 65.321 | 同上 |
| 未授权仓库 | waiting | — | 无注册、无实例，没有取得执行资源 |

本轮原生入口直接使用生产 controller 的 stdin trust 帧，没有手工向 guest 复制 CA 或
更新系统 trust；TLS 验证保持开启。它不覆盖 Forgejo 网页取消、state volume 丢失、任意
工作流镜像中的 checkout/registry trust，也不替代完整 Core/Compose 业务栈。

公开证据位于
`/data/anas-incus-20260924.Hd6AM0/runner-trust-r2/reports/public-evidence.tar.gz`，SHA-256：
`94a346910ec0a5740a41aaff4b60bc5fcfa591ac7069679aeb52ee17978e1549`。
完整证据与导出镜像已再次独立读取：全部工作流 case 及 image pass 的计数与状态匹配，
source-manifest 的实际二进制/脚本摘要一致；镜像归档三文件的逐部分大小与摘要和合并后的
Incus fingerprint 一致，第二次 build 的 release 与首次相同且 existing=true。
公开归档不包含私钥、会话、token、原始状态库或数据库。独立结论写入同目录
`independent-verification.json`，不是只相信外层成功码。

VM 正常关机、实际 QEMU wait=0、无强制退出、无清理错误，22186/22187 无监听且无遗留
QEMU。物理宿主前后 **24 个原容器、17 个网络**、卷、Docker 服务/配置/unit 摘要、nft、
IPv4/IPv6 路由和 named netns 全部相同。第一轮准备失败盘、新镜像和全部公开证据保留；
没有修改物理业务 Docker 或用旧失败状态重跑来制造通过。

## 最终代码与文档校验边界

全仓 Go 测试、controller/computeimage/Forgejo Hook 竞态、全仓 go vet、**27 项 Forgejo
Python 回归与 68 项 Incus Python 回归**通过。后加的输入负例还持有实际 pipe 的读端，
确认无效头部/已有链接拒绝时 token 字节未被读取，而不只是检查没有文件。controller 与
其测试程序的 Linux ARM64 交叉编译通过，仍不计作 ARM64 原生执行。

实机对应 123 个构建前后不变的输入。其后变化只有 Runner README 与离线输入负例测试，
实际控制器、guest 脚本、配方、原生测试入口及交付工件未变化。文档生成、状态/覆盖、
共享构建静态检查、升级目录和工作树差异格式检查分别运行，不能替代镜像/工作流实机证据。

本轮修复与验收的是默认内部证书下 Runner 进程的实际信任交付。完整 Forgejo/AI Agent
业务部署、工作流 checkout 的 trust、VM/ARM64、正式签名目录/分发、完整故障恢复和生产
入站仍沿原计划推进；Module 与相关大里程碑不因为这个闭环被全部标记完成。

真实业务接线复核还确认现有 `FORGEJO-R-070` 仍未实现：
`modules/forgejo/hook/main.go` 的 `reconcileActionsAccount` 在 Actions 关闭时直接返回，
没有停用已创建的 controller 账号。现有要求已把该项及站点管理员权限偏差单独登记。
本轮不把工作流执行实例的回收当作该账号凭据已撤销；后续须连同实际 Core 开关顺序、
执行面排空、已知账号归属及重新启用一起验证，不能直接删除同名未知账号或先撤销仍被
清理流程需要的凭据。
