# Cloudflare 收件源候选

本目录是正式 Mail Module 的 Worker/R2 协议实现候选，区别于 `test-env/mail-ingress-poc/` 历史原型。**除早期真实协议和 SMTP 子集外，隔离测试已验证真实 Worker 定时通知重试及恢复后 Go/PG/DMS/Roundcube 入箱；该测试使用合成目录，并非完整安装、真实 AD 或生产启用验收，不能单独作为生产就绪声明。**结果及边界见[邮件集成计划](../../../dev-docs/plans/mail-integration.md) §4.130。

`worker.mjs` 沿用 Go 的 `anas.mail-source/v1`：Reader 只列举/取原文/提交最终交付回执，独立 Control 凭据比较版本更新接收许可。两者均是该来源的随机 32 字节凭据，不是 Cloudflare 管理 Token。接收前按真实信封地址确认许可并预留容量和序号；许可关闭保存最后接受序号，上传中的旧邮件保持原人员/箱。原文不按 To/Cc 分箱。

当前非秘密输入为 `SOURCE_ID`、`GENERATION`、JSON `DOMAINS`、显式 `CAPACITY_BYTES` 与 `DELIVERED_DAYS`；`MAIL` 绑定专用私有 R2 bucket，Reader/Control 分别通过 Worker Secret `READER_TOKEN`/`CONTROL_TOKEN` 交付。finance 的已确认正式值为 2 GiB/7 天，本地小配额测试不改变该设置。启用时还须接上每分钟 scheduled 维护，完成实际套餐/额度核验及通知配置；当前不提供未经验证的一键启用配置。

`install-preflight.mjs` 是安装器的只读候选：使用本机绝对路径的私有凭据文件（JSON 含 `account_id`、`token`，当前用户拥有且权限不宽于 0600）验证 Token 状态、检查现有 Worker 及分页 R2 bucket 的重名，并读取 Workers 子域。使用 `node modules/mail/cloudflare/install-preflight.mjs /绝对路径/凭据.json WORKER名称 BUCKET名称`；令牌不会写入结果或错误。它只发送 GET，不创建、覆盖、删除资源或修改 DNS；结果中的写入权限、实际额度及计费状态保持未知。重名检查不是创建事务：外部管理员可能在检查后创建同名资源，故检查通过不能作为安全覆盖或启用的依据。真实安装、冲突并发处理、持久恢复、权限和费用核验尚待实现和独立验收。

`installer` 中的资源预留候选使用私有追加日志和进程锁，先同步意图再调用创建接口；明确的同名冲突不接管，创建响应丢失时停止并保留未知，不能盲目重试。已确认的 Worker ID 和 bucket 指纹在恢复时重新回读，日志损坏、并发运行及文件路径不安全时拒绝操作。固定 Cloudflare HTTPS 目标的创建/回读客户端与日志引擎已通过随机 Worker 和空 bucket 的真实组合预留、重复回读和安全清理子集；在真实创建成功后由测试包装器丢弃回执，重启保持未知且没有重试。生产发布协调、路由、界面和链路任意阶段断网恢复仍未完成，不代表安装完成。bucket 创建元数据只是漂移检查，不是永久唯一身份；请求丢失后不能凭同名自动认领。

版本发布候选以内嵌的固定 `worker.mjs` 和 `notification.mjs`、随机 Reader/Control 凭据及不可变 Worker ID 发布；操作日志记录意图，先检查没有既有部署，再回读代码、非秘密绑定和实际生效版本。来源凭据在发布前写入受进程锁保护的 0600 私有文件并同步，重启读取相同值；文件损坏、路径不安全或发布后丢失时停止，不自动生成替代凭据。真实隔离测试覆盖重开日志和私有凭据后的回读，以及实际发布后模拟丢回执时不重复发布；测试资源及凭据均已清理。该协调过程尚未接入产品入口和 ANAS Secret 交接；提供商元数据不证明 Secret 值或实际收件。定时维护、Email Routing 与启用验证仍需分别完成；预检无法保证其他管理员不会在请求间并发修改。

定时维护候选在空计划回读后同步意图并设置每分钟任务；已有计划不覆盖，丢失回执后停止。安装器隔离云测试验证实际计划写入、回读及模拟丢回执后不重复写入；独立的真实 Worker 组合测试另验证定时触发后的通知重试和恢复，但未将两次测试拼接为完整安装验收。Cloudflare 按名称替换计划，事前回读不能原子排除其他管理员并发更改；计划变更的传播可能需要等待，回读成功不表示计划已经执行。

