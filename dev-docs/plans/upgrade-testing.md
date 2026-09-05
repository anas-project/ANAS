---
doc_type: plan
status: implementing
created: 2026-09-03
updated: 2026-09-05
---

# 版本升级 E2E 测试实施计划

验收依据是[版本升级 E2E 测试要求](../requirements/upgrade-testing.md)。当前已建立统一 catalog、精确
基线校验和 Core 真实旧版升级 E2E；服务器 Module runner 与数据探针已经落地，尚需在隔离测试服务器
逐 suite 执行并补齐 Web 首次发布后的第一条历史升级用例。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：统一 catalog 与版本漂移门禁 | R-001—R-006、R-023—R-025 | 已完成 |
| M1：Core 历史 workspace 升级 E2E | R-007—R-010 | 已完成 |
| M2：Web 首发基线与后续迁移 E2E | R-011—R-013 | 实施中；当前无历史 Web 发布，首发浏览器基线已登记 |
| M3：全部已发布 Module 的服务器升级往返 | R-014—R-019、R-026—R-030 | 实施中；runner/seed/verify/readiness/namespace/代理门禁已完成，四组真实运行待执行 |
| M4：发布编排、证据与旧测试重新分类 | R-020—R-022 | 实施中；发布前静态门禁、结构化报告和旧测试分类已完成，真实报告待采集 |

## 2. 已落地机制

- [x] `test-env/upgrades/catalog.yml` 登记 Core、Web 和 `.github/modules.json` 的全部 Module。
- [x] `cmd/check-upgrade-tests` 严格解析 catalog，校验文件、ID、Module 完整性和 manifest 当前版本。
- [x] `--base-ref --scope modules` 精确比较历史 manifest；新 Module 只允许当前版本的
      `no_prior_release`，既有 Module 变化必须匹配旧/新 `version-rN`。
- [x] `--base-ref --scope core,web` 解析 Git commit 身份；已有产品必须从同一 commit 建 transition，
      历史树不存在 Web 时才允许首次发布 baseline。
- [x] `scripts/ci/core-upgrade-e2e.sh` 从 tag 构建旧 CLI，创建旧 workspace/deployment，再由当前 CLI
      更新同一 workspace；已覆盖 `v0.1.0` 和 `v0.1.1`。
- [x] `server-module-upgrade-e2e.sh` 统一旧版启动、新版升级、兼容回退和再次升级的生命周期。
- [x] 四组 Module suite 使用独立的跨版本 fixture；校验器要求 suite 模块均被 config 显式选择，镜像发布
      门禁还会分别用精确旧版与当前版 CLI 执行 `init`。
- [x] catalog 静态拒绝 Module 升级 fixture 启用构建期加速或顶层 `env`，确保旧端可以直接复用
      registry 中的不可变发布镜像，而不会被旧 CLI 强制重建漂移后的 Dockerfile。
- [x] Module seed/verify 在旧容器持久目录写入唯一标记；应用与 Authentik 组复用既有深度探针。
- [x] 旧端写种子前与每个往返验证阶段统一检查容器 readiness；长期服务必须 running 且健康，只有
      成功退出且 restart policy 为 `no` 的 `*_init` 一次性容器可豁免。默认 1200 秒窗口覆盖当前
      最大的 Nextcloud 900 秒 start period 与 retry 窗口；真实 `modules-base12` 发现并修复了原 90 秒
      窗口恰好早于 SambaFS 第一次有效 healthcheck 的问题。
- [x] Module runner 要求进程与隔离 Docker daemon 位于同一个显式测试网络 namespace，避免在生产
      namespace 探测网卡或创建 host-LAN 资源。
- [x] Module runner 清理时先用当前 CLI 删除本次 workspace 的 Btrfs 快照，再删除普通目录；路径门禁
      和单元测试阻止清理触及升级测试范围之外的位置。
- [x] 精确历史 `samba_fs 4.23.6-r5` 的已知 macvlan host 隔离由测试环境兼容 helper 临时处理：
      预置成员 A 记录并在 netns 内启用 proxy ARP；当前版本激活前与最终清理都会恢复原网络状态，
      当前镜像仍须在没有兼容垫片时通过，旧发布产物不被重建或改写。
- [x] 精确历史 `authentik 2026.5.6-r8` 的冷库 migration 可能超过旧镜像自带的 270 秒健康
      窗口；runner 只对结构化 `start_failed` 启用最多三次的旧端启动尝试，复用同一旧镜像和
      持久数据库继续 migration，并为每次失败和兼容判定保留脱敏证据。其他错误、版本或 suite
      不得使用该重试。
- [x] 每组 config 有与 catalog 严格一致的 `.targets` inventory；runner 以旧发布 Module 树为基底，
      仅覆盖本 suite 负责的当前 Module。真实 `modules-base10` 发现此前会把未分配的 Samba DC r7
      顺带升级到 data-breaking r11；隔离根机制已阻止这类跨 suite 升级和错误归因。
