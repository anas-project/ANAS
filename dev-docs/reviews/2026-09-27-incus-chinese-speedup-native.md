# Incus 国内软件源实机核验

状态：三档宿主后端及 guest 修正后的 r5 构建验收通过；保留 r3 脚本断言错误的失败记录。基线日期：2026-09-27；补证更新：2026-09-28。
r1 基线为 `fe85e51089d6225debceb394cb70ad86cc8347d9` 加当轮冻结的工作树，不能用后续 HEAD
或重新编译的二进制解释该轮结果。需求见 [Incus 要求矩阵](../requirements/incus-module.md)
`INCUS-R-108`、`INCUS-R-109`、`INCUS-R-110`；实施归属见[配套计划](../plans/incus-module.md) M10/M12。

## 范围与隔离

操作者授权在 `ssh -p 2200 whl@ln.hlong.wang` 验证国内源支持，并明确禁止修改其他 Docker 容器。
物理宿主只承载本轮独立 QEMU 进程和私有测试文件，并做只读基线检查。安装、软件源写入、Incus
生命周期及镜像构建都限制在对应的一次性 VM 内。QEMU 使用 user-mode NAT，SSH 转发只绑定物理
回环地址，不接入 TAP/bridge、业务 Docker socket、物理块设备或共享卷。

远端实验根为 `/data/anas-incus-speedup-20260926.vtfqdcbs`，Debian 宿主后端轮目录为
`r1-debian13`。测试输出与归档保留，VM 可写盘、密钥和 cloud-init 文件按本轮固定清单清理。

本报告区分两个开关和两条路径：

- 宿主安装使用工作区有效 `CHINESE_SPEEDUP`，安装计划冻结为 `Request.chinese_speedup`。
  三档测试直接向真实后端传入 `true`，验证实际软件源和安装行为；没有经过工作区配置读取、host-action
  入队、用户确认及执行交接链路，不能用本轮结果替代 R-109 全链路证明。
- guest 发布使用 `CHINESE_BUILD_SPEEDUP`，在生成配方时选择 bootstrap 并改写已有 APT 源。
  r5 已通过实际构建、复用、冲突拒绝和导出 rootfs 内容检查；r3 最终内容断言失败的原记录保留。

## 输入与来源

| 轮次 | 冻结来源 | 关键工件 SHA-256 | 状态 |
| --- | --- | --- | --- |
| r1-debian13 | `fe85e51089d6225debceb394cb70ad86cc8347d9` 加当轮工作树；逐文件摘要记录于该轮输入清单 | `incusprovision.test`：`886322c835ff255bc168f01b3f0b5c938e3ad1d17645883901a6cd162c988df3` | 通过 |
| r2-ubuntu2404、r4（Ubuntu 26.04） | `dddae0c2beaae84566d17c7e0276475fcf76c094` 加当轮冻结工作树，已重新编译，含后续卸载修改 | `incusprovision.test`：`0c44087a5940047bd0ce1cad897f8ab7da276cdb903d06ca7c58eb9ebbea4565` | 两轮均通过 |
| r3-builder | 工件工具、配方与逐文件源码以当轮输入清单为准；builder/runner 摘要见下节 | 冻结配方摘要：`e6cdfed02ba8c66ab2ca7bcc423bfd7efb5b637583df7323f76775b226ce35fd` | 验收脚本失败，保留原终态 |
| r5-builder-repeat | 工具版本、摘要和配方与 r3 相同；修正验收断言，使用新 revision `speedup-native-r2` | 冻结配方摘要与 r3 相同；新工件和归档摘要见 r5 结果 | 通过 |

后续工作树已包含其他任务的卸载修改。新轮次的来源和结果必须另记；r1 的旧工件来源不得被当前
`dddae0c2` 或之后的源码清单覆盖。每轮结果只归因到表中对应工件与当轮输入。

## 三档宿主后端结果

环境分别为一次性 Debian 13、Ubuntu 24.04 和 Ubuntu 26.04 / amd64 VM，均为 `ChineseSpeedup=true`。入口是
`test-env/scripts/server-incus-host-provision-e2e.py --chinese-speedup`，调用实际
`internal/incusprovision` 后端；测试 Docker daemon 位于 VM 内的独立数据目录，初始容器清单为空。

每档 `TestNativeHostProvisionLifecycle` 的 10 项必需事件全部有 `run` 和 `pass`，无 `fail` 或 `skip`。
10 项包括顶层测试与下列 9 个子项，不能写成 10 个独立业务子项：

