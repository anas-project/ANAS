# Incus 7.0.1 宿主供给审批实机验收

状态：实机验收记录。日期：2026-09-26。默认 Incus 改为 Zabbly `lts-7.0`（`7f0e8620`）后，
用实际安装的审批链路（`test-env/scripts/server-incus-host-action-e2e.py`）在三个一级发行版上重跑宿主
供给验收；需求 `INCUS-R-047`、`INCUS-R-048`、`INCUS-R-057`、`INCUS-R-107`、`INCUS-R-108`。

## 范围与隔离

操作者授权使用 `ssh whl@ln.hlong.wang -p 2200`。物理宿主只做只读盘点和本次私有 QEMU 文件/进程管理；
审批服务、Incus、Docker 夹具与测试都在全新一次性 VM 内。VM 使用 user-mode NAT，SSH 只绑物理回环
端口，不接 TAP/bridge、业务 Docker socket、块设备或共享卷；一次只运行一台 VM。每轮开始和结束各记录
宿主 Docker 容器/网络/卷、服务身份、配置摘要、nft 与双栈路由并逐项比较。实验根为
`/data/anas-incus-hostaction-20260926.NuHLP9`（0700）；失败轮保留 VM 磁盘与诊断，通过轮只删除本轮
固定的 VM 文件清单。

## 前九轮（`0.0.0-native.10`，`397db017`）暴露的问题

二进制取自 `397db017` 的干净检出；hostd 单元与入口脚本随修复取自工作树。

| 轮次 | 系统 | 结果 | 原因与处理 |
| --- | --- | --- | --- |
| r1 | Ubuntu 26.04 | 夹具阶段失败 | 实验脚本以普通用户展开 root-only 目录的通配符；改为 `sudo sh -c` 内执行。非产品问题 |
| r2 | Ubuntu 26.04 | `confirmed_install` 失败 | Zabbly 包装在 `/opt/incus`，hostd 单元 `ProtectSystem=strict` 下 `/opt` 只读；`ReadWritePaths` 加 `-/opt` |
| r3、r4 | Ubuntu 26.04 | `confirmed_configure` 失败 | 入口脚本在 umask 077 下建出 0700 的 `/usr/local/lib/anas`，relay 以 203/EXEC 失败；夹具目录显式 0755、文件按请求模式 `fchmod`。入口脚本问题 |
| r5 | Ubuntu 26.04 | 21 项通过，`confirmed_owned_package_removal` 失败 | 删除最后一个 Zabbly 包时 `dpkg` 要 `rmdir /opt`；`/` 的直接子目录在 `ProtectSystem=strict` 下删不掉（EROFS），`dpkg` 视为致命 |
| r6 | Ubuntu 24.04 | 同 r5 | 同 r5 |
| r7、r8 | Debian 13 | 夹具阶段失败 | VM 内实验 Docker 刚启动时 `docker info` 超过入口的 5 秒上限；实验准备改为等到连续三次快速应答。非产品问题 |
| r9 | Debian 13 | 同 r5 | 同 r5；诊断中 `incus-client` 停在 `rH`（请求删除、半安装） |

`-/opt` 修复后，三个发行版在安装、配置、登记、控制桥 mTLS 正反例、跨工作区拒绝、重放拒绝、服务重启
后消费持久、五分钟真实过期等 21 项上都通过；只剩删包失败。

## 修复

1. 固定 `dpkg --remove` 改经 `systemd-run --wait --pipe --collect` 在不带 `ProtectSystem` 的临时单元里
   执行，保留 `ProtectHome`、`PrivateTmp`、`NoNewPrivileges` 与 600 秒运行上限。执行器只接受这一条编译
   argv 加去重的 Debian 包名，其他 `systemd-run` 调用一律拒绝（`TestPackageRemovalRunsOnlyCompiledTransientDPKG`）。
2. 按操作者要求，卸载总是删除 ANAS 记录为自己安装的包，去掉 `remove_packages` 请求字段与控制台勾选框；
   原有包、未托管依赖和外部 daemon 包不动。审批门禁由 25 项合并为 23 项（`confirmed_uninstall` 校验
   精确删包与 daemon 停止，过期后的新计划即重复卸载且库存不变），原生后端门禁由 11 项合并为 10 项。

## 第一次重跑（`0.0.0-native.11`）

输入取自工作树快照提交 `a65c600c`（树 `b6c4c62f`，父提交 `397db017`，不在任何分支上）。

