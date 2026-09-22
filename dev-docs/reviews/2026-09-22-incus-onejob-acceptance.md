---
doc_type: review
status: current
created: 2026-09-22
updated: 2026-09-22
---

# lab-r6 不可变镜像准入与真实工作流接续

接续[运行期修复](2026-09-22-incus-onejob-runtime-completion.md)，仓库仍为
`claude/forgejo-docs-audit-20260920` / `3f5242e` 加累积工作树。目标为
`whl@ln.hlong.wang:2200`；不修改或重启既有 Docker，不提交或推送，不网页检索。
当前范围是实际 rootless 引擎、one-job 和回收，不能用 API/单元通过替代工作流验收。

## 恢复、镜像和证据

恢复时前轮 QEMU 已停止，旧工具会话已无法轮询。核验磁盘无 corrupt/dirty 标记、正确
backing file、私有目录/磁盘归属和空闲回环端口后，只恢复原 Debian 实验机
`anas-runner-bake-uefkek`，1 vCPU / 2048 MiB / QEMU 用户态网络，SSH 回环 22131。
实际持久记录确认 `lab-r6` build 退出 0，export 已存在；没有重烘焙或覆盖版本。

| 项目 | 实测值 |
| --- | --- |
| 版本键 | forgejo-runner / lab-r6 / amd64 / incus_container |
| fingerprint | `cbb06e37de780c69dd8d730e92f110a15656218e4f506f3015e05d47f666cdb5` |
| recipe digest | `978516a785a7f1e69293cd13e6f4c89a8c2cf49a9fc4605ae469c6c5af83deb0` |
| metadata | 692 字节；`af9947cc0eb03dbba5dd1fdce81bee043ddfb23221e197fe7c6327ee979bc462` |
| rootfs | 244,748,288 字节；`cbb07b72cfaa54178d6e7756e2bdabc59b79a6c4f129505fa250637261794aa9` |

`/var/lib/anas-r6-build/smoke-final01` 的完整镜像门禁已退出 0：实际 Provider 导入、重复
ensure/inspect、受限容器启动、真实 Runner/one-job CLI 与 runner-agent 访问 rootless API
全部通过。它没有运行工作流。镜像没有修改 nesting、设备、AppArmor 或使用业务 Docker。

本轮物理证据根为 `/home/whl/anas-onejob-final-20260922.WOtQgG`，本机日志根为
`/tmp/anas-onejob-final-20260922.H7PRYP`；实验磁盘仍在
`/home/whl/anas-onejob-debian-20260922.UefkeK`。新的 Docker 前置基线实际记录 22 个容器、
16 个网络。后置和停机状态须以本轮最终记录为准，不能沿用前轮结果。

原归档经过 inspect 后选择性保存 object store、release、冻结配方与 Runner 输入，不包括
cache、重复 output、运行期 TLS 私钥或测试数据库。`reports/final-r6-archive.tar` 已复制到
物理证据根并独立验 SHA-256：`08c65480f74dc6cddd07cbf3e9015351087f6f593b309d8eb868a29c2739613a`，
267,110,400 字节。候选仍不是签名发布。

## 真实测试夹具的修正和失败保留

`onejob-final01` 因 Debian 实验机未安装 Git 而在 Forgejo migrate 阶段失败，未创建 Runner。
新增固定依赖预检，在目录/网络/daemon 变更前拒绝缺失的 git、openssl、runuser 或 incus。
仅在核验过的可销毁 VM 安装 Git；物理宿主包和 Docker 不变。旧夹具和数据库保留。

`onejob-final02` 的生产 controller 在公开测试 CA 安装阶段反复失败，最后对已核验的
controller PID 发 SIGTERM，让原生产循环执行补偿，然后测试脚本回收资源。该 helper 的
退出成功不等于工作流成功。CA 改为在 guest 启动后复制，并明确为公开副本设置 0644；
源文件和 fixture 根仍私有。该适配器不改变候选镜像、引擎单元或 Incus 围栏。

`onejob-final03/04` 确认复制成功，失败落在 update-ca-certificates。有界私有诊断捕获
`/tmp/ca-certificates.tmp.*` 在读取时已不存在；镜像原始工具、证书目录和 /tmp 均存在。
测试 CA 工具因此使用 guest `TMPDIR=/run`，不依赖启动阶段的 /tmp 生命周期。原始诊断
仅在 root 私有 fixture 中，controller 日志只给固定阶段、退出码和封闭类别，便携证据不
包含该原始文件。每次失败的资源回收与工作流结果分开记录，没有把后一次覆盖前一次。

公开 CA 的复制时序/模式、诊断上界两个 Linux 回归通过；依赖预检两个回归通过。原生工作流
后续结果待本轮最终记录，当前不能据此声明正常/取消/crash 已验收。

