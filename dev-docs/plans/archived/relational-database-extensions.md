---
doc_type: plan
status: done
created: 2026-10-03
updated: 2026-10-03
---

# relational_database 扩展生命周期实施计划

验收见[需求矩阵](../../requirements/relational-database-extensions.md)，实现边界见
[设计](../../../docs/architecture/relational-database-extension-lifecycle.md)。
当前状态：M1、M2、M3 已完成。固定组合与真实应用角色在本机及 finance 专用主机通过，
ANAS 新装、重复 apply、重启、真实扩展升级、维护失败后的匹配旧/新恢复、维护成功后的真实
SIGKILL 与原冻结候选重试均通过。全 workspace 恢复已核对媒体、身份、配置/Secret、匹配镜像
和另一共享 PG Consumer。Immich 浏览器/移动端与目录验收在其私有计划单独跟踪。

## 1. 范围与顺序

ANAS 尚未发版，直接完善当前 Contract `1.0.0`；不交付 `1.1.0` 过渡、旧 lock 转换或历史版本矩阵。
最小增量是扩展名称列表、Provider 固定版本管理及升级验证。沿用现有 Resource、Compose Provider、
Secret、plan/apply、锁和 ANAS 备份；不新建目录求解器、结果 ABI、调度或自动续跑框架。

M1 → M2 → M3。M2 的安全前置工作由[凭据轮换计划 M4](../credential-rotation.md)交付；
本计划不重复认领其 CRED 要求。可以先交付 PG 认证子项，不依赖完整跨类凭据轮换功能。

## 2. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：最小字段、传参与固定镜像 | R-002、R-005、R-007—R-008、R-014 | 已完成 |
| M2：认证、初始化与实际就绪 | R-012—R-013、R-017—R-018、R-023、R-026 | 已完成 |
| M3：受控升级与 ANAS 恢复 | R-022、R-024、R-028—R-030、R-032—R-034 | 已完成 |

19 项有效要求各归属一个里程碑；17 项已废弃条目不分配，不计为完成。

## 3. M1：最小字段、传参与固定镜像

- [x] 在 `contracts/relational_database/schemas/resource.yml` 增加可选 `postgres.extensions` 名称列表；
  补当前 Runner 的严格字段校验，拒绝空列表、重复/非法名称和 MariaDB 使用 PG 块。
- [x] 在 `internal/runner/resources.go` 与 PG Compose 增加列表环境投影，沿用现有凭据注入，
  测试请求没有在 Runner 或 Compose 中被丢弃。无需临时请求文件和新 helper。
- [x] 在 `modules/postgres` 的构建输入和 Provider 脚本固定扩展版本、依赖及 preload；
  先验证当前 PG 18/Alpine 和拟声明平台。服务与 provision 使用同一镜像组合。
- [x] Provider 在写入前拒绝不支持的扩展。运行时不安装包；沿用现有镜像发布流程，
  不为固定列表新增对外目录格式、版本范围或依赖图解析器。
- [x] 覆盖当前普通 PG/MariaDB Consumer、合法扩展请求及拒绝场景；同步 Contract 四份中英文源文档，
  运行既有生成器，不提前把草案字段写成已实现能力。

落点：Contract schema/文档、Runner manifest/resource 校验与投影、PG 构建/Compose/provision。
普通 MariaDB 请求不需要扩展管理；只有 interface 拒绝与当前行为回归，不重做 MariaDB Provider。

## 4. M2：认证、初始化与实际就绪

- [x] 对接凭据主题的 PG 认证修复，实测正确/错误口令、空口令、冒用管理员与库间隔离；
  不将修改初始化变量视为真实 HBA 已生效的证据。
- [x] 以 Provider 权限在 Consumer 库启用固定版本扩展及依赖；应用只拿自己的普通角色。
- [x] ensure 与 inspect 复用检查逻辑：实际应用连接、权限、已启用版本及 preload；
  inspect 无写操作，ensure 只在检查成功后退出 0，失败不得保存 ready。
