# Incus 宿主连接到 Core/Compose 的自动投影接续

状态：实施与验收记录，完整业务部署及未列出的平台仍分别验收。日期：2026-09-23。

后续启动就绪/跨项目探针及新的独立实机结果见
[2026-09-24 接续核对](2026-09-24-incus-core-projection-continuation.md)。本记录保留前轮
实际失败和修复，不将较早阶段当作最新阻塞或最终验收结论。

接续 `/Users/whl/Documents/anas` 的原有暂存与未暂存工作；HEAD 保持
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`，本轮不提交、不推送、不重新暂存。
沿需求/计划索引、Incus 要求与计划、宿主供给架构接续 M10。测试只使用指定
`ssh whl@ln.hlong.wang -p 2200` 的独立 VM，不修改物理宿主既有 Docker。

## 实际部署路径的两个缺口

`materializeDeployment` 原先先为全部消费者准备资源，再运行所有 calculate Hook。
compute 镜像目标却必须从 Provider 的 `IMAGE_ARCHITECTURE` 冻结；默认 Incus 路径只有
在 calculate Hook 读取已批准的宿主连接 bundle 后才得到这个架构。因此只测预填环境的
资源函数、独立 Hook 或宿主审批，不能证明无需手填连接参数的 Core 部署能走通。

新增回归使用不同名称的 `sandbox_provider` 复现顺序问题。现在每轮 calculate 持有一个
资源准备器，按已有依赖顺序，在 Provider 计算后、消费者 Hook 前准备该消费者的资源。
数据库/对象存储/compute 共用原实现与跨消费者冲突表；镜像目标仍先校验，随后才生成
稳定凭据。没有在 Core 中新增 Incus 文件读取或按 Provider 名称开特例。完整部署生成
回归经过真实模块清单、Hook 子进程、Secret Store、渲染、持久清单和第二次生成。

另一个缺口是只读镜像文件投影权限。Core 生成的描述符/镜像副本为 0400、目录为 0500，
而真实 Provider 以 65532:65532 运行。只读 bind mount 不会使 root 私有文件对非 root
可读。新增权限回归复现后，仅把校验过的非秘密镜像副本/描述符改为 0444、投影目录改为
0555；私有 staging 父目录仍为 0700，原始归档权限不变，挂载仍只读且只有两处固定输入。
不投影证书、Secret Store 或整个宿主目录，也不把 Provider 改为 root。

## 本机验证与原生入口

Runner、Incus Hook 与 Provider 的完整包测试通过；新消费探针的权限与秘密边界回归、
原生报告完整性回归分别运行。新增 `server-incus-core-projection-e2e.py` 复用已有真实
HTTPS owner/共享 job/hostd 供给以及有界日志监督器，不替换认证或后端。

原生计划使用原样 Incus Module/Hook/Provider、真实 CLI render/apply 和两个明确的测试
Compose 消费者。配置不提供 endpoint、架构、管理证书/key、存储池或控制网桥名。每个
消费者通过其实际投影证书验证自己的 project，并拒绝另一个 project/default project。
同时核对业务网络的 gateway priority、私有凭据隔离和重复 render/apply 的稳定身份。
已批准宿主卸载撤销 bundle 后，保留的自动来源 Secret 不能让新 render 继续成功。

测试用镜像是精确字节绑定的导入夹具，不是可启动 guest、正式 distrobuilder 或签名发布；
两个消费者也不代替完整 Forgejo/AI Agent 业务流程。原生终态、归档、源码与工件摘要、
QEMU 实际退出和物理宿主对照将在实际取得后追加，不由入口存在或本机测试推导成功。

## 第三十二轮：夹具初始化前置条件

全新 Ubuntu 26.04 amd64 VM `anas-incus-host-5897c5`（22173）先通过真实审批的安装、
配置和登记，以及原生测试的环境、镜像运输与源文件准备；实际 `anas render` 随后失败。
完整 Core 门禁未通过。公开归档 SHA-256 为
`7c5798814a7a6101d659ca8b0fd561664ec4a67d7e060c247d7c5ea8f227751b`。
监督器 test_exit=1、qemu_exit=0、正常关机、无强制退出、cleanup_passed=true，物理
宿主原有 Docker 与全部网络对照一致。VM 内未完成的测试资源保留，不能把它的未恢复
实验库存与物理宿主业务 Docker 混为一谈。

在独立写时复制 overlay 上读回私有命令诊断，实际错误为 `usage`：测试目录没有经
`anas init` 建立 `.anas`，因此根本没有进入 Provider 计算。旧短时诊断未等到 SSH 就绪，
增加独立诊断等待后取得该原始错误；两次诊断均正常关机，原失败盘 SHA-256 始终保持
`d8f1de6d3d347f1511147ce26dc8798f419aa539be8e606a728355872b08a163`。没有在旧盘上
运行新的 render、覆盖私有状态或清除 failed intent。

夹具改为实际 `anas init`、`anas config import` 再 render；不手写 config-managed.yml，
不放宽工作区或配置摘要准入。本机完整准备回归也经过这些公开初始化/导入步骤，并保留
原有 `resource_invalid` 错误码。新修订工件为 `0.0.0-native.20260923.11`，在编译前后
绑定 772 个未变化的源码/测试/模块输入；不是正式签名发行。

第三十三轮使用独立 VM `anas-incus-host-06e9f9`（22175）和新测试程序，旧第三十二轮
仍保留失败身份。当前本机完整 Go、相关竞态、go vet 与 68 项 Python 门禁通过；这些
结果不替代新一轮的真实 Core/Compose 终态。

## 第三十三轮：实际 render/apply 与空服务集合缺陷

实际 `init`、配置导入、render、宿主架构冻结及两消费者的私有凭据投影均通过，真实
`apply` 返回成功，两个 project 也已创建；消费者探针未通过，故完整入口仍失败。
公开归档摘要为
`b2cedbfc6edd53e68e2477b62763a7eefa22fe71b72d167b1aae9853a430cb61`。
该轮正常关机、实际退出 0、物理宿主前后对照相同；失败消费者与私有诊断留在原盘。

独立 overlay 只读盘点进一步发现，除两个消费者外，Core 还错误启动了一个没有操作
参数的 Provider 常驻容器。原 `services()` 已移除所有 Contract operation service，
但调用方把空列表交给 `compose up -d`，Compose 将其解释为“全部”。新增通用回归
实际复现一次性 Provider 与全服务被禁用两种情况；混合常驻/一次性服务的正对照保持。
两条启动路径现在共用空集合检查，只抑制容器启动，不跳过资源/凭据/ready barrier。
没有在 Compose 中伪造占位服务或把 Provider 改为常驻。

原生门禁另要求 apply 后只有两个真实消费者、没有持久 Provider 容器。跨 project 负例
也单独确认双方 project 已存在后再运行，避免把第一个消费者启动时另一个 project 尚未
创建的 404 当成授权证明。消费探针只输出封闭的失败阶段和 HTTP 状态，不复制证书、
密钥、响应原文或任意错误文本。第三十三轮本身不计为这些新增门禁通过。