## 已执行源码门禁

macOS 缓存 Go 1.26.6 的全仓 vet/test 已通过；computeclient、computeimage、controller、
发布工具四包 race 通过；Python Incus 系列 18 项与 Forgejo 系列 17 项共 35 项通过。
后续修改须补跑相关门禁。没有将 Linux 交叉编译或替身测试当作原生工作流。

Incus 仍为 developing / 30/75，生产 ingress gate 关闭。最终工作流矩阵、清理和文档门禁
将在实际完成后补充，不在此预先标为成功。

## lab-r7：公共配置父目录与更严格镜像门禁

final05 已通过测试 CA 阶段，生产 controller 进入 running，真实 Runner 启动后报告
无法读取 `/etc/forgejo-runner/config.yml`。原始 squashfs 核验该文件为 0644，但父目录
为 0700。四目标回归先复现，再在 post-files 和 provision.sh 显式登记两个公共父目录
`/etc/forgejo-runner` 与 `/usr/local/libexec` 为 root:root / 0755；不递归修改私有目录。

`lab-r7` 在新版本键真实烘焙成功，保存独立原始/实验配方与镜像源变更记录。fingerprint
`4257622e5f8fc8d1a061b15e7c819814b02f2a593b20a7ed70d77763697300e9`，recipe digest
`7f6272d3e23926ebb3423aedcbd7d32b2ebaf7751fe12a1911badce3fcb43e16`。完整门禁
`/var/lib/anas-r7-build/smoke-r7` 退出 0，增加实际 runner-agent 配置可读性必需子项后仍全部
通过；两份镜像测试入口都要求该子项，旧测试程序不能静默漏测。

`reports/final-r7-archive.tar` 已回收到物理证据根并独立核验，267,110,400 字节，
SHA-256 `adbb0a7544e6abee9938472a5867fc606d69e2abc03a2a7caa94ea8b0ce9055d`。

## 实际 Forgejo 字段、取消边界与外部 registry

matrix-r7-01 的工作流实际已入队，但测试误用 head_sha 未匹配到。通过只读 SQLite 与
短时启动原实验 Forgejo 的 GET 响应及自带 swagger，确认运行记录使用 commit_sha，且
没有假设的 run/jobs/steps 或 run/cancel API。探针均正常退出，不修改 job 状态。一个
网页登录/取消入口探针被工具安全检查拦截，未执行，未记作网页取消验收。

最终测试用实际 commit_sha 绑定新提交；从实验数据库只读 task/step 的非秘密列，验证
payload 开始和终态，不读取 token 列、不写 job 状态。取消场景使用真实 controller 的
SIGTERM → context 取消 → 独立清理；与 SIGKILL 后保留 state 的进程重建分别验证，不
冒充 Forgejo 网页取消、授权账号权限收敛或 state volume 丢失恢复。

matrix-r7-02 真实 Runner 已声明并领取 task，但拉取 Docker Hub BusyBox 时连续连接
超时，最终正常场景失败并清理。原 guest 的地址和 resolver 已就绪；未修改物理 DNS、
Docker 或 Incus 网络围栏。验证 ECR 公共 registry 可达后，选择同类 amd64 BusyBox 的
固定 manifest `sha256:7a3ebe5bfd1a4a19797d20b0c0bb39d44393e9a03fd852c0865b0f540d868df0`，
按返回字节重新计算摘要一致。上层 index 的 HTTP digest 未被用作校验证据；原探针的
index header 比较非成功，只有独立子 manifest 字节校验被记录为通过。OCI layer 描述为
`sha256:436a1b1fd078ee8e117111472724c2827077657189af7a781829d0825d48d2ab`，2,211,507 字节。
实际 layer 校验仍由真实 Podman pull 执行，不用 descriptor 代替运行证据。

matrix-r7-03 已成功拉取该固定镜像并发起真实 create/start，随后明确失败：netavark 的
aardvark-dns 无法连接 user scope bus。payload 尚未开始，未将此 setup failure 算作预期
的失败场景。归档任务日志经有界 zstd 解码取得该证据，无 token 输出。

后续源修复显式安装 dbus-user-session，仅启用 engine UID 的 lingering；Podman service
依赖 user@1002.service，并使用真实 /run/user/1002 与其 bus，保持共享 socket 权限与
Incus 围栏不变。四目标配方和 service 回归先失败后通过。首次打印测试结果的 shell 命令
使用 zsh 只读变量 status 而失败；实际测试失败日志已独立读回，未混淆原因。新镜像门禁
增加 rootless-user-session，lab-r8 使用新 revision 烘焙，实际终态需另行记录。