- [x] 已有扩展版本不符时拒绝普通 ensure，提示 Provider 维护；不由应用启动隐式升级。
- [x] 验证空库初始化、重复 ensure、扩展/依赖缺失、preload 失效及再次检查；
  已验证 PG 基础场景；固定 Immich 普通角色启动与真实主机的 ANAS 入口分别记录，不以此宣称整个 Module 已验收。

落点：`modules/postgres/providers/relational_database/provision.sh`、现有 PG Hook/Compose、
Runner 对失败的处理及定向测试。认证修复仍记在原凭据主题。

## 5. M3：受控升级与 ANAS 恢复

- [x] 为一个经过验证的扩展版本升级路径提供 Provider 固定维护脚本，明确来源、目标和必要索引维护。
  应用特有索引步骤归 Consumer，不向 Contract 增加任意 SQL 或 maintenance operation。
- [x] 核对并补齐现有 Provider 更新执行顺序：列出全部受影响消费者，停写并用 ANAS 取得恢复点，
  更新镜像/preload、执行扩展维护及检查，然后才启动消费者。共享 PG/IAM 的影响不能藏起来。
- [x] 在现有失败处理处阻止不安全的镜像回退；升级部分失败或中断时保留停止状态及恢复点，
  重试先查真实版本，未知/不可重试状态使用 ANAS 恢复，不先建设跨阶段自动续跑框架。
- [x] 验证移除声明不会 DROP 扩展；沿用现有 retain 行为，不借本计划实现 delete/rotate_credential。
- [x] 通过 ANAS 在隔离 workspace 恢复 PG、媒体、配置、Secret 和匹配镜像；核对照片 hash、相册、
  anchor 关联及至少一个其他数据库消费者的数据。核对外部挂载与嵌套子卷是否实际覆盖。
- [x] 同步发布说明、Module/Contract 文档和测试证据，运行仓库门禁；仅声明真实验证过的组合。

落点：现有 PG 生命周期/固定执行资产、Runner 更新与失败路径、Consumer 必要维护及 ANAS 备份验证。
本阶段可补齐必要的执行接线，不依赖未来统一动作 ABI、全量凭据轮换或资源档位框架完成。

## 6. e2e 执行记录

已新增 `modules/postgres/tests/container-e2e.sh` 与调用它的服务器隔离入口
`test-env/scripts/server-relational-database-extensions-e2e.sh`。本机完整脚本使用专属容器/网络/卷，
旧 fixture 构建真实 vector 0.8.1 二进制；没有伪改版本目录冒充旧版本。下表区分本机证据与服务器缺口。

2026-10-03 本机 Docker Desktop linux/arm64 完整测试通过：新装、重复 ensure、重启、普通角色函数，
正确/错误/空密码、冒用管理员、库间隔离与角色成员权限拒绝，缺扩展/preload 漂移拒绝、inspect
对象 hash 不变、retain，以及真实 0.8.1 → 0.8.2 升级、旧 HNSW 索引读取、未知来源拒绝和部分状态重试。
linux/amd64 模拟镜像构建及新装基础场景通过；这不是原生 amd64 服务器验收。

Runner 已实现全消费者计划、全 workspace 停写与必需 Btrfs 恢复点、匹配镜像捕获、维护先于
Consumer 的生命周期接线及持久失败保护。镜像捕获/恢复在隔离 Docker 实测旧不可变 ID 恢复且
原 tag 不变；单元回归不代替整台服务器数据恢复。外部挂载、嵌套子卷和逃逸符号链接不能假称覆盖。

