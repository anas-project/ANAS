# ANAS 设计问题审查报告（2026-07-19）

本报告基于当前 `master`（f287011）的实际代码行为，覆盖 `internal/`、
`cmd/`、`casks/mods/*` 与现有设计文档。每个问题给出证据位置、影响和解决
方案，并按优先级分级：

- **P0**：正确性或安全缺陷，会在真实部署中造成数据错误或凭据泄露面。
- **P1**：架构性设计问题，随 cask 数量增长会持续放大成本。
- **P2**：可维护性与文档一致性问题。

已有设计文档（`config-state-lifecycle.md`、`config-cli-lifecycle.md`、
`iam-capability-design.md`）识别的方向本报告不重复展开，只在相关问题处
引用并指出实现缺口。

---

## P0-1 全量环境与全部密钥注入每个容器

**现象**

- `renderAll` 把完整的全局 env（含所有模块的派生凭据）写进每个 cask 的
  `.env`（`internal/runner/runner.go:573-587`）。
- compose 文件普遍使用 `env_file: .env` 把整个文件注入容器进程环境，
  例如 `casks/mods/nextcloud/docker-compose.yml:48,71,83,96`——连
  `redis`、`imaginary` 这类纯内部服务也拿到 Samba 管理员密码、DNSPod
  API key、OIDC 签名密钥等全部凭据。
- 每个 cask hook 收到全部生成密钥：`hook.go:50`
  （`Secrets: a.secrets.clone()`）。
- `ai-design.md` 承诺的 `internalEnv` 渲染期过滤在代码中不存在
  （全仓库 grep 无结果），意味着"仅渲染期使用"的敏感值也会落入 `.env`。

**影响**：任何一个容器被攻破、任何一个 `.env` 泄露、任何一个 hook 进程
被劫持，等于全部服务凭据同时泄露。这也让"最小权限"类的后续设计
（IAM capability 模型）失去地基。

**解决方案**

1. 在 manifest 增加消费声明，runner 据此过滤每个 cask 的 `.env`：
   全局白名单键（`BASE_DOMAIN`、`TZ` 等）+ 本 cask 前缀键 +
   `config.consumes:` 显式列出的外部键。先以 warning 模式输出"本 cask
   实际引用但未声明"的键（可从 compose/模板中扫描 `${KEY}` 得出），再切
   强制。
2. secrets 按前缀或 manifest 声明做同样的作用域过滤后再传给 hook。
3. 容器注入层面：优先把 `env_file: .env` 替换为显式 `environment:`
   映射（只列本服务需要的键）；`.env` 只服务于 compose 变量替换。
4. 恢复（或删除文档中的）`internalEnv` 机制：渲染期专用值绝不写入
   `.env`。

## P0-2 `quoteEnv` 的引号方案与 compose dotenv 解析不兼容

**现象**：`runner.go:716-724` 对含特殊字符的值使用 shell 风格拼接
`'…'"'"'…'`。docker compose 的 dotenv 解析器（compose-go/godotenv 系）
不支持相邻字符串拼接：含单引号的值会被解析成错误内容，含换行的值直接破坏
行式 `.env` 文件。用户密码里带 `'` 或生成密钥带换行时静默损坏。

**解决方案**：改用 dotenv 规范的双引号转义（`\\`、`\"`、`\n`；`$` 按
compose 插值规则转义为 `$$` 或按需保留），并加一条往返测试：写入含
`'"$\n#` 等字符的值，经 `docker compose config` 读回比对。

## P0-3 模块从配置移除后成为孤儿容器；stop 错误被静默吞掉

**现象**：`stopRelease`（`runner.go:274-298`）按**新配置**解析出的
`a.order` 遍历。用户从 `modules` 移除一个模块后执行 `start -c`：

1. 旧容器不在新 order 中 → 不会被 `down`，继续运行；
2. `promoteRelease` 随后删除其 release 目录 → 连事后手工
   `compose down` 的项目目录都没了；
