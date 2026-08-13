# 仓库结构

```text
cmd/anas/             CLI 入口
internal/             Runner 实现、配置、状态和测试
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

跨 Module 共享的语义应成为 Contract 或 Resource，而不是通过读取另一个 Module 的私有文件实现。详细边界见[架构设计](/architecture/)。
