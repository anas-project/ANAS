---
doc_type: review
status: current
created: 2026-09-22
updated: 2026-09-22
---

# Runner 真实构建尝试、copy 路径修复与取消后版本保护

接续 `whl@ln.hlong.wang:2200`，仍不修改既有 Docker。代码基线为
`claude/forgejo-docs-audit-20260920` / `3f5242e` 加累积工作树，未切换分支、提交或推送。
本轮涉及 M12 / INCUS-R-055、R-066、R-067 和 M5a 的前置测试，不把单项通过当作整个需求完成。

## 环境、输入与实际执行范围

物理宿主使用新的 `/home/whl/anas-runner-bake-20260922.LWJLXt` 保存基线与报告。
复用此前核验的 Ubuntu 26.04 cloud image，重新核对原 SHA-256；新建可写磁盘和 SSH 身份。
独立 QEMU VM 为 1 vCPU、2048 MiB、32 GiB 稀疏磁盘，使用用户态网络，回环 SSH 为 22128，
没有 TAP、宿主 bridge 或 Docker 接入。QEMU 以 whl UID、专用 kvm GID 和空附加组运行，
有 2400 秒外部上限。首次指定 sudo 运行组的启动方式要求密码，实际未启动；改由已有授权
root 入口降权启动，没有改用户组、sudo 配置或设备权限。

测试 VM 中安装发行版官方 distrobuilder 3.2-4、debootstrap、Debian archive keyring 与
Incus 6.0.5-8 等依赖，物理宿主未安装这些包。固定 Runner 输入来自官方发布元数据选出的
13.2.0 Linux amd64 文件，实测 `--version` 与公布 SHA-256 匹配。没有进行独立发布签名验证，
不能据此把测试候选登记成正式签名发布。

| 输入 | SHA-256 |
| --- | --- |
| VM 中 `/usr/bin/distrobuilder` | `c095ea24c87d4a95dce7c0515534583eed25dcd7585aa6027c2fa2aff3856027` |
| Runner 13.2.0 | `fadaec897f5e6641c363f87ecaf75f866f99c81b7097ec6b433e4c48123fcad1` |
| 首次默认容器配方 | `abfd7c348c450ad07f86cc0f0d80d794b78dcfaf5d8de9d8a28565d28c33d7e2` |

真实调用了现有 `incus-image-artifacts init/build`，没有另写生产构建器。`lab-r1` 已写入持久
attempt/冻结配方与工具输入摘要，并运行真实 distrobuilder/debootstrap。Debian Release 签名
校验通过，但基础包下载缓慢；独立同文件探针在 20 秒内分别只取得 189,776 和 83,087 字节。
这不是 HTTP 拒绝或签名错误，不能把局部下载当成完整 rootfs。

## 发现、反例与修复

`BuildOnce` 把固定 Runner 放在 recipe 工作目录的 `sources/forgejo-runner`，而默认配方
copy generator 原来读取 `forgejo-runner`。真实 distrobuilder 3.2 的独立 `pack-incus` 控制
明确因找不到该文件失败；`--sources-dir` 控制发行版下载，不会补齐 copy 的相对路径。

先补 Go 结构化配方回归，容器和 VM 两档在旧代码均失败；再把共用默认配方改为
`source: sources/forgejo-runner`。真实 pack 正对照通过，并用 unsquashfs 读出目标文件，
核对与冻结输入完全一致。可复现入口 `test-distrobuilder-copy-native.py` 要求同时满足旧路径
失败、新路径成功和实际打包字节一致；该入口已在上述 VM 执行。

原生控制使用小型合成 rootfs/字节以隔离路径问题，**不是完整 Runner 镜像或其启动证据**。
它没有改变正式配方的 Debian 来源、包签名、guest nesting/device 限制或生产 catalog。
修改配方改变摘要；旧已发布 revision 不允许覆盖，未完成 revision 也不能隐式重试。

确认旧配方有必然失败的路径之后，通过核验过的进程身份和 pidfd 向本轮 build owner 发出
SIGTERM。原工具正常报告 `context canceled` 和 build incomplete，退出 1，保留 attempt。
随后用相同 revision/原配方/原输入真实重试：立即拒绝，原 attempt inode 和内容摘要不变，
新增 attempt 数为零。这验证失败版本保护，不证明完整镜像构建成功。

## 新的完整镜像测试入口及未执行边界

