# Incus 浏览器审批与会话恢复接续

状态：实施与真实浏览器验收核对。日期：2026-09-23。

沿用 `/Users/whl/Documents/anas` 的已有暂存/未暂存改动，HEAD 保持
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`。本轮不提交、推送、reset 或重新暂存。
目标是接续 M10 的真实 Web 确认交互及 HOSTACT-R-011；保留全部服务器 Docker 对象，
仅在指定 SSH 服务器上的新建 QEMU VM 执行安装与服务操作。

## 已核实的前两轮浏览器结果

第十八轮的失败是未取得浏览器证书观察证据。第十九轮已经通过真实证书 SPKI 核对，但
`embedded_product_assets` 阶段未等到审批组件，不能把它标成确认过期或授权失败。
两轮都由外层监督归档并正常关机，QMP socket 和回环端口释放，物理宿主前后对照全部
相同。失败报告与私有实验盘各自保留，不复用它们消除旧证据。

## 本轮产品修复

源码与 Vue 响应式回归确认了初始导航竞态：打开 `#/maintenance` 时，system 请求先
返回工作区，`sections` 因此变化，但实际 HttpOnly owner 会话尚未恢复。旧监听器立即
依据这个临时匿名能力列表将当前页面改为 overview；随后会话恢复成功也不会还原目标。
真实页面 URL 与显示组件可能因此不一致。

`web/src/nav.ts` 新增同组件 effect scope 内的 `guardSectionAccess`，同时观察当前页、
可访问页与首次会话恢复状态。只有恢复已结束，才依据真实能力集合重定向；App 在初始化
的 finally 中关闭恢复窗口，失败同样不会无限保留。组件渲染及请求原有认证条件不变，
没有用 URL、cookie 存在或加载中状态授予权限。

七项导航测试覆盖深链接跨两次异步状态变化、恢复失败、匿名重新导航、公开页面以及
登录后丧失权限；旧实现先复现两项失败，再验证修复。安全测试同时检查实际 App 接线
和 `authenticated` 渲染条件。完整前端构建、类型检查与 **96 项**测试通过。

## 新的原生运行

本轮重新构建实际嵌入前端的产品工件，实验版本为 `0.0.0-native.20260923.4`，编译前后
核对全部 Go/嵌入资源与前端源码摘要，独立浏览器 manifest 绑定 driver/bootstrap。
不是修改磁盘上的旧 anasd 或通过 Playwright 替换网络响应来验证新代码。

新的第 20 轮 VM 为 `anas-incus-host-1a45a5`，Ubuntu 26.04 amd64、单核 KVM/2048 MiB，
SSH 回环 22154。浏览器只访问精确测试 origin，session 通过私有 stdin 交付，报告不含
密码/token/私钥。证书先按实际浏览器连接核对，页面主资源摘要必须等于工件 manifest。
测试继续使用真实五分钟时间，不快进时钟，不将旧勾选继承到新计划。

## 第二十轮浏览器结果与收尾缺陷

实际 Chrome `153.0.8010.53` 的 **8 项**浏览器门禁均通过。旧计划
`job_3d02f17d1d3516a9a3f756f923d07ee3` 在实际 **302958 ms** 后被新计划
`job_a1bc7f97f74742ae6603f119e6a0d12f` 替换，旧勾选清除，新计划按钮保持禁用；
过期阶段没有确认或 apply 请求。再次勾选只产生一次新确认与一次 apply，实际作业
`job_fb28ed46aae00323946f19e09d043f8b` 成功卸载，`compute_ready=false`。
页面 DOM、URL 与 local/session storage 不含测试 session、确认 token 或私钥。

服务端独立读回确认实验 Docker 回到基线、没有活动 host executor、9 个实际激活对应
6 个准备作业及浏览器的两个计划/一次 apply。公开归档
`host-v20/reports/browser-evidence.tar.gz` SHA-256 为
`7a2f67d54ee324bb4f360442ef54e1663b1bb7da418a53ccafc3007151d37951`。

但是旧外层监督器在关闭 SSH tunnel 后尝试 bind 同一端口，得到 EADDRINUSE，导致 finally
在 QMP 关机之前异常退出。浏览器通过不代表这份外层收尾也通过。本轮立即核对 QMP 精确 VM
身份并请求正常关机；QMP socket 和监听消失，物理宿主全部对照项相同。由于原 Popen 所有者
已经退出，**没有可靠的 QEMU wait status**，恢复记录明确保存 `qemu_exit=null`，不补写 0。
恢复证据保存在该轮 `reports/supervisor-recovery.json`。

新增 `incus_vm_cleanup.py`：隧道、正常关机、实际 wait、必要的显式 QMP quit、资源缺席和
宿主对照分别有界执行，前一步错误不能跳过后一步；所有错误保留固定阶段与异常类型，不回显
私有错误文本。端口改查真实 listener，只有 ECONNREFUSED 可证明缺席，TIME_WAIT 不会阻断
关机。8 项回归覆盖原故障顺序、未知退出、强制退出、对照失败和真实 loopback listener，
已经通过并加入 CI。

第二十二轮新 VM `anas-incus-host-d361a8` / 22156 使用完全相同的产品和浏览器工件、改进后的
监督器重测，单独要求浏览器和外层收尾都通过；不使用第二十轮的恢复报告代替独立运行。

## 第二十二轮终态：浏览器与外层监督均通过

8 项真实浏览器门禁再次全部通过。新一轮的旧计划
`job_a2c99d0797499feb46a9550734f52bf5` 在 **302242 ms** 后更新为
`job_6bc7c7c418e7d1aaeae293a1d51957e0`，旧确认清除、没有自动确认或执行；重新勾选后，
实际作业 `job_84fb3214df1647fff58069f14754b88a` 成功。服务端独立检查通过。

这一次原外层进程保留了完整 wait 证据：`test_exit=0`、`qemu_exit=0`、
`normal_shutdown_requested=true`、`forced_quit_requested=false`、`cleanup_errors=[]`、
`cleanup_passed=true`。精确 QMP socket 及 SSH/HTTP forward 监听消失，物理宿主的
24 个既有容器、17 个网络、卷、服务身份/配置、nft、双栈路由和 named netns 前后全部相同。
未终止任何既有业务容器或修改其网络。

公开归档 `host-v22/reports/browser-evidence.tar.gz` SHA-256：
`dcdc586821bdc4da6b95ef7440b6ebe5c24d79ebd7731e032c3ec0807e3d90fd`。
产品 manifest 的 447 个源码/嵌入输入在构建前后未变化；anasd 摘要为
`f9bfe78764b2770cb20d0a93d522617013caa1252b08a1a34753ddd53c6eda33`，实际浏览器读取的
主资源摘要为 `5cbed182c75d4e428275e512bc2d5342d9eac095511157fe434828f579b66bae`。

本轮全仓 Go 测试、关键五包竞态、go vet、前端完整构建及 96 项测试通过；新增清理用例后，
Incus Python 回归共 **63 项**通过，浏览器离线 guard 的 **4 项**也通过。最终文档生成/索引
和合并差异核对单独记录。这关闭的是已定义的真实过期/重新确认交互，不代表浏览器所有
产品流程、发行升级、其他平台或完整 M10/M11/M12 均已验收。
