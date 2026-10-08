# finance 最小编辑栈

`server-workspace-temp-storage-editing-e2e.sh` 是 `TEMP-T-021` 的准备和运行入口。配置源为 `test-env/server-workspace-temp-storage-editing.yml.in`，不包含密码。脚本仅管理本轮 `collabora-workspace`；它不创建或接管 Docker daemon，也不调整生产 DNS、端口或网络。真正的文档验收仍使用 [Playwright 用例](../../playwright/workspace-temp-storage-collabora.spec.mjs) 与 [固定动作服务器](../../scripts/server-workspace-temp-collabora-action.py)。

栈只启用 lego、traefik、samba_dc、postgres、llng、eturnal、nextcloud、collabora。配置明确关闭 Nextcloud Talk、Memories、LLNG 测试入口和 Postgres Adminer；Nextcloud cron、push、imaginary、redis 均保留。Nextcloud PHP 上限为 512M，Samba DNS 缓存为 64M。应用仍使用现有镜像和启动链路，不修改 Compose 或启动健康判据。

2026-10-05 的候选复验将固定 Nextcloud 镜像清单同步为 `34.0.2-r11`，与当前 Module 声明一致。r11 仍为 `developing`；清单更新不表示镜像已发布或已通过编辑验收。新一轮必须独立冻结源码、记录实际镜像来源并执行全部内容断言，不能复用 r10 的浏览器通过记录。

在本轮 holder 的 **mount 和 network namespace** 内运行入口。它强制要求当前、dockerd、containerd 与 holder 共用私有 mount namespace，且当前和两个 daemon 共用私有 network namespace。所有产物位于 `/home/whl/anas-temp-storage-e2e/<run-id>`。Traefik 默认端口 `19071`、TURN 默认端口 `13478`；Samba 的 DNS/LDAP/认证端口只在私有 network namespace 中绑定。虚拟域名默认 `temp-edit.test`，只接受 `.test` 或 `.invalid`。入口 IP 必须真实属于这个 namespace 的唯一接口，接口、前缀长度和网关从当前 namespace 获取。

需要继承 [RUNBOOK](RUNBOOK.md) 的 `ANAS_TEST_WORK_ROOT`、`ANAS_TEST_RUN_ID`、`ANAS_TEST_ANAS_CMD`、`ANAS_TEST_MODULE_ROOT=<run>/src`、`ANAS_TEST_SOURCE_DIGEST`、`ANAS_TEST_DOCKER_SOCKET`、`ANAS_TEST_MOUNT_NAMESPACE`、`ANAS_TEST_DOCKER_PID`、`ANAS_TEST_CONTAINERD_PID`，另设置：

```sh
export ANAS_TEST_NETWORK_NAMESPACE=/run/netns/<本轮已核验的名称>
export ANAS_TEST_ENTRY_IP=<本轮私有地址>
# 可选，默认如上；不要使用生产域名。
export ANAS_TEST_DOMAIN=temp-edit.test
export ANAS_TEST_ENTRY_PORT=19071
export ANAS_TEST_TURN_PORT=13478
```

Go SDK 固定放在 `<run>/tools/go1.26.6/go`，只读挂入固定工具容器，不在宿主运行 Go。`hooks` 先在网络关闭、内存 1GiB 的容器中串行编译八个 Hook，配置 `GOMAXPROCS=2`、`GOFLAGS=-p=1`、`CGO_ENABLED=0`。缓存默认位于 `<run>/collabora-hook-cache`；可用 `ANAS_TEST_GOMODCACHE`、`ANAS_TEST_GOCACHE` 指向本轮目录内的已校验缓存。默认 `GOPROXY=off`，也可设置 `ANAS_TEST_GOPROXY=file://<本轮公开Go包缓存目录>`，将本轮文件代理只读挂入容器。编译不会联网下载或改宿主 Go 环境。

