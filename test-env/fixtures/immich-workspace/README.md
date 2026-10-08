# Immich 真实 ANAS workspace 验收入口

[`server-immich-workspace-e2e.sh`](../../scripts/server-immich-workspace-e2e.sh)
通过真实 `anas init/apply/restart/backup` 调用部署仓库 Module。
它使用 Samba AD 和 Authentik 原生 OIDC；Authentik 同时作为另一共享 PostgreSQL Consumer。
现有 `server-immich-e2e.sh` 的手写 Docker fixture 是独立上游行为测试，不能替代本入口。

必须先由操作者明确选定测试主机并通过既有流程准备独立 Docker/containerd 和 ANAS test netns。
脚本不 SSH、不准备 namespace、不修改宿主 DNS/firewall，不连接历史测试主机。
它复用现有 daemon、netns 和 proxy guards，并拒绝默认 socket、生产 data root、非空 daemon、
已有 workspace、symlink 路径及非 Btrfs。升级目标 PG/Immich/Authentik 固定 Module 组合为
`18.4.0-r4 / 3.2.4-r1 / 2026.5.6-r15`；PG `vector=0.8.2`、`cube=1.5`、`earthdistance=1.2`，preload 为空。
版本变化必须先复核脚本与上游行为。测试关闭 ML，不验证 4GB 或移动端。

主机需要 root、Btrfs 工具、findmnt、支持 ACL/xattr 的 rsync、Docker Compose、curl、Go、
Python 3.9+ 和 python3-pyyaml；`anas` 与旁边的 `anas-helper` 必须来自同一源码。
Python 断言必须启用；禁止 `python -O` 或 `PYTHONOPTIMIZE` 跳过验收断言。
`--modules` 必须指向同一源码 checkout 的 modules，旁边保留现有 go.mod/go.sum 与 contracts/；
脚本将它们复制到私有 fixture 根，供当前 Contract 校验及源 Hook 编译使用，不增加 Go 依赖。
重复创建 fixture 时核对共享 Contract 内容，拒绝目录漂移。
为整个 cold build、完整媒体/共享数据库副本及多个恢复点的全服务镜像归档留足磁盘空间。
缺少真实 Btrfs、namespace、DNS 或构建源网络时退出，不使用 fake Docker、普通目录或 fixture 替代。
固定 Authentik 原生 shell 仍输出两行横幅；驱动使用 verbosity 0 后严格核对并移除这两行，保留其余 stdout 供 JSON/整数/Task ID 校验。横幅变化时拒绝继续，需重新核对上游。
首次 apply 前，脚本按当前 `.github/mirrors.json` 的固定上游摘要准备 Immich mirrors，并仅在隔离 daemon 内标记目标名称；不要求尚未发布的 Module 镜像已存在 registry，不执行推送。
首次备份覆盖 inactive Compose 服务，因此测试显式缓存它们的固定镜像。

```sh
go build -o /tmp/immich-workspace-bin/anas ./cmd/anas
go build -o /tmp/immich-workspace-bin/anas-helper ./cmd/anas-helper

# 以下 IP、port 和 namespace 必须由操作者从隔离环境分配；示例不是主机授权。
sudo nsenter --net=/run/netns/anas-immich-test -- env \
  DOCKER_HOST=unix:///run/anas-immich-test.sock \
  ANAS_UPGRADE_NETNS_PATH=/run/netns/anas-immich-test \
  ./test-env/scripts/server-immich-workspace-e2e.sh \
  --anas /tmp/immich-workspace-bin/anas --modules "$PWD/modules" \
  --workspace /data/anas-immich-workspace-01234567abcdef89 \
  --host-ip 10.254.0.2 --host-lan-ip 10.254.0.250 \
  --host-lan-bridge-ip 10.254.0.249 --port 29443
```

