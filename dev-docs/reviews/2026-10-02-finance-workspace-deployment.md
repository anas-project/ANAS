# finance 保留工作区部署

用户要求在 `whl@finance.hlong.wang` 保留一套可直接使用的部署，应用根域名为 `finance.hlong.wang`。本记录是部署操作与真实结果，不作为 Workspace 临时存储的完整验收报告。

## 运行范围

- 长期 workspace：`/home/whl/anas-finance`。
- 长期 CLI 与审核源码：`/home/whl/anas-finance-runtime`。
- 使用主机日常 Docker（ID `4d0f29b6-a8e7-416f-bead-faac27d8a308`），独立前缀 `anas_finance_`。
- 八 Module：lego、traefik、samba_dc、postgres、casdoor、eturnal、nextcloud、collabora。IAM 按用户要求切为 Casdoor；旧 LLNG 数据库保留。关闭 Talk、Memories 和 Adminer。
- Traefik 发布10000。最初的443部署已通过原生 `config set --defer`、`plan`、`apply` 改为10000；7000被既有frps占用，9000被既有Traefik占用，均未接管。现有Nginx的80与原有14个容器保留。
- 业务使用新工作区；没有迁入测试数据、测试租约或历史运行状态。
- 容器使用原生 `restart: unless-stopped`，日常 Docker 已实测为 enabled/active；未重启服务器验证。

当前入口为 `https://nc.finance.hlong.wang:10000`、`https://auth.finance.hlong.wang:10000` 和 `https://collabora.finance.hlong.wang:10000`。裸 `finance.hlong.wang` 只作为命名根，当前没有指定应用路由。

## 可信输入与已有检查

审核源码摘要 `sha256:25d4626a86fdff648f899b780c262405f1302d2640f0133b37525e93091a7b2a`，共1963个文件。CLI 摘要 `sha256:2c7e964bad5e6ecdd8a570b6efe6ab8f6db1f99de4384c305cc143a9bf537099`。八个 Hook 复用本轮隔离工具容器已编译并核验的静态 Linux amd64 二进制。复制工具29项本地反例通过，真实复制完整成功。

固定13个 ANAS 镜像已从测试引擎逐个复制到主机日常 Docker。原工具因 `frpc` 自身重启的 StartedAt 变化在首次复制前停止。窄修仅允许既有容器自身运行状态变化，仍核验完整容器ID、名称与镜像。后续连接中断导致回执保留失败状态；重新只读核验13个实际镜像ID全部一致、原14容器身份与镜像不变，并另存 `finance-image-post-interruption-check-20261002.json`，不改写旧回执。

新workspace的原生init、lock、plan、首次apply及10000端口apply均实际退出0；14个本工作区容器中，所有声明健康检查的服务均healthy，初始化容器正常退出0，未观察到OOM或重启。10000更新的独立回执明确核验原14个容器定义的ID、名称与镜像保持。主机内三个入口均HTTP200；原生status及temp status实际退出0。源码、Hook与镜像的操作摘要见 [progress.json](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-02-finance-deployment/progress.json)。

## 证书与可达性（初始内部 CA 部署）

当前先使用内置 CA（显式 `global.virtual_domain: true`），证书包含 `finance.hlong.wang` 与 `*.finance.hlong.wang`。此配置不表示公网受信任证书已签发。现有 DNSPod 只有旧 API Token，固定 Lego5 不再支持；公网 ACME 自动签发及续期等待用户提供含现代 `TENCENTCLOUD_SECRET_ID`、`TENCENTCLOUD_SECRET_KEY` 的受保护文件路径。不得将密钥写入聊天、日志或本记录。

主机入口为 `192.168.0.222/enp3s0`，公网根域名与子域名在主机实测解析到 `221.212.214.94`。初始外网443测试实际失败（TLS提前关闭），保留该失败。用户最终指定10000后，本机绕过代理DNS虚拟地址，直接连接公网IP `221.212.214.94:10000`，分别使用nc/auth/collabora的TLS SNI与Host，三个入口均HTTP200，Nextcloud installed为true，Collabora返回有效discovery；使用本部署公开CA实际验证了证书。另一次按域名访问的278个discovery action地址均使用10000。证据为 [公网IP回执](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-02-finance-deployment/public-ip-port-10000.json)、[域名访问回执](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-02-finance-deployment/public-port-10000.json)；后者的socket peer是本机代理虚拟地址，不能单独当作公网直连证据。

Samba的IPv4监听使用 `lo 192.168.0.222/24` 与 `bind interfaces only=Yes`。BIND的IPv6监听由现有模板定义为any，查询ACL限loopback及私有IPv4。未执行服务器重启、完整IPv6访问或公网CA验收。