| 需求 ID | 脚本/场景 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-007 | PG `container-e2e.sh` allowlist/写前拒绝 | 本机 arm64 PG18.4/vector0.8.2 | 2026-10-03 | 本机及finance原生amd64隔离主机通过 |
| R-012 | 同脚本，普通角色/成员权限/库间隔离 | 同上；Immich 实际普通角色启动 | 2026-10-03 | 本机及finance原生amd64隔离主机通过 |
| R-013 | 同脚本，trust→SCRAM/正确错空密码 | 同上；真实旧 fixture | 2026-10-03 | 本机及finance原生amd64隔离主机通过 |
| R-017 | 同脚本，inspect 前后对象 hash | 同上 | 2026-10-03 | 本机及finance原生amd64隔离主机通过 |
| R-018 | 同脚本 + Immich HTTP fixture，就绪拒绝 | 同上；真实应用普通角色 | 2026-10-03 | 本机及finance真实 ANAS 普通角色/入口通过 |
| R-023 | 同脚本，新装/repeat/restart | 同上 | 2026-10-03 | 本机及finance新装/重复apply/重启通过 |
| R-024 | 同脚本；ANAS workspace真实升级/HNSW | 本机 arm64；finance Linux/Btrfs真实AD/Authentik/Immich | 2026-10-03 | 主机通过：全消费者先停写、匹配旧镜像恢复点、维护后启动、旧HNSW/媒体/共享数据正确 |
| R-026 | 同脚本，缺依赖/未知版本/preload | 同上 | 2026-10-03 | 本机及finance原生amd64隔离主机通过 |
| R-028 | 同脚本，部分状态重试；公开 Apply 失败候选回归 | 同上；Runner fixture | 2026-10-03 | 主机通过：维护成功后真实SIGKILL、四项持久屏障、实际版本复查与同冻结candidate重试，原point/镜像hash不变 |
| R-029 | Runner回归；主机维护后失败/四项阻断/匹配旧point恢复 | 本机Go；finance真实ANAS/Btrfs | 2026-10-03 | 主机通过：维护失败及SIGKILL四项阻断；匹配旧0.8.1 snapshot/new0.8.2全备份恢复，未用旧镜像打开新数据 |
| R-030 | PG `container-e2e.sh` retain | 本机 arm64 PG18.4/vector0.8.2 | 2026-10-03 | 本机及finance原生amd64隔离主机通过 |
| R-033 | Runner覆盖检查；主机Btrfs恢复点/镜像归档 | 本机Go；finance真实ANAS/Btrfs | 2026-10-03 | 主机通过：维护前data覆盖、旧实际镜像归档verify及全workspace恢复 |
| R-034 | 原生Provider完整脚本 + ANAS全workspace恢复 | finance专用Linux/Btrfs/隔离daemon | 2026-10-03 | 主机通过：Provider完整场景；恢复后目标应用/其他Consumer数据身份及精确镜像通过 |

## 7. 检查与阻塞

