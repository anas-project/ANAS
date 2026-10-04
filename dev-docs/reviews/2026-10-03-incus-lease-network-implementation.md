---
doc_type: review
status: current
created: 2026-10-03
updated: 2026-10-03
---

# Incus 租约网络、HTTP 发布与端口绑定实施记录

基线：2026-10-03，`c7891162` 加当前未提交工作树（工作树里另有未提交的 Workspace 临时存储改动，与本轮无关、未触碰）。
范围：[Incus 计划](../plans/incus-module.md) M10a、M10b、M11、M11b、M11c，[宿主通道计划](../plans/host-action-channel.md)
M5，[运行问题记录计划](../plans/runtime-issues.md) M0 与 M1 的 CLI 部分。本轮只写代码与文档并跑本机门禁，**没有**在
真实宿主上运行任何新路径。

## 1. 结论

五个 Incus 里程碑的实现项全部落地，有单元测试覆盖；里程碑状态保持「实施中」，剩余是实机 e2e。没有内置 Module
声明 `publish.http` 或 `publish.ports`，所以 HTTP 发布与端口绑定只在单元层被调用过。

## 2. 实现了什么

| 里程碑 | 内容 | 主要位置 |
| --- | --- | --- |
| M10a | 出站四档、`module_access`、`intra_lease` 随部署冻结；Provider 把档位写进租约 ACL，两个方向默认丢弃；全局 address set `anas-leases`（Provider 每次 `ensure` 改写）与 `anas-traefik`（只由 hostd 写）；网桥改名 `lease`+10 位十六进制并迁移旧网桥；窄 FORWARD 静态规则与开机恢复单元；撤销清空 ACL 并停止实例 | `internal/computenet`、`modules/incus/provisioner/{acl,network,ops}.go`、`internal/incusprovision/lease_network.go` |
| M10b | 控制台只读租约网络视图：API `GET /api/v1/workspaces/{ws}/compute/leases` 与 SVG 页面；`ensure` 交回网段、网关与槽位地址，Core 记入 resource state `lease_network` | `internal/runner/compute_view.go`、`web/src/network/` |
| M11b | `network.ingress`（`none`/`published`）与 `publish.http`；`none` 拒绝发布声明；ACL 入站规则 | `internal/computenet`、`internal/computeingress/policy.go` |
| M11 | 请求改为 `{instance, address, port, label?}`；anasd 内的中介每 3 秒全量重算，写 Traefik 动态目录的 `compute-http/` 子目录并改写 `compute-http.reload`；激活时按授权摘要清理路由；删除启动拦截；manifest 新增 `http_request_owner`，Core 建请求目录并投影 `HTTP_REQUEST_DIR`/`HTTP_POLICY`/`HTTP_BASE_DOMAIN`；共享客户端带实例地址、停止/删除时撤销、janitor 清理、`HTTPPublicationFromLookup` | `internal/runner/compute_http.go`、`internal/computeingress/route.go`、`cmd/anasd/compute_http.go`、`internal/computeclient/http_publication.go` |
| M11c | `network.slots`/`publish.ports`；槽位固定地址（DHCP 范围外，IPv6 有状态 DHCPv6）；`auto` 从批准范围分配并跨 apply 保持；显式端口冲突时 apply 失败并记运行问题；hostd 同步动作 `incus.ports.sync`（nft 表 `inet anas_incus_ports` 与 `anas-port-{tcp,udp}@` 占位单元，先占位后转发）；开机先占位后恢复；anasd 在启动与激活后触发同步，每分钟及容器启动时检查并记运行问题；共享客户端为槽位实例写地址 | `internal/runner/compute_network.go`、`internal/incusprovision/port_bindings.go`、`cmd/anasd/port_bindings.go`、`internal/computeclient/client.go` |
| HOSTACT M5 | 同步动作 `incus.traefik.sync`、`incus.ports.sync`：参数恒为 `{}`，需 configure 批准，经共享队列与审计，anasd 以系统身份触发 | `internal/hostaction`、`internal/jobexecutor/host_action_service.go`、`cmd/anasd/traefik_sync.go` |
| ISSUE M0/M1 | 记录包（按键去重、解决与 30 天保留、敏感值拦截、文件锁原子写、开启/解决各一条日志、读不出即报错）；写入方 apply、anasd、hostd、开机恢复；CLI `anas issues [--json]` | `internal/runtimeissues`、`internal/runner/issues_cli.go` |

