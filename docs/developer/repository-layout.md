# 仓库结构

```text
cmd/anas/             CLI 入口
cmd/anasd/            Web API 守护进程入口（M1A 管理通道与本地认证底座）
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

## 共享 Go 包与 Module 镜像

跨 Module 共享的**语义**应成为 Contract；跨 Module 共享的**代码**放在 `internal/` 下的普通 Go 包
里，两个 Module 各自 import 它。两者不冲突：Contract 决定「谁向谁承诺什么」，共享包只是避免同一
份客户端实现被复制两遍。

问题出在镜像构建。`.github/images.json` 给每个镜像声明一个 Docker `context`（通常是
`modules/<name>/<component>`），而 Docker 无法 COPY context 之外的文件——共享包在 `internal/`，
够不到。**通过 go.mod 从网络拉取不是办法**：那会编译已发布的版本而不是工作区里的代码，未 push
的改动无法测试。

解决方式是**命名 build context**：镜像条目声明 `shared_paths`，CI 就把仓库根作为名为 `shared` 的
第二个 context 传给 buildx，同时把这些路径纳入该镜像的重建判断。

```json
{
  "module": "incus",
  "image": "anas-incus-provisioner",
  "context": "modules/incus/provisioner",
  "dockerfile": "modules/incus/provisioner/Dockerfile",
  "platforms": "linux/amd64,linux/arm64",
  "shared_paths": ["go.mod", "go.sum", "internal/computeclient"]
}
```

Dockerfile 相应改为 module 模式：

```dockerfile
COPY --from=shared go.mod go.sum ./
COPY --from=shared internal/computeclient ./internal/computeclient
COPY *.go ./modules/incus/provisioner/
ENV GOFLAGS=-mod=mod GOPROXY=off
RUN go build -o /out/binary ./modules/incus/provisioner
```

`GOPROXY=off` 是有意的：构建一旦试图走网络就当场失败，而不是安静地编译一份已发布的旧代码。

Compose 的 `build:` 走另一条路径（本地 `apply --build`），用 `additional_contexts` 表达同一件事：

```yaml
    build:
      context: ./provisioner
      additional_contexts:
        shared: ${ANAS_SHARED_BUILD_CONTEXT:-../..}
```

三处仍需手工保持一致，加共享包时必须一起改：

1. `shared_paths` 只影响 CI 的重建判断，**不会**自动生成 Dockerfile 的 COPY；
2. Compose 里 `../..` 的默认值绑死了 `modules/<name>/` 这一层嵌套深度；
3. 目前没有校验确认 `shared_paths` 的路径存在、且 Dockerfile 确实 COPY 了它们。写错一个路径的
   后果是「改了共享库却不重建镜像」——一个安静的失败。补这条校验已登记为待开发项。
