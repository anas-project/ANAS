---
doc_type: review
status: current
created: 2026-09-10
updated: 2026-09-10
review_baseline: 2026-09-10 工作树，ANAS HEAD `f7642c5`；上游 Incus 源码 tag `v7.3.0`
---

# Incus v7.3.0 网络与 proxy 权限核验

基线：2026-09-10，ANAS HEAD `f7642c5` 与当前文档工作树；上游源码 tag `v7.3.0`。
本次仅核验源码调用链，不修改 Provider，不执行宿主安装或资源变更。

## 1. 结论

| 事项 | 分类 | 结论 |
| --- | --- | --- |
| 租约 project 内创建 bridge | 已确认的固定版本代码兼容性缺陷 | ANAS 请求与 v7.3.0 支持的 project/network 组合不兼容 |
| 管理证书为受限 project 添加 proxy | 已确认的设计冲突 | 正常实例更新和 profile 更新仍检查 project 禁令，不存在此路径上的证书豁免 |
| default project 独立 bridge + 精确授权 | 修复候选 | 源码支持有效 network project 与名字访问限制；实机权限及流量隔离待验证 |
| 真实网络转发、VM/容器启动 | 未验证 | 本机无 Incus，Docker daemon 不可用，不能形成 e2e 证据 |

## 2. bridge 请求的拒绝路径

ANAS `modules/incus/provisioner/ops.go` 的 `projectConfig` 设置 `features.networks=true`，
`ensureNetwork` 向 `/1.0/networks?project=<sandbox>` 提交 `type=bridge`。

上游路径：

1. [NetworkProjectFromRecord](https://github.com/lxc/incus/blob/v7.3.0/internal/server/project/project.go#L222)
   在 networks feature 开启时保留租约 project；
2. [networksPost](https://github.com/lxc/incus/blob/v7.3.0/cmd/incusd/networks.go#L385)
   检查非 default project 与驱动的 `Projects` 标志；
3. [bridge.Info](https://github.com/lxc/incus/blob/v7.3.0/internal/server/network/driver_bridge.go#L61)
   继承 [common.Info](https://github.com/lxc/incus/blob/v7.3.0/internal/server/network/driver_common.go#L287)
   的 `Projects=false`，未覆盖；因此请求进入不支持非 default project 的拒绝分支。

这不是单凭文档推断。现有 HTTP fake 将 network 只按名字保存，既不实现有效 network project 解析，
也不执行 driver capability 校验，因此已有单元测试无法发现此问题。

建议的修复范围：保留每消费者独立 restricted project、profile、证书与配额；bridge 由 Provider
在 default project 按租约唯一命名创建，租约使用 `features.networks=false`，并显式设置
`restricted.networks.access=<本租约 bridge>`。`restricted.devices.nic=managed` 保持。
网络访问限制为空时并不代表拒绝，不能只切换 feature 而漏掉精确授权。

修复还应包括网络所有权校验、读回、幂等与消费者不能修改共享网络的实机反例。不同 bridge 不等于
跨网段流量自动隔离；路由与防火墙效果要单独测。旧环境若已有 OVN/其他网络，不自动改 feature
或迁移运行实例，先清点并报告迁移冲突。

## 3. proxy 禁令的更新路径

已追踪正常更新入口，而非只看一个校验函数：

- [instance PUT](https://github.com/lxc/incus/blob/v7.3.0/cmd/incusd/instance_put.go#L164)
  与 [PATCH](https://github.com/lxc/incus/blob/v7.3.0/cmd/incusd/instance_patch.go#L222)
  调用 `AllowInstanceUpdate`；
- [profile 更新](https://github.com/lxc/incus/blob/v7.3.0/cmd/incusd/profiles_utils.go#L21)
  调用 `AllowProfileUpdate`；
- 两条路径进入
  [checkRestrictionsAndAggregateLimits](https://github.com/lxc/incus/blob/v7.3.0/internal/server/project/permissions.go#L420)
  和 `checkRestrictions`；proxy 未设为 allow 即拒绝，默认是 block。

调用身份的 API 授权与 project 内容校验是两层。拥有管理权限不使正常更新跳过后者，委托管理证书
本身不能使既有 proxy 方案成立。此结论限定为已检查的 v7.3.0 正常 API 路径，不声称已跑过全部
版本、迁移或恢复路径。

继续保留 project 禁令和消费者直连模型。暂不通过放开 proxy、临时关闭 restricted、直接改数据库
或将所有生命周期移入 Core 来解决。若改用受管 network forward，需要先修订 R-053/R-070 的
指定机制，并验证端口授权、后端归属及双方协议族；本次未修改这些需求。

## 4. 实机验证清单（全部未执行）

使用专用 Linux 测试宿主、固定 v7.3.0、测试证书与一次性资源命名空间。仅清理本次创建的资源；
不在生产 project 上修改限制。异常时先记录配置和残留，再按所有权逆序撤销。

| 用例 | 输入/操作 | 预期 |
| --- | --- | --- |
| bridge 原路径 | 非 default project，networks=true，创建 bridge | 拒绝，且未创建目标 network |
| bridge 候选路径 | default project 创建 A/B 独立 bridge，租约精确授权 | 各自合法 NIC 可用 |
| 越界接网 | 租约 A 证书引用 B bridge | 拒绝，配置无变化 |
| 网络写权限 | A 证书修改 default project 受管 bridge | 拒绝 |
| 真实数据隔离 | 两租约 guest 尝试跨 bridge 访问 | 按既定隔离策略拒绝；不能仅凭 API 拒绝代替 |
| proxy 实例写入 | 管理/受限证书分别 PUT、PATCH 添加 proxy，restricted 保持 true | 禁令拒绝，实例配置无新增 device |
| proxy profile 写入 | 管理/受限证书分别更新租约 profile | 禁令拒绝，profile 无新增 device |
| 回归生命周期 | 容器档、VM 档分别 create/start/exec/delete | 合法路径成功，配额与证书作用域仍有效 |

按每个用例记录实际 daemon 版本、架构、提交、API 状态、资源最终状态与清理结果；错误文本只是
辅助，不能把“任意错误”当作预期权限拒绝。证书、私钥与真实 endpoint 不写入公开报告。

## 5. 本轮检查记录

- 工作环境为 macOS，无 `incus` 可执行文件；Docker 客户端存在但 daemon 不可达。
- 已读取官方 v7.3.0 源码归档并逐处追踪上述路径；源码下载仅落临时目录。
- 未启动 Docker/Incus，未安装软件，未使用或寻找私人远端凭据。
- 未执行实机清单，未用现有 fake 单测宣称宿主验收通过。

下一步：先修复并测试 bridge 的归属与精确授权，再在独立宿主执行上述反例；proxy 机制调整仍需
需求层决策。

## 6. 后续解决状态（2026-09-10）

bridge 代码已改为 default project 归属、精确 network access 和归属读回；旧 network-isolated
project 拒绝隐式迁移。回归测试覆盖非 default 请求、双租约、错误归属、过宽授权、读回失效与子网
稳定性。以上为本地实现与测试证据，不改写原评审基线；实机清单与 proxy 设计冲突仍未解决。