`--preflight-only` 只检查前置条件；`--keep-workspace` 保留此次新建 workspace。
返回码 0 仅表示所有实际步骤和清理成功；2 表示前置阻塞；1 表示实际阶段失败。
报告位于 `<workspace>.reports/report.json`，阶段 CLI JSON、stderr 与临时凭据文件权限为 0600。
CLI 非零退出或超时也保留已有输出；失败清理前另保存有限的 active/deployment 状态到
`failure-workspace-state.json`，包括维护/恢复保护，避免删除现场后丢失诊断证据。
目录内 `private-state.json` 含一次性用户密码，运行者应保护并在审阅后删除；不会打印 token 或密码。
默认仅停止并清理此次新建 workspace、其备份、容器和 snapshot；保留镜像缓存，不全局 prune。
ANAS stop 或清理失败会保留现场并报告失败。
stop 返回成功但仍有此次 workspace 容器时也保留现场，防止删除仍在使用的数据。

入口覆盖以下检查，但只有主机执行报告才是验收证据：

- 空库新 apply、空库重复 apply，以及上传后重复 apply 的容器/Secret/媒体/相册/身份/Valkey 保留。
- 真实 AD anchor 经 Authentik 的 native OIDC code flow；首个普通访客拒绝建号，roleClaim 管理员和普通用户建号。
- 独立 AD 用户的邮箱修改、同邮箱不同 anchor 冲突、同 sub 并发首次 callback 和原生软删除后重新登录；
  使用实际 LDAP 同步、两个不同授权码及管理员 HTTP 删除入口，读取数据库仅作身份断言，不直接建号或修改绑定。
- Immich 和 Authentik 实际普通角色 TCP 登录及错误密码拒绝，普通角色校验精确扩展版本和 PG 18.4；
  受限 preload 设置由 Provider 管理员通过私有 TCP 独立读取，不给应用角色添加 settings 权限。
- 照片、真实 H.264 视频上传、原件 hash、thumbnail 作业及相册；真实 `anas restart` 后复核。
- 全 workspace `backup create --mode copy` 与 `verify`；按实际服务 image ID 校验归档。
- 先用私有模块副本的新装 PG 18.4/SCRAM/真实 pgvector 0.8.1、媒体和 HNSW 建立基线，
  再通过真实 ANAS apply 升到目标 0.8.2。副本使用已审计源码摘要，保留当前 Provider 和受管入口，
  `18.4.0-r3` 只是隔离 daemon 中的测试身份，**不是已发布版本或历史版本验收**；元数据、localization 和两个 Compose 服务一致。
  所有补丁锚点先校验，原 modules 不被修改；运行时复核固定 Immich compiled 版本范围允许 0.8.1。
  Docker 事件必须实证所有旧运行容器先停写再启动新 PG，配合 ANAS 完整恢复点、普通角色版本和 HNSW/媒体/共享 Consumer 断言。
- 升级前后的真实 `anas plan` 保存两个共享消费者及完整 stop/restart scope，并复核 runtime、config、Secret 和 active 状态未改变。
- 修改两个数据库的状态、受管媒体、Valkey、config 和 Secret 后，通过 ANAS restore/start 恢复；
  复核内置 user.id/anchor、照片/视频 hash、相册、共享 Consumer marker、配置/Secret 和精确镜像。
- 再恢复匹配旧数据/镜像的 0.8.1 point；在临时 PG Hook 原生维护成功后故意失败，
  硬断言已有 `postgres_maintenance_target` 记录、workspace 已停、普通 start/restart、不同 apply、rollback 均被阻断。
  只用 ANAS 匹配 snapshot 恢复旧 0.8.1，再用匹配全 workspace backup 恢复新 0.8.2；
  restore 返回后所有写容器必须停止，显式 start 后校验对应 image ID、实际版本、HNSW、媒体和另一共享数据库。
  不修改 pg_extension catalog、不直接让旧镜像打开新数据。Docker 事件是辅助证据，不把请求顺序当作停写验收。