浏览器需信任 [本部署内部CA](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-02-finance-deployment/finance-internal-ca.crt)。这不表示系统已全局导入该CA；自动化编辑工具显式允许内部证书并在回执中记录 `tls_verification_enabled=false`，公网HTTP检查使用公开CA执行了证书验证。

## 真实编辑与初始化故障

首次独立frame诊断 `finance-frame-d1d7d965494e` 实际失败：token POST返回500，Collabora HTML POST零次，编辑frame为零。Nextcloud异常链是Richdocuments `WOPI/Parser.php:42` 对false调用 `xpath()`。正常启动日志进一步确认Nextcloud在Collabora路由建立前执行 `richdocuments:activate-config`，获取discovery返回404；该命令失败没有阻止Nextcloud健康检查通过。固定Richdocuments 11.1.0后台刷新间隔为一小时。以上是长期工作区的真实观察，不据此改写原Source25正式浏览器失败的首因。

全部Module就绪后，以www-data执行官方 `occ richdocuments:activate-config` 实际退出0。10000更新后再次执行，实际wopi_url和public_wopi_url均为10000；未修改固定应用源码、关闭WOPI白名单或跳过服务器证书检查。

独立真实编辑 `finance-edit-b7bcba84f1f4` 实际退出0：打开一个新建ODT，以编辑器原生键盘输入并保存；通过WebDAV解包真实ODT，核验初始内容和新增文本；正常关闭后重开，用原生CtrlA/CtrlC与剪贴板再次确认新增文本，并重新检查ODT。两次token与两次Collabora HTML POST均HTTP200；清理前有恰好一个编辑frame。测试文档已移除，WebDAV、context、browser均正常关闭，工具容器已移除。原始回执逐字节SHA256核验后取回：

- [编辑结果](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-02-finance-deployment/office-edit-smoke-b7bcba84f1f4.json)，SHA256 `2e9bfb37c8650bfbe566b280d7bf95be5a481ac06e61cbc931f3c9a6dee1b75c`。
- [清理前观察](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-02-finance-deployment/office-edit-smoke-b7bcba84f1f4.json.observation.json)，SHA256 `69c81811264996e0b044402dce9e02fdc15eac383f20f27e5ec0d71e8838988c`。
- [执行及容器清理](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-02-finance-deployment/browser-edit-execution.json)，SHA256 `ac0f7133663ee0c85d3cb738a5d24e32125427845c83664110f231024e7308db`。

该验证使用固定Playwright工具容器、512MiB tmpfs、1GiB内存上限，未挂载Docker socket，未执行停启、重建或路径切换，明确 `counts_as_e2e=false`。它证明长期部署的真实编辑可用，不替代TEMP-T-021的三个生命周期或完整40条验收。

## Casdoor 与 Office 启动修复后的实际结果

北京时间10月3日，本次原生 lock、plan、apply 实际退出0，Casdoor r9 与 Nextcloud r10 已投入运行。15个本工作区容器中，全部声明健康检查的服务 healthy，初始化容器退出0，无 OOM；原有14个容器定义的 ID、名称和镜像保持。部署源码为原 Source25 加24项审核公开文件，覆盖清单摘要 `c2e32d31ed00d81f6e12939c4c5945b810aa00bc4c377cd3a0e383daea61be71`，没有将旧源码登记为完整 Source44。

Casdoor r8 的实际首因是降为 UID1000 后无法读取 root:root、0400 的直接挂载配置。r9 保持历史配置0400及只读绑定，在入口复制到私有的1000:1000、0600运行配置后降权；真实只读核验确认 UID1000 可读取且字节与冻结源配置相同。Nextcloud r10 的工作进程等待真实 Collabora discovery，再调用官方 `occ richdocuments:activate-config`；真实启动已自动生成 Office 就绪标记，测试没有补执行激活命令。证据见[运行核验](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-02-finance-deployment/startup-verify-v2-observation.json)。两镜像在 finance 基于已验证固定版本镜像叠加审核脚本构建，未宣称重跑完整上游 Dockerfile 构建。

Casdoor 恢复管理员已通过真实登录及账户核验；实际 OIDC issuer 为 `https://auth.finance.hlong.wang:10000`。独立真实目录账号经 Casdoor SSO 进入 Nextcloud，并核验对应身份，打开、编辑、保存、关闭和再打开均通过；原生剪贴板及 WebDAV ODT 内容均匹配。两次 token 及两次 Collabora HTML POST 均200，编辑 frame 恰好一个，测试文档与浏览器工具均已清理。[编辑结果](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-02-finance-deployment/casdoor-756159227865-office-edit-smoke-756159227865.json)、[执行与清理](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-02-finance-deployment/casdoor-756159227865-execution.json)明确 `counts_as_e2e=false`、零生命周期动作。此前首次 WebDAV 创建超时的失败及清理记录原样保留，不据后续成功推断超时首因。