`server-incus-runner-image-e2e.py` 接受独立记录的 bake fingerprint 和真实 export，先核对
split 字节，再经实际 Provider 的固定供给路径执行导入、重复 ensure 与 inspect。
`TestNativeBakedForgejoRunnerImage` 使用共享客户端启动受限容器，分别核验真实 Runner、
one-job 参数及 runner-agent 对 rootless Podman 的实际访问。缺 pass、skip 或失败不能放行。
入口要求精确可销毁 QEMU 实例身份、无 Docker 和空 Incus；未知对象不自动删除。

该完整 smoke **本轮未执行**：全量 Debian/Podman 构建没有完成，不能给它喂入前轮最小
生命周期夹具或编造 fingerprint。单独运行真实 Runner 的 `one-job --help` 已确认
`--handle`、`--token-url`、`--wait` 存在，但没有连接 Forgejo 或执行真实作业。
说明位于 `test-env/fixtures/incus-runner-image/README.md`。固定配方的完整重新烘焙、导入/启动、
rootless engine、one-job、正式签名、回滚/prune 与 VM/arm64 矩阵均继续待验收。

## 最终检查

| 检查 | 本轮结果 |
| --- | --- |
| 旧默认 copy 路径 Go 回归 | 容器/VM 两档先失败；修复后通过 |
| 真实 distrobuilder copy 控制 | 仓库原生入口通过：旧路径退出 1、新路径退出 0、squashfs 内字节与冻结输入一致 |
| 真实 build 取消与重试 | 取消退出 1，原 revision 再次调用被拒绝，attempt inode/摘要相同，新增尝试为零 |
| 真实 Runner one-job CLI | 13.2.0 help 及三个必需参数通过；不是作业执行 |
| macOS / Go 1.26.6 `go vet ./...`、`go test ./...` | 通过；未改变包允许缓存，不算 Linux 整仓执行 |
| computeimage、release CLI `-race -count=1` | 两包通过 |
| Linux amd64 / arm64 computeclient 测试程序 | 编译通过；新的完整镜像测试未执行 |
| Python `test_incus_*.py` | 11 项通过，其中四项为本轮新增的 VM/工件拒绝控制 |
| 共享构建、升级目录 | 静态门禁通过；没有 Docker build 或真实升级 |
| Module/Contract 双语生成与检查、需求/计划/状态门禁、双语 docs build | 通过；Incus 仍为 30/75 |
| `git diff --check HEAD` | 首次发现四处文档 EOF 空行，修正并重新生成后通过 |

一个组合源码读取请求被工具安全检查拦截，未作为执行或测试证据。本轮没有修改全局工具链、
放宽生产入口、替换旧 revision 或把未运行的完整镜像 gate 标为通过。

## 证据、退出和物理宿主基线

本机日志根为 `/tmp/anas-runner-bake-20260922.VIVNRK`。物理测试根保留以下从 VM 回收并
再次核对摘要的归档。失败 archive、已冻结输入、未完成下载片段和原生控制记录均保留；没有
把它们当成可启动镜像，也没有删除持久尝试记录后重新构建同一 revision。

| 物理测试根中的工件 | SHA-256 |
| --- | --- |
| `reports/bake-state-evidence.tgz` | `cb3cd3727203e7958c4e47e8ddf00b3e1fa1cd1a8570909d968c0ea94294b757` |
| `reports/bake-reports.tgz` | `99656c24f1b5a767d26bd26f31ab7bae09ae3345c9fce4f528571f26ffb9c1bc` |

停止前独立确认 build owner、distrobuilder、debootstrap、wget、mksquashfs 均无残留；VM 内
Incus service/socket 已停止。归档使用固定目录清单，不包含临时 SSH 私钥或业务配置。
按本次私有 QMP socket 核验 VM 名后请求正常关机，QEMU 退出码 0。本次 QEMU 进程、QMP
socket 和测试挂载均不存在，回环 22128 可重新绑定；逐项删除本次密钥、cloud-init/seed 和
可写 qcow2，没有删除基础镜像、旧实验或归档证据。

物理前后基线各字段全部一致：22 个 Docker 容器的状态/PID/启动时间/重启次数/健康状态、
16 个网络、已有卷、Docker service/socket 身份、daemon 配置与单元摘要、nft stateless
规则、IPv4/IPv6 路由和命名 namespace。没有修改或重启已有 Docker；这不替代逐应用功能探测。

本轮保持 `7.3.0-r2 / developing`、30/75 及关闭的 production ingress。下一项仍是修正配方
在新 revision 的完整烘焙及真实镜像/one-job 验收，不在失败的 `lab-r1` 上擦除记录重试。
