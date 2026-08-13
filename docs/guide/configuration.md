# 配置

## 配置文件的职责

`<workspace>/config.yml` 是用户维护的期望状态；`config.lock.yml` 是 ANAS 解析并固化的 Module 版本、能力提供方和宿主机策略。不要手工编辑 `.anas/` 中的运行状态。

配置只支持结构化 YAML。主要区域为：

- `modules`：选择参与部署的 Module；
- `global`：域名、邮箱、时区等共享设置；
- `administration`：引导管理员和 Module 本地管理员默认策略；
- `identity`：目录与 IAM Provider 选择；
- `dynamic_dns`：负责 ANAS 声明记录的 DDNS Module 和 DNS 厂商；
- `rollback`：本地快照后端与保留策略；
- `modules.<name>`：单个 Module 的启用状态、身份协议和 `config` 参数；
- `secrets`：需要显式提供的敏感值；
- `env`：无法用结构化字段表示的原始环境变量。

完整字段清单见[配置结构参考](/reference/configuration)。

## 修改和预览

可以直接编辑 YAML，也可以使用 CLI：

```bash
anas config explain nextcloud.domain_prefix
anas config set global.timezone Asia/Singapore -w /srv/anas
anas config plan -w /srv/anas
anas apply -w /srv/anas
```

`config plan` 用于查看待应用变更。某些会改变服务内部持久状态的配置需要迁移步骤，ANAS 会拒绝普通应用；只有在已经完成对应迁移准备时才可显式使用 `anas apply --allow-risky`。这个标志只解除门禁，不会代替数据库迁移或凭据轮换。

## 时区、语言与区域格式

三个全局字段相互独立：

```yaml
global:
  timezone: Asia/Singapore
  default_language: zh-Hans
  default_locale: zh-SG
```

- `timezone` 使用 IANA 时区名，控制时间解释和夏令时规则；
- `default_language` 使用 BCP 47，表示 UI 文本的部署默认语言；
- `default_locale` 使用 BCP 47，表示日期、数字、货币等区域格式。

未填写时按以下规则解析：

1. `timezone` 从 `TZ` 或系统 zoneinfo 继承；
2. `default_language` 从 `LC_ALL`、`LC_MESSAGES`、`LANG` 继承，macOS 还读取 `AppleLocale`；
3. `default_locale` 优先采用带明确地区的显式 `default_language`，例如 `en-GB`、`pt-BR`、`zh-Hant-TW`；
4. 语言只有 `en`、`pt`、`zh-Hans` 等无地区信息时，`default_locale` 使用宿主 locale；
5. 宿主 locale 也不可用时，通过 CLDR likely-subtag 推断地区，最后才回退 `en-US`。

解析后的时区规范为 IANA 名称，语言与 locale 规范为 BCP 47。为了让多台主机和灾难恢复后的结果完全一致，生产部署仍建议显式填写 `default_locale`。

例如，只填写 `default_language: en-GB` 会得到 `default_locale: en-GB`；填写 `default_language: zh-Hans` 时不会直接假定国家，而是优先保留宿主的 `zh-SG`、`zh-CN` 等 locale。

“随浏览器自动切换”不是把 `default_language` 写成 `auto`。标记为 browser 的应用继续使用已保存的用户偏好和浏览器 `Accept-Language`；ANAS 不写 `force_language` 一类强制项。通常直接修改浏览器首选语言即可，应用内保存的用户语言会优先于浏览器。只有支持部署 fallback 的应用才消费全局默认；其他应用使用自己的 fallback。Collabora 的语言由 WOPI 集成按会话传入，固定英文或没有 UI 的 Module 不会伪造一个无效语言开关。

每个 Module 当前支持的语言、全局值消费状态、选择机制和版本证据见 [Module 时区与语言支持矩阵](/reference/module-localization)。提供显式语言参数的 Module 遇到不支持值时会输出 `module_localization_fallback` 告警并继续运行，使用该 Module 声明的 fallback；继承的全局默认无法匹配时也采用同一 fallback，但不会阻断部署。

Module 继承全局语言不需要重复填写语言字段。例如：

```yaml
global:
  default_language: zh-Hant
  default_locale: zh-SG
modules:
  lam: {}       # LAM_LANGUAGE 未设置，因此继承 DEFAULT_LANGUAGE
  nextcloud: {} # 继承为 default_language/default_locale fallback
```

Runner 先读取显式 `global.default_language`；若省略，则从宿主机 `LC_ALL`、`LC_MESSAGES`、`LANG`（macOS 还包括 `AppleLocale`）解析默认值。该值规范化为 BCP 47 后发布为全局 `DEFAULT_LANGUAGE`。Module 自己的 `modules.<name>.config.language` 若存在则优先，否则 LAM、Nextcloud 等声明消费全局值的 Hook 使用 `DEFAULT_LANGUAGE`。浏览器型或固定语言 Module 即使能在 `.env` 中看到该全局变量，也不会因此自动改变 UI；以支持矩阵的 `Global language` 列为准。

`DEFAULT_LOCALE` 同样是最终解析值：显式 `global.default_locale` 的优先级最高；省略时才执行“带地区的显式语言 → 宿主 locale → CLDR 推断”的链路。Module 自己的 locale 参数仍优先于 `DEFAULT_LOCALE`。

没有新增更多全局“通用格式”字段：字符集统一使用 UTF-8；日期、数字、货币、星期首日和度量衡从 locale 或应用内用户偏好派生；电话默认国家等业务语义仍是 Module 参数（例如 Nextcloud 的 `phone_region`）。自动化需要稳定解析命令输出时，应在该脚本边界设置 `LC_ALL=C`，不能用用户 UI 语言控制机器接口。

## Secret

不要把真实 Secret 写进示例文件或提交到版本库。`config secret list` 只列键名；只有明确的 `config secret get` 操作会输出明文。生成的 Secret 位于受保护的 workspace 运行目录中，并由 ANAS 备份流程处理。
