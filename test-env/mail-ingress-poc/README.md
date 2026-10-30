# 云端收件协议隔离原型

用于已获同意的 Worker/R2 先行验证，结果统一记入 `dev-docs/plans/mail-integration.md` §4.1—4.7。不是正式邮件 Module 或可直接部署的生产服务。

```sh
npm ci --prefix test-env/mail-ingress-poc --ignore-scripts --no-audit --no-fund
npm test --prefix test-env/mail-ingress-poc
```

Node 22，Miniflare 固定 `4.20260730.0`；仅此目录增加开发依赖和 lockfile。Miniflare 启动本机回环监听和 workerd，以临时磁盘目录持久化模拟 R2；测试结束清理，不使用 Docker 或现有服务。测试拦截全部 Worker 出站请求，只有合成数据、合成凭据，不接真实账号、不改 DNS、不发邮件。

- `worker.mjs`：原文和信封元数据写入同一个 R2 对象；真实 Email Worker 原文流通过 `FixedLengthStream(rawSize)` 交给 R2，保持流式处理并拒绝长度不符；来源级受认证的枚举、原文下载、幂等交付确认；通知 HMAC、独立通知成功标记、有限重试。所有对象均为私有。
- `ingress.test.mjs`：10 个测试覆盖写入异常、未知长度原文流、截断/超长流、NAS 离线、workerd 重启后的原文/附件、可信目标/头伪造、多页补读、通知 5 秒超时、分离确认/并发回执及重定向。测试包装层的 `/__receive` 和 `/__notify` 仅通过本地 Miniflare 内存模块注入，不在 Worker 导出 HTTP API 中。

当前有意保留的缺口：没有生产部署配置、Cron 全量/分页扫描器、容量和保管/清理策略、CSRF/IAM 或实际 Tutorial Web。独立 Go/PostgreSQL/DMS 与 SMTP 试验另见下节，不能用本地测试证明 NAS 已保存邮件；最初的 Worker 测试只验证签名生成；后续 `consumer/notify.go` 已在隔离 NAS 验证 HMAC、五分钟时间窗、目标和持久登记。Worker 的 1/5 分钟重试仍使用注入时间，不代表长期保管实测。

Worker 邮件事件不提供已证明的上游幂等 ID，因此每次调用生成新 delivery_id，不能用发件人提供的 Message-ID 抑制新的 SMTP 投递。原文写入失败抛出错误而不伪报保存成功；Cloudflare 对这种异常的实际 SMTP 响应与重投必须云端验证，不能用 `setReject`（永久失败）替代；后续已测得受控异常返回 421，但未验证上游自动重投。当前不删除原文，避免以通知成功或过早回执触发损失；这也意味着尚未实现有限保管容量管理。

测试原始日志、快照和真实凭据只放 Git 忽略的 `test-env/reports/mail-ingress-20261024/`，密钥文件使用私有权限；不把这个本机目录描述成共享持久证据。

## 真实 Cloudflare 隔离实验

`cloud-test.mjs` 使用 Node 内置 Fetch 调用官方 Cloudflare API，无新增依赖。**运行会创建付费账号内的云资源，必须先取得对应账号/套餐/隔离验证授权**；普通 `npm test` 不触发它。凭据 JSON 只包含 `account_id`、`token` 等本机元数据，文件必须为私有权限；不把密钥放命令行或仓库。

```sh
MAIL_POC_CREDENTIAL_FILE=/private/path/cloudflare-token.json \
MAIL_POC_REPORT_DIR=/private/path/mail-test-reports \
node test-env/mail-ingress-poc/cloud-test.mjs
```

