---
doc_type: plan
status: done
created: 2026-10-07
updated: 2026-10-07
---

# 内部证书持久保存实施计划

验收依据：[内部证书持久保存要求](../../requirements/internal-certificate.md)。
实现与变量说明见[技术文档](../../../docs/technical.md)。

## 里程碑与归属

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：固定文件名及环境导出 | R-002 | 已完成 |
| M1：候选镜像、实际发布与回退 | R-001、R-003—R-005 | 已完成 |

## 实施检查表

- [x] 固定保存内部材料，服务证书与内部证书分开发布。
- [x] 导出文件名并同步中英文技术文档。
- [x] 真实 OpenSSL Hook 回归通过。
- [x] 编写隔离 Docker 容器验证脚本，使用独立证书卷与临时替代 CA。
- [x] 构建缓存运行基线候选，15 项容器检查全部通过。
- [x] 通过现有公开镜像源参数完成完整仓库 Dockerfile 构建，15 项容器检查全部通过。
- [x] 核验本地与远端源码摘要，记录最终基线并确认临时资源清理。

## e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-001 | `server-lego-certificate-e2e.py` | finance Linux amd64 隔离 Docker，完整 Dockerfile 候选 | 2026-10-07 | 已通过：真实重启保留内部材料；过期重签 |
| R-003 | 同上 | 同上 | 2026-10-07 | 已通过：首次缺证书、过期 ACME、私钥缺失/不匹配回退及 TLS；域名变更由 OpenSSL 单元回归覆盖 |
| R-004 | 同上 | 同上；替代 CA 模拟 ACME 输出 | 2026-10-07 | 已通过：实际 cert.sh 发布替代 CA 证书，保留内部材料，消费者 TLS 通过；不代表真实公网 DNS-01 |
| R-005 | 同上 | 同上 | 2026-10-07 | 已通过：有效 30 天替代 CA 证书在启动和定时检查后保持不变 |

## 验证与证据

本地 `go test ./modules/lego/hook`、`go run ./cmd/gen-module-docs --check`、
Shell 语法与 Python 编译检查通过。候选以本轮工作树源码摘要及构建 image ID 为基线，
不使用仓库 HEAD 代替已有未提交变更的源码基线。原始日志和执行 JSON 保存在测试服务器的
独立 evidence 目录，后续在本计划补充路径和摘要。

容器脚本会主动重新加载 OpenSSL TLS 服务，以验证读相同文件路径的消费者。
现有 Traefik、Samba 等服务自动重新加载，以及真实公网 DNS-01 签发尚未验证。

## 构建历史与当前执行

用户于 2026-10-07 明确授权将 Lego 源码和测试脚本上传到 `whl@finance.hlong.wang`。
上传包仅包含四个 Lego 构建文件及两个测试脚本，不含证书私钥或凭据。
远端复核包 SHA-256 为 `6ef4becc8d68a1a2632d7bb0779cbf21379c88f7ae17e01feecce7347ddffd2a`；
随后更新测试脚本补充真实重启、内部证书过期和内部 CA 私钥不匹配的验证。

- 专用测试 socket：`/run/anas-casdoor-test-20261003.sock`。
- Docker data-root：`/home/whl/anas-casdoor-test-20261003/docker`，Docker 29.7.2。
- 本轮独立源码/证据根：`/home/whl/anas-lego-test-20261007-6ef4becc`。
- 首次完整 Dockerfile 构建失败：Docker Hub registry TLS handshake timeout，原始日志及失败 JSON 保存在 `evidence/`。
- 当前候选显式使用已有 `ghcr.io/anas-project/anas-lego:5.3.1-r5` 运行镜像，覆盖当前三个证书脚本后构建；基线 image ID 为 `sha256:2ee4edb014b645c6b4a239338bd45fa4cc879971f4521233e409f8ba7d65d7ab`。
- 当前构建和容器记录保存在 `evidence-cached-runtime/`，候选构建成功，容器内 Lego 5.3.1 已核对，15 项检查全部通过；临时容器和证书卷清理通过。

## 已通过的缓存候选基线

