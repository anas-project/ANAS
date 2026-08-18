# 仓库结构

```text
cmd/anas/             CLI 入口
cmd/anasd/            Web API 守护进程入口（M0 开发骨架）
internal/             Go 内部实现、平台适配与测试
internal/application/ CLI 与 HTTP 共享的类型化用例
internal/deployment/  只读部署状态模型与存储
internal/api/httpapi/ HTTP 路由、DTO 与错误映射
internal/runner/      CLI 适配器、迁移期实现与测试
api/openapi.yaml      HTTP API 的 OpenAPI 3.1 契约
modules/              可发现的 Module bundle
test-env/             集成与远端验证环境
docs/                 VitePress 文档源文件
.github/workflows/    CI、镜像和文档发布
```

每个 Module 通常包含：

```text
modules/<name>/
  module.yml
  hook/
    main.go
  docker-compose.yml
  <build contexts, templates, assets>
```

跨 Module 共享的语义应成为 Contract 或 Resource，而不是通过读取另一个 Module 的私有文件实现。CLI 与 HTTP 共享业务用例，但分别维护 `anas.dev/cli/v1` 和 `anas.dev/api/v1` 外部契约。详细边界见[架构设计](/architecture/)。
