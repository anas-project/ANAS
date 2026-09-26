# Runner 镜像引导验签与工具恢复后的接续

状态：执行与写入接口已恢复；真实验签拒绝路径已验证，新镜像运行验收分别记录。日期：2026-09-25。

继续实际 `/Users/whl/Documents/anas`，HEAD 为
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`。保留原有暂存和未暂存修改，不提交或推送。
读取要求、计划索引及既有 Runner/停止前清理记录后，先核对失效会话的独立实验状态。
旧进程编号失效不等于 VM 未关闭；结论以远端报告、实际进程和监听读回为准。

## 已核实的旧实验与诊断边界

`runner-policy-r1` 未产出有效新镜像；最后观察标签为 `hooks`，该标签只标明最后识别的
日志阶段，不能定位到某段 Hook 代码。`runner-policy-r2` 同样构建失败；其公开归档摘要
为 `c1665a679b13fc049659d091d8687b9729dfbcc543672ee5568c3b540bb8fb50`。
两轮的20字节失败导出流都不作为镜像候选。

独立 `diagnose-runner-policy-r1-builder` 用新的 output/cache 重现构建，日志明确出现
`W: Cannot check Release signature; keyring file not available`，随后仍继续下载并验证包
哈希。它缺少固定 `/usr/share/keyrings/debian-archive-keyring.gpg`，因此 HTTPS 与同源
哈希不能构成该次 Debian bootstrap 的 Release 签名证据。这是一个实际观察到的独立
验签风险，不能反向把原 `hooks` 失败归因于它。

该诊断最终因自己的15分钟限时终止在包安装阶段，没有完成镜像，也没有记录任何发行版。
独立收尾报告确认正常关机、QEMU退出0、无强制退出和清理错误；原失败盘摘要保持
`09b796a8661a0479414e07f589f6352eed6569351c1b5484d99097bf069c6b9f`。
文件系统未以可写方式打开原失败盘。已有物理宿主对照全部相同。

## 构建前置检查与补充回归

接续工作树已有的 `validateRecipeBuildTrust` 与缺失签名警告观察：默认 Debian/debootstrap
在预留 revision、调用可提权的构建器之前，要求固定 keyring 的可信文件身份，拒绝关闭
验签、别名、重复或含糊字段。文件存在本身不是签名验证；实际 bootstrap 仍须完成验签。
构建子进程即使退出0，也不能用后续打包日志清除已观察到的跳过签名警告。

新增 `recipe_trust_boundary_test.go` 独立覆盖 source/image/downloader 的大小写混淆、
重复、YAML alias/merge、字符串形式的验签开关、调用中取消以及显式 false 不能代替
实际 keyring。两个相关 Go 包回归通过。运行说明补入 Ubuntu 构建 Debian 镜像时也需
安装官方 `debian-archive-keyring` 的前置条件，不在物理宿主自动安装包。

## 本轮全新实验

新目录为 `/data/anas-incus-20260924.Hd6AM0/runner-policy-restored-r1`，VM身份
`anas-runner-bake-72a94b`，回环端口22220，Ubuntu26.04 amd64、KVM双核/4096MiB。
继续用 SHA-256 为
`4908fb59ccd4e87ae4e8e973b7ef56f535448eacb24a87fd787270c0048987bc`
的只读基础镜像和新可写盘；未复用原失败 revision 或业务容器。QEMU使用临时UID1000/
GID108、无附加组和capability、no-new-privileges。独立owner负责最终归档、身份核对、
关机、真实wait、端口与物理宿主对照。

构建前后核对133个源码输入不变，工件另有逐文件摘要。实际镜像工具SHA-256为
`49a1bade1bdc3eaf3a425ce53db5a404242e4dbb0c1e1b8e336ce8aa7d363394`。
源码来自明确的dirty checkout，不冒充正式发布。

在安装任何构建依赖之前，真实CLI已完成负例：默认配方因缺少keyring被拒绝，stdout
无成功结果，builder没有启动，archive中没有新建build attempt目录。之后才使用Ubuntu
官方包源安装 `debian-archive-keyring` 及其他构建依赖。负例证据由单独
`bootstrap-preflight/summary.json` 保存，不将负例通过当作镜像构建通过。

本轮采用新的 `policy-restored-r1` revision。导出先保存为 `.partial`，只有传输成功、
三项归档成员准确、分片大小/摘要及合并fingerprint均匹配后才命名为镜像导出；失败
流不能因有 `.tar.gz` 文件名而进入后续测试。原始构建stderr、凭据、数据库和私有运行
目录不加入公开归档。

完整镜像、自动策略加载、OCI运行、工作流与本轮收尾的实际终态需另行追加；旧镜像或
诊断写层的成功观察均不得替代。Forgejo完整停止链路及剩余Incus里程碑不提前关闭。

## 第一轮终态

`runner-policy-restored-r1` 已退出1：准备与官方包安装成功；真实缺keyring拒绝、Linux上
的构建子进程零退出但未验签仍拒绝、文件身份和revision预检回归通过。官方
`debian-archive-keyring 2025.1ubuntu1` 已安装，所读keyring内容SHA-256为
`506b815cbb32d9b6066b4a2aa524071e071761e7e7f68c3ac74f3061ba852017`；
distrobuilder仍为官方3.2、ELF摘要为
`c095ea24c87d4a95dce7c0515534583eed25dcd7585aa6027c2fa2aff3856027`。
随后新镜像构建失败，未执行镜像启动或工作流门禁，不把已安装keyring误记成构建成功。

公开归档SHA-256为
`fccb2866ade1e162adc3816e63c870f83c9265b0f2a846742a3784247a99d761`。
没有将失败的 `.partial` 流命名为有效镜像导出。VM正常关机、QEMU退出0，没有强制退出。

物理宿主24个原容器、17个网络、卷、Docker身份/配置/unit、双栈路由和netns均一致；
nft原始对照为false，独立owner因此保留 `cleanup_passed=false`。精确差异仅为
`f2b-sshd` 删除一条封禁，Fail2ban日志记录01:37:11封禁、02:37:11自动解封，处于本轮
02:35—02:39的基线窗口。未操作宿主规则、恢复封禁或将历史对照改为true。
下一轮使用自己的新基线，这一差异不被隐藏，也不声称所有宿主状态都完全未变。

## 官方keyring布局误拒与精确兼容

独立只读诊断写层确认第一轮在构建前被拒绝，`attempt_count=0`；错误为keyring前置
检查。官方包的 `.gpg` 并不是普通文件，而是root所有、单链接、目标字符串为
`debian-archive-keyring.pgp` 的同目录相对别名。原预检拒绝所有符号链接，所以误拒了
合法发行版布局。这不是Debian签名验证失败，也没有触及镜像Hook阶段。

先新增九种布局回归，实际复现只有官方别名被误拒；随后只兼容这一固定别名。通过已经
验证的目录描述符读取链接，并以NOFOLLOW打开固定 `.pgp`，要求目标为可信单链接普通
文件，再次核对别名/目标/目录身份。绝对路径、父目录、其他名字、二次链接、额外硬链接
和可写目标仍拒绝；不修改安装包、不允许请求选择keyring。原普通 `.gpg` 布局保留。
相关镜像工具与库回归通过。

诊断写层 `inspect-restored-build-r1` 正常关机、QEMU0，全部物理宿主对照一致；原第一轮
失败盘摘要保持 `1336f77ab9306d2147dba11440cc6300d6b6e3d92cc4f9ff511aa747f878191b`。

第二轮新环境为 `runner-policy-restored-r2` / `anas-runner-bake-299c77` / 22222，仍为
全新Ubuntu26.04 amd64、2CPU/4GiB。仅在新实验VM目录设置no-COW，避免Btrfs与qcow2
重复写时复制，不改变业务卷、旧失败盘、QEMU持久化策略或宿主服务。构建前后134个源码
输入一致，新CLI摘要为
`40a69ff83f4001e5f5214f54f8405043ff8ffa36cb07d5984cfeb6bd42e1c30f`。
第二轮同时记录缺keyring负例、官方别名原生回归、新的 `policy-restored-r2` 构建及后续
自动加载/工作流结果；它们的终态分别核验。

## 第二轮：验签修复通过，仍有独立构建故障

第二轮实际通过官方包别名与Linux进程回归。独立读取测试输出确认四个必需顶层测试
各有run/pass且无skip/fail/漏项，包括错误签名警告即使子进程退出0仍拒绝、预检不占用
revision、普通keyring文件身份，以及官方相对别名与八种非法布局。不是只根据命令0
退出推断所有测试已执行。

这次真实debootstrap日志给出了三条 `gpgv: Good signature`，对应Debian archive与
stable Release签名，固定keyring内容摘要与官方安装包读回一致。单独保存只读观察的
日志摘要与计数，不把文件存在或单元回归当作实际签名验证。其后镜像构建仍退出1，
封闭诊断为 `builder_failure`、最后观察阶段 `hooks`。该标签不等于具体故障命令，
验签已成功也不能把镜像构建失败升级为成功；镜像/工作流阶段均未执行。

第二轮公开归档SHA-256：
`4349eac3b9964705cd9ecbf65b40a995f9cb2e787d1b7af2105b937793e508a4`。
归档包含独立验签观察和四项原生回归总结，无原始凭据、私有builder日志或数据库。
VM正常关机、QEMU退出0，无强制退出、无清理错误；物理宿主24个原容器、17个网络、卷、
Docker服务/配置/unit、nft、双栈路由和netns全部与该轮开始一致。失败revision和磁盘保留。

另开 `diagnose-signed-hooks-restored-r2` 写层定位具体构建错误。它使用相同测量的sealed
builder、官方keyring和源配方，仅在诊断副本的固定post-files脚本启用shell trace；
独立output/cache不登记发行版，不在原archive中重试，不把诊断产物用于正式验收。
原失败盘作为只读backing，退出后必须再次核对原摘要及物理宿主。诊断终态另记。

该诊断现已结束并完成正常关机、QEMU0、原盘摘要不变与完整宿主对照。完整日志补读确认
`apparmor` 包配置失败是宿主TMPDIR被继承进chroot后目录不存在，尚未执行post-files；
不是引导签名或guest策略加载失败。生产构建环境的同类问题已补回归并修复，新不可变
镜像与完整链路接续见[构建chroot环境](2026-09-25-incus-build-chroot-environment.md)。
