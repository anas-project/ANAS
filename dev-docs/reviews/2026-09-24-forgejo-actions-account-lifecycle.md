# Forgejo Actions 账号停用与清理证据接续

状态：实施与独立验收记录；完整 Core/Compose 开关及运行作业排空另验。日期：2026-09-24。

接续 `/Users/whl/Documents/anas` 既有暂存和未暂存改动，HEAD 保持
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`。本轮不提交、不推送、不重新暂存。
先读需求/计划索引、Incus 要求/计划及宿主供给架构，再核对真实消费者接续。
指定测试连接为 `ssh whl@ln.hlong.wang -p 2200`，无联网搜索；物理业务 Docker 不作为
任何写操作目标。前轮 `trust-r2` 镜像/工作流与宿主收尾的独立证据已复核，不重复烘焙。

## 实施范围

前轮工作树已包含专用 `actions-account` helper、HMAC 账号归属回执及恢复管理员后的
Hook 接线，本轮继续审查和实测，不把这些已存在代码归为新发现的空白实现。
要求依据为 `FORGEJO-R-067`—`R-070`：独立账号、stdin 凭据、controller 的 scope API
边界、既有站点管理员权限偏差及关闭功能后管理口令不得继续可用。

本轮实际修复两处会阻止安全停用的缺陷。

第一处，Compose 启动禁用模式的清理进程是异步的，原 Hook 立即要求它已经 exited，
没有等到清理完成的机会。现在限时等待同一不可变容器 ID，连续两次 terminal 读回均须
确认固定入口、UID/GID、无参数、exit 0、未重启。期间替换、恢复运行、异常退出、取消
均失败。观察 JSON 的重复/别名/缺失/null 字段不能被结构体解码静默覆盖。
期限传入实际 Docker 子进程，单次命令有界，输出最多 64 KiB；失败文本不回显。

第二处，controller 状态原先通过无界 `os.ReadFile` 读取，会跟随链接；失效链接被当成
全新空状态，重复 workloads 字段或缺少 workloads 也可能覆盖未完成工作。新增回归实际
复现后，改为私有父目录的描述符、NOFOLLOW/O_NONBLOCK 单链接普通文件读取，大小
最多 4 MiB，并核对读前后身份。JSON 拒绝递归重复字段、顶层别名、缺失或 null 的 workload 清单以及
未知字段；真正缺失的全新状态不创建目录/文件。它不解决整个 state volume 丢失后的
孤儿重建，也不把消费者可写的状态当成抵抗已攻陷进程的独立授权证明。

账号 helper 保持固定回环 API、独立恢复管理员、精确数值 ID/HMAC 回执和变更前后读回。
禁用只使 ANAS 管理的密码失效，不删除账号、不撤销人工 token/SSH key，不掩盖站点
管理员角色仍未按 scope 收敛。清理未证明完成时不先撤销其仍可能需要的密码。

## 实机入口与前两轮准备失败

新入口 `test-env/scripts/server-forgejo-account-e2e.py` 在全新 Debian 13 amd64 VM 运行
固定 Forgejo 15.0.7 与实际 helper、controller。使用 SQLite 作为独立测试后端，服务
UID/GID 1000、HTTP 仅回环，无 Docker daemon，不伪装成完整业务栈。
只读密码哈希相等性验证不向公开证据输出哈希、盐、密码、会话或数据库。

`forgejo-account-r1` / `anas-incus-host-da592b` / 22189 和 `forgejo-account-r2` /
`anas-incus-host-3622a9` / 22190 均在服务启动阶段失败，尚未验证账号变更。两轮正常
关机、QEMU 退出 0、无强制退出、物理宿主全项对照一致。公开归档 SHA-256 分别为：

```text
2fee676d78bd857a7e86d4b7a30c5905b4314427a272ea0f228075c48adbe1dc
cba2c2d25471b61529d5dc5a14798bc97f124d71c92f59db6712a4b15143b699
```

离线回归确认测试安装器在 umask 077 下把所声明的公开 0755 executable 写成了 0700，
与服务 UID 1000 不兼容。修复仅对排他创建的文件描述符执行其声明模式；秘密默认0600
不变。匿名版本探测也不再发送空 basic-auth。不能因此把旧轮次重写为账号测试通过。

第三轮位于 `/data/anas-incus-20260924.Hd6AM0/forgejo-account-r3`，新 VM
`anas-incus-host-c57759`，22192。使用同一已核对 Debian 基础镜像的独立写盘，QEMU
临时 UID1000/GID108、无附加组/capability、KVM 单核/2048 MiB。官方 Debian keyring
验证的传输镜像只在新 VM 的 `/run/anas-account.sources` 使用，不修改产品来源表或
物理宿主 apt。该轮完整终态取得后追加；入口和离线通过本身不是实机成功。

## 验证边界

最初修改后的全仓 Go 测试、三包竞态、go vet、31 项 Forgejo Python 和 68 项 Incus
Python 回归通过。后加的 executable-mode 回归先复现 0700/0755 差异，修复后五项账号
入口离线测试通过。Hook 的实际命令期限、取消和输出边界另有新增回归并通过。
最终组合验证与原生终态将继续单独记录；交叉编译不作为 ARM64 原生证明。

## 第三轮实际终态

新 VM `anas-incus-host-c57759` 的 **17 / 17** 必需门禁全部通过，native exit=0，
单独 Forgejo 服务正常退出。恢复管理员与 controller 为不同 ID，受管账号数值 ID 为3，
禁用/重新启用/重启都保持该 ID；已禁用密码确实被 API 拒绝，重复执行不改变数据库中
密码记录。手工构造的同名同角色外部账号和替换 ID 均不能被接管。真实 controller 进程
验证空状态成功、有未完任务且无租约失败、失效链接失败，原状态保留。

实际工件绑定78个编译前后未变的输入，HEAD 仅作基线，不冒充整个 dirty checkout：

```text
Forgejo 15.0.7: cb75c2780d13a8a8b91390e42615354219aac58fd86a9d20baa40f5281c8e9a3
account helper: e5561d29047b5bba3638937c6eb4fe33e4c72ea1aa326f63e78878f622312105
controller: 57115ee44cd9775eec782cf612c97718812baf05200b0db7c42ee2e99c7241ef
native driver: cf0fd902c6f22ac7f8f00f01adbfae00b84cc8afaa55c79fa8a20e11ccf59a70
```

公开证据仅包含固定阶段/身份结果，不含私密诊断、数据库、HMAC receipt 或凭据。归档：
`/data/anas-incus-20260924.Hd6AM0/forgejo-account-r3/reports/public-evidence.tar.gz`，
SHA-256：`dc4a4b89d4aecbda52fa54832464322f34765cd4c2bc7f40c8df12532bd136dd`。

外层独立结果：正常 QMP 关机、未请求强制退出、QEMU exit=0、cleanup_passed=true。
物理宿主24个原容器、17个网络、卷、Docker服务/配置、nft、IPv4/IPv6路由及named netns
全部与开始基线相同。此轮没有安装 Docker，也没有测试完整 Compose Hook→容器停用、
真实运行 guest 的开关排空或 PostgreSQL/IAM 业务链路；这些仍不能由17项账号结果推导。

## 最终组合检查

公开归档已再次独立读取：只有 summary.json、17 个唯一成功阶段，实际工件摘要与交付
清单相符；三个账号实验端口均无监听，宿主没有遗留 QEMU。独立结果保存在该轮
`reports/independent-verification.json`。前两轮失败盘和各自报告保留。

后续退出证明复核还发现，旧 inspect 模板只查找一个 `FORGEJO_ACTIONS_ENABLED=false`，
会忽略同一列表中冲突的 true/1。反例实际失败后，改为对精确 key 的每个出现位置投影
一个布尔值；缺失、重复、冲突、非规范值均不能通过严格 JSON 校验。只投影这一值，
不输出其他环境变量。该追加修复只涉及 Hook 和其回归；账号 helper/controller 及依赖
输入与第三轮实测保持相同，不能把第三轮描述成这个 Docker 模板的原生执行。

最终全仓 Go 测试、三个 Forgejo 包竞态、全仓 go vet、32 项 Forgejo Python 与68项
Incus Python 测试通过。追加模板修改后又通过完整 Hook、竞态、全仓 Go 和相关 vet。
helper/controller/Hook 的 Linux ARM64 交叉编译通过，但未做 ARM64 原生运行。
Module/Contract 文档生成检查、需求归属、状态索引、共享构建静态检查和升级目录检查
通过；共享构建此处仅为静态检查，不把它记为新的 Docker 构建验收。

最后核对的 HEAD 不变，未提交/推送/重新暂存。78 个历史原生构建输入与当前源码的差异
仅为明确追加的两份 Hook 文件；实际原生 helper/controller 输入没有变更。最终本机
日志与来源核对位于 `/tmp/anas-account-20260924.BfXgJv`。