公网 `221.212.214.94:10000` 的 nc/auth/collabora 三入口再次实际200，使用公开内部CA验证TLS，并核验新的Casdoor issuer，见[公网复核](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-02-finance-deployment/public-casdoor-r9-office-r10-10000.json)。自动化浏览器显式允许内部证书，其记录 `tls_verification_enabled=false`；这不等于公网受信任证书已签发。

## 日常管理

在 finance 执行，均明确指定工作区：

```sh
sudo /home/whl/anas-finance-runtime/anas status -w /home/whl/anas-finance
sudo /home/whl/anas-finance-runtime/anas temp status -w /home/whl/anas-finance
sudo /home/whl/anas-finance-runtime/anas stop -w /home/whl/anas-finance
sudo /home/whl/anas-finance-runtime/anas start -w /home/whl/anas-finance
```

恢复管理员凭据通过 `anas admin local credential nextcloud break_glass -w /home/whl/anas-finance` 在受保护终端查询；用户名与密码不放公共报告。直接登录入口为 `/login?direct=1`。正常用户使用 Casdoor/OIDC。后续 plan/apply 明确指定上述当前源码根。

## 与原验收的关系

本轮隔离测试Source25的Go/vet、前端、文档与catalog、存储核心及扩展用例已通过。其最终正式编辑重跑中，正常停启、重建、A→B 三个真实动作均通过；随后切换后的重开编辑 frame 为0而预期1，整体仍失败，不能登记完整编辑验收。旧候选报告及失败材料保持原字节和身份。

新审核候选 Source44（1973文件，摘要 `sha256:44a64e5b72f26f4289e2dd9fe7cdc7416608e1f0d2d07420055725b8db143c87`）已完成 finance 的完整 Go/vet、前端、十项支持、核心、扩展及正式完整编辑验收。严格最终汇总为 40/40 有效需求、23/23 实机需求通过，见[最终验收记录](../plans/archived/workspace-temp-storage.md)。历史 Source25 失败保留原状态。长期 workspace 不属于测试运行资源清理范围。

完整串行测试结束后，私有运行资源已经释放；常驻工作区于 2026-10-02T21:42:10Z 原生启动成功，[恢复回执](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-03-workspace-temp-storage/retained-finance-start.json)核验 15 个容器就绪、Office 自动激活、原有14个容器定义不变。[恢复后公网检查](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-03-workspace-temp-storage/public-after-restore-10000.json)以内部CA验证真实公网IP的三个10000入口，全部200，Casdoor issuer正确。

下一步：长期部署保留10000入口。公网证书已通过 ln 的 DNS key 签发；外网10000目前TLS提前关闭，主机入口验证正常，需复核公网转发链路。


## 2026-10-03 使用 ln DNS key 签发公网证书

用户明确要求把 ln 上的 DNS key 用到 finance。仅读取 ln 的现有 `anas_lego` 容器，确认使用现代 `TENCENTCLOUD_SECRET_ID`/`TENCENTCLOUD_SECRET_KEY`。通过两端 SSH 加密连接进行内存转发，未落本地或打印值；finance 接收目录 `/home/whl/anas-finance-runtime/dns-ln-20261003` 为 root 私有目录，凭据文件0600。ln 配置和服务保持不变。

finance 使用原生 config import、lock、plan、apply，四步实际退出0；配置切换为 TencentCloud DNS-01 与 `global.virtual_domain=false`，原配置、lock和secret store先保存到受保护恢复目录。运行时凭据与迁移值逐项一致，仅记录布尔结果。[迁移回执](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-03-finance-dns-key/migration.json)、[验证回执](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-03-finance-dns-key/verification.json)不含密钥。

Let’s Encrypt YE1 实际签发 `finance.hlong.wang` 和 `*.finance.hlong.wang`，有效期2026-10-03T07:11:17Z—2027-01-01T07:11:16Z，发布标记为acme。每周六03:00的 `/root/cert.sh` 自动续期任务已存在，crond运行；未强制提前续期以重复签发。15个应用容器就绪、Office自动激活、未见OOM。

Traefik启动时先读到内部证书，证书文件更新后仍缓存旧证书；实际主机TLS确认旧issuer后，仅重启 `anas_finance_traefik` 加载新证书。主机10000的nc/auth/collabora三个入口均HTTP200，使用系统信任根验证Let’s Encrypt证书成功，无内部CA或跳过校验，见[主机TLS回执](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-03-finance-dns-key/host_tls.json)。

本轮从本机直连公网 `221.212.214.94:10000` 的三个TLS握手仍提前关闭，未达到证书验证阶段，见[公网实际失败](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-03-finance-dns-key/public-verification.json)。当前不宣称外网可用或归因于DNS key；主机证书和服务已验证正常。此现象与上次公网访问通过的结果分别保留，需另查公网转发/网络链路。