- [x] 当前目标 Module 构建可用格式受限的 `ANAS_UPGRADE_CURRENT_BUILD_GHCR_REGISTRY` 覆盖
      `GHCR_REGISTRY`；它只注入新 CLI 的 targets-only build 子进程，不写入 fixture、旧端或 deployment，
      并在报告目录留下非敏感审计记录。真实 `modules-authentik19` 发现 GHCR blob CDN TLS 超时后补齐。
- [x] 真实 `modules-authentik20` 发现 sealed deployment 为 root-only，而当前 Authentik worker 以 UID
      1000 运行，直接 bind mount 令生成 blueprint 不可遍历。init 服务现只将非 Secret blueprint
      模板复制到私有 data tree 并移交给运行 UID，server/worker 只读挂载该副本；deployment 其他权限
      不放宽。
- [x] 真实 `modules-authentik22` 暴露根路径 `403`，最初被误归因为匿名 tenant 策略；run23 的
      discovery 非 JSON 与 fresh run24 诊断确认响应实际来自 Docker client 注入到 Traefik 的
      Ubuntu-only 构建代理。内部 HTTPS probe 现强制直连且只接受根路径 `200/302`；runner 在创建
      容器前还要求 Docker client `noProxy` 覆盖 localhost、loopback 与全部 RFC1918 CIDR，既保留
      构建代理，也阻止旧/新运行容器把私有 backend 请求送往出站代理。
- [x] 成功和失败都会生成权限 `0600` 的 JSON、JUnit、Markdown 脱敏报告，记录 source/deployment
      身份、逐阶段断言、环境和清理结果；普通 CI 覆盖报告格式。
- [x] 普通 CI 运行 catalog 校验和两条 Core E2E；ANAS release 与 Module artifact workflow 在构建发布
      产物前执行基线校验。

## 3. 待完成

- [ ] 在隔离测试服务器从 `image-release/46-2` 构建旧二进制/Module 树，依次执行 `modules-base`、
      `modules-app`、`modules-authentik`、`modules-ddns`，修复真实升级发现的问题。
- [ ] 执行四组服务器 suite 并收集已经由 runner 生成的 JSON、JUnit、Markdown 脱敏报告，将实际
      from/to、deployment、数据与清理结果登记到本计划。
- [ ] Web 首次正式发布后，保留旧发布二进制、console store 与浏览器 fixture，新增旧 Web → 当前 Web
      的 owner 登录、静态资源、API 和浏览器存储迁移用例；届时删除首次发布豁免。
- [x] `test-upgrade.sh`/`test-upgrade-render.sh` 已在脚本输出和 `test-env/README.md` 中明确标记为
      lock/render compatibility，避免它们被误报为真实升级证据。
- [ ] 数据库与目录服务从文件标记扩展到业务对象：PostgreSQL/MariaDB 行、Samba 用户/组、Nextcloud
      WebDAV 文件，并验证升级与往返后对象身份不变。

## 4. 当前版本矩阵

- Core：`v0.1.0 → worktree`、`v0.1.1 → worktree`。
- Web：worktree 为首个包含完整嵌入式 Web UI 的发布候选，当前执行首次发布浏览器基线。
- 已发布 Module：以 `image-release/46-2` 为旧端，catalog 为每个发生变化的 Module 登记精确 transition。
- 新 Module：Casdoor、Forgejo、Incus、VersityGW、Vikunja 在该镜像基线不存在，登记
  `no_prior_release`；任一进入成功镜像发布后再次变化，校验器会强制要求 transition。

## 5. 验证命令

```bash
go test ./internal/upgradetest ./cmd/check-upgrade-tests
go run ./cmd/check-upgrade-tests
go run ./cmd/check-upgrade-tests --base-ref v0.1.1 --scope core,web
go run ./cmd/check-upgrade-tests --base-ref image-release/46-2 --scope modules
bash scripts/ci/module-upgrade-fixture-compatibility.sh image-release/46-2
bash test-env/scripts/test-upgrade-report.sh
bash test-env/scripts/test-upgrade-service-readiness.sh
bash test-env/scripts/test-upgrade-workspace-cleanup.sh
bash test-env/scripts/test-upgrade-old-compat.sh
bash test-env/scripts/test-upgrade-suite-targets.sh
bash test-env/scripts/test-authentik-upgrade-probes.sh
bash test-env/scripts/test-upgrade-proxy-boundary.sh
bash scripts/ci/core-upgrade-e2e.sh v0.1.0
bash scripts/ci/core-upgrade-e2e.sh v0.1.1
bash -n scripts/ci/core-upgrade-e2e.sh test-env/scripts/server-*upgrade*.sh
npm run docs:check-requirements
npm run docs:check-requirement-status
npm run docs:check-plan-status
```

