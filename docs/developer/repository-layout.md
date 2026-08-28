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
docs/                 VitePress 文档源文件（面向使用者，会发布）
dev-docs/             需求、实施计划与时点评审（开发过程工件，不发布）
.github/workflows/    CI、镜像和文档发布
```

`dev-docs/` 与 `docs/` 的分界是读者：`docs/` 给部署和使用 ANAS 的人，`dev-docs/` 给提交者。

```text
dev-docs/
  requirements/       目标、范围、硬约束与验收标准（需求矩阵是规范来源）
  plans/              里程碑、迁移顺序与剩余工作
  reviews/            绑定日期或提交基线的评审快照
```

单个 Module 私有的需求与计划放 `modules/<name>/dev-docs/`，判据是「这份文档描述的约束会不会在
该 Module 被移除后失去意义」；只有 ANAS 自身或跨 Module 的才放仓库根 `dev-docs/`。同一主题不得
在两个位置同时存在。目录分工与写作规则见[文档写作标准](/developer/documentation-standard) §1。

每个 Module 通常包含：

```text
modules/<name>/
  module.yml
  hook/
    main.go
  docker-compose.yml
  <build contexts, templates, assets>
  dev-docs/           仅在该 Module 存在时才有意义的需求与计划（可选）
```

跨 Module 共享的语义应成为 Contract 或 Resource，而不是通过读取另一个 Module 的私有文件实现。CLI 与 HTTP 共享业务用例，但分别维护 `anas.dev/cli/v1` 和 `anas.dev/api/v1` 外部契约。详细边界见[架构设计](/architecture/)。
