# Incus 已安装审批链路原生接续

状态：实施与原生验收核对。日期：2026-09-23。

接续 `/Users/whl/Documents/anas` 的已有暂存与未暂存改动，HEAD 仍为
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`。本轮没有提交、推送、reset 或重新暂存
既有工作。对应宿主动作通道及 Incus M10，不改为全部需求完成。

## 范围与已知基线

上一轮[服务期限与原生供给](2026-09-23-incus-service-execution-budgets.md)已经完成主要
发行版的直接后端 11 项门禁。本文另验真实已安装的 CLI/HTTPS owner、共享 job、一次性
确认、socket 激活 hostd，以及 PID 1 的独立进程退出证据，不能由直接后端结果推导。

仍使用指定 `ssh whl@ln.hlong.wang -p 2200` 上的独立 QEMU VM；本轮没有联网搜索。
VM 内是 Ubuntu 26.04 amd64、内核 `7.0.0-31-generic`，Docker 使用实验数据目录
`/var/lib/anas-host-provision-test`。物理宿主已有 24 个容器与 17 个网络不是测试写入对象。
远端实验根为 `/home/whl/anas-incus-followup-20260923.4p9ob_nk`。

## 第七轮真实失败及产品修复

第七轮 `anas-incus-host-0107ee` 的测试退出 1，表面错误为 `https_not_ready`。实际
systemd 日志显示 anasd 在宿主动作初始化阶段退出：`host action boundary is unavailable`，
没有进入 HTTPS owner 注册，更没有执行 Incus 写动作。

在精确 VM 内，独立只读探针核对 `/run/systemd/private` 的内核 peer 是 PID 1/root/root，
EXTERNAL 认证成功，但立即跟在 `BEGIN` 后面的第一条二进制服务查询可能滞留到客户端
期限结束/连接关闭。短时 PID 1 调试日志验证了该时序；调试级别只在测试 VM 内改变，并在
finally 中恢复。对照探针等待 UNIX 发送队列实际清空后，20 次服务查询全部成功。

另一个独立问题是直接 PID 1 连接上的 `Unit.Ref/Unref` 返回
`org.freedesktop.DBus.Error.InvalidArgs`；这些操作需要消息总线客户端身份。固定系统
总线上的 Ref/Unref 对照成功。不能简单删除引用保留，也不能将普通系统总线的应答当成
独立进程身份或退出证明。

修复位于 `internal/hostaction/systemd_auth_linux.go`、`systemd_reference_linux.go`
和 `systemd_exit_linux.go`：认证与方法请求之间按实际 `TIOCOUTQ` 排空衔接，等待有界、
可取消，既不靠固定睡眠，也不重新发送可能已执行的方法。身份、单元 invocation、退出
状态及空进程集仍全部来自经内核核验的直接 PID 1 连接。

辅助 `/run/dbus/system_bus_socket` 连接只持有 executor unit 的引用，防止单元记录被
回收；不提供任何授权或退出数据。取得引用后重新核对实际存活进程与同一 invocation，
结束时仍先取得真实退出及空进程集，再释放引用。辅助连接错误/丢失不能把缺失证据升级
成成功。没有新特权动作、第三个 root 程序、环境指定 bus 地址或开发认证开关。

真实 Linux UNIX socket 回归先以旧行为复现两个失败，再验证：未消费认证字节不前进、
实际消费后前进、期限/取消生效、空/关闭连接拒绝。原有 PID 1 假冒、进程替换、丢失管理器、
非零/信号退出及仍有进程的终态回归同时通过。

## 测试夹具修复与门禁收紧

第八轮 `anas-incus-host-e90d2f` 已通过真实安装身份、HTTPS owner 注册/登录、未认证拒绝、
socket 激活 hostd 预检和未知参数拒绝。跨工作区负例中，旧夹具没有把产品约定的
`400 invalid_json` 作为正确拒绝。第九轮进一步发现夹具只识别 `application/json`，
将实际 `application/problem+json` 丢为 `{}`，因此严格错误码断言仍不能完成。
这些是夹具错误，不作为产品越权的证据；旧失败日志保留而不改写成通过。

当前夹具严格解析两种 JSON 媒体类型及合法参数/大小写，不再用空对象吞掉错误响应；
HTML、伪媒体类型、null/数组和损坏 JSON 拒绝。owner 创建要求真实 `201 Created`。
重启就绪只重试只读请求，证书校验错误不重试，已经提交的写请求从不自动重发。

完整入口现要求 **17 个不同的通过阶段**：增加使用有效 token 在错误工作区执行的负例，
随后同一 token 必须仍可在正确工作区使用。工作区不匹配必须匹配 `400 invalid_json`，
重放必须匹配 `409 confirmation_consumed`；认证失败、过期或服务不可用不算这些负例通过。
原始 token、密码和私钥不出现在公开 job 结果与测试报告。

## 工件身份

第八至第十轮产品工件由同一批未提交源码构建，编译前后核对 390 个输入，实验版本为
`0.0.0-native.20260923.2`。这只是带来源记录的实验预发布身份，不是签名或正式发布。
第九/十轮仅修正夹具，复用前述产品二进制；新 manifest 逐一验证全部非夹具输入和工件
未变化，记录父 manifest、夹具源码与执行文件摘要。

| 产品工件 | SHA-256 |
| --- | --- |
| `anas` | `fa85507351178ffc44b52d35da297712df37873c10bbe6d267100a1b73c24e34` |
| `anasd` | `5f393bdf4826eb3aad1d92863c55f1d04ae45ca8c1510e417c92f12ec924a440` |
| `anas-hostd` | `fc0ac3f7b157ca132bfd553fd44ecbef030595b64120755846f42bbb8e303e1d` |

## 验证与环境收尾

产品修复已通过全仓 `go test -p 2 -count=1 ./...`、hostaction/jobexecutor/
incusprovision/anasd 竞态测试、全仓 `go vet` 及 Linux hostaction vet。Linux arm64 的
hostaction 测试程序和 hostd 交叉构建通过，不称为 ARM64 原生验收。Linux 原生 socket
回归在第七轮独立 VM 内通过，不在物理 Docker 宿主上执行服务写动作。

第七至第九轮均已正常关机，QEMU 退出 0，精确 QMP socket 与回环 SSH 端口释放。
各轮独立的物理宿主前后比较中，原容器、网络、卷、Docker 服务/配置/单元、nft、
IPv4/IPv6 路由及 named netns 全部相同。私有失败状态保留在各自测试盘，不清除后重跑。
公开归档只包含 `reports`，不含 TLS、bootstrap、完整认证状态或 Incus 管理凭据。

| 轮次 | 公开归档 `host-vN/reports/root-action-run01-evidence.tar.gz` SHA-256 |
| --- | --- |
| 第七轮 | `bd1eba3452c476dfbdeb3fc2404de9b150a94a933e36bb426df640ddf4e2558d` |
| 第八轮 | `667ace6bf241fd5184b679583c775df4fc71ffb7d9a9756a9a47a8d06afab23d` |
| 第九轮 | `aca7c99d6bda2b8e9e7bc81774590079166c190bd20ed4e406e7d23338aba995` |

第十轮 `anas-incus-host-b05124` 使用新的干净磁盘，仍为 KVM 单核/2048 MiB、普通 UID
QEMU、空 capability 和 no-new-privileges；其完整终态、归档及宿主对照另列，不能用前述
部分阶段或本机回归代替。

## 第十轮：真实确认执行与 root 单元包管理能力

第十轮已通过安装身份、真实 HTTPS owner、预检、参数拒绝、两个跨工作区负例、已确认
skip 和 consumed-token 重放拒绝，共十个阶段。真实安装 job 正常取得审批后，在
`install.packages` effect 失败，持久失败记录和 `needs_compensation_check=true` 均保留；
没有将其误记为未知进程或清除 intent。实验 Docker 身份/基线相同。

独立诊断只在该 VM 的新建缓存目录执行索引更新，不安装包、不复用原失败缓存，每次前后
验证供给 state 摘要不变。同样 systemd 保护组合下更新退出 100，`seteuid 42`、
`setresuid` 返回 EPERM；进程 permitted/effective capability 缺少 `CAP_SETUID`，bounding
集合仍含它。单独的 NoNewPrivileges、ProtectSystem、PrivateTmp、ProtectHome 或地址
族限制均不能复现组合失败，不能归因成其中单项必然不兼容。

完整组合仅显式增加保留 `AmbientCapabilities=CAP_SETUID` 的精确对照成功切换 UID，
APT 官方索引更新也退出 0。发行单元据此修复，测试先复现缺项，再要求能力与原保护同时
存在，且不允许把该能力加给 anasd 或 relay。没有禁用 APT 的沙箱配置或放宽包/命令边界。
诊断目录刻意保持私有，APT 明确报告 root 下载回退：这只证明失败的用户切换问题被修复，
**不是** `_apt` 负责所有下载的安全验收。本次不把该警告藏掉或写成下载隔离通过。

## 第十一轮：17 项已安装审批门禁全部通过

第十一轮 `anas-incus-host-445391` 的完整入口退出 **0**，17 个不同阶段全部通过。
包括真实 HTTPS owner 注册/登录、CLI 预检、具名计划/一次性确认、两个跨工作区反例、
skip、完整安装/配置/登记/卸载，以及真实重启 anasd 后 consumed token 仍被拒绝。
11 个共享 job 全部成功，独立 journal 中观察到 11 个 hostd 激活单元，没有遗留活动的
hostd 执行进程；该轮实验 Docker 恢复其初始基线。它验证主要发行版的实际安装服务链，
不是 Mock HTTP、直接调用后端或正式签名发布。

本轮接续实际发现：测试在 16:13 已完成，但此前会话中断，QEMU 在 17:41 被外层时限
看门狗发送 SIGTERM 结束，退出 **124**。因此不能宣称该 VM 正常关机。确认进程与 QMP
socket 消失、22145 端口释放后，对原 qcow2 做只读 qemu-img 分区导出，通过无写入模式
debugfs 只取 reports 下 12 个固定 JSON 文件；不 mount、不重启 guest、不执行原测试，
也不修改失败/成功记录。导出的 summary 与外层保存的完整终态逐字段一致，11 个 job
报告均为 succeeded 且不需补偿。导出时无法把派生文件 chown 为 guest root，但内容逐个
解析验证成功；派生文件位于宿主 whl 所有的私有实验目录，不将其权限当成 guest 权限证据。

原测试盘导出前后 SHA-256 一致：
`9ce664b7095c4b24a06f6b19e153489188d19b622c19ba2e1314b8fe255bc3e7`。
只含公开 reports 的 `host-v11/reports/root-action-run01-evidence.tar.gz` 摘要为
`705868be6960dafce707d23b6c299b821183a475cb207ee4d758f8077ce225b6`。
包含完整文件系统的临时 raw 导出已删除，原实验盘保留。物理宿主独立前后对照的 24 个
已有容器、17 个网络、卷、服务、配置、nft、IPv4/IPv6 路由及 named netns 均相同。

第十一轮实际交付清单是 `root-action-v6/source-manifest.json`，仍显式记录未提交源码与
native 实验版本；不使用早期轮次摘要代替当前交付：

| 工件 | SHA-256 |
| --- | --- |
| `anas` | `93a0858975e63ac681e8e1e4120c59c0ba0ed16fce5deef981657c9c15aa9e3a` |
| `anasd` | `7f4868f8358781d3e32633f84ea8e9101dbb9f95eac0fb7e0fff9230a5ba9a0c` |
| `anas-hostd` | `8ba41e68c6875afc3da078b392a3044b38e0eb715729d3c7f86af4365924df01` |
| hostd unit | `e6a62ec9539974baf740efde3f03e20a73ab6916ee6c3346beffcdc08c8dc3ce` |

后续另加真实消费者容器的控制桥验证，将必需阶段增至 21 项。旧 17 项结果仍是本轮审批
链路的有效历史记录，但不能替代新增四项。实现与新运行记录见
[消费者控制桥接续](2026-09-23-incus-consumer-control-bridge.md)。