派生二进制保存在 `<run>/collabora-prebuilt-hooks/<module>/anas-hook`，完成全部编译后再复制到 Runner 支持的 `src/modules/<module>/hook/bin/linux-amd64/anas-hook`。输入源码摘要不变；`reports/collabora-prebuilt-hooks.json` 独立记录输入文件摘要和八个二进制摘要。准备、启动、浏览器和停止前均核对输入与产物；缺失或变化就失败，避免 Runner 回退到宿主 Go。固定栈镜像见 [镜像清单](../../server-workspace-temp-storage-editing-images.txt)：13 个去重的 GHCR 引用，关闭的 Talk/Adminer 镜像不在清单中。拉取需由本轮执行者在私有 Docker 中串行进行，并另外记录实际 repo digest；清单不代表已下载。

工具容器保持 root 身份与 `no-new-privileges`，先 `cap-drop ALL`，仅补 `DAC_OVERRIDE` 来遍历由 `whl` 上传的 mode `0700` 源码/缓存目录。源码和 SDK 仍是只读 bind；浏览器仍无 Docker socket、host PID 或 privileged 权限，固定动作 socket 的 root 对端身份校验保持有效。

按下面顺序串行执行；这些命令是真实操作，文档和本地反例并不代表已在 finance 执行：

```sh
runner="$ANAS_TEST_MODULE_ROOT/test-env/scripts/server-workspace-temp-storage-editing-e2e.sh"
bash "$runner" dependencies
bash "$runner" hooks
bash "$runner" prepare
bash "$runner" start
bash "$runner" browser
bash "$runner" status
# 检查报告后，通过既有正常停止入口停止整个测试工作区。
bash "$runner" stop
```

`dependencies` 使用固定官方 Playwright `1.62.1` 镜像，按本轮 `package-lock.json` 执行 `npm ci --ignore-scripts`。镜像已包含 Node、Python 与浏览器；JS 包单独放在 `<run>/collabora-browser-node-modules`，浏览器中只读挂到 `/node_modules`，不在只读源码树内创建挂载点。它继承调用者的 `HTTPS_PROXY`、`HTTP_PROXY`、`NO_PROXY`；如使用本轮限制域名的下载代理，必须允许官方 npm registry。不会自动修改代理或宿主网络。

实际编辑浏览器容器的内存上限为 1 GiB、pids 上限 256、`/dev/shm` 为 256 MiB、`/tmp` tmpfs 为 512 MiB；安装依赖容器仍为 768 MiB。finance 的编辑栈、浏览器和原生门禁串行运行。提高浏览器预算依据实际 768 MiB 预试的内存压力和 swap 观测；512 MiB tmpfs 对照已进入真实保存，但不能据单次结果确定旧崩溃原因。没有 OOM 计数的浏览器崩溃仍保留为未知原因，不归因为 OOM。

用户明确指定本机 Chrome 时，Playwright 配置接受 `ANAS_TEST_BROWSER_CHANNEL=chrome`，`ANAS_TEST_HEADED=1` 打开可见窗口；仍使用独立临时浏览器配置。通过仅绑定本机 `127.0.0.1` 的 SSH 隧道转发本轮 Traefik 端口，浏览器的 `ANAS_TEST_ENTRY_IP` 相应为 `127.0.0.1`。服务端继续使用原私有 namespace、镜像、Hook 和固定动作服务器，不向本机开放 Docker endpoint。

本机动作客户端设置 `ANAS_TEST_LIFECYCLE_SSH_TARGET=<用户@宿主>`、`ANAS_TEST_REMOTE_WORK_ROOT=/home/whl/anas-temp-storage-e2e/<run-id>`、`ANAS_TEST_REMOTE_NETNS=<本轮 namespace 名>`。用例只构造三种既有动作的 SSH 命令，在指定 namespace 中运行冻结的 Python `--client`；服务器仍通过私有 UNIX socket、对端身份和真实运行状态核验动作。凭据仍仅保留内存并传入临时进程环境。运行结束必须关闭本机浏览器、隧道及远端固定动作服务器，再按原流程停止和清理隔离栈。

该模式须单独记录 Chrome 实际版本、本地测试源码与远端冻结后端的摘要、动作报告和两端清理结果；容器浏览器资源预算不适用于本机进程，不能将本机 Chrome 通过记为固定工具镜像通过。真实编辑、WebDAV 保存、重开剪贴板和三个生命周期内容断言完全保留。