- 私有 Hook 在原生扩展维护成功后暂停，精确核对本次 CLI/Hook 的 Linux 进程身份后 SIGKILL；
  验证持久保护阻断普通启动和其他候选，再通过 `apply --deployment` 重试同一个冻结候选。
  原恢复点仍须完整，重试须先停止已失败候选再恢复消费者；之后复核媒体、HNSW、共享数据库及保护清除。
  仅测试副本含暂停标记，生产 Hook 和请求 ABI 不变；本步骤只能在专用主机运行后作为验收证据。
- 真实 AD 停用、移出 APP_immich、删除账号、先普通 RP/IAM 登出再移组，以及保留 APP_immich 准入但失去 Admins 的独立用户矩阵；
  等待既有 LDAP sync 和目录通知任务 DONE，并以旧 Bearer/Cookie/API key/分享 HTTP 失效为准。
  复核媒体原件和 anchor 保留、其他用户的 session/API key/分享仍有效。
- Admins 降权后，等到晚于撤权截止的 PG 秒再走原生 OIDC 登录；同一内部 id 变为普通用户，新的凭据有效，旧管理员凭据保持失效。
- 重新恢复许可组后原生登录仍使用同一内部 id；使用冻结的原生 Provider、issuer、anchor 和接收表原截止时间重新调用原生签名发送器，
  验证旧凭据不恢复、新建 session/API key/分享与其他用户不被撤销。普通登出单独验证 API key/分享保留，
  再在无活跃 Immich OAuth grant 的情况下验证目录通知仍能撤销它们。

固定 Authentik worker 在 DONE 后清空任务消息，脚本不解码已完成任务。报告中的
`sender_tasks_done_window` 只记录时间窗口内任务的 message_id、retries 和 mtime，不推断其目标或事件时间；
`receiver_signed_cutoff` 只读 Immich 原生签名接收路径写入的 anchor/epoch，结合 HTTP 断言作为目标撤权证据。
重试按返回的精确 message_id 等待 DONE，再复查新旧凭据，未增加生产任务消息保留协议。

对应 `IMMI-R-001/002/003/005/007/008/010/011/012` 及已有 RDBEXT/ALOG/备份要求的本入口范围；
不代表这些含移动端、满盘、全部凭据竞态或完整登出安全负例的整行要求均完成。
本机 harness 单元测试也不能替代真实主机验收：

```sh
PYTHONDONTWRITEBYTECODE=1 python3 test-env/scripts/test_immich_workspace_e2e.py
PYTHONDONTWRITEBYTECODE=1 python3 test-env/scripts/test_immich_workspace_plan.py
PYTHONDONTWRITEBYTECODE=1 python3 test-env/scripts/test_immich_workspace_crash.py
```