服务器 runner 的八个显式参数依次为旧/新 `anas`、旧/新 Module 根、suite config、seed、verify 和
一次性 workspace；环境还必须给出 `ANAS_UPGRADE_SUITE`、`ANAS_UPGRADE_FROM`、
`ANAS_UPGRADE_TO`、`ANAS_UPGRADE_NETNS_PATH`。测试网络不能稳定访问 GHCR 时，可额外设置
`ANAS_UPGRADE_CURRENT_BUILD_GHCR_REGISTRY` 为 registry host；它只影响当前目标构建且会记录到报告。
runner 拒绝默认 Docker socket、非测试 data-root、namespace 不匹配、非升级测试路径和已存在
workspace；Docker client 配置构建代理时，还会拒绝未用 `noProxy` 覆盖 localhost、loopback、
`10.0.0.0/8`、`172.16.0.0/12` 与 `192.168.0.0/16` 的环境。报告保留在
`${workspace}.reports`。

## 6. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-007 | `core-upgrade-e2e.sh v0.1.0`、`v0.1.1` | `core-workspace-compatibility` / macOS arm64、真实旧 CLI → worktree | 2026-09-03 | 通过 |
| R-008 | `core-upgrade-e2e.sh v0.1.0`、`v0.1.1` | 同一旧 workspace、lock 与不可变 deployment | 2026-09-03 | 通过 |
| R-009 | `core-upgrade-e2e.sh v0.1.0`、`v0.1.1` | 配置摘要与旧 deployment 前后读取 | 2026-09-03 | 通过 |
| R-011 | 待新增首个 Web transition runner | 历史 console store 迁移；当前尚无历史完整 Web 产物 | — | 待执行 |
| R-012 | `server-console-m1c-browser-e2e.sh` | `web-first-release-browser` / Linux + Chromium | — | 待执行 |
| R-013 | `server-console-m1c-browser-e2e.sh` | `web-first-release-browser` / Linux + Chromium | — | 待执行 |
| R-014 | `server-module-upgrade-e2e.sh` | `image-release/46-2 → worktree`，四组隔离 Docker suite | — | 待执行 |
| R-016 | `server-upgrade-seed-markers.sh` + verify | 旧端持久标记、升级/往返逐阶段核对 | — | 待执行 |
| R-017 | app/authentik upgrade probes | `modules-authentik20` 补修 root-only blueprint staging；run23/fresh run24 识别代理 403，probe 强制直连并验证 OAuth2 provider、公开 discovery 与精确 issuer | 2026-09-05 | fresh run24 通过 / 完整往返待执行 |
| R-018 | `server-module-upgrade-e2e.sh` | 旧 deployment 回退与新 deployment 再应用 | — | 待执行 |
| R-019 | isolation/netns guard + `test-upgrade-workspace-cleanup.sh` | 专属 Docker socket/data-root、匹配 namespace 与受限 Btrfs-aware 清理；真实 suite 待执行 | 2026-09-05 | 单元通过 / e2e 待执行 |
| R-021 | `server-upgrade-write-report.sh` | JSON/JUnit/Markdown 脱敏格式已验证；真实 suite 证据待采集 | — | 待执行 |
| R-026 | `test-upgrade-service-readiness.sh` + server suite | readiness 状态组合单元测试；真实旧端/往返阶段待执行 | 2026-09-05 | 单元通过 / e2e 待执行 |
| R-027 | `server-upgrade-old-compat.sh` + `test-upgrade-old-compat.sh` | 精确 r5 门禁、proxy ARP/A 记录准备、升级前撤销与失败清理；真实往返待执行 | 2026-09-05 | 单元通过 / e2e 待执行 |
| R-028 | catalog `.targets` 校验 + `server-module-upgrade-e2e.sh` | suite 专属旧树基底/当前目标覆盖；`modules-base10` 发现并复现跨 suite Samba DC 升级后补门禁 | 2026-09-05 | 单元通过 / 修复后 e2e 待执行 |
| R-029 | `server-module-upgrade-e2e.sh` + `test-upgrade-suite-targets.sh` | `modules-authentik19` 发现 GHCR blob CDN TLS 超时；当前目标构建可显式使用受限 registry host，旧端与 fixture 不变 | 2026-09-05 | 静态通过 / e2e 待执行 |
| R-030 | proxy-boundary guard + Authentik probe | `modules-authentik23` 与 fresh run24 复现 Docker client 构建代理污染 Traefik backend；完整 RFC1918 `noProxy` 后 discovery `200`、issuer 精确且深度 probe 通过 | 2026-09-05 | 单元 + fresh e2e 通过 / 完整往返待执行 |

## 7. 当前阻塞

- 当前工作树同时包含尚未完成的 Incus/Forgejo 等改动；它们在最近成功镜像发布中不存在，因此本轮
  只建立首次发布边界，不把未执行的首次发布 smoke test 伪装成升级通过。
- 四组 Module suite 需要 Linux、隔离 Docker daemon、网络和较长镜像构建时间；脚本存在不计通过，
  必须完成服务器运行并回收证据后才能关闭 M3。
- 测试服务器为 `finance.hlong.wang`；四组 Module suite 正在该服务器的隔离
  Docker daemon 与专属 network namespace 中执行。
- 当前尚无包含完整 Web UI 的历史不可变发布产物，无法制造“旧 Web → 新 Web”；M2 先以首发浏览器
  基线守门，第一次 Web 发布后自动转为真实历史升级边界。