`prepare` 初始化一次新 workspace；已有 workspace 或临时根不会被覆盖。`start` 经 `anas apply` 启动现有链路，再核对八个主服务、保留的 Nextcloud 服务、被关闭的服务/应用、真实 Nextcloud installed 状态，以及 Collabora 的 UID/GID、实际 bind、容器内 marker 和同文件系统 jail 树。失败时保留工作区与恢复材料。

`up -d` 返回后，新租约仍可能在 Go Hook 中初始化约 1.2 GiB 的模板。`start` 和三个固定生命周期动作共用最多 300 秒的就绪等待，锁定同一 owned 容器 ID、PID 和零重启次数；固定初始化入口尚未 `exec coolwsd`，或 discovery 的传输失败及 HTTP 404/502/503/504，才可继续等待。health `starting` 允许继续观察，退出、OOM、重启、ID/PID 替换、`unhealthy`、未知状态以及模板、权限、租约、挂载或 marker 错误立即失败。轮询间隔最多 2 秒，每个 Docker/CLI 子命令启动前重新计算剩余预算并限制到 30 秒，discovery 请求最多 5 秒，均受同一截止时间限制。通过仍须核验 PID 1 为 UID/GID 1001 的 coolwsd、实际注册 bind 与容器 marker、固定版本的同文件系统模板树，以及当前测试域的真实 WOPI discovery XML；等待本身不能代替完整编辑验收。

配置通过 `global.host_ip`、`global.dns_server` 指定地址和 DNS；不能同时传入 `env.HOST_IP`，即使值相同也会被真实导入器拒绝。`INTERFACE`、`DEFAULT_GATEWAY_IP`、`HOST_SUBNET_MASK`、`LOCAL_DNS_SERVER`、`HOST_DNS_SERVER`、`SERVER_NAME` 由 runner 派生，不能作为用户 env 配置传入。夹具保留合法的私有 `DOCKER_SOCKET_PATH`、`NETWORK_NAMESPACE_PATH` 和 Module 专属 `SAMBA_DC_HOST_IP`、`SAMBA_DC_INTERFACES` 覆盖。`go test ./internal/runner -run TestTemporaryEditingFixture -count=1` 使用实际内置 Module registry 校验完整渲染模板的导入和规范化，并逐项恢复错误字段核验拒绝；它不运行 Hook、Docker、宿主探测或文档编辑，也不替代 finance 验收。

管理帐号使用现有 `anas admin local credential nextcloud break_glass -w <workspace> --json`。JSON 仅在内存中读取，先通过 Nextcloud 的 `checkPassword` 和标准输入验证，再以临时浏览器进程环境传递。密码不写 fixture、命令参数、终端或报告。用户名也不进入报告。浏览器容器只挂本轮源码和 JS 依赖（只读）、固定动作 socket（只读）及独立浏览器产物目录（可写）；不挂 Docker socket、工作区 Secret Store、host PID 或 namespace 文件。

Playwright 经 WebDAV 上传 ODT 并从 PROPFIND 取得文件 ID，登录后使用 Nextcloud 34 的官方 `/f/<id>` 入口。该入口将 ID 放入 Files 路径并设置 `openfile=true`；`openfile` 是是否打开文件的开关，不能用它传入文件 ID。测试仍须等待真实 `cool.html` 编辑框架，并从 WebDAV XML 和重开的编辑器核验保存内容；文件列表或 Office 菜单可见不算编辑完成。

编辑前还须等待真实 `#document-canvas` 可见、`#map.initialized`、只读检查 `app.map._docLoaded=true` 和 `isEditMode()=true`、`div.clipboard#clipboard-area[contenteditable="true"]` attached，且没有 `#busypopup-overlay`。未知状态不能视为就绪。输入、保存和复制的键盘事件由该 Collabora 输入节点的 `locator.press`/`pressSequentially` 发出；测试不写 DOM、app 状态或直接发送编辑协议。就绪和焦点仍不替代 WebDAV 中保存后的 ODT XML、关闭重开及三次生命周期后的全部内容断言。

