---
doc_type: plan
status: implementing
created: 2026-08-21
updated: 2026-09-13
---

# VersityGW S3 兼容 Module 实施计划

验收依据是[VersityGW S3 兼容 Module 集成要求](../requirements/versitygw-module.md)的需求矩阵。
M1、M1.1 Capability 与 M1.2 Resource 扩展已完成；当前里程碑为 M2，需要可用的真实 Docker 主机与 S3
客户端证据。

## 1. 里程碑

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：Module、Hook、安全入口、文档与静态门禁 | R-001—R-013 | 已完成 |
| M1.1：`object_storage/s3` Capability、统一输出与隔离 | R-016—R-023 | 已完成 |
| M1.2：`object_storage` Contract、per-Module bucket/凭据与 lifecycle | R-024—R-032 | 已完成 |
| M2：真实 S3 客户端、恢复与发布验收 | R-014—R-015 | 未开始 |

## 2. M1 检查表

- [x] 新增 `modules/versitygw` manifest、Compose、派生镜像与非 root entrypoint。
- [x] Hook 生成稳定 root Secret，并派生 endpoint、region、容器名和持久目录。
- [x] 单元测试覆盖配置派生、Secret 稳定性和 Compose/入口安全边界。
- [x] 补齐双语 README、技术文档和 `localization.yml`。
- [x] 登记 `.github/modules.json`、`.github/images.json` 与中英文文档索引。
- [x] 更新配置 inventory 精确基线。
- [x] 通过 `go test ./...`。
- [x] 通过 `go run ./cmd/gen-module-docs --check`。
- [x] 通过 `go run ./cmd/gen-contract-docs --check`。
- [x] 通过 `npm run docs:check-requirements`。
- [x] 通过实际 render 产物的 `docker compose config --quiet`。

## 3. M1.1 Capability 检查表

- [x] Runner 注册 `object_storage/s3`，并支持单 interface 的 name-only Consumer 声明。
- [x] `versitygw` 声明 Provider，Hook 发布统一 `ANAS_OBJECT_STORAGE_S3_*` 配置。
- [x] Runner 在 Provider calculate 后投影 per-Consumer binding，并在 Consumer 前校验完整性。
- [x] binding Secret 保持敏感，只进入目标 Consumer 的渲染环境。
- [x] synthetic resolver 测试覆盖自动绑定、依赖顺序、完整投影、隔离和缺失输出失败。
- [x] 同步 Capability 开发标准、架构说明及中英文 Module 文档。

## 4. M1.2 Resource 检查表

- [x] 新增 `contracts/object_storage` 1.0.0、双语文档与 operation schemas。
- [x] Runner 泛化 Resource Secret、Provider 输入、Consumer 输出和持久状态。
- [x] 每个 Resource 生成独立 AK/SK，拒绝重复 bucket/access key，并保留未声明 Module 的空边界。
- [x] VersityGW 启用持久 internal IAM 和仅容器网络可达的 Admin listener。
- [x] Provider 实现幂等 ensure、inspect 和 rotate_credential；bucket owner 冲突 fail closed。
- [x] 移除声明继续使用 retained 安全语义，不隐式执行破坏性 delete。
- [x] 单元测试覆盖隔离、敏感性、状态引用、Provider operation 与 Compose 管理面边界。

## 5. CI 门禁

| 门禁 | 最近全绿提交 |
| --- | --- |
| `go test ./...`（Hook、`object_storage/s3` 解析与 Resource 泛化） | `25433bd`（GitHub CI，2026-08-29），已包含 `e9e15af`。`5306b63` 上 `modules/versitygw/hook` 通过，Capability 与 Resource 用例所在的 `internal/runner` 包因无关用例失败；其后提交均未经 CI |
| `go run ./cmd/gen-module-docs --check`、`go run ./cmd/gen-contract-docs --check` | `5306b63`（GitHub CI，2026-09-05） |
| `npm run docs:check-requirements` | `5306b63`（GitHub CI）；本计划与要求文档在那之后直到本次补写检查表都没有改动；最近本地记录 `6823232` |
| 渲染产物 `docker compose config --quiet` | 不在 CI 中；M1 检查表记为通过，未注明日期与提交 |

## 6. M2 真实验收记录

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-014 | 待新增 `test-env/scripts/server-versitygw-s3-e2e.sh` | amd64 + arm64 | — | 待执行 |
| R-015 | 待新增 `test-env/scripts/server-versitygw-restore-e2e.sh` | 空机恢复 | — | 待执行 |
| R-029 | 待新增 `test-env/scripts/server-versitygw-resource-e2e.sh` | 两个 Consumer、owner 冲突与凭据隔离 | — | 待执行 |

M2 必须记录固定镜像 digest、客户端版本、path-style 配置与对象样本摘要。只完成静态 Compose
解析或未签名 health 请求不能替代 S3 协议与恢复验收。

## 7. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| `versitygw` 的 README、技术文档与 `localization.yml`（中英文），以及中英文 Module 文档索引 | M1 新建 | 已完成 |
| [对象存储 Capability 设计](../../docs/architecture/object-storage-capability-design.md)与英文镜像、[Capability 开发标准](../../docs/developer/capability-development.md)、[Module、Contract 与 Resource 设计](../../docs/architecture/module-contract-resource-design.md) | M1.1 的 `object_storage/s3` 与统一输出，M1.2 的 per-Resource 模型 | 已完成（`e9e15af`） |
| `contracts/object_storage` 的 README 与技术文档（中英文） | M1.2 新建 Contract | 已完成（`e9e15af`） |
| `versitygw` 的 README、技术文档与 `module.yml` | M2 通过后按真实证据更新 `status: developing` 与文档中的成熟度说明 | 未开始 |

## 8. 当前阻塞

- 当前 Docker daemon 未运行，无法构建/启动派生镜像，也没有可复用的真实服务器执行记录；
  因此 Module 保持 `developing`。
- per-Resource user/bucket 的真实 Admin API 与 S3 权限隔离尚未在运行中的 v1.7.0 容器执行；
  静态与单元验证不能替代 M2 证据。
- 上游 POSIX versioning 仍为实验性；首期不启用，也不承诺 object lock/WORM。