## 3. 本轮自行决定、需要操作者知晓的点

1. **撤销停止实例。** ACL 放行已建立连接的回包，清空规则挡不住它们；`INCUS-R-124` 要求结束已建立的连接，所以
   `revoke` 在删证书、清空 ACL 后停止租约内全部运行中的实例（磁盘保留）。这比原先「只撤证书」影响大：移除或关闭
   compute 消费者时，它正在跑的作业会被中断。
2. **中介用 3 秒全量重算代替文件事件。** 计划写的是「文件事件 + 激活触发 + 低频全量」；一次重算只读几个小文件，
   短周期轮询覆盖三种触发且无需 inotify 的平台代码。发布生效延迟最多约 3 秒。
3. **端口同步由 anasd 触发。** CLI apply 不连 hostd（hostd 只接受经 anasd 队列的调用），所以 anasd 监视各工作区
   `active.yml`，激活后几秒内触发 `incus.ports.sync`；anasd 不在时，CLI apply 的端口绑定要等 anasd 启动后才生效。
4. **运行问题保留期暂定 30 天**（与 hostd 调用记录相同）。计划 §2 把它留给单独讨论，这里只是可改的暂定值；控制台
   合并展示同样待定，因此控制台视图（`ISSUE-R-007`）没有实现。
5. **M11、M11c 先于第一个消费者实施。** 计划原写「等第一个声明的消费者」；本轮按「完成全部文档目标」实施，但
   内置 Module 都没有声明 HTTP 发布或端口绑定，真实链路的第一次运行仍要等一个消费者或实验 Module。

## 4. 验证

本机（macOS，Go 1.26）：

- `go build ./...`、`go vet ./...`、`GOOS=linux go vet ./...`：通过。
- `go test ./...`：通过（首轮发现 `internal/computenet` 一个未随 `TraefikPort` 校验更新的测试夹具，已修正后重跑）。
- 新增测试：`internal/runner/compute_http_test.go`（中介校验、冲突、撤销、激活清理、请求目录权限）、
  `internal/incusprovision/port_bindings_test.go`（同步先占位后转发、八类拒绝、跨工作区冲突、开机恢复、只读检查、
  规则集与回读解析、占位单元与 systemctl 参数白名单、Docker 已发布端口、批准与运行问题）、
  `cmd/anasd/{compute_http,port_bindings}_test.go`、`internal/runtimeissues/issues_test.go`、
  `internal/runner/issues_cli_test.go`、`internal/hostaction/sync_action_test.go` 的端口同步用例、
  `internal/computeclient` 的槽位地址与 `HTTPPublicationFromLookup` 用例。
- `go run ./cmd/check-shared-build`、`go run ./cmd/gen-module-docs --check`、`go run ./cmd/gen-contract-docs --check`：通过。
- `web`：`npm run typecheck` 与 `npm test`（22 个文件、105 个用例）通过。
- 文档门禁见计划提交时的 `docs:check-*` 结果。

Linux 专属测试只在 macOS 上 `GOOS=linux go vet` 编译过，未执行。

## 5. 没有做的

- **实机 e2e 全部未运行**：出站四档与默认拒绝入站、撤销中断连接、`anas-traefik` 同步、真实 Traefik 加载
  `compute-http/` 路由与 reload、端口表与占位（含开机顺序、IPv6、UDP）、anasd 检查记录问题、控制台视图。nft 规则沿用
  2026-09-30 探测验证过的写法，但产品代码生成的规则集、`nft -j` 回读解析（按 nft 1.0 的 JSON 形式编写）都没有在真实
  nft 上跑过。
- 运行问题的控制台视图（`ISSUE-R-007`）与保留期定案。
- M8（长驻实例档，需另立需求）与 M14（其余发行版，需真实宿主）不在本轮范围。
