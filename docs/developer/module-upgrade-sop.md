# Module 上游升级 SOP

本 SOP 供升级 `module.yml` 中上游版本的维护者使用，确保时区、语言和区域格式不会在版本更新时静默漂移。

## 1. 固定升级对象

记录应用版本、镜像 tag/digest、Module `version`/`revision` 和所有附带 Web UI 的版本。数据库 Module 中的 Adminer、NetBird 的 Dashboard 等附带组件也必须单独复核，不能只检查主服务。任何运行时上游或附带组件变化都必须提升 Module `version` 或 `revision`，使文档检查门禁生效。

## 2. 检查上游变化

阅读目标版本到新版本之间的 release notes、迁移说明和配置文档，搜索 `locale`、`language`、`translation`、`i18n`、`timezone`、`TZ`、`date` 和 `format`。确认：

- 是否新增、删除或重命名语言；
- 浏览器协商、用户偏好和默认语言的优先级是否改变；
- 语言和 locale 的配置键、编码或格式是否改变；
- 镜像是否仍包含 IANA zoneinfo 和所需 POSIX locale；
- 每个长运行服务是否实际收到 `TZ`，而不只是 Compose 主服务。

## 3. 按固定版本取证

优先读取 tag/版本分支中的翻译 manifest、locale 目录和源码资源。只有二进制或镜像时，在精确 digest 上检查资源目录、`locale -a`、`/usr/share/zoneinfo`、环境变量和应用 API。宣传网页可以帮助定位功能，但最终证据必须落到当前发布版本。

把结果写入 `modules/<name>/localization.yml`：同步 `module_version` 和 `module_revision`，更新 `reviewed_at`、清单、fallback、说明和证据链接。即使清单没有变化，也必须更新版本/revision 和复核日期。

## 4. 处理差异

- 新增语言：使用规范 BCP 47 加入清单，并确认镜像确实打包。
- 删除语言：先检查已有显式配置；删除后输出告警并使用清单 fallback，不能阻断部署。
- 上游代码改名：保持 ANAS 输入为 BCP 47，在转换层更新上游值并加测试。
- 脚本变体变化：分别验证 `zh-Hans`/`zh-Hant` 等，不允许跨脚本猜测。
- 浏览器协商变化：验证 `Accept-Language`、用户保存偏好、部署 fallback 三层，不写强制语言配置。
- 时区格式变化：ANAS 仍使用 IANA 名称；需要 POSIX 或应用专用格式时通过库转换并覆盖 DST 测试。

## 5. 生成与验证

```bash
go run ./cmd/gen-module-docs
go run ./cmd/gen-module-docs --check
go test ./...
npm run docs:build
```

有容器测试条件时，至少用一个非英文语言、一个不支持语言、一个非 UTC 时区和一个 DST 时区验证 UI/日志/定时任务。浏览器协商应用还应验证用户偏好优先于浏览器、浏览器优先于部署 fallback。

`--check` 会验证每个 Module 都有清单、版本与 `module.yml` 一致、BCP 47 拼写规范、README 和汇总页没有过期。检查通过后，在升级评审中明确写出“清单变化”或“复核无变化”，不能只写镜像已启动。

生成结果必须同时包含中文和英文汇总页。维护者或 AI Agent 在结束升级任务前要检查两侧 diff 和导航；任一语言缺失都视为升级未完成。