3. `runner.go:208` 的 `_ = a.stopRelease(release)` 把停止失败完全吞掉，
   端口未释放时新启动会以更难解释的方式失败。

**解决方案**：停止阶段以**旧状态**为准——枚举 `release/` 下实际存在的
cask 目录（或 `cask.lock.yml` 记录），与新模块集合做差；被移除的项目显式
`down` 后再 promote。`stopRelease` 的错误应中止流程或至少醒目上报，不能
丢弃。可另加兜底：用 `docker compose ls` 过滤 `anas_*` 项目，对不在
期望集合中的项目给出警告。

## P0-4 无 `-c` 的 `start`/`restart` 在 release 上就地重渲染，非原子

**现象**：`useTmp` 仅在 build/render/带 `-c` 的 start 时为真
（`runner.go:119`）。普通 `start`/`restart` 的工作目录就是 `release`，
`renderAll` → `copyDir` 会先 `os.RemoveAll` 正在被运行中容器使用的
cask 目录再重拷（`runner.go:765-769`）。中途崩溃会留下半损坏的
release；这也和 README 宣称的"tmp + 原子晋升"矛盾。

**解决方案**：二选一，推荐前者：

1. **release 即制品**：无 `-c` 的 `start`/`restart` 不重新
   calculate/render，直接用现有 release `up -d`（这才是"稳定重启"
   的语义，hook 的随机因素也被排除）。
2. 所有渲染一律走 `tmp` + `promoteRelease`。

---

## P1-5 部署模型是"全停全启"，没有利用 compose 的对账能力

**现象**：带 `-c` 的 start 先停掉整个旧 release 再逐一启动
（`runner.go:206-208`），任何一处配置改动都导致全栈停机窗口；而
`docker compose up -d` 本身就是幂等对账，项目名稳定（`anas_<cask>`），
大多数情况根本不需要先 down。

**解决方案**：默认不做全停。对每个 cask 直接从新渲染目录 `up -d`，
compose 自行重建有差异的容器；只有被移除的项目才 `down`（配合 P0-3）。
可选优化：对渲染目录做内容哈希，未变化的 cask 直接跳过。这与
`config-state-lifecycle.md` 提出的 `resolve → plan → reconcile` 分层
一致，可作为其第一块落地。

## P1-6 运行时依赖 Go 工具链，hook 每阶段重复 `go run` 编译

**现象**：所有 cask 的 hook 声明为 `go run ./hook`，由 runner 在
**源码目录**执行并注入 GOCACHE（`hook.go:56-62`）。后果：

- 生产 NAS 必须安装完整 Go 工具链；
- 一次 `start` 触发 16 个 cask × calculate/render_env/services/
  after_start 多阶段的重复 `go run`（首次全量编译尤其慢，测试文档里的
  go-build-cache 目录就是为此打的补丁）；
- hook 在源码树而非渲染副本中运行，release 不再是自包含制品。

**解决方案**：`build` 阶段把每个 hook `go build` 成静态二进制放入
release（记录 hash 进 lock），start 阶段只执行二进制；或收敛为一个
多路复用 hooks 二进制（子命令 = cask 名）。长期看，多数 hook 只做 env
派生，可在 manifest 中用声明式派生规则表达，真正的特殊逻辑才留 hook。

## P1-7 跨 cask 契约靠命名约定与无限制的 env 写入

**现象**：

- runner 用 `<MOD>_DOMAIN` 命名约定聚合 `DOMAINS`
  （`runner.go:547-554`），用 `USE_LDAP_MODS_NAME` 等逗号串传递能力
  信息（`runner.go:513-531`）；
- `applyHookEnv` 允许任何 hook 覆盖任意键（`hook.go:83-87`），后执行
  的 cask 可以悄悄改写先执行 cask 的输出，排序即契约；
- 消费方直接读取实现私有变量（`iam-capability-design.md` 第 2 节已列举
  netbird/nextcloud 的例子）。