| 子项 | 结果 |
| --- | --- |
| `confirmation_is_required` | 通过；后端拒绝缺少确认绑定及过期计划 |
| `skip_without_host_effects` | 通过 |
| `install_pinned_packages` | 通过 |
| `configure_owned_host_resources` | 通过 |
| `enroll_private_management_connection` | 通过 |
| `idempotent_reenrollment` | 通过 |
| `uninstall_preflight_preserves_retained_storage` | 通过 |
| `uninstall_removes_owned_packages` | 通过 |
| `repeat_uninstall_is_idempotent` | 通过 |

Linux 上的纯解析测试 `TestNativeHostPackagePolicyReadback` 另行通过。解析测试使用固定文本正反例，
不能单独证明实际包版本、下载来源或 daemon 行为。r1 真实安装耗时 `420995 ms`。三档实际包读取如下，
每档 `incus`、`incus-base`、`incus-client` 均为表内同一版本，候选等于已安装版本，Zabbly 版本优先级为
`995`，发行版版本优先级为 `-1`，源选择均为 `chinese_speedup=true`：

| 轮次与系统 | 三包实际版本 | Zabbly 索引数（每包） | 国内发行版索引数 |
| --- | --- | --- | --- |
| r1 / Debian 13 | `1:7.0.1-debian13-202609250206` | 3 | 三包各 2 |
| r2 / Ubuntu 24.04 | `1:7.0.1-ubuntu24.04-202609250206` | 2 | `incus`、`incus-client` 各 3；`incus-base` 为 0 |
| r4 / Ubuntu 26.04 | `1:7.0.1-ubuntu26.04-202609250211` | 2 | 三包各 1 |

私有源、签名声明与钉包文件通过编译配置逐字节核验。Ubuntu 24.04 国内镜像没有 `incus-base` 条目，
该包由 Zabbly 提供，发行版索引数为 0 不构成失败；已出现的发行版版本必须保持负优先级。

各轮 `reports/native-evidence.tgz` 的 SHA-256：

| 轮次 | 证据归档 SHA-256 |
| --- | --- |
| r1 | `0eb0ccf5c8d4679fbf3b9e4b2139f96b3a04ea83d63127f4258c10d4c5cb732a` |
| r2 | `a32210aa9b0831dd064c625fb5e1ac3ac2ba7f3cb9db3ca8f4da0fbddae643b2` |
| r4 | `bdfe9691be44e488fd7407689b307240d57cc6186696d790b32fcb5c78022aee` |

## 退出、清理与物理宿主对照

r1、r2、r4、r5 supervisor 的已确认终态均为：

| 字段 | 值 |
| --- | --- |
| `test_exit` | `0` |
| `qemu_exit` | `0` |
| `normal_shutdown_requested` | `true` |
| `forced_quit_requested` | `false` |
| `cleanup_errors` | `[]` |
| `cleanup_passed` | `true` |

每轮仅清理本轮所属的 8 个文件：`lab.qcow2`、`guest-key`、`guest-key.pub`、`known_hosts`、
`seed.iso`、`meta-data`、`network-config`、`user-data`。报告与证据保留；没有删除物理宿主上的
Docker 容器、网络或卷。

物理宿主基线包含 24 个 Docker 容器和 17 个网络，数量未变。各轮以下十项前后比较全部为 `true`：
`containers`、`docker_config`、`docker_services`、`docker_unit_digest`、`named_netns`、
`networks`、`nft_sha256`、`routes4`、`routes6`、`volumes`。该结果证明本轮记录的这些状态保持
一致，不扩大为未采集指标或其他时段的结论。

## r3 guest 构建结果与验收脚本错误

r3 使用 Ubuntu 26.04 builder 构建 Debian 13 / amd64 / `incus_container` 镜像，revision 为
`speedup-native-r1`。实际 build 返回 `existing=false`，repeat 返回 `existing=true`；同 revision
换为默认源配方以退出码 1 拒绝，原归档不变。export 和 `unsquashfs` 均成功。

但整轮 `test_exit=1`：脚本错误要求 guest 必须含 `debian-security` 源。现有默认配方的 debootstrap
实际只生成 `trixie main`，开关只改写已有源，不新增 suite；该断言不符合配方。本轮安全源路径未出现，
不能声称实际验证了 guest 安全源换源。失败发生在后续 keyring 和软件包内容断言之前，这些断言未形成
本轮通过证据。原失败状态和 fingerprint 保留，不以此前构建步骤成功改写整轮结果。

| r3 输入或产物 | 版本 / SHA-256 |
| --- | --- |
| distrobuilder | `3.2`，Ubuntu 包 `3.2-4`；`c095ea24c87d4a95dce7c0515534583eed25dcd7585aa6027c2fa2aff3856027` |
| builder Debian keyring | 包 `2025.1ubuntu1`；`506b815cbb32d9b6066b4a2aa524071e071761e7e7f68c3ac74f3061ba852017` |
| debootstrap | `1.0.142ubuntu2` |
| Forgejo Runner 二进制 | `fadaec897f5e6641c363f87ecaf75f866f99c81b7097ec6b433e4c48123fcad1` |
| 原 fingerprint | `fc9ce92300b1314ad0bbd50ebff466f7dcf52d4f518f7f1438faa4e2014f22b3` |
| 冻结 `recipe_digest` | `e6cdfed02ba8c66ab2ca7bcc423bfd7efb5b637583df7323f76775b226ce35fd` |
| 证据归档 | `af3ffbf6ef881dc87b72d103a38d742377bfccab42faae28701d744610197e21` |