- 候选标签：`anas-lego-e2e-005bdc304935:candidate`。
- 候选 image ID：`sha256:b3ef3b620500186e6cf997cfb5b418cd37faf50923b5d53708e8cb16cdb60103`，平台 `linux/amd64`。
- 测试脚本 SHA-256：`7c4fed7aa6b81c75f407be63d39163723f67f0e35d25a714dc36cf8bae5085d1`。
- 执行 JSON SHA-256：`32e66e466b369cd5e7537b954b2af9622f1c933ac4bef3b71fd6bf4387a6c4ef`。
- Docker 日志 SHA-256：`5e80dae50c2cb48c556158cc47f8a40629acb883625ee2b01f2dba1e3defa8ce`。
- 容器日志 SHA-256：`0add5a9329c49afeef3e02726a5dfa64a802b916aebd5733948edb963636bcfe`。
- 本地辅助副本：`/private/tmp/anas-lego-certificate-evidence-20261007`；此临时副本不作为共享持久证据，服务器上的本轮独立证据目录保留。

| 源码 | SHA-256 |
| --- | --- |
| `ca.sh` | `4e5d54387a762a5eae880ceb2c0340f3cae253d6cf0b8f60ce64276921c2d1a2` |
| `run.sh` | `4a132a52c5e09f24133e4877d4dff8107042a07c219e4be5d1a56d0c6f2e764b` |
| `cert.sh` | `84425bdd1fdab5a8e8979f35f7b2f0464325c39b4aa413cc286493414936ff1d` |
| `Dockerfile` | `78eb0cbe23305dea85da0bf6f911c4dcab46cccca376d2e55e3efa47313961fd` |

`R-002` 在本地 `go test ./modules/lego/hook` 的环境导出契约测试通过；Hook 代码基线
SHA-256 为 `c74b293e01b931fae8306a0559a836d2a1e26ed05e82cf6f9a5d3e2bc697390e`。

## 最终完整构建基线与验收结论

2026-10-07 使用完整仓库 `modules/lego/lego/Dockerfile`，通过仓库已有构建参数
`DOCKER_HUB_REGISTRY=m.daocloud.io/docker.io`、`CHINESE_BUILD_SPEEDUP=true`、
`APK_MIRROR_URL=https://mirrors.aliyun.com`，在隔离 namespace 使用 `DOCKER_BUILD_NETWORK=host`
完成构建。上游镜像解析摘要为 `sha256:f4fd80df0ef94d2f536cc2e7fb5bdbd090fb0aa81b3595226b9fe814bb9a2bfe`。
该运行基线已重新执行全部 15 项容器检查，结果全部通过，临时容器和证书卷清理通过。

- 最终候选标签：`anas-lego-e2e-19bc9379485c:candidate`。
- 最终 image ID：`sha256:e7a52906f87d8b7eb956cb781ecebfb12aa8cbaa5663f87b527f0c7505bd353d`，平台 `linux/amd64`，Lego `5.3.1`。
- 最终测试脚本 SHA-256：`42461749bcc59e7e0e27695357fefd5bf2eed19a6b3352539ca7710bf236d19c`。
- 最终执行 JSON SHA-256：`3d5554a75e6230120c26e8acb3ea69fce417f33aa27019494fb8cff968efbb1e`。
- 最终 Docker 日志 SHA-256：`0ca662bbc2340448912c3ce1a261ea9b0d187db9d058ec8fa598737024b5f2fd`。
- 最终容器日志 SHA-256：`0add5a9329c49afeef3e02726a5dfa64a802b916aebd5733948edb963636bcfe`。
- 共享证据位置：`whl@finance.hlong.wang:/home/whl/anas-lego-test-20261007-6ef4becc/evidence-full-dockerfile/`。
- 本地辅助副本：`/private/tmp/anas-lego-certificate-evidence-20261007/full-dockerfile/`。

最终运行源码与上表源码摘要完全一致，本地工作树再次逐文件核验通过。
`R-001`—`R-005` 全部适用判据已通过；本地 Hook 环境导出测试、Go vet、生成 Module 文档检查、
Shell 语法和 Python 编译检查通过。首次 Docker Hub 直连构建失败保留为历史记录，
镜像源参数解决完整构建阻塞，未放宽判据。

## 已交付结论与后续边界

固定文件位置、环境导出、签发与回退行为已经沉淀到中英文技术文档，本主题验收完成并归档。
候选镜像保留在隔离 daemon，未发布到 registry，未更新现有应用部署。
真实公网 DNS-01 签发和 Traefik、Samba 等应用的自动重载未验证，不属于本矩阵的替代 CA 发布流程验收。
上线时按现有发布流水线计算 Module revision 并发布新镜像；部署更新作为后续操作。
