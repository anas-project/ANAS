# Lego ACME certificates

通过 ACME DNS-01 或内部 CA 生成和续期证书。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `lego` |
| 版本 / revision | `5.3.1-r5` |
| 状态 | `release` |
| 类别 | `certificate` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| — | — | — |

## 最简配置

```yaml
modules:
  lego: {}
```

## 身份、用户与 Group

没有人员用户、目录同步或 IAM 登录。DNS API 凭据是机器 Secret。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | 不支持/不适用 |
| Group | 未声明 |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

没有 Web 管理入口或私有管理员账号。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `lego.dns_provider` | string | — | — | — | `LEGO_DNS_PROVIDER` | 否 | 否 | 否 | 是 | `reconcile` | DNS 厂商 |
| `lego.dns_server` | string | — | `223.5.5.5` | `static` | `LEGO_DNS_SERVER` | 否 | 否 | 否 | 是 | `container_recreate` | DNS 校验服务器 |

### 查询和修改

```bash
anas config list lego -w /srv/anas
anas config explain lego.dns_provider
anas config set lego.dns_provider VALUE -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## DNS 厂商与凭据作用域

DNS 厂商按引擎选择；证书和动态 DNS 可以使用不同厂商。真实域名需要 ACME DNS-01，虚拟域名使用内部 CA 时不要求 `dns_provider`：

```yaml
modules:
  lego:
    config:
      dns_provider: tencentcloud

secrets:
  tencentcloud_secret_id: replace-me
  tencentcloud_secret_key: replace-me
```

支持厂商和凭据键以 `hook/dns_registry_gen.go` 与 `internal/dns/providers.yml` 为准。共享厂商凭据可以供多个引擎使用；`lego_<vendor>_*` 形式只授予 lego。`anas plan` 会报告各引擎选择及凭据能否共享。lego v5 不再支持旧 `dnspod` provider，应使用 `tencentcloud`；旧 DNSPod token 不能转换为腾讯云 API 密钥。

Runner 在 lego 私有作用域中保存带前缀的凭据；仅证书工作进程在执行时把它翻译为上游变量。其他 Module 不会因依赖关系自动获得 DNS API Secret。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list lego -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

不要用环境变量手工复制 DNS Secret；应使用结构化 `secrets` 配置和 Module 作用域。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`5.3.1-r5`（reviewed 2026-08-13）
- Timezone / 时区：`container` — The certificate worker receives TZ for process and log timestamps.
- Language scope / 语言范围：certificate automation CLI
- Selection / 选择方式：`none`
- ANAS global defaults / 全局默认：`default_language=not_applicable`; `default_locale=not_applicable`
- Upstream format / 上游格式：none
- Fallback / 回退：No user-facing language exists.
- Supported languages / 支持语言：not applicable / 不适用

Evidence / 证据：

- [v5.3.1 — CLI without localized UI resources](https://github.com/go-acme/lego/tree/v5.3.1)
<!-- generated:localization:end -->