**解决方案**：`iam-capability-design.md` 的"能力绑定 + 统一环境契约"
方向是对的，建议把它从 IAM 推广为通用机制：manifest 声明
`provides`/`consumes` 能力及其导出变量集合；runner 校验 hook 的 env
patch 只能写本 cask 前缀键和声明导出的能力键，其余一律报错。
`DOMAINS`/`USE_*_MODS_NAME` 改由 runner 从 manifest 能力声明计算，
而不是从 env 键名反推。

## P1-8 cask `version` 同时承担"上游镜像版本"和"打包版本"两种语义

**现象**：`ai-design.md` 规定 version 默认跟随主镜像版本，但
`upgrade.from` 约束、依赖版本约束、降级拒绝（`versions.go`）全都作用在
同一个字段上。镜像 bump 一次 major，就会连带触发所有依赖约束与升级路径
语义，而 cask 打包本身可能毫无变化；反之打包大改而镜像未动时无法表达。

**解决方案**：拆成两个字段：`version`（cask 打包版本，语义化，约束与
升级检查只看它）和 `app_version`（上游镜像版本，展示与记录用）。迁移时
现有值原样拷到 `app_version`，`version` 从 1.0.0 重新起步，lock 文件
同时记录两者。

## P1-9 单一 `default_service_root_password` 复用到所有管理员账户

**现象**：所有人机管理员账户继承同一口令（README:44），仅有 8 字符下限
校验（`config.go:64`）。一处被钓走等于全部管理后台失守；而且该值以明文
存在于用户配置和各 `.env` 中。

**解决方案**：默认改为按服务生成随机管理员密码存入 secretStore，提供
`anas config secret get <module>.admin_password` 查询；
`default_service_root_password` 降级为可选的显式选择。配合
`config-cli-lifecycle.md` 已规划的 `credential_rotate` 操作实现轮换。

## P1-10 promote 成功后立即删除上一版 release，没有回滚路径

**现象**：`promoteRelease`（`runner.go:696-714`）在成功后
`os.RemoveAll(backup)`。升级出问题时没有本地回滚制品，而 lock 机制又
拒绝降级（`versions.go:137-139`），回滚只能靠人肉重建。

**解决方案**：保留 `release.previous`（连同对应的 `cask.lock.yml`
快照）直到下一次成功启动；提供 `anas rollback` 把上一版 release 原子换
回并恢复 lock。磁盘成本可忽略（render 产物很小）。

## P1-11 `env.*` 变更策略归属判定不确定

**现象**：`policyOwnerForEnv`（`config_cli.go:151-160`）在 `reg` map
上迭代，多个模块的参数映射到同一 env 键时归属取决于 Go map 随机序，
`config explain`/start 守卫可能次次给出不同答案。

**解决方案**：对模块名排序后遍历；命中多个 owner 时直接报错要求显式
路径，而不是默默选一个。

---

## P2-12 正则实现的 ERB 模板引擎是静默失败源

**现象**：`runner.go:726-763` 用两个正则实现模板：未定义键渲染为空串
不报错；`if` 只支持相等比较、无 else、不可嵌套（嵌套时非贪婪匹配会在
第一个 `<% end %>` 截断）；渲染残留的 `<%` 无检测。

**解决方案**：短期加两道闸：键不存在即报错（模板引用集与 env 键集比
对）、渲染结果中残留 `<%` 即报错。长期逐 cask 迁到 Go
`text/template`（`missingkey=error`），`.erb` 只作为兼容期后缀。

## P2-13 `locateRoot` 用 `runtime.Caller` 猜项目根

**现象**：`runner.go:675-677` 把编译机上的源码路径作为根目录候选。
开发便利被烧进了发布二进制，在生产机上可能猜中一个完全无关的路径。

**解决方案**：候选只保留 `--root`、`ANAS_CASK_ROOT`、cwd、可执行文件目录；
`runtime.Caller` 候选用 build tag 限定在开发构建。

## P2-14 文档与代码漂移

- `ai-design.md:344` 与 README 要求"用 `internalEnv` 过滤渲染期专用
  值"，代码中该机制不存在（见 P0-1）。
