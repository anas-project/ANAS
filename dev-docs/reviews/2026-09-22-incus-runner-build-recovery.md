---
doc_type: review
status: current
created: 2026-09-22
updated: 2026-09-22
---

# Runner 构建中断恢复、resolver 与真实 Forgejo API 核对

接续操作者指定的 `whl@ln.hlong.wang:2200`，不修改既有 Docker。代码基线仍为
`claude/forgejo-docs-audit-20260920` / `3f5242e` 加累积工作树，未切换分支、提交或推送。
本轮复用上一对话已启动、尚未收尾的独立 QEMU VM `anas-runner-bake-bzsj8q`，没有新建
另一台虚拟机或使用业务 Docker。物理证据根为 `/home/whl/anas-runner-complete-20260922.bzSJ8q`，
本机结果根为 `/tmp/anas-runner-recovery-20260922.zdZWWd`。关联 M12/M5a 与 INCUS-R-055、
R-066/R-067、FORGEJO-R-068，不把子项通过当作整个里程碑完成。

## 恢复与实际问题

恢复连接后确认旧 QEMU 仍运行，旧 `lab-r2` 已退出 1，未生成发布记录。原始日志和 attempt
保持原状。一个组合读取请求及一次构建会话轮询被工具安全检查拦截，均不算实际执行；没有
由此推断 SSH 或构建成功/失败。后续事实分别来自真正执行的测试和独立产物读回。

原发布工具丢弃所有 distrobuilder 输出，归档层还会移除错误上下文。新增失败阶段类型只允许
固定枚举；输出观察器只处理完整且不超过 4 KiB 的行，清除原始片段；归档层不透传任意包裹
的错误文本。测试先证明旧归档吞掉阶段，再在 macOS 和真实 Linux 子进程证明仅输出固定名称，
不回显模拟私密错误。阶段不是成功回执，也不授权重试旧 revision。

新 revision `lab-r3` 使用相同实验配方，在真实 distrobuilder 3.2 中仍失败，新诊断给出最后
阶段 `hooks`。独立小型 rootfs 控制进一步证明：空 post-files hook 可完成，原
`ln -sf /run/systemd/resolve/resolv.conf /etc/resolv.conf` 导致 post-files 失败。没有把该
合成镜像当作完整 Runner。经 Debian 签名元数据和实际 deb 内容核对，networkd unit 已在
`systemd` 包内，排除了“缺少单独 networkd 包”的猜测。

默认配方改用 `/usr/lib/tmpfiles.d/anas-resolver.conf` 的 `L+` 规则，在 guest 启动时建立
resolver 链接，不在构建 chroot 中替换它；另显式声明 `systemd-sysv`。四种架构/档位配方
回归先失败后通过。`test-distrobuilder-resolver-native.py` 实际复现旧 hook 失败、新配置
打包成功，并从 squashfs 提取文件，通过真实 systemd-tmpfiles 检查链接创建；这仍是局部
原生控制，不是 guest DNS 或 one-job 的替代。

`lab-r4` 继续使用独立记录的实验镜像源变更，不修改默认 Debian 来源、不关闭签名校验，
也不在旧 revision 上删除 attempt 重试。完整构建与后续运行结果见下方最终记录。

## 真实 Forgejo API 与传输边界

新增显式可销毁 QEMU 验证入口，以普通用户启动独立、回环监听的 Forgejo 15.0.7 / SQLite。
专用随机账号密码不作为 CLI 参数；生产客户端实际调用 repo/org 两种 scope 的 jobs、
ephemeral create、delete 和重复 delete，脚本随后独立读回两种 scope 的注册列表为空。
未运行真实工作流，也不算 PostgreSQL/MariaDB 矩阵或账号权限收敛。

首次夹具因真实 CLI 输出格式不符失败，未进入 HTTP 测试；修正后 run02 通过。之后针对生产
HTTP 客户端新增反例，证明它会跟随 307 重定向，从获批路径发送第二个请求到 `/admin/`；
原实现只解码第一个 JSON 值，并会回显自定义 transport 错误。新增回归覆盖尾随 JSON 和
超限有效前缀；修复禁止重定向、完整读取
有界 JSON 后要求 EOF，并返回固定错误和原始取消身份。

中间一次“必须为非 null 数组”的收紧导致真实 run03 失败。Forgejo 15.0.7 的正常空队列实际为
`null`，因此最终实现接受完整、限额内的 nullable array，并将合法 null 归一为空数组。
截断、多重 JSON、错误状态与超限响应仍拒绝；未为了保留本地假设而宣布实际 API 不兼容。
失败及后续复测分别保留。

## 最终记录

### 完整候选镜像

`lab-r4` 的真实 `incus-image-artifacts build` 退出 0，成功记录并导出 split 产物。这次不再
使用合成 rootfs：完整 Debian Runner 镜像由 distrobuilder 3.2 烘焙，内含已核对公布摘要的
Runner 13.2.0。实验配方明确采用 TUNA 镜像源，默认配方源未修改；没有关闭 Debian 签名校验。
Runner 发行签名和正式 catalog 发布未验收，因此仍是实验候选，不是正式发布。