| 轮次 | 系统 | 删包 | 结果 | 证据归档 SHA-256 |
| --- | --- | --- | --- | --- |
| r1-n11 | Ubuntu 26.04 | 删 4 个受管包，681 个原有包保留 | 21/23，`fresh_plan_after_expiry` 失败 | `d586b5e4465ee172ae83522c89105d2b2758c787ec81bdf28d6eb9c43c192e4d` |
| r2-n11 | Ubuntu 24.04 | 删 4 个，667 个保留 | 同上 | `1dd3a237c9709db35e85bdda4ba5e9d333caa4aa581e9ecf22ce5c7dc91c8a42` |
| r3-n11 | Debian 13 | 删 6 个，327 个保留 | 同上 | `ad8626680a94809f65ef4352796b7273d7301ce00d8f1e8948eb3da2e872b357` |

三个发行版的 `confirmed_uninstall` 都已通过：临时单元内的 `dpkg --remove` 成功，受管包精确删除、原有包
保留、daemon 读回 inactive。失败码均为 `repeat_uninstall_changed_inventory`，原因在入口脚本：五分钟过期
等待循环复用了变量名 `remaining` 保存剩余秒数，覆盖了删包后的库存集合，重复卸载后的比较于是拿集合比
浮点数。库存集合改名为 `after_removal`，并加 AST 回归测试
（`test_package_inventory_snapshots_are_never_rebound`，对旧代码失败），产品代码未改。

## 第二次重跑（`0.0.0-native.12`）

输入取自工作树快照提交 `3ac6576d`（树 `707cc729`，父提交 `397db017`，不在任何分支上；含上述修复及
并行会话未提交的国内加速改动，本轮未开启 `CHINESE_SPEEDUP`）。`CGO_ENABLED=0 GOOS=linux GOARCH=amd64
GOPROXY=off -trimpath` 加发布 ldflags 构建；与 native.11 相比二进制只差内嵌版本与提交。

| 输入 | SHA-256 |
| --- | --- |
| `source-manifest.json`（逐个产物摘要） | `3f9fe4f65561c1543eca491b30a538b74c50cc507fbe707163ff9852bec2dce7` |
| `server-incus-host-action-e2e.py` | `d925b144f4c70d635b3b6df3cae1adf983a5d663adc4dcf4680a17d95d9768f3` |
| `anas-hostd@.service` | `7e0cd2b4a183471f2bcc19930104b405a0c8dac6ce71365e6878e0e708fa222e` |

| 轮次 | 系统 | Incus（Zabbly `lts-7.0`） | 删包 | 必需门禁 | 证据归档 SHA-256 |
| --- | --- | --- | --- | --- | --- |
| r4-n12 | Ubuntu 26.04 | `1:7.0.1-ubuntu26.04-202609250211` | 删 4 个受管包，681 个原有包保留 | 23/23 | `3296cd49a67f81658528e101bc0861aad06fbc105ff1471986488eaba4d77851` |
| r5-n12 | Ubuntu 24.04 | `1:7.0.1-ubuntu24.04-202609250206` | 删 4 个，667 个保留 | 23/23 | `4e9e92106410740643f661634a48ec7e1098d3f729b8dad1dd24b1ba21598351` |
| r6-n12 | Debian 13 | `1:7.0.1-debian13-202609250206` | 删 6 个，327 个保留 | 23/23 | `a8e8fb652ebd4ff46970b7f568f4605028d9e052e774143aa98cd7ab30e9aa66` |

每轮 14 个成功 job，均有独立观察到的 hostd 退出；两次卸载的 disposition 都是 `uninstalled`，重复卸载前后
dpkg 库存完全相同。正常关机、QEMU 退出码 0、清理通过，物理宿主 10 项基线前后一致，通过轮已删除 VM 文件。
包版本取自同源同日 native.11 轮的失败诊断（native.12 证据不记录包版本），两次运行间隔约一小时。

## 结论

- Incus 7.0.1 LTS（Zabbly `lts-7.0`）在三个一级发行版 amd64 上通过实际审批链路的完整宿主供给与卸载，
  满足本次 `INCUS-R-047`、`INCUS-R-057`、`INCUS-R-107`、`INCUS-R-108` 的宿主验收范围。
- 卸载总是精确删除受管包；安装前已有的包与未托管依赖保留，重复卸载幂等（`INCUS-R-048`）。
- 已发现并修复的产品问题两项：hostd 单元缺 `-/opt`、删包时 `rmdir /opt` 的 EROFS。其余失败为实验脚本
  或入口脚本问题，已分别修复。

## 未覆盖

- 国内加速镜像的实机安装；ARM64 与 VM 隔离档的宿主供给；故障恢复（半途失败后的重试与回滚）。
- 旧 6.0 配方宿主向 Zabbly 7.0 的迁移：按操作者决定不做兼容。
- 快照提交不是正式签名发行版；正式发布仍须以发布流水线产物重验。
