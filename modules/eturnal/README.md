# Eturnal TURN

为实时通信 Module 提供 TURN 中继。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `eturnal` |
| 版本 / revision | `1.12.2-r6` |
| 状态 | `release` |
| 类别 | `communication` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |

## 最简配置

```yaml
modules:
  eturnal: {}
```

## 身份、用户与 Group

协议服务没有人员用户、目录同步、OIDC、SAML 或 Group 管理。Consumer 使用生成的 TURN shared secret。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | 不支持/不适用 |
| Group | 未声明 |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

没有 Web 管理员入口或本地管理员账号。

本 Module 没有声明由 `anas admin local` 管理的人员账号。`eturnal.secret` 是 deployment 管理的
机器凭据：每次激活都会在下游 Module 启动前自动 probe/verify，只有 ANAS authority 且不匹配时
才通过重启 Eturnal 幂等协调。可用以下命令查看无明文库存、预演和执行轮换：

```bash
anas credential list -w /srv/anas
anas credential rotate eturnal.secret --dry-run -w /srv/anas
anas credential rotate eturnal.secret -y -w /srv/anas
```

实际轮换会停止 Eturnal 及受影响的 Nextcloud/NetBird 闭包，验证 candidate 后才一次性提交 Secret
Store 并 promotion；失败时恢复 previous deployment。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `eturnal.domain_prefix` | string | — | `turn` | `static` | `TURN_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `eturnal.port` | int | `1..65535` | `3478` | `static` | `TURN_PORT` | 否 | 否 | 否 | 是 | `container_recreate` | 服务端口 |

### 查询和修改

```bash
anas config list eturnal -w /srv/anas
anas config explain eturnal.domain_prefix
anas config set eturnal.domain_prefix turn -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list eturnal -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

TURN Secret 是机器凭据，不应作为人员密码查询或共享。当前 verify 检查运行容器实际读取的
`eturnal.yml`，尚未执行完整 TURN 鉴权握手；主动轮换因此证明的是运行配置收敛和容器可用性，
不是端到端 TURN 客户端鉴权。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`1.12.2-r6`（reviewed 2026-08-17）
- Timezone / 时区：`container` — The TURN service receives TZ for process and log timestamps.
- Language scope / 语言范围：TURN protocol service
- Selection / 选择方式：`none`
- ANAS global defaults / 全局默认：`default_language=not_applicable`; `default_locale=not_applicable`
- Upstream format / 上游格式：none
- Fallback / 回退：No user-facing language exists.
- Supported languages / 支持语言：not applicable / 不适用

Evidence / 证据：

- [1.12.2 — protocol service without a localized UI](https://github.com/processone/eturnal/tree/1.12.2)
<!-- generated:localization:end -->
