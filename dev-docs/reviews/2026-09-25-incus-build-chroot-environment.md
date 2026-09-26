# Runner 构建子进程的 chroot 环境修复

状态：构建环境错误已修复，新固定加载器镜像与工作流闭环已验收；联合停止链路单列。
日期：2026-09-25。

继续实际 `/Users/whl/Documents/anas`，HEAD 为
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`，保留原有暂存及未暂存工作，不提交或推送。
接续[引导验签](2026-09-25-incus-bootstrap-signature-validation.md)及
[guest 用户命名空间策略](2026-09-24-forgejo-runner-userns-policy.md)，测试仍仅使用指定
`ssh whl@ln.hlong.wang -p 2200` 下的独立实验环境，没有联网搜索。

## 已证实的具体失败

已结束的 `diagnose-signed-hooks-restored-r2` 构建器退出1，但外层正常关机、QEMU0，原盘
摘要保持且物理宿主前后对照全部相同。重新提取它保存的完整日志，实际失败发生在
`apparmor 4.1.0-1` 的包配置阶段，不是配方的 `post-files` 脚本：包的 post-installation
脚本调用 `mktemp`，继承的 `TMPDIR` 指向仅存在于构建宿主的私有 cache 绝对路径，
进入 chroot 后该目录不存在，故返回 `No such file or directory`，导致 dpkg 和 builder
失败。日志 SHA-256 为
`20a4fcdbc5fbfb6dd1f9778752253f6441e67a46fe28335297e4bfa78cf6ccc6`。

生产 `distroProgram.Build` 同样把 request.CacheDirectory 放进 TMPDIR，并把宿主 recipe
目录放进 HOME。因此这不是只在诊断脚本中出现的错误。此前 `last observed stage: hooks`
仅是最后观察到的日志标签，不能作为具体故障命令或安全策略拒绝的证据。

成功的补读使用 `inspect-signed-apparmor-package` 独立写层，不在原失败环境中重试构建。
补读后正常关机、QEMU0，原诊断磁盘摘要仍为
`0774587e8ce888fe823b3a41177c3ccacbeb6a57021adde6c874786ec3b7ce1f`，全部宿主对照相同。
另一次冗余 raw 副本读取在分区选择阶段退出，尚未创建 loop、挂载或读取日志；原盘未变，
这次失败不计作成功证据。其独立宿主对照中 IPv6 新增一条本地地址路由，其他项相同；
原始 false 保留，未将原因推断为已证实的业务或系统自动行为。

## 实现与回归

先从实际代码抽出构建环境并添加回归，旧实现确实失败；随后将构建环境固定为最小的
PATH/语言设置及 `HOME=/root`、`TMPDIR=/tmp`。这两个路径在构建进程与 guest chroot
中具有各自正确的含义。cache、sources、recipe 和输出仍由既有显式参数指向私有构建
目录，不把 archive 挂载进 guest、不改包维护脚本、不忽略 dpkg 失败、不关闭 AppArmor。
外部代理和其他调用方环境不被继承，既有 keyring 身份与签名拒绝、sealed ELF、期限、
取消进程组和不可变 revision 规则不变。

新增 Linux 原生回归通过实际子进程 chroot 到临时空根，再创建/检查/清理0600临时文件，
证明环境在 chroot 后仍可用。它不运行包管理器、不挂载或访问网络、不改变父进程的根。
无 root 时显式 skip，所以本机通过或交叉编译不能替代该项在独立 Linux VM 内的执行。
可移植回归同时钉住严格环境清单和保留的 cache/sources/output 参数。

## 新实验与证据边界

新目录 `/data/anas-incus-20260924.Hd6AM0/runner-policy-tmpdir-r1`，身份
`anas-runner-bake-6213d8`，回环端口22226。使用已核验的 Ubuntu26.04 amd64 基础镜像，
KVM单核/2048MiB、独立新写盘；新 revision 为 `policy-tmpdir-r1`。
基础镜像 SHA-256 为
`4908fb59ccd4e87ae4e8e973b7ef56f535448eacb24a87fd787270c0048987bc`。
QEMU临时UID1000/GID108、无附加组/capability、no-new-privileges，原有Docker不接入VM。
观察到另一个独立诊断进程正在运行，本轮不停止它，也不把它的生命周期作为本轮证据。

构建前后134个源码输入一致，新CLI SHA-256为
`24fbf42db13f862a27e62d6f2b373e93c19bc005f58055e5718f4e0def4adb87`；Linux CLI测试工件为
`26390575093afac82092e655e055a81641bd167881dea36fb0c3d44ef58c1418`。
实际配方未因本修复改变，签名与Podman内层策略输入保留。实验入口额外要求六个具名
Linux回归各实际执行且通过，然后独立验证新镜像烘焙/重复复用、十项镜像门禁及真实
工作流。`.partial` 只在完整导出和逐分片/合并摘要全部核对后才成为候选镜像。

当前本机两个相关Go包回归通过；新镜像、工作流、后续Core停止链路及物理宿主收尾须
记录实际终态，不从旧镜像、诊断对照或单元通过推导。源码来自dirty checkout，未发布
或签署正式镜像目录，M10/M11/M12仍不整体提前关闭。

## 本轮运行中的独立读回

全新VM已实际通过六项具名Linux回归，各顶层测试均有一次run/pass，无skip/fail：包括
真实子进程chroot临时文件、固定环境、不占用revision的验签前置检查、可信keyring文件
及官方包别名、未验签警告不能被零退出掩盖。准备阶段的真实缺keyring负例同样通过。

独立只读读取本次bootstrap.log取得三条 `gpgv: Good signature`，没有缺keyring跳过
验签警告，日志SHA-256为
`db9c8a9b0207476c560992abada4868808f4a438e761b16becf0438e21ba18d1`。
摘要及计数保存在远端本轮 `reports/bootstrap-signature-observation.json`，没有归档原始
构建日志或凭据。运行中的debootstrap环境读回确认HOME为/root、TMPDIR为/tmp。

该轮dpkg日志还明确记录 `apparmor 4.1.0-1` 成功完成配置并进入installed，随后其他
包继续安装。它直接验证旧mktemp故障点已通过；这仍不能代替完整镜像打包、启动、
工作流与最终物理宿主收尾。

最终代码已通过全仓Go测试、镜像CLI/computeimage竞态及全仓go vet；Linux ARM64的
CLI与测试程序交叉编译通过，未计作ARM64实机验收。Module/Contract生成、需求/计划
索引与覆盖、共享构建静态检查、升级目录和两种工作树差异格式检查均通过。

## 新镜像构建终态与尚未通过的运行准入

`runner-policy-tmpdir-r1` 的实际build、export和重复build均退出0；重复明确
`existing=true` 且release不变。新fingerprint为
`76adc973d1a6d3bc690dd794bdbed014ae19ea593a337b87a8fd7f1fcc7a799c`。
导出归档 `reports/runner-export.tar.gz` 的SHA-256为
`a42d5a296fe9b2fc350eeb1cf3d2e10ad77648b838eaee30a3cc7f3b67f7a0d3`，两部分的
实际大小/摘要与合并fingerprint已由外层核验；文件未覆盖旧revision或生产catalog。

随后镜像运行准入退出1：Provider导入、namespace围栏、真实Runner二进制、one-job接口、
配置可读与engine目录归属已通过，但普通程序的userns拒绝探针未通过。固定Podman
策略单元本身已实际active/exited且success；这并不证明普通程序仍受原来的限制，因此
没有执行后续rootless/OCI和Forgejo工作流，更没有启动停止链路作为成功验收。

本轮公开归档SHA-256为
`a8ec1617e4b0dc45a356a7373918cbce65c822a086b9af3257d7d24991077e02`。
VM正常关机、QEMU0、无强制退出与清理错误；物理宿主24个原容器、17个网络、卷、
Docker身份/配置/unit、nft、IPv4/IPv6路由与named netns均与本轮开始相同。

独立只读解包发现官方AppArmor包还带入允许userns的 `unprivileged_userns` fallback
（complain模式），以及自动启动的发行版完整profile加载器。其影响与仅加载固定Podman
策略的早期诊断不同。现继续把guest AppArmor服务的启动/重载限定为同一个固定策略，
不禁用服务或内核安全机制、不卸载外层约束；其因果对照和新不可变镜像需分别验收。

后续 `policy-loader-r1` 的完整终态已取得：build/export/repeat、十项镜像运行门禁、
五种真实工作流均通过；并用更严格的逐项run/pass判定独立复核原始JSONL。guest普通
userns许可没有放宽，物理宿主各项基线相同且VM正常退出。该结果不覆盖本文失败的
`policy-tmpdir-r1`，新工件与完整记录见[固定策略加载器](2026-09-25-forgejo-fixed-policy-loader.md)。
