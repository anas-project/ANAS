# 测试

## Go 测试

```bash
go test ./...
```

修改 Runner、配置解析、状态或 Hook 时，应运行覆盖对应包的单元测试。CLI 契约变更还必须更新契约测试和文档。

## 集成测试

`test-env/` 包含本地和远端验证脚本。只运行与改动相关的脚本，并为需要真实网络、Docker、DNS 或远端主机的测试明确准备隔离环境。

### 需求、用例与 Agent 生成

当前仓库已经用稳定需求 ID、计划里程碑和 e2e 执行记录约束范围，但测试代码和服务器执行入口仍主要
人工组织。目标流程见[文档驱动测试生成与远程执行要求](/requirements/document-driven-test-automation)
及其[实施计划](/plans/document-driven-test-automation)：需求矩阵生成机器可读用例清单和可阅读 catalog，
Agent 再根据需求与用例生成或更新完整测试代码。

Agent 不限于生成测试脚手架，可以生成完整 Go、Shell、Python 和 Playwright 测试。“完整生成”只说明
代码的产出方式，不代表测试已经证明需求；生成代码仍必须声明需求/用例 ID、断言外部可观察结果，
并通过反例或故障注入、真实执行、覆盖门禁和审阅。这样既保留自动生成效率，也避免同一次误解同时进入
用例和断言后产生假绿。

### SSH 自动服务器测试（目标状态）

可以把专用测试服务器登记为本地 SSH target，或由用户为单次运行明确指定准确服务器，之后由一个非交互命令自动完成源包传输、隔离部署、
suite 执行、报告回收和精确清理。target profile 只能保存 SSH config alias、能力和远端工作根；认证走
SSH agent/config，必须校验 host key，Secret 不得进入 profile、命令行或报告。

统一运行器尚未实现。实现后，每次运行必须使用唯一 `run-id` 和独立 workspace、端口、网络、
containerd、Docker socket/data-root；先验证目标登记或单次明确授权和资源 preflight，再部署。SSH 断开后必须可以
按 `run-id` 查询、继续收集或清理，不能把连接中断误报为“测试失败且已清理”。

默认使用独立的非生产测试服务器。用户或操作者为当前运行明确指定准确服务器时，可以在该目标执行
E2E、回归、实验性部署或创建临时容器、网络、卷的测试脚本，即使目标承载正式服务。明确指定不豁免
下述隔离边界，也不授权接管、停止、重建或清理目标上的既有资源。

### Docker 与 Compose 隔离边界

每个可创建或清理容器的测试运行必须同时拥有独立 workspace、`container_prefix`、
`network_prefix` 和端口范围。Runner 使用 `<CONTAINER_PREFIX><module>` 作为 Compose
project name；默认 `anas_` 因而仍得到 `anas_<module>`，不会改变现有生产部署。任何
Compose 启停、`run` 或 `down` 之前，Runner 都读取现有容器的
`com.docker.compose.project.working_dir` 标签：若 project 属于另一 workspace，立即拒绝，
不得尝试接管、重建或清理。

服务器 E2E 还有第二道边界：必须显式使用测试专用 Unix Docker socket，而且该 daemon 的
`DockerRootDir` 也必须包含 ANAS 测试作用域。默认 `/run/docker.sock`、
`/var/run/docker.sock`、仅改名但仍指向生产 `/var/lib/docker` 的 socket 均会被
`server-require-isolated-docker.sh` 拒绝。并发运行必须使用不同 socket、Docker data root、
workspace 和前缀。`server-isolated-docker.sh` 还必须在同一 network namespace 内启动专用
containerd 并让 dockerd 显式连接；仅隔离 dockerd 会使 host-network 容器继承宿主
containerd/shim 的网络 namespace。并发运行因此也必须使用不同的 containerd socket、root、
state 和 systemd unit；清理只能按本次运行的 project/label 执行，禁止全局 prune 或按通用
`anas_` 前缀删除资源。

以下确定性测试不连接真实 daemon：

```bash
./test-env/scripts/test-compose-project-isolation.sh
./test-env/scripts/test-server-docker-isolation.sh
sh ./test-env/scripts/test-container-config.sh
```

它们分别覆盖跨 workspace project 拒绝、测试 daemon 双重校验，以及持久目录必须显式
绑定到 workspace `data`、不得依赖镜像 `VOLUME` 产生匿名卷。

### Vikunja 发布 E2E

Vikunja 的服务器发布门禁使用 `server-vikunja-e2e.sh`（runtime、数据库重启、API v2、附件、
CalDAV、webhook、轮换）和 `server-vikunja-oidc-e2e.sh`（Authentik 授权码与应用组矩阵）。
webhook 接收端固定使用 `server-vikunja-webhook-receiver.py`，在隔离 network namespace 内验证
原始 body 的 HMAC-SHA256；错误签名必须返回 401，接收端不得记录 Secret。

真实 UI 与登出必须另运行 `npm run e2e:vikunja-browser`。浏览器报告必须使用
`sanitized-reporter.mjs` 且保存到 `test-env/reports/`；密码、授权码、JWT、API token 和 webhook
Secret 不得进入 line reporter、截图或 trace。若浏览器运行在专用 Docker daemon 中，只能挂载该
daemon 的 socket，并在 `finally` 恢复被暂停的 IAM 测试容器。

测试报告是时间点记录，不是永久操作指南。稳定结论应回写当前文档；原始日志和带主机信息的报告应保存为受控的 CI artifact、Issue 附件或外部私有记录，不要提交到 `docs/`。

## 文档测试

```bash
npm ci
npm run docs:build
```

生产构建会检查 Markdown 编译和站内链接。任何文档路径迁移都应在同一提交中修复链接。