取信 HTTPS 入口候选在 Worker 和定时计划确认后，保存意图并为已确认 Worker 启用 `workers.dev`，不启用预览。回执丢失保持未知；收到回执后探测失败可只读重试。隔离云测试验证无凭据拒绝、Reader 认证及状态匹配，定时步骤在该测试中为模拟；尚未验证 Control 实际写入、SMTP 或通知，不能作为完整安装或对外收件成功。

内部协调候选在创建云资源前检查收件域、容量、保留期、通知地址和取信代理，再顺序执行上述准备步骤并在重启后逐项复验，不重发未知或已确认写入；不返回明文凭据。它尚未接入产品安装入口、ANAS Secret 和邮件地址规则。真实串联测试的定时步骤使用模拟，另有一次未解释的入口冲突需继续调查。

`PrepareRuntime` 是运行配置交接候选，输出可公开的来源描述，凭据须由调用者显式写入私有目标；它不将凭据保存到 ANAS Secret Store，也不启用邮件或证明路由写权限。合并辅助函数保留已有来源并检查域和全部来源容量，写入配置和 Secret 仍由调用者负责。

准备来源前会以独立路由 Token 只读一页规则，认证拒绝或不可确认时不创建资源；读取成功不证明写权限、域名归属或收件成功。

`CommitSource` 候选通过 ANAS 配置事务提交合并结果，并在事务内比较调用者提供的既有 Secret；要求与实际存储字节一致，冲突不覆盖。可显式把旧来源标为排空，但新来源必须覆盖它的全部域；候选不直接撤销云路由，未连接安装页面或自动部署。

总量包含原文及控制记录的保守预留：固定控制根 4 MiB，每封另预留 4 KiB；清理后保留 2 KiB 的最小回执/占位预算。已交付原文到期清理；容量压力从最早接受的已交付副本开始，足够后停止。未交付完整原文不自动删除。不完整上传先写永久占位，再释放原文预算，防止很晚完成的旧上传复活。跨对象清理有持久意图与有界日志；清理前压缩记录，避免释放容量后仍暂存较大的旧记录。最小记录仍计入容量，不能宣称无期限无限保管。

API 的正常响应不包含 Token、原文 URL 或任意 R2 key。错误不回显存储服务器原始异常。可选 `NOTIFY_ORIGIN`/`NOTIFY_TOKEN` 使用独立 HMAC 及最多三次延迟重试，通知成功与最终交付分开；未配置时如实为 unconfigured，配置正确也不代表 NAS 已实际可达。通知组件在部署时与 worker.mjs 一起上传。R2 条件写是原子操作，但[同一对象每秒最多写一次](https://developers.cloudflare.com/r2/platform/limits/)；共享容量根会限制突发写入，控制文档采用有界退避，消耗过的邮件流不盲目重试。第 102 轮小负载真实 R2 子集已通过，尚无大规模吞吐承诺。

安装候选可以指定通知的 HTTPS origin，并生成与取信及控制凭据不同的通知密钥；实际通知地址固定为该 origin 下的 `/api/ingress/notify`。隔离 Cloudflare 发布测试验证了非秘密地址和 Secret 类型回读及重启恢复，没有向通知地址发送请求。正式 Secret 交接、实际网络通知和完整安装入口仍待完成。

本地验证：`node --test modules/mail/cloudflare/worker.test.mjs modules/mail/cloudflare/install-preflight.test.mjs`。Worker 用例使用带条件写语义的受控存储，涵盖地址移交时上传中邮件的旧归属、原文/回执、并发预留、容量、留存、原文持久后记录失败、过期上传晚到，以及清理每一步的确认丢失；预检用例覆盖 R2 分页、冲突、失败与私有凭据文件。它们不代替真实 Cloudflare、SMTP 或 NAS 验收。

API 参考：[R2 Workers API](https://developers.cloudflare.com/r2/api/workers/workers-api-reference/)、[R2 bucket 分页](https://developers.cloudflare.com/api/resources/r2/subresources/buckets/methods/list/)、[R2 一致性](https://developers.cloudflare.com/r2/reference/consistency/)、[Email Worker](https://developers.cloudflare.com/email-service/api/route-emails/email-handler/)。更新 CAS 文档必须改变正文 nonce，不能仅修改对象 metadata 后假定 ETag 已变化。
