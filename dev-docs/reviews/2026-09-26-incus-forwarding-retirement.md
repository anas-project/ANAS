# Incus 转发显式退役与原始内核证据接续

状态：实施与原生验证接续；生产启用、自动续期及真实 guest/Forgejo 默认 Docker 路径未关闭。
记录日期：2026-09-26。原生日志时间以服务器实际 UTC 时间为准，不改写已有记录。

继续实际 `/Users/whl/Documents/anas`，HEAD 为
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`。原暂存 diff SHA-256 为
`fc6a330643646f357c5c7e95f3a17cb63b663f7fa47313e8a1e0c534c209d0fb`，本轮不暂存、
提交或推送。所有实机操作经 `ssh whl@ln.hlong.wang -p 2200`，不联网搜索。

## 先核实实际接续状态

上一条可见回复不是最后的执行事实。重新读取仓库和保留报告，确认 `kernel-r1/r2/r3`
均正常关机，随后发现保留的 `kernel-r4` 已完成第六轮。尝试排他创建同名实验目录时
因目录已存在而拒绝，没有覆盖、复用或修改原实验；随后只读核验其真实结果。

第六轮为 `anas-incus-host-05f398` / 22276，内核测试退出0。独立解析17项唯一
run/pass、最终 package pass、`kernel-result.json` 和宿主收尾结果，确认精确许可、
其他源/端口拒绝、更早管理员拒绝、物理源冒用拒绝、新连接关闭与同一既有连接撤销，
并完成原对象移除。端点仍为 namespace 进程，不是 booted guest 或业务工作流。

78 个原输入与本轮开工前源码相同。原测试二进制 SHA-256：
`64d02e0295d8da6d9e429fdc5edc2dec314f1d0ad3c7a8a6bebd8ef80df89e03`。
公开归档：
`/data/anas-incus-forwarding-20260925.bwWwTX/kernel-r4/reports/public-evidence.tar.gz`。
SHA-256：`9abc4c795de777f90279be37e84b0fa36287cd497ecef3447b3d66490ac548ac`。
QEMU0、正常关机、无强制退出、十项物理宿主对照全相同；重新查进程与22276无监听。

## 本轮产品变化

新增同一 `incus.forwarding.permission.plan` / `incus.forwarding.permission` 动作的
`retire` 操作，不新增 root 入口、消费者 capability 或任意脚本接口。先复现旧请求
拒绝 retire，再补实现、共享确认和 API 回归。disable 仍只撤销连接并保留拒绝表，
不能把“已停用”误当作“无残留”。

退役需原记录已 disabled 且两类连接撤销已确认；同一已注册部署已 stopped、运行锁
无残留标记；原证书撤销且不存在可进入该项目的替代证书或其他全局证书；整个项目的
实例及操作为空，原物理网桥没有任何端口。实际 API 与内核双重读回跨越同一宿主锁
和工作区共享锁。它不代替用户执行 stop/revoke/delete，也不要求被撤销的旧凭据再
次登录。失败、旧 boot/接口变化、观察缺失仍保留原拒绝对象。

成功经原内核 Release 移除兼容链、集合与拒绝表，保留原授权和失败历史的 released
墓碑；原依赖阻止才可解除。又以红灯回归复现并修复两处缺口：released 字符串可掩盖
非零句柄、集合、兼容入口或未撤销连接；最终会话清理失败会返回错误但没有持久保留
依赖阻止。现在均拒绝虚假终态，未确认退役保留 failed 状态。

生产 enable 的 `forwarding_lifecycle_integration_unavailable` 门禁保持。没有自动
续期、跨重启恢复或已退役许可复活能力，不将本次退役接线标为全部生命周期完成。

## 新冻结工件与测试范围

新环境为 `/data/anas-incus-retirement-20260926.5p9Wxh/retirement-r1`，VM
`anas-incus-host-d27480` / 22280，Ubuntu26.04 amd64、KVM双核/2048MiB，独立32GiB
写盘。只读基础镜像 SHA-256：
`4908fb59ccd4e87ae4e8e973b7ef56f535448eacb24a87fd787270c0048987bc`。
QEMU UID1000/GID108、无附加组/capability、no-new-privileges，无业务目录/socket映射。

252个源码输入冻结后重新编译；构建前后摘要一致。内核测试 SHA-256：
`50241dcb129a4f7135a57691353ef34e2e2f8e581f2aa7e74e654e8b5903b7e8`；退役测试 SHA-256：
`3a026bbe4d0a581f194b9d3b29623c457241ce0c8b281c86cc7307f0080ae4ed`。
准备脚本 SHA-256：`ccc85ea837894f7bb4a9f5262390ef78592e888bc3eb5478054096f72246663f`。
由新 Docker 自行开启转发并保留 FORWARD DROP，没有使用预先启用转发的旧夹具。

退役专项调用实际安装后端、真实 Incus 证书/空系统容器及网桥、内核执行器和原宿主
持久状态。Core stopped 元数据与历史 grant 是明确准备的夹具，不冒充原始宿主审批、
Core stop、Provider 整体供给、启动 guest 或 Forgejo 业务验收。完整终态、公开证据
和正常关机/宿主对照须以实际返回追加，当前不能仅凭启动测试宣告通过。

## 后续实际终态

第一轮最终内核通过、退役计划拒绝，完整入口退出1；正常关机、QEMU0、物理宿主十项
对照全部相同。第二轮增加独立正例和诊断，明确拒绝来自删除后暂留的两条 success
操作记录，未放宽产品或删除记录。第三轮以新冻结工件等待记录自然消失，再验证独立
端口反例、实际退役和重复退役；完整入口通过，原状态及失败历史保留，正常关机与宿主
对照通过。同步修复许可刷新后读回/保存/会话失败未撤回的产品出口。源码身份、全部
实际范围与最终归档见[失败撤回及退役终态](2026-09-26-incus-forwarding-failure-withdrawal.md)，
不将第三轮通过倒填给本文件前述第一轮工件，生产启用及真实 guest/Forgejo 仍未关闭。