- `ai-design.md` 新 cask 步骤 9 提到 `before` 依赖字段，manifest 结构
  没有该字段且 `KnownFields(true)` 会直接拒绝。
- 修复方式：实现或删改，二选一；建议在 CI 中对文档列举的 manifest 字段
  与 Go 结构体做一致性检查（很小的测试即可）。

## P2-15 结构与仓库卫生

- `runner.go` 869 行混合了 CLI 解析、生命周期编排、依赖解析、模板渲染、
  文件工具；建议按 lifecycle / render / envscope 拆文件（不必拆包）。
- `legacy/anas-0.1.0.gem` 是提交进仓库的二进制包；`legacy/` 若仅为参考
  可移到独立分支或 tag 后删除，符合 `ai-design.md` 自己定的替换标准
  （"After replacement, remove old Ruby project files"）。
- `stop` 也要求配置里必须有合法的 `default_service_root_password`
  （`config.Load` 无条件校验），语义上 stop 不应依赖这个；校验应下沉到
  需要它的路径。

## P2-16 macvlan 特权操作的前提未文档化

**现象**：`network.go:84-90` 依赖免密 `sudo nsenter/sh` 执行写在 base
目录下的脚本。功能可用，但对 sudoers 的要求、脚本固定路径
`anas_service.sh` 的生命周期都没有文档。

**解决方案**：在文档中给出最小 sudoers 授权样例（限定脚本绝对路径），
脚本内容变化时先校验再执行；或改为生成一次、由管理员审查后安装的
systemd unit。

---

## 建议实施顺序

| 阶段 | 内容 | 理由 |
| --- | --- | --- |
| 1（小改，先落地） | P0-2 quoteEnv、P0-3 孤儿容器与 stop 错误、P1-11 归属排序、P2-14 文档修正 | 每项半天内可完成，均为确定性缺陷 |
| 2（中等） | P0-4 渲染原子性（release 即制品）、P1-10 回滚保留、P1-5 去掉全停全启 | 三者同属发布/对账模型，一起改最省 |
| 3（结构性） | P0-1 env/secrets 作用域 + manifest `consumes`、P1-7 能力契约（与 IAM 设计合并推进）、P1-6 hook 预编译 | 需要 manifest 演进，建议随 `anas.dev/v2` 或兼容字段渐进 |
| 4（产品面） | P1-9 按服务管理员密码、P1-8 版本语义拆分、P2-12 模板引擎 | 依赖阶段 3 的 manifest 与 secret 基建 |

阶段 1、2 不改变 manifest 格式，对现有 cask 零侵入；阶段 3 开始涉及
cask ABI/manifest 演进，建议先在 `config-state-lifecycle.md` 的
reconcile 框架下统一设计，避免两套并行的状态模型。

---

## 解决状态（2026-07-20 实施）

