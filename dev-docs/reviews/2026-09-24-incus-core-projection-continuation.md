# Core/Compose 投影：启动就绪与跨项目验收接续

日期：2026-09-24。状态：实施与独立实机核对；仅按下文已取得的终态确认验收范围。

接续 `/Users/whl/Documents/anas` 的暂存及未暂存工作；HEAD 仍为
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`，不提交、不推送、不重新暂存。
按需求/计划索引、两份 Incus 文档及宿主供给架构继续 M10。指定 SSH 为
`whl@ln.hlong.wang:2200`，只操作独立实验 VM，不使用物理宿主既有 Docker 执行测试。

## 接续时核实的实际状态

工作树已经包含前轮的资源计算顺序、非 root 镜像副本权限和 run-only Provider 空服务集合
修复，不能根据较早答复重新覆盖它们。相关实现背景见
[前轮 Core 投影记录](2026-09-23-incus-core-projection.md)。

第三十四轮 `anas-incus-host-27aba8` 的公开报告确认：实际安装/配置/登记、Core 初始化、
render、架构冻结及私有凭据投影通过，消费者在 `foreign_project_status` 收到 HTTP 500，
因此完整 Core 门禁失败。这不是自身 mTLS 连接成功就足以证明隔离。公开归档摘要为
`2a472e20be4f1fd0bf75a02d6e0be44c391e57853880018ffa0a609c8f5ceafc`。
外层记录 test_exit=1、qemu_exit=0、正常关机、无强制退出和 cleanup_passed=true；原盘与
报告保留。实验 Docker 未恢复不被混同为物理宿主 Docker 发生变更。

## 本轮修订

Core 按依赖顺序启动消费者；第一个消费者启动时，后一个项目尚不保证存在。原测试助手
在启动时执行自身、对方、default、自身的完整探针，把后续项目创建时序混入了当前服务
就绪条件。单独回归已实际复现这一依赖；第三十四轮的 500 是否完全由该时序导致仍需新
实机结果验证，不把假设登记为已经证明的 daemon 缺陷。

现在常驻模式只验证自身项目可用并输出 `own_project_ready`；有限 `--once` 模式保留
完整跨项目与最后自身正对照。原生驱动先独立确认两个真实 restricted project 和 default
存在，再从双方实际容器执行有限探针，且只接受 `existing_project_isolation` 结果。
500、503、401、429、连接失败、最后正对照失败或错误范围报告均不算隔离通过；没有放宽
生产认证、增加消费者管理凭据或以不存在的项目充当权限反例。

新增三个行为回归及原有助手、Runner、Hook、Provider 包测试通过。新输入以
`0.0.0-native.20260924.1` 显式实验版本构建，773 个源码/测试/模块输入在编译前后未变化，
不是正式签名发行。实际同版本 CLI 摘要为
`39f28ffbfedfb9c247b74672b2c77185b84aec560576d239985fdcc96297ce45`，
原生测试摘要为 `24f42d326fc1449496210f36653fe031a15f13353cf19144dcaae90a13c29e0c`。

新第三十五轮为 `anas-incus-host-94f579`，独立 Ubuntu 26.04 amd64、KVM 单核/2048 MiB、
回环 SSH 22180。源输入、日志及本机回归保存在
`/tmp/anas-incus-core-20260924.xrD9jv`；远端实验目录为
`/home/whl/anas-incus-followup-20260923.4p9ob_nk/host-v35`。
完整结果、公开归档及外层收尾在实际取得终态后追加，不能由本机测试或入口存在推导。

## 第三十五轮：实际部署通过，实例枚举不能充当隔离证据

真实 `apply`、非 root Provider 的镜像导入、两个消费者的自身租约访问及双网络检查均已
通过，且没有常驻 Provider。驱动独立确认两个 restricted project 与 default 均存在后，
有限隔离探针仍失败。该轮公开归档摘要为
`34fd8459575ff2c7c340c5ec467cf92994064a38feaaec6f60d1bdf607e3c93d`。
外层 test_exit=1、qemu_exit=0、正常关机、无强制退出、cleanup_passed=true，物理宿主
容器、网络、卷、服务配置、nft 与双栈路由对照全部相同。

另用独立写时复制层只读取得该探针的封闭诊断字段：`existing_project_isolation` 下
`foreign_project_status=500`。诊断没有读取或导出客户端私钥；原失败盘 SHA-256
`aff0efea39629fdee48bbbc3627ec5d3e11543de84f7289f533ed451767dc53a` 保持不变，诊断
正常关机、实际 QEMU 退出 0，宿主对照一致。因此不能再将此错误单纯归因于项目尚未创建，
也不能把内部错误码改判为授权拒绝。

后续有限探针改用固定 named-project GET 检查实际项目可见性，自己项目的成功响应必须
给出一致的项目名与 restricted 标志；另一项目及 default 仍须明确拒绝。启动检查继续
请求自己的实例清单。500 不算通过，实例枚举错误也不作为项目可见性证明。该验证范围
是投影证书的项目权限，不是 guest 作业或全部 Incus API 的兼容性验收。

物理系统盘剩余空间不足以继续安全堆积实验盘。新实验改用现有 `/data` 文件系统中的
独立 0700 私有目录 `/data/anas-incus-20260924.Hd6AM0`，未更改该盘已有数据，也没有
为腾空间删除失败盘、业务容器或私有状态。新 VM 的完整终态仍单独记录。

## 第三十六轮：双方项目隔离通过，重复激活须遵守部署状态

新 `.2` 工件在 `anas-incus-host-9ad8bb` / 22182 上通过真实 render/apply、非 root 镜像
导入、自身实例清单访问以及两个已存在项目的双向可见性限制，最后自身正对照也成功。
这证明正确投影的受限证书与控制网络可以工作；没有把旧的实例枚举 500 判成通过。
完整入口仍失败在对同一个已 active 的冻结 deployment 再次 `apply` 的测试命令。

独立 overlay 只读诊断确认精确机器错误 `deployment_not_ready`、requires_ready=true，
与现有 `workspaceDeploymentPlanApplication.Apply` 的 ready-only 合约一致。因此修正
的是测试流程，不把 active 状态改回 ready，也不放开旧版本的重放。新增普通 Go 回归
检查重复激活的错误码和状态不变；原生流程先验证同样的拒绝及私有 Store 不变，再实际
render/apply 第二份冻结部署，复核原证书及两个容器的项目权限。

第三十六轮归档摘要为
`aacfbd2326a286cbf2f03638cf693bbdb0e2ba4a409332496913894250a141ca`；正常关机、
实际 QEMU 退出 0、无强制退出，物理宿主全部对照相同。它不是完整 Core 生命周期通过。
新第三十七轮使用 `0.0.0-native.20260924.3`、独立 VM `anas-incus-host-d9e177` / 22184，
同样位于新的私有数据盘目录，终态单独追加。

## 第三十七轮终态：完整 Core 投影闭环通过

`anas-incus-host-d9e177` 的独立完整入口退出 **0**。五个外层阶段全部通过：真实已安装
审批完成宿主安装/配置/登记、Core CLI/Compose 投影、确认后的宿主连接撤销、撤销后
拒绝自动凭据回退，以及实验 Docker 基线恢复。归档包括 **8 份成功宿主作业**；Core 主
测试及八个子项共 **9 个**必需 pass，独立撤销测试另 **1 个**pass，无缺项、重复或 skip。

实际流程包括 public `init/config import/render/apply`，没有手填 endpoint、架构、
管理证书、私钥或控制网桥。两个合成消费者运行于 UID/GID 65532、空 capability 与 NNP，
各持独立私钥，通过实际控制桥读取自身实例与项目；另一个已存在项目及 default 被拒绝，
最终自身正对照保持成功。非 root 生产 Provider 从固定只读副本导入实际 fingerprint，
没有常驻 Provider 容器，业务网关优先级保持不变。

第二份真实冻结 deployment 已 render 并 apply，原私钥保持，两个实际消费者在新激活后
再次通过项目权限探针。随后实际 CLI stop、精确 Compose 清理及归属核对完成，宿主确认
卸载撤销连接。再 render 时明确 `calculate_failed`，原私有 Store 未变、无残留 staging，
没有用旧 Secret Store 中的凭据绕过已撤销的 host bundle。

公开归档：`/data/anas-incus-20260924.Hd6AM0/host-v37/reports/root-action-run01-evidence.tar.gz`。
SHA-256：`9652e4f18b97c49d9ba7ca91bbe1506517763285374cb7c80edbb48d6b5b4f56`。
独立复核报告为同目录 `independent-verification.json`，逐项检查实际归档、必需事件、
8 个作业和源工件摘要；归档只含具名公开结果，不含凭据目录、TLS key 或完整私有状态。

外层 test_exit=0、qemu_exit=0、normal_shutdown_requested=true、forced_quit_requested=false、
cleanup_errors=[]、cleanup_passed=true。精确 QMP socket 与 22184 监听消失；物理宿主
24 个既有容器、17 个网络、卷、Docker 服务/配置/单元、nft、双栈路由及 named netns
全部与本轮开始对照相同。新实验盘保留在私有数据盘目录，没有删除或重置旧失败盘。

实际工件绑定 `0.0.0-native.20260924.3` 与 773 个构建前后未变的源输入：anas 为
`1982c49cc676508340ab3fd7593d0472ebe002984b18eb14073a70e247057b7b`，Core 原生测试为
`0d2f2f347e2b29554ca51bcaa25bbb66d1ae0e5d8ca68fb7cffdbb7316897a8b`，Core 输入清单为
`487166d445130281cda5689eda4b58401c245a2efffb0e4b7f35b5004ebd1155`。
这些是明确的 dirty-checkout 实验工件，不是签名正式发布。

本轮全仓 Go 测试、Runner/消费者探针竞态、go vet、68 项 Incus Python 回归及 4 项
浏览器离线 guard 通过。最新 Core 测试和消费者探针的 Linux ARM64 交叉编译通过，不计
为 ARM64 原生执行；原生实际平台仍仅为上述 Ubuntu 26.04 amd64。

最终 Module/Contract 文档生成检查、需求覆盖、计划与需求状态索引、共享构建静态检查、
升级目录门禁和暂存/未暂存差异格式检查通过。对照已测 manifest，运行代码、测试和安装
输入未再改变；终态后仅更新两份 Module 技术文档的验收说明。HEAD 保持不变，原有暂存
与未暂存工作保留，未提交或推送。

## 验收边界

本轮关闭的是已定义的真实 Core/Compose 自动投影、双合成消费者、重复新部署、清理及
撤销后拒绝闭环，不是完整 Forgejo/AI Agent 产品部署，也不是 guest 启动或 one-job。
本夹具的不可变镜像只是实际导入测试，不可启动且不是签名 catalog 产物。ARM64/VM、
未适配主机的完整降级、失败 intent 恢复、生产 ingress、正式镜像/回滚仍沿原里程碑
继续实施；M10/M11/M12 与 Module developing 不由此整体关闭。
