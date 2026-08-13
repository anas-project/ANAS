# Module 开发

## 基本职责

Module 是独立的发布和部署单元。它拥有：

- `module.yml` 中的身份、版本、ABI、依赖、能力和配置声明；
- Docker Compose 定义；
- 需要时使用的 Hook；
- 模板、构建上下文和运行资产。

Module 不应依赖仓库检出的相对路径才能启动。冻结后的 deployment 必须携带运行所需的 Compose、环境、文件和 Hook 二进制。

## 依赖和能力

- 硬依赖必须显式声明并参与选择与排序；
- 可替代实现通过能力 Provider 绑定；
- 仅排序关系不能隐式选择另一个 Module；
- 持久资源通过 Resource/Provider operation 管理，而不是共享数据库密码约定。

## 配置与 Secret

只声明 Module 实际消费和导出的配置。不要依赖全量环境注入；生成的 `.env` 应只包含当前 Module、依赖闭包和显式声明的键。敏感值不得写入日志或非必要容器。

开始实现前阅读[Module、Contract 与 Resource 设计](/architecture/module-contract-resource-design)和[环境变量契约](/reference/module-environment-variables)。

## 文档、时区与语言

每个 Module 必须维护 `README.md` 和与当前 `module.yml` 版本一致的 `localization.yml`。支持语言必须从当前固定版本的源码、官方文档或精确镜像中取证，并以规范 BCP 47 记录；浏览器协商、部署默认、固定语言和无 UI 必须明确区分。

字段、fallback 策略和生成命令见 [Module 文档规范](/developer/module-documentation)。每次升级上游版本必须执行 [Module 上游升级 SOP](/developer/module-upgrade-sop)。