| 问题 | 状态 | 实施摘要 |
| --- | --- | --- |
| P0-1 全量凭据注入 | 已解决 | env 归属跟踪（`envscope.go`）；每 cask `.env`/模板/`render_env`/`services`/`after_start` hook 输入按"全局 ∪ 自身 ∪ 依赖闭包 ∪ `config.consumes`"过滤；用户 secrets 必须显式认领；非 calculate 阶段 secrets 同规则过滤；hook 响应新增 `internal_env` 排除渲染专用值 |
| P0-2 quoteEnv 引号 | 已解决 | `envfile.go` 改为 dotenv 单形式引号（plain/单引号字面量/双引号转义 + `$$`），新增 `parseEnvFile` 逆解析与往返单测 |
| P0-3 孤儿容器 / stop 吞错 | 已解决 | `releaseModules` 以 release 目录实际内容为准（移除模块排最后、逆序先停）；`stopRemoved` 在 reconcile 启动前显式 down 被移除模块；所有 stop 错误上抛终止流程 |
| P0-4 就地渲染 | 已解决 | 无 `-c` 的 `start`/`restart` 以 release 为不可变制品：不 calculate、不渲染，`.env` 与 `.hook.bin` 冻结使用；旧格式 release 显式报错要求重渲染；渲染一律走 `tmp` + 原子晋升 |
| P1-5 全停全启 | 已解决 | 改为按 cask `up -d --remove-orphans` 对账 + 仅 down 被移除模块；全停只保留在 `restart`（语义即全量重启）与 `rollback` |
| P1-6 hook go run | 已解决 | `go run` hook 每次运行编译一次（`hook-bin/`），并冻结进渲染产物 `.hook.bin`；制品启动直接执行冻结二进制，无需 Go 工具链；渲染流程始终从源码重编译，杜绝旧二进制泄入新 release |
| P1-7 隐式契约 | 已解决（机制） | calculate 补丁强制"自身前缀或 `config.exports`"写契约；跨界读取显式化为 `config.consumes`（支持前/后缀 glob）；已为 nextcloud/netbird/mariadb/samba_fs 声明 exports，llng/keycloak/postgres/lego/ddns 声明 consumes。IAM 协议协商仍按 `iam-capability-design.md` 推进 |
| P1-8 版本双语义 | 已解决 | manifest/lock 新增 `app_version` 记录上游镜像版本；`version` 明确为打包版本，约束与升级检查只作用于它；文档定义 bump 规则，存量号保持不变避免破坏已部署实例 |
| P1-9 单一管理密码 | 已解决 | `default_service_root_password` 改为可选；缺省时每个 cask 生成独立根密码（`<PREFIX>_DEFAULT_ROOT_PASSWORD`）存入 secret store；新增 `anas config secret list/get` |
| P1-10 无回滚 | 已解决 | promote 保留 `release.previous` + `.cask.lock.snapshot`；新增 `anas rollback` 交换目录、恢复 lock、以制品方式启动并更新 applied 快照 |
| P1-11 归属不确定 | 已解决 | `policyOwnerForEnv` 排序遍历，多归属直接报错要求使用模块路径 |
| P2-12 模板静默失败 | 已解决并移除 | 2026-08-01 删除 Go 正则 ERB 渲染器及全部 `.erb`；JSON/YAML/文本配置改由容器入口基于作用域化 `.env` 生成，静态测试禁止旧后缀和 ERB 标记 |
| P2-13 locateRoot 魔法 | 已解决 | 移除 `runtime.Caller` 候选；`bin/anas` 显式传 `ANAS_CASK_ROOT` |
| P2-14 文档漂移 | 已解决 | 删除 `before` 字段描述；`internalEnv` 文档改为实际存在的 `internal_env` 协议字段与作用域机制 |
| P2-15 结构/卫生 | 部分解决 | 从 `runner.go` 拆出 `envfile.go`/`envscope.go`；原 `render.go` 随 ERB 能力删除。stop 不再被密码长度校验卡住（校验仅在设置了口令时生效）。`legacy/` 目录（含 .gem 二进制）删除留待用户确认 |
| P2-16 sudoers 未文档化 | 已解决 | 新增 `docs/util/macvlan-sudoers.md`：脚本行为、最小 sudoers 授权样例、更严格的 root-owned 路径方案 |

实施过程中额外发现并修复：hook 在 calculate 阶段用临时渲染路径构造持久
值（如 `APPS_LIST__*__LOGO_PATH`），promote 后路径失效会破坏制品启动的
`docker cp`。现在 hook 请求的 `workdir` 一律是晋升后的稳定 release 路
径，`after_start` 阶段也改为在 promote 之后统一执行。

### 验证状态

静态验证已完成：编辑器编译诊断全绿；全仓扫描确认 compose `${VAR}` 引用、
两个 ERB 模板、容器初始化脚本的跨 cask env 读取全部落在闭包或已声明的
consumes/exports 内。动态验证（`go test ./...`、
`test-env/scripts/test-render.sh` 全配置渲染、与改动前渲染树的 diff）
在本次会话中因执行环境不可用尚未运行，命令如下：

```sh
GOCACHE=$PWD/.gocache go test ./...
sh test-env/scripts/test-render.sh
sh test-env/scripts/test-compose-config.sh   # 需要 docker
```
