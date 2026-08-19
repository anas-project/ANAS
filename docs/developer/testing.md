# 测试

## Go 测试

```bash
go test ./...
```

修改 Runner、配置解析、状态或 Hook 时，应运行覆盖对应包的单元测试。CLI 契约变更还必须更新契约测试和文档。

## 集成测试

`test-env/` 包含本地和远端验证脚本。只运行与改动相关的脚本，并为需要真实网络、Docker、DNS 或远端主机的测试明确准备隔离环境。

承载正式服务的生产主机禁止作为测试服务器。不得在其上运行 E2E、回归、实验性部署或
任何会创建临时容器、网络、卷的测试脚本；此类测试必须使用独立的非生产环境。

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
workspace 和前缀；清理只能按本次运行的 project/label 执行，禁止全局 prune 或按通用
`anas_` 前缀删除资源。

以下确定性测试不连接真实 daemon：

```bash
./test-env/scripts/test-compose-project-isolation.sh
./test-env/scripts/test-server-docker-isolation.sh
sh ./test-env/scripts/test-container-config.sh
```

它们分别覆盖跨 workspace project 拒绝、测试 daemon 双重校验，以及持久目录必须显式
绑定到 workspace `data`、不得依赖镜像 `VOLUME` 产生匿名卷。

测试报告是时间点记录，不是永久操作指南。稳定结论应回写当前文档；原始日志和带主机信息的报告应保存为受控的 CI artifact、Issue 附件或外部私有记录，不要提交到 `docs/`。

## 文档测试

```bash
npm ci
npm run docs:build
```

生产构建会检查 Markdown 编译和站内链接。任何文档路径迁移都应在同一提交中修复链接。