脚本使用随机后缀的新资源名，先确认无同名资源，只创建一个私有 Standard R2 桶及两个临时 Worker。`cloud-fixture.mjs` 包装原型，提供独立测试令牌保护的合成信封注入、通知触发和状态观察；不导出 Email Routing 入口。`cloud-sink.mjs` 模拟 NAS 的 HTTPS 通知端，验证 HMAC、事件字段及 R2 原文已存在，再按固定模式返回 2xx/429/403/503/302 或延迟 6 秒。两端分别等待实际 URL 就绪，部署声明 `global_fetch_strictly_public` 以允许经公网 HTTPS 调用另一个 Worker，依据[官方 Fetch 说明](https://developers.cloudflare.com/workers/runtime-apis/fetch/)。原文测试入口仅接受 32 KiB，运行令牌随机生成，全部入口两小时后拒绝服务。该实验使用合成地址和正文，不配置域名/MX、生产路由或 SMTP 发信。

测试覆盖私有桶/认证、信封分箱、503 后重新部署仍可拉取、通知与交付标记分离、并发回执不可覆盖、真实 HTTPS 5 秒超时、有限重试/错误分类及保留完成项的分页。1/5 分钟重试用注入时间；重新部署不等于强制终止所有 Worker 实例；回执由测试程序模拟，不是 DMS 的持久交付证明。此脚本不覆盖真实 SMTP、NAS/Go 消费者、完整调度或到期清理；后续 SMTP 与 Go/DMS 实机结果见计划 §4.3—4.4。

脚本 `finally` 清理本轮合成对象、两个 Worker 和桶，并记录清理结果；失败不能伪报成功。API Token 与已获授权的 R2 订阅保留。每轮 JSON 保存源码 SHA-256、部署版本、逐项结果及清理状态，不含运行密钥；日志/结果只写私有的忽略目录。中途进程被强制杀死可能留下资源，应按该轮 JSON 的精确资源名检查，不能按前缀批量删除其他轮次或既有资源。

API 依据：[Worker 模块上传](https://developers.cloudflare.com/api/resources/workers/subresources/scripts/methods/update/)、[绑定元数据](https://developers.cloudflare.com/workers/configuration/multipart-upload-metadata/)、[R2 建桶](https://developers.cloudflare.com/api/resources/r2/subresources/buckets/methods/create/)、[r2.dev 公开状态](https://developers.cloudflare.com/api/resources/r2/subresources/buckets/subresources/domains/subresources/managed/methods/list/)。

## Go/PostgreSQL/DMS 与 SMTP 独立试验

下列文件固定绑定已获授权的 finance 隔离环境，普通 `npm test` 不调用它们。Go 复用根项目现有 `pgx`，Python 只用标准库；不新增生产依赖。

- `consumer/main.go`：一次分页取信；按来源取得 PostgreSQL 会话锁，保存原文字节及来源/投递 ID/目标账本，用稳定 Dovecot GUID 核对收件结果，然后写云端最终回执。完成清单会继续枚举，以供账本丢失后的核对。未知结果且查不到 GUID 时保持 `uncertain`，不盲目重投，其他邮件仍可处理。
- `consumer/main_test.go`：重定向不外传凭据、截断/超限/未授权响应拒绝、原文私有保存和有限重试。云请求最长 30 秒、最多 3 次，间隔 1/2 秒，一次取信最多 5 分钟；后台原型见 `consumer/notify.go`，正式 Module 的调度和认证仍未接入。
- `consumer_server.py`：固定 Compose 工作区 `/home/whl/anas-mail-ingress-test-20261025`、项目 `anas_mail_ingress_1025` 和专用 Docker socket；校验 daemon 数据根、容器标签及挂载，只管理两个合成邮箱。复用专用 daemon 的镜像缓存，不接默认 Docker；重置只针对本轮合成收件箱及账本。
- `consumer-cloud.mjs`：显式创建/清理独立私有桶与 Worker；`custom-domain` 绑定已授权测试域的 HTTPS 地址，运行凭据交付目标需事先授权。管理 Token 留在本机，`cloud-runtime.json` 仅含本轮取信权限；服务入口按最初设定的三小时到期。
- `consumer-test.mjs`：并发冷启动、重复取信、DMS 写后/本地完成后/云确认后中断、丢失账本，以及未知结果保留等 7 组实机检查。独立回读 DMS 原文、GUID、箱归属、PostgreSQL 和云标记；模拟清空账本不是完整备份恢复。`MAIL_POC_EXISTING_COMPLETE=1` 只用于已核验的两封 SMTP 完成项继续故障测试，首项明确记录为完成基线核对，不冒充另一轮并发冷启动。
- `smtp-fixture.mjs` / `smtp_sender.py`：显式启用 `a`、`b`、`fail` 三个 `mail-poc.bengbu.cc.cd` 字面规则；原文限 32 KiB，保留原有到期时间。`fail` 先记录处理器确实被调用的证据，再注入原文保存异常；这不是耗尽真实 R2 配额。发件地址仅为测试子域，使用观测到的 finance IPv4 配置 SPF；SMTP DATA 开始前记录意图，结果不明时不自动重发。
- `smtp-test.mjs`：停机期间真实 SMTP 收件、保存异常临时拒绝、未知收件人拒绝，再启动 DMS/PG 核对分箱、正文/附件及重复取信。修复后如需重发，须先检查云存储，再显式启用 `MAIL_POC_RETRY_REJECTED=1`；发送器仍只接受记录中有明确 DATA 4xx/5xx 的旧事务，接受结果不明不能重发。`--resume-import` 从保存的 SMTP 成功记录继续下载/入箱，不再次发信；未知地址的网络失败不会阻断独立入箱核验，但仍使整份报告失败。各轮失败证据先另存，最后恢复服务状态。

首次真实 SMTP 暴露旧合成夹具只传入字节数组、未覆盖 Email Worker 原文流的问题；本机复现 R2 要求已知长度，修复采用官方 [FixedLengthStream](https://developers.cloudflare.com/workers/runtime-apis/streams/transformstream/#fixedlengthstream)。历史云协议 8/8 对应修复前源码，不代表旧版真实 SMTP 可用；各版边界和实机结果见计划 §4.3。

本机控制端可用 `workers.dev` 不代表 finance 可用。该主机实测 `workers.dev` 解析异常，测试自定义域的 IPv6 连接也超时；本轮消费者配置显式 `ipv4_only` 和 `http1_only`，正常验证 HTTPS 证书，并用本地 TLS 测试确认实际协商为 HTTP/1.1。不能把控制端就绪或单次 TLS 握手当作 NAS 可完整下载并确认邮件；最终连通性和测试结果见配套计划。finance 的出站 25 只用于扮演受控 SMTP 发件源，NAS 取信仍走 HTTPS，不开放入站 25。

用户后来提供 Shadowsocks 代理重测：复用已有 V2Ray 二进制，以 0600 私有配置启动仅监听回环的临时客户端。`consumer-test.mjs` 可显式设置 `MAIL_POC_HTTPS_PROXY=socks5h://127.0.0.1:10829`，只传给远端消费者命令的 `HTTPS_PROXY`，不更改系统/生产代理，也不记录外部代理凭据。该轮使用新 Worker/R2 来源及空邮箱/账本，直接通过代理访问 `workers.dev`；证据在 `test-env/reports/mail-ingress-proxy-20261025/`。代理结果不能当成 NAS 直连通过；临时进程、代理配置与取信凭据均须清理。此项只是明确授权的测试路径，不是已实现的产品代理管理功能。

`doveadm save -g` 仅验证固定 DMS 的存储接口；[官方文档](https://doc.dovecot.org/2.4.0/core/man/doveadm-save.1.html)明确其不运行 Sieve。隔离 DMS 关闭反垃圾/病毒组件，不能把此路径定为生产入信方案或声称验证了过滤规则、配额与完整 SMTP 管道。

运行顺序为：专用 namespace/daemon 预检 → `prepare/start` → 云 `create` → 获准后传受限运行配置 → `configure` → `consumer-test.mjs`。SMTP 试验必须另获域名/DNS/路由授权，确认无同名记录及已有规则后才能 `smtp-enable`；这里固定的测试域/IP 不应照搬到其他环境。实机日志和配置均写 Git 忽略的 `test-env/reports/mail-ingress-consumer-20261025/` 及服务器 `evidence/`。

清理必须显式执行：关闭本轮 SMTP 规则和自定义域、删除本轮云对象/Worker/桶，移除本轮新建 DNS 并核对基线，再删除服务器取信凭据、停止本轮 Compose 及专用 daemon。不得按前缀全局删除；数据与不含云凭据的证据保留。Cloudflare 子域启用界面实测会自动添加父域 SPF/共用 DKIM，需要比较前后 DNS 并恢复本轮副作用，不能只检查子域 MX。


## 真实通知、调度和自管云补充

- `consumer/notify.go` / `notify_test.go`：隔离回环 HTTPS 通知端，验 HMAC/时间窗/来源和目标，提交 PostgreSQL 任务后才返回 202；单消费者将通知和实际每 60 秒 ticker 合并。新通知在一轮开始后到达时留给下一轮，失败不伪报任务完成。只监听 finance 28443，外部入口由明确授权的临时测试隧道承担。
- `scheduler-fixture.py` / `scheduler-test.mjs`：真实 Worker/R2 → HTTPS → NAS、8 个重复通知、认证反例、PG 故障、DMS 离线/消费者重启、真实时间轮询和实际通知超时七组检查。原始失败及端口预检原因见计划；测试不是生产 Tunnel 部署。
- `delivery-test.py`：Dovecot LDA 的正文附件、Sieve 分箱/丢弃、配额及未知用户。`discard` 成功但无入箱，说明退出 0 不能代替生产交付确认；该脚本不修改现有稳定 GUID 消费者的投递路径。
- `relay/main.go` / `main_test.go`：独立 Postfix pipe 的私有文件源；原文/信封原子发布并 fsync，兼容列表/下载/不可变回执及有限通知。以 source/长队列号/目标固定投递编号，相同队列重试核对原文后复用，不按 Message-ID 合并。队列号保管/代际策略、容量、备份和部署自动化仍未实现。
- `relay-fixture.py` / `relay-smtp.py` / `relay-test.mjs` / `relay-retry-test.mjs`：用户指定 osa 的专用 daemon/namespace，固定镜像 Postfix，不改宿主邮件服务。仅允许受控 finance SMTP 测试源；检查停机接收、重启、未知目标/开放中继拒绝、恢复/重复取信、真实通知，以及发布前/后失败的真实 Postfix 队列恢复。此轮没有执行 Cloudflare → osa 转发规则或供应商代发。

后台消费者用 `MAIL_POC_SERVE=1` 显式启动，配置文件/密钥需 0600；停止进程、隧道、SSH 转发和测试监听后删除运行凭据。自管云、通知源和正式模型的原始证据分别保存于 Git 忽略的 `mail-ingress-relay-20261025/`、`mail-ingress-scheduler-20261025/`，全部只用合成邮件。管理 Token 留本机。清理结果和源码/制品摘要统一记录在根计划，不在本 README 中声称正式邮件功能已验收。

正式 M1 代码已从验证阶段开始建立于 `modules/mail/`；`test-env/scripts/mail-module-foundation.py` 仅在此前批准的 finance 专用 PostgreSQL 执行模型/基础服务测试，不是正式部署或发布脚本。
