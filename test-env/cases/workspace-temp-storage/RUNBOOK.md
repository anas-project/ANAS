# Workspace 临时存储测试

需求范围和稳定用例见 [cases.yml](cases.yml)。本地单元测试、Linux 交叉编译和脚本反例不能替代 finance 的真实 Docker、文件系统和浏览器证据。远端每轮使用 `/home/whl/anas-temp-storage-e2e/<run-id>`，显式私有 Docker socket/data-root，运行结束保留报告及失败恢复材料。

`TEMP-T-009` 使用只读命令 `python3 test-env/scripts/review-workspace-temp-stop.py` 生成当前入口行号和摘要，由 [计划中保留的正常停止调用链审阅](../../../dev-docs/plans/archived/workspace-temp-storage.md#历史设计与停止审阅的结论) 记录人工结论。

`TEMP-T-018` 先显式配置 `<workspace>/tmp` 的绝对路径并核对真实容器挂载，再在受管树写入临时哨兵。这个位置属于备份源目录，能验证临时内容确实被排除；使用本来就不在源范围内的外部根不能证明排除策略。随后分别执行 `snapshot`、`send`、`send-file`、`copy` 和 workspace 快照，恢复业务哨兵并验证临时内容和源租约未恢复；克隆与恢复必须获得新身份、真实挂载，保留源容器及内容。外部根的目标侧重新验证另用本轮独立 Btrfs loopback 留证。

其中 `snapshot` 备份采用默认暂停方式，其他三个模式使用 `--no-stop`。每次备份后核验原容器 ID、deployment、workspace 身份、实际挂载及 active 租约路径、临时哨兵和运行状态；暂停后的原容器复启不能悄悄分配新目录。

`TEMP-T-019` 还在 filesystem 阶段保留一个外盘历史 deployment，切回根 A 运行后卸载本轮 loopback，实际执行 `anas rollback <历史ID> -w <workspace> --json`。命令不接受 `-y`，参数错误不能冒充缺盘拒绝。缺盘预检必须保留当前容器 ID、active、已应用根及哨兵；原盘重新挂载后必须生成新 deployment 与新租约，历史制品保持原摘要且不恢复临时内容。这项与 core 的 A→B→历史 A 正常回滚分别留证。

`TEMP-T-021` 对同一个 ODT 文档逐步执行：编辑保存重开、正常停止再启动、容器重建、临时根 A→B 全量切换。每次编辑都读取 Nextcloud WebDAV 的真实 `content.xml`，每次生命周期动作后重开真实编辑器并核对已保存文本。测试保持编辑页面打开来触发应用的正常停止行为；Core 没有新增应用保存协议。

八个 Module 的最小应用栈、离线 Hook 编译、隔离 Node 依赖和凭据标准输入验证入口见 [EDITING-STACK.md](EDITING-STACK.md)。准备和启动成功不代表真实编辑验收通过。

core 故障命令的原始 stdout/stderr/argv 保存在本轮 `reports/private-cli/`（目录 `0700`、文件 `0600`）；异常 traceback 也仅写入这个私有目录。常规报告中的错误使用固定 code/message，不附原始命令或输出。诊断保留失败与每个停止的非零/异常分别记录；故障后保留恢复材料和未能证明安全可卸载的本轮挂载，不把失败的停止记为清理完成。

host bind 用例中的正常 down 可以成功删除所有 Docker 容器，但释放租约仍必须因 `host_mount_reference` 拒绝。脚本只接受对应 Module 的精确 `stop_failed` 释放首因，另外核验完整 Docker 清单为空；持挂载时 GC 保留，卸载后再正常停止释放并回收。失败材料停止时，成功清单证明容器已删除才跳过；仍存在的容器必须核对完整 ID、project 和本 workspace 冻结 working_dir，按已核验 ID 停止并复查。查询失败不能当作不存在。

四种备份的 restore 目标须先证明为本 run 下尚不存在的非符号链接路径，再创建 `0700` 空目录并执行真实的 `anas init <target> -y --json`。既有 restore CLI 要求 `.anas/` 已存在；空 init 只创建骨架，不导入源配置、不启动 Module。脚本检查初始化后的 workspace 路径、权限与 `.anas/`，拒绝意外产生的临时租约或 active deployment，再执行 restore 并要求结构校验成功。每次目标还原后重新核验源容器 ID、active、身份、租约、运行状态和临时内容；已有目标不可复用，还原后的身份与临时根预检仍须独立通过。

workspace 快照创建使用 `--label temp-exclusion`，结果从真实 JSON 的 `snapshot.id` 读取；同样要求 `snapshot.label` 一致、`complete=true`、`problems=[]`。随后使用支持 `-y` 的 snapshot restore，启动后核验新租约和业务内容。顶层伪造 id、错误标签、不完整或损坏的快照不能成为恢复证据。

默认浏览器通过私有 UNIX socket 提交以下固定动作；用户指定本机 Chrome 时，通过 SSH 调用同一冻结 Python 客户端，再由该 socket 提交动作，配置见 [EDITING-STACK](EDITING-STACK.md)。动作服务不能接收任意 shell、命令参数或 shutdown：

| 动作 | 受控服务器执行 | 独立核验 |
| --- | --- | --- |
| `stop-start` | `anas stop collabora`，再 `anas start collabora` | 停止后旧容器不存在，启动后新容器与新租约 |
| `rebuild` | `anas restart collabora` | 新容器与新租约，继续使用根 A |
| `switch-a-to-b` | 延后设置根 B，再 `anas apply --no-snapshot -y` | 所有已启用容器重建、实际挂载根 B、旧受管目录删除 |

服务器脚本：[server-workspace-temp-collabora-action.py](../../scripts/server-workspace-temp-collabora-action.py)。只用 Python 标准库，不作为产品服务。运行布局固定：

```text
<run>/src                          本轮冻结仓库根
<run>/collabora-workspace          已初始化并运行的应用工作区
<run>/collabora-temp-a             初始临时根
<run>/collabora-temp-b             切换目标根
<run>/collabora-actions.sock       mode 0600 的动作端点
<run>/reports                      永久测试报告，独立于临时根
```

受控服务器在本轮持久 holder 的 mount/net namespace 中启动 `python3 <run>/src/test-env/scripts/server-workspace-temp-collabora-action.py --serve`。它要求 `ANAS_TEST_WORK_ROOT`、`ANAS_TEST_RUN_ID`、`ANAS_TEST_MODULE_ROOT=<run>/src`、`ANAS_TEST_ANAS_CMD`、`ANAS_TEST_SOURCE_DIGEST`、`DOCKER_HOST`、`ANAS_TEST_MOUNT_NAMESPACE`、`ANAS_TEST_DOCKER_PID`、`ANAS_TEST_CONTAINERD_PID`。每个请求重新核验 namespace 和 daemon 私有 data-root，按顺序执行动作；只允许当前 UID 或 root 连接。完成后由测试运行器停止 server 进程，socket 通过退出清理移除，不向浏览器公开退出动作。

隔离 Node 工具容器只共享本轮目录和 socket，运行 [Playwright 配置](../../playwright/workspace-temp-storage-collabora.config.mjs)。目录挂载应只读，另提供可写测试报告目录；无需 Docker socket、host PID 或 namespace 操作权限。浏览器客户端要求 `ANAS_TEST_WORK_ROOT`、`ANAS_TEST_RUN_ID`，可显式指定相同固定路径的 `ANAS_TEST_COLLABORA_ACTION_SOCKET`；UI 登录和 WebDAV 另需配置中的测试域名、入口 IP、应用 URL、专用用户名/密码与报告路径。源代码内固定客户端命令为 `python3 .../server-workspace-temp-collabora-action.py --client <固定动作>`。

服务器为三步动作分别写 `reports/collabora-lifecycle-<action>.json`，包括本轮 source/binary 摘要、实际容器命名空间 marker、新容器/租约及全量重建核验。同一 run 不重复已完成动作。浏览器报告记录 ODT 保存和重开结果；报告中不写登录密码、token、页面截图或原始 CLI 输出。

脚本的命令注入、重复消息、超长消息、路径逃逸和跨轮 socket 反例：`python3 test-env/scripts/test-workspace-temp-collabora-action.py`（`TEMP-T-023`）。这些只验证测试入口边界，不代表真实应用测试已通过。