已使用临时 Go 1.26.6 工具链与隔离 Linux 检查容器，不向项目引入依赖。
定向 Contract/Runner/PG Hook、Vet、CLI Contract 26断言与文档生成/索引/构建门禁已通过。
全仓库 Go 测试未通过，干净 HEAD 对照已复现相关既有失败；具体证据见
[2026-10-03 实施记录](../../../modules/immich/dev-docs/plans/immich-module.md#执行记录归并)。
必需检查为：

```bash
go test ./internal/runner ./internal/compose ./modules/postgres/hook
go run ./cmd/gen-contract-docs --check
test-env/scripts/test-contract.sh
npm run docs:check-requirements
npm run docs:check-requirement-status
npm run docs:check-plan-status
npm run docs:check-status
npm run docs:build
```

本主题当前无未完成验收项。finance 的主机/源码授权、隔离执行和逐轮失败/接续证据见实施记录；
不把原failed报告改写为单轮全绿。扩展升级的匹配旧/新两方向恢复在r7通过；r10维护成功后实际
CLI -9、四项postgres_recovery_required及精确冻结candidate重试通过，原恢复点/二进制/镜像摘要
不变、普通角色与业务数据正确。后续共享消费者目录与真实手机验收归Immich私有计划。

## 2026-10-03 主机验收入口补齐

`test-env/scripts/server-immich-workspace-e2e.sh` 现在包含真实 ANAS 0.8.1→0.8.2 升级和失败恢复：
测试副本沿当前受管 SCRAM Provider 只替换审定的0.8.1源码/预期版本，r3仅为test-only，非历史发行版；
真实 Docker 事件核对全workspace停写，恢复点核对from/to、data覆盖及实际镜像归档。
维护成功后故意失败的私有Hook用于验证普通start/restart/different apply/rollback阻断，再分别恢复匹配
旧snapshot0.8.1与完整backup0.8.2。生产模块和扩展目录不被修改，不用改SQL extversion模拟升级。

harness定向回归、实际Hook编译通过；生成的旧PG镜像实际构建，普通TCP角色、SCRAM、HNSW、
扩展0.8.1与空preload通过。PG18限制普通角色读取preload，因此应用查engine/extversion，Provider单独
查preload，未授予pg_read_all_settings。主机前置检查退出2、workspace_mutated:false；真实ANAS
维护/中断/全恢复仍未运行，M2/M3保持实施中。

收尾另补私有Hook在原生维护成功后暂停、精确核对本次Linux CLI/冻结Hook进程再SIGKILL的驱动；
同一冻结候选必须通过原恢复点核对后重试，原point与候选hash不变，随后匹配新版backup恢复。
副本移除PG预构建Hook，避免旧binary掩盖测试注入；生产Hook和ABI不变。
升级前后真实 `anas plan` 保存共享消费者及完整stop/restart scope，失败阶段输出与保护状态先保存再清理。
实际本机当前CLI init/plan已核对消费者authentik/immich与完整范围及config/Secret不变，未启动应用；
主机升级/进程中断/恢复验收仍待专用Linux/Btrfs目标，不把驱动单测当作R-028/R-034完成。

## 2026-10-03 已授权主机接续

用户明确允许当前源码及Linux二进制上传。严格隔离preflight通过，真实PG18.4/pgvector0.8.1与0.8.2
amd64镜像在指定主机构建成功。首轮发现fixture遗漏共享Contract目录，已修复并通过34项Linux非root
回归；第二轮公开APT下载代理不支持GET，已补齐临时网络通道并实测InRelease返回200。
第三轮在新空workspace运行；M2/M3仍待真实业务、维护/中断及全恢复结果，详见
[实施核对记录](../../../modules/immich/dev-docs/plans/immich-module.md#执行记录归并)。

## 原生 PG 主机验收结果

2026-10-03 finance 专用daemon的native-pg-r4完整脚本通过，原生linux/amd64：新装/重复/重启、
普通角色SQL/正确错空口令/冒用管理员/库隔离/成员权限拒绝、只读inspect hash不变、缺依赖与
preload漂移拒绝、retain。旧fixture明确验证真实trust HBA及postgres/photos/otherapp三角色
MD5存储，再以新Provider迁移到全SCRAM。真实vector0.8.1→0.8.2、旧HNSW读取、未知来源
写前拒绝、两库部分状态重试与另一消费者数据保留通过；不是修改extversion冒充升级。
镜像新sha256:339c4dd98a38d35ef9ad4c9164231c9905c4cbd046287813fb8170f5f3ddd362，
旧测试fixture sha256:cf0a375028906623b41276dc65ae5b7a190585e197e687c535c0f6da259c1ce8。
完整workspace restore CLI返回ok并verify7项；恢复后普通角色、媒体/HNSW、身份、相册、共享消费者标记、配置/Secret字节与实际镜像通过。接续r7维护失败后的四项屏障及匹配旧snapshot/new backup两方向恢复通过；维护成功后的真实SIGKILL/原冻结candidate重试已通过，原恢复点与镜像摘要不变。