本入口只使用 workspace 内受管数据，不测试外部图库或重建外部挂载/嵌套子卷布局。
固定上游 API 以 Immich `v3.2.4` 的
[`constants.ts`](https://github.com/immich-app/immich/blob/v3.2.4/server/src/constants.ts)（pgvector 范围 `>=0.5 <1`）、
[`server.controller.ts`](https://github.com/immich-app/immich/blob/v3.2.4/server/src/controllers/server.controller.ts) 和
[`album.controller.ts`](https://github.com/immich-app/immich/blob/v3.2.4/server/src/controllers/album.controller.ts) 为准。
原生退出接口见固定版本
[`auth.controller.ts`](https://github.com/immich-app/immich/blob/v3.2.4/server/src/controllers/auth.controller.ts)。
[`backup` 规范](../../../docs/reference/contracts/backup.md)与
[`Immich 私有矩阵`](../../../modules/immich/dev-docs/requirements/immich-module.md)仍是验收规范来源。

网页验收复用 `npm run e2e:immich-browser`、现有 Playwright 与安装的 Chrome；只允许本入口
`photos.iw<8位hex>.immich.test` 的 HTTPS origin。将同一端口经 SSH 本地转发到专用 netns 的
入口地址；Chromium 自有 resolver 只映射此测试域到 127.0.0.1，不修改宿主 DNS 或浏览器配置。
在本轮服务器脚本停止后显式 `anas start` 同一保留 workspace，再分别提供管理员/普通测试账号：
`ANAS_TEST_APP_URL`、`ANAS_TEST_IAM_URL`、`ANAS_TEST_USERNAME`、`ANAS_TEST_PASSWORD`、
`ANAS_TEST_ROLE=admin|user`、`ANAS_TEST_PHOTO`、`ANAS_TEST_VIDEO` 和私有 `ANAS_TEST_REPORT_FILE`。
原件必须是本测试创建的 PNG/H.264 fixture，测试账号应尚未上传这些内容（重复文件明确失败）。
凭据只在私有执行环境传入，不写命令历史或报告。测试执行原生 OIDC 与引导、UI 上传两种媒体、
SHA256/缩略图、UI 创建/改名相册、原生RP退出与直接IAM退出、旧Bearer/Cookie会话401和下一次OIDC需要IAM登录。
使用现有脱敏 reporter，禁用截图/trace/录像；此入口仍不代替真实手机后台备份验收。

也可在原测试主机的同一专用 daemon/network namespace 内使用官方
`mcr.microsoft.com/playwright:v1.62.1-noble` 和 `ANAS_TEST_BROWSER_CHANNEL=chromium` 运行同一入口。
沿用服务器隔离检查；仅挂载公开测试运行代码、精选私有测试登录字段、合成媒体与独立输出目录，
不挂载 Docker socket 或整个 Secret store。Chromium 的 127.0.0.1 是专用 namespace 的入口。
该执行方式无需导出私有测试夹具；本次原生 UI 选择 en-US，报告仍复用脱敏格式。

官方大镜像下载受限时，可在同一专用容器内复用既有 Node 镜像和对应 Playwright 1.62.1 的官方
Chromium headless shell 151.0.7922.34/linux64；安装所需系统库，以 `ANAS_TEST_BROWSER_EXECUTABLE`
指定其绝对路径。执行前核对公开运行时摘要，仍保持凭据/媒体在原主机及上述挂载边界。


自动目录事件验收使用 `verify_directory_event_delivery`：准备测试账号时可显式同步，移出 `APP_immich` 后不允许手工同步。核对本次新 `Modify/member` 事件、受管监听器的持久游标与触发时间、原生 LDAP/签名通知 DONE 任务、已验签接收截止以及真实旧 Bearer/Cookie/API key/分享失效；保留媒体/绑定与无关用户凭据。记录实际耗时，并以300秒作为本次健康固定组合的主机验收上限。该时限需要真实主机结果，不由单元测试推定，不声明4GB整机或普遍容量保证。

扩展升级使用常规 `apply`，复用已准备且核对固定源的镜像；不在该数据生命周期步骤
强制重建所有模块。固定镜像构建单独验收。仍必须在真实应用角色下核对升级后的扩展
版本、preload 和 HNSW，并通过 ANAS 捕获实际运行镜像与全工作区恢复点。
Casdoor 入口复用同一隔离驱动；并发首次回调在授权完成后会合，验证单一有效绑定。
软删除后以新邮箱重登必须无凭据并保留完整 tombstone，不限定上游拒绝为 HTTP 400。

网页入口使用 `ANAS_TEST_IAM_PROVIDER=casdoor` 选择固定 Casdoor 的原生登录表单和 `/api/logout`，默认仍为 `authentik`。两种 Provider 共用媒体、相册及旧会话失效断言；新增 Casdoor 分支必须完成真实浏览器验收，语法检查不能计为通过。

浏览器默认将隔离域名映射到 `127.0.0.1`。Docker 的 host 网络没有进入测试主机的独立网络命名空间时，使用 `ANAS_TEST_HOST_ADDRESS` 指定该空间的隔离 IPv4 地址（仅允许回环或 `10.0.0.0/8`）；先核对入口响应，避免连接另一套代理。

源站预检查使用 HEAD 验证 HTTPS/HTTP 可达性，避免下载 GitHub 首页占用网络检查时间；真实构建输入仍独立下载并校验固定摘要。2026-10-08 用户授权临时暂停 finance 负载后的完整套件与原服务恢复记录见 modules/immich/dev-docs/plans/immich-module.md。