就绪后仅对实际可见的已知窗口执行普通 UI 关闭：欢迎窗口在 `iframe.iframe-welcome-modal` 内点击 `#slide-3-indicator`，再点击第三页真实 Close 按钮 `#slide-3-button`；Close 已可见时直接点击。设置窗口通过父编辑框架中的 `#iframe-settings-cancel` 取消，不保存设置。固定镜像的欢迎按钮会发送正常 `welcome-close` 通知；实际 Escape 监听位于父窗口，内层 body Escape 与它不匹配。控件点击和窗口移除各最多等待 30 秒，不强制点击隐藏的 `#welcome-close` DIV。随后真实点击文档 canvas，每次最多等待 5 秒、最多三次；仅真实 Playwright `TimeoutError` 且明确可见已知窗口时，按上述普通按钮路径关闭并确认移除后重试，处理首次检查后才出现的窗口。未知错误、没有已知窗口的超时、关闭失败及达到上限均保留原点击首因。点击成功后仍只读等待 `app.map.editorHasFocus()=true`，才返回编辑框架并定向输入。DOM 焦点位于 clipboard 不能代替 Core 文档焦点；未知对话框和恢复不了的焦点仍使测试失败。`workspace-temp-storage-collabora-focus.test.mjs` 直接执行此 UI 助手，验证正常按钮关闭、迟到窗口、第三页已有 Close、拒绝未知失败和有限重试；反例不能代替真实文档验收。

重开后的复制操作以真实 `Control+Home` / `Control+A` 选中文档，等待 `.text-selection-handle-start` 和 `.text-selection-handle-end` 均 attached 后才发送 `Control+C`，与固定上游 `selectAllText` 的选区等待一致。仍须从原生 `navigator.clipboard.readText()` 读到保存 marker，并逐次核验 WebDAV ODT XML；选区存在不能替代这两个内容断言，也不直接调用复制协议或写 clipboard/app 状态。

两项剪贴板权限通过 `context.grantPermissions` 授予本次独立 Playwright context，不指定单一 origin，以覆盖 Nextcloud 页面内的跨源 Collabora iframe。[固定 Playwright 1.62.1](https://github.com/microsoft/playwright/blob/v1.62.1/packages/playwright-core/src/server/chromium/crBrowser.ts)按 origin 调用 Chromium 授权；[固定 Chromium 实现](https://github.com/chromium/chromium/blob/151.0.7922.34/content/browser/devtools/protocol/browser_handler.cc)同时将该 origin 作为请求源和嵌入源。授权不影响其它浏览器 context，权限存在也不替代真实复制内容验证。

`browser` 由私有动作服务器执行 stop/start、rebuild、A→B 三个固定动作。只有真实编辑用例、三个绑定 source/binary/run 摘要的动作报告都通过时，`reports/collabora-stack-browser.json` 才记录 `TEMP-R-029/030` 已通过。准备、启动和失败报告均将这些需求列为 `not_run`。浏览器报告是 `<run>/collabora-browser-artifacts/playwright.json`；动作报告仍位于 `reports/collabora-lifecycle-*.json`。全部报告 mode `0600`，原始 CLI/Compose 输出不进入报告。

CLI 失败报告保留固定 `error.code`，以及白名单内的 `primary.command.phase`、`exit_code`、本轮 project 对应的 Module 名和独立 recovery phase/status，便于区分首因与恢复结果。错误 message、命令 argv、stdout/stderr 原文、凭据和完整 project 均不写入报告；未知或畸形输出只保留操作名与退出码，不推断首因。成功的管理凭据输出仍只保存在内存中。

真实文档删除和 WebDAV context 关闭结果另存 `playwright.json.cleanup.json`，并投影到编辑栈报告的 `browser_cleanup` 字段。清理失败保留首个编辑/生命周期失败原因；若先前成功，清理失败会使整个用例失败。清理报告只使用固定结果码，不记录异常原文或凭据。纯 Node 反例使用 `node --test test-env/playwright/workspace-temp-storage-collabora-cleanup.test.mjs`，也应在本轮工具容器内执行。

不直接删除应用业务数据、报告或失败恢复材料。完成 `stop` 后，另由本轮执行者审阅 `anas temp gc -w <workspace> --dry-run --json`，再决定是否显式 GC。脚本本地反例：`python3 test-env/scripts/test-workspace-temp-storage-editing-e2e.py`（`TEMP-T-023`）；它不启动服务，也不能替代实机验收。