| 属性 | 实际值 |
| --- | --- |
| 版本键 | `forgejo-runner / lab-r4 / amd64 / incus_container` |
| 完整 fingerprint | `037153fd3525c1ba7d84d582cb1ffffe0bfc1f2e15db0f44bf38860a33c09507` |
| 冻结 recipe digest | `4db32457f90d3ae2521e1abcfebb80ae2a5c7f61b165f6d05b42857d7140c14e` |
| metadata | 712 字节，SHA-256 `11a61f491b192de9b920d81ca28806f074114d501a3a0d579865a9e62e1bfe84` |
| rootfs | 244,748,288 字节，SHA-256 `511c4b7f39cb72bf729a65298a09d024c520e6467c987d83c83f51c17a6e1071` |
| 同版本再次调用 build | `existing=true`，release 完全相同，新增 attempt 为零 |

真实 Provider 从固定只读 supply 路径导入这些字节，重复 ensure 与 inspect 均通过；共享
客户端实际创建并启动受限容器。`real-runner-binary` 和 `one-job-interface` 子项通过，
真实版本为 13.2.0；`rootless-engine-api` 子项失败，调用退出 125，整个 smoke 父项和包
因此失败。未将两个子项通过改写成完整镜像 gate 成功，也未执行真实 Forgejo workflow。

现有证据尚不足以区分引擎就绪时序、socket 权限和容器权限限制；没有放宽 nesting、devices、
project 或改用宿主 socket 来换取通过。后续可从已保存的不可变归档恢复，不需要再次烘焙该版本。

### API、清理与证据

最终 nullable-array 兼容修复后的真实 `forgejo-api-run04` 通过全部三个必需 pass 事件
（父用例及 repo/org 两个子用例），无 fail/skip，两个 scope 的 registration 独立读回为空。
run03 的中间失败保留，不用 run04 覆盖。该门禁是实际 API 兼容性，不是 one-job、数据库
矩阵或 controller crash 后 janitor 的验收。

原生测试结束后，独立确认 Incus 无实例、镜像、池、受管桥和受信客户端，只有 default
project；停止实验 VM 内 Incus service/socket 并确认 incusd 不存在。归档先复制到物理机并
核对大小和 SHA-256，再通过核验名称的私有 QMP 请求正常关机。原 QEMU 会话退出码 0；
原 PID、QMP socket、测试挂载消失，回环 22129 可重新绑定。只删除本轮临时 SSH 身份、
cloud-init/seed 和可写 VM 磁盘，不删除基础镜像和已有实验目录。

物理前后比较所有字段相同：22 个 Docker 容器的状态/PID/启动时间/重启次数/health、
16 个网络、已有卷、Docker service/socket 的身份与启动信息、配置及单元摘要、nft stateless
规则、IPv4/IPv6 路由和命名 namespace。未修改、重启或使用已有 Docker 作为执行面。

| 物理 `reports/` 下的归档 | SHA-256 / 大小 |
| --- | --- |
| `recovered-build.tar` | `67d40cc2995ba7283ff0fd57e1647c71e2754b231324335a1950f7dd142b367b` / 311,705,600 字节 |
| `recovery-reports.tar` | `20787187f3e33280f5de34877d1a1f89890ccbf73a4ea77c0c23fd56803024c8` / 30,720 字节 |

构建归档保留 object store、release 记录、attempt 和冻结输入；省略缓存及重复的 build output
副本，不是整台 VM 镜像。报告归档不含测试数据库、账号口令或运行期 TLS 私钥。源代码最终门禁
单独记录，不将 macOS 或交叉编译的结果写成 Linux 全仓执行。

### 最终源码与文档门禁

| 检查 | 实际结果 |
| --- | --- |
| 最终源码 `go vet ./...`、`go test ./...` | macOS / 缓存 Go 1.26.6 全仓通过；未变更包允许缓存 |
| computeimage、release CLI、Forgejo controller `-race -count=1` | 三包通过 |
| Linux 构建输出阶段控制 | 真实 ELF 子进程测试通过，不运行 distrobuilder、不访问网络 |
| Linux 最终 Forgejo scope API | run04 具名父/子用例及包成功，无 skip；不是纯 mock |
| Linux arm64 release CLI / controller 测试程序 | 两份编译通过，没有在 arm64 运行 |
| Python 回归 | 原 Incus 系列 11 项、新 API 夹具 3 项，合计 14 项通过 |
| 共享构建与升级目录 | 静态检查通过，不是 Docker build 或真实升级 E2E |
| Module/Contract 双语生成与检查、需求覆盖、状态与索引 | 全部通过；Incus 仍为 30/75 |
| 双语 `docs:build`、`git diff --check HEAD` | 通过；仅原有非阻断 chunk 大小提示 |

首次 Go 调用因自动工具链与关闭校验库的组合失败，改为显式使用已缓存工具链后通过；没有
修改全局 Go。一次聚合检查在 Go/竞态/交叉编译均完成后，被 zsh 未加引号的 Python 通配符
中断；保留该失败，修正命令引用后补跑 Python 和静态门禁通过，没有把未执行命令算作成功。

当前剩余工作首先是复用归档候选定位 rootless Podman exit 125，再进入真实 one-job 及
controller crash/迟到创建/registration 回收；不以继续烘焙同一版本代替运行期排查。