归档内 `build-*/recipe.yml`、`attempt.json.recipe_digest` 和发布描述的 `entry.recipe_digest`
比较一致。`BuildOnce` 会给原配方追加 builder 与 runner provenance 注释，因此这里记录的是冻结
配方摘要。容器导出的 rootfs 路径为 `rootfs.squashfs`。r3 的 VM 清理通过，物理宿主十项基线均为
`true`；清理成功不改变验收脚本的失败终态。

## r5 guest 构建与产物检查通过

`r5-builder-repeat` 使用实例 `anas-incus-host-71de6c`、回环 SSH 端口 `22485` 和新 revision
`speedup-native-r2`；builder、keyring、debootstrap、Runner 的版本与摘要均与 r3 表中相同。
修正断言准确核验现有配方，不新增任何软件源，也不覆盖旧 revision 或 r3 的失败结果。

真实首次 build 为 `existing=false`；repeat 为 `existing=true`，release 完全一致。改变 recipe
被拒绝，`archive_unchanged=true`。最终报告为 `passed=true`、`chinese_build_speedup=true`、
`guest_apt_sources_verified=true`；解包 rootfs 内发行版 keyring 存在，读取到 271 个已安装软件包。
`etc/apt/sources.list` 的完整内容只有下列一行及结尾 LF：

```text
deb https://mirrors.aliyun.com/debian trixie main
```

| r5 产物 | SHA-256 / 内容摘要 | 字节数 |
| --- | --- | --- |
| fingerprint | `a11e418c7f1e67a47456d4cc44786b532ac8dd62734471627a430b5e57ffc527` | — |
| `recipe_digest` | `e6cdfed02ba8c66ab2ca7bcc423bfd7efb5b637583df7323f76775b226ce35fd` | — |
| `incus.tar.xz` | `a6e81442398500d5d3f3ca62d51f5a9d1247bcab035fe8dae41d20d272b871fb` | 740 |
| `rootfs.squashfs` | `e5e7a79514c10a45d7dc78dc6563315a6fd9f0fcb2d8a781f1ca344efc9a5367` | 245522432 |
| 证据归档 `native-evidence.tgz` | `9c6aeb0174a607bfcb0da408a166d58d7258daf61e68f2d510cc3594d4d58948` | — |
| 保留的 `baked-artifacts.tar` | `f729a318be290c74472f68c9aafa5196f5e192e0352a98610296658129a67843` | — |

`baked-artifacts.tar` 保留在远端 r5 轮的 `reports/`。r5 与 r3 配方摘要相同，但新 revision 的
产物 fingerprint 不同；两份映射独立保留，没有覆盖已记录字节。r5 的 `test_exit=0`、`qemu_exit=0`、
`supervisor_error=null`，正常关机且没有强制退出，`cleanup_errors=[]`、`cleanup_passed=true`；
8 个自有 VM 文件已删除，物理宿主十项基线全部一致。

本轮证明真实 amd64 系统容器镜像烘焙及不可变产物检查；未运行 guest，也未验证 one-job。
guest 安全源没有出现在该默认配方中，其实际换源仍不属于本次原生覆盖。

## 当前本地回归

重新编译后，当前 `internal/incusprovision` 的 Go 测试与 `go vet` 通过；
`test-env/scripts/test_incus_host_provision_e2e.py` 的 11 项 Python 回归通过。这些记录属于后续
源码的本地验证，不把 r3 的验收脚本失败改记为原生通过，也不改变 r1 的冻结来源。

## 结论边界

目前确认的是三个一级发行版 / amd64 开启国内源后的真实宿主后端生命周期，以及各轮物理宿主对照。
guest r5 完成实际烘焙、不可变工件及 rootfs 内容检查；r3 的原失败终态保留。本轮没有验证关闭开关的真实安装、
ARM64、guest VM 镜像、guest 启动、Forgejo one-job 或正式签名发布，也没有通过真实工作区审批链路
验证开关冻结。guest 安全源的实际替换不在本轮配方覆盖内。

本报告不单独构成 M10/M12 的整体完成依据，不修改其他独立验收的结论；R-109/R-110 的本地
回归、真实后端证据与完整产品链路分别记录，不能把旧版本或其他轮次的通过结果归给本轮未覆盖的路径。
