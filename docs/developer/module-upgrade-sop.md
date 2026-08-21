# Module 上游升级 SOP

本 SOP 适用于升级 Module 的上游版本或运行资产。目标是让升级可重复执行、兼容边界明确，并保留必要的恢复路径。
执行时使用 [Module 升级检查表](/developer/module-upgrade-checklist) 逐项记录证据和签署结论。

## 1. 固定范围并检查上游

记录应用版本、镜像 tag/digest、Module `version`/`revision`、附带 Web UI 和其他运行组件版本。Adminer、NetBird Dashboard 等附带组件必须单独复核。阅读源版本到目标版本之间的 release notes、迁移和配置文档，并搜索 `locale`、`language`、`translation`、`i18n`、`timezone`、`TZ`、`date` 和 `format`。确认：

- 数据格式、数据库 schema、配置键、默认值、端口和权限是否变化；
- 镜像入口、健康检查、依赖、API、Hook 和 Compose 行为是否变化；
- 是否有安全修复、弃用项或必须按顺序经过的中间版本；
- 语言、locale、时区和日期格式是否变化。

任何运行时上游或附带组件变化都必须提升 Module `version` 或 `revision`。上游版本变化时更新 `version` 并把 `revision` 重置为 `1`；仅 ANAS 打包变化时递增 `revision`。上游版本无法规范化为 SemVer 时同步维护 `app_version`。

## 2. 优先使用幂等升级

优先使用上游自带的初始化、迁移或 reconcile，使全新安装、重复 `apply` 和重启都收敛到同一状态。声明式配置或上游入口点能完成的适配，不增加 ANAS 升级脚本。

只有上游不能完成必要转换时，才在入口脚本、Hook 或 lifecycle operation 中增加适配。适配必须：

- 先检查实际状态，不只依赖版本标记；
- 可重复运行，成功后再次运行不产生额外变化；
- 中断后可安全重试，失败时不留下无法识别的半迁移状态；
- 限定输入状态和适用版本，并有对应测试。

不要为推测中的历史版本保留永久分支。若后续版本不再支持适配前的来源版本，应在同一个 Module release 中提高 `upgrade.from` 下界并删除已不可达的脚本。优先在上游 major 版本升级时收窄兼容范围，减少额外断代；但兼容范围仍以实际支持和测试结果为准。

删除脚本前必须满足：`upgrade.from` 已排除所有仍需要该脚本的来源版本，且从最低受支持版本升级、重复执行和中断重试均已验证。

## 3. 声明兼容和数据边界

不兼容旧 Module 版本必须写入 `module.yml`，不能只写在评审说明中：

```yaml
upgrade:
  from: ">=34.0.0"
  data_breaking: ["35.0.0"]
```

- `upgrade.from`：允许升级到当前 release 的来源版本范围。删除旧适配路径时，将下界提高到首个已经具备目标状态的 Module 版本。
- `upgrade.data_breaking`：改写磁盘数据格式的版本点。若 35.0.0 写出的数据不能被更早版本读取，就列出 `35.0.0`；确认没有断代时显式写 `[]`。

两者不能互相替代：`from` 决定能否向前升级，`data_breaking` 决定升级前是否需要数据快照以及能否只回滚制品。断代点必须和造成断代的版本同批提交；历史条目只增不改。提高 `upgrade.from` 下界后，才可删除低于或等于该下界、已无法命中的断代点。详细规则见[快照契约](/reference/contracts/snapshot#给-module-作者的规则)。

## 4. 修改 Module

升级时逐项检查并按需修改：

- `module.yml` 的版本、依赖约束、ABI、配置、`upgrade.from` 和 `data_breaking`；
- Dockerfile、Compose、入口脚本、Hook、模板、健康检查和运行资产；
- 上游配置转换、持久目录、权限、Secret、网络和 Provider/Consumer 契约；
- Module README、技术文档、测试和升级评审记录。

数据库或其他持久状态迁移优先采用上游支持的路径。需要自有适配时，评审记录必须说明为什么上游能力不足、脚本的幂等依据、支持的来源版本和删除条件。

## 5. 复核时区和语言

在目标 tag、版本分支或精确镜像 digest 上检查翻译 manifest、locale 目录、应用 API、`locale -a`、`/usr/share/zoneinfo` 和环境变量。宣传网页只能用于定位功能，不能作为版本证据。确认：

- 是否新增、删除或重命名语言；
- 浏览器、用户偏好和部署默认值的优先级是否变化；
- 语言和 locale 配置键、编码或格式是否变化；
- 镜像是否包含 IANA zoneinfo 和所需 POSIX locale；
- 每个长运行服务是否实际收到 `TZ`。

同步更新 `modules/<name>/localization.yml` 的 `module_version`、`module_revision`、`reviewed_at`、清单、fallback、说明和证据。即使清单未变化，也要更新版本、revision 和复核日期。

差异处理规则：

- 新增语言使用规范 BCP 47，并确认镜像已打包；
- 删除语言时告警并使用声明的 fallback，不阻断部署；
- 上游命名变化只修改转换层并增加测试，ANAS 输入保持 BCP 47；
- `zh-Hans`、`zh-Hant` 等脚本变体分别验证，不跨脚本猜测；
- 浏览器协商按用户偏好、浏览器、部署 fallback 的顺序验证，不强制覆盖；
- ANAS 时区保持 IANA 名称；上游需要其他格式时使用库转换并测试 DST。

## 6. 验证

至少验证：

1. 全新安装；
2. 最低受支持版本和上一版本升级到目标版本；
3. 重复 `apply`、重启和适配脚本重复执行；
4. 失败或中断后的重试；
5. 数据、配置、权限、健康检查及依赖 Module；
6. `upgrade.from` 拒绝不受支持的来源，`data_breaking` 触发预期的快照和回滚保护。

有容器测试条件时，使用真实持久数据执行升级和恢复验证；不能只证明镜像能启动。时区和语言还要在 UI、日志和定时任务中覆盖一个非英文语言、一个不支持语言、一个非 UTC 时区和一个 DST 时区。浏览器协商应用需通过 `Accept-Language` 验证用户偏好高于浏览器、浏览器高于部署 fallback。

运行：

```bash
go run ./cmd/gen-module-docs
go run ./cmd/gen-module-docs --check
go test ./...
npm run docs:build
```

`--check` 验证 Module 清单、版本一致性、BCP 47、README 和汇总页。生成结果必须同时包含中文和英文页面；结束前检查两侧 diff 和导航。

升级评审应简要记录上游变化、升级路径、兼容下界、数据断代点、脚本保留或删除理由、测试结果，以及本地化“清单变化”或“复核无变化”。
