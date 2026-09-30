<!-- Generated from cases.yml by cmd/gen-test-case-docs. DO NOT EDIT. -->

# Nextcloud 官方回收站配置验收用例

> 需求来源：[`nextcloud-trashbin.md`](../../../modules/nextcloud/dev-docs/requirements/nextcloud-trashbin.md)
>
> 实施计划：[`nextcloud-trashbin.md`](../../../modules/nextcloud/dev-docs/plans/nextcloud-trashbin.md)
> 本文由同目录 `cases.yml` 生成；修改用例后运行 `go run ./cmd/gen-test-case-docs`。

## 覆盖总览

| 用例 ID | 级别 | 需求 ID | 实现 |
| --- | --- | --- | --- |
| `NCT-T-001` | unit | `NCT-R-001`、`NCT-R-002` | modules/nextcloud/hook/main_test.go |
| `NCT-T-002` | e2e | `NCT-R-001`、`NCT-R-003`、`NCT-R-004`、`NCT-R-005`、`NCT-R-006`、`NCT-R-007`、`NCT-R-008`、`NCT-R-009` | test-env/scripts/server-nextcloud-trashbin-e2e.sh<br>test-env/scripts/server-nextcloud-trashbin-e2e.py |
| `NCT-T-003` | unit | `NCT-R-007`、`NCT-R-008`、`NCT-R-009` | test-env/scripts/test_nextcloud_trashbin_e2e.py |

## `NCT-T-001` 默认配置、原生 occ 类型与写入失败路径

- 级别：`unit`
- 覆盖需求：`NCT-R-001`、`NCT-R-002`
- 需求复核摘要：`sha256:a7dc6092fb1ecfb9f3442e553d4f6770dde6124ed25d7d0a81b6f34e32968cd1`
- 实现复核摘要：`sha256:738701b7cd779255f70a9540d4ed743ae6e58c76b5588c1873e954272538eb26`
- Fixture：Hook 输入和真实 task.sh 回收站配置块；occ 替身只记录调用与注入失败
- 目标能力：`go`、`bash`
- Oracle 来源：`return-value`、`error-contract`
- 有效性证明：`fault-injection`
- 有效性证据：对真实脚本配置块注入 occ 非零退出码，若未停止即失败
- 超时：`5m`
- 敏感数据：使用合成值，不含真实账号密码

前置条件：

- 无。

执行步骤：

- 读取默认值并验证显式覆盖与非法布尔值
- 执行真实配置块并分别注入两条 occ 写入失败

可观察断言：

- 默认值为 false 和 60逗号365
- occ 写入 dotted key 的类型为 boolean 且 retention 为 string
- 写入失败不进入就绪步骤

反例与故障路径：

- 非法布尔值拒绝
- 第一或第二条 occ 写入失败必须停止

清理：

- 只使用 Go 临时目录和子进程

执行入口：

```bash
go test ./modules/nextcloud/hook -run TestTrashbin -count=1
```

有效性验证入口：

```bash
go test ./modules/nextcloud/hook -run TestTrashbin -count=1
```

## `NCT-T-002` 真实 Nextcloud 配置与 WebDAV 回收站行为

- 级别：`e2e`
- 覆盖需求：`NCT-R-001`、`NCT-R-003`、`NCT-R-004`、`NCT-R-005`、`NCT-R-006`、`NCT-R-007`、`NCT-R-008`、`NCT-R-009`
- 需求复核摘要：`sha256:655c6e744a8e971840d492bdabd9b90fd8cc7327ccec92c8f05554e3303baddb`
- 实现复核摘要：`sha256:4b9c89de84c7799e196ddcd65c5dc65e6fafcc09a5b746128a922d570fcc8804`
- Fixture：已应用的 domain-separation Authentik 或 LLNG fixture，包含固定 Nextcloud 34.0.2 镜像
- 目标能力：`docker`、`nextcloud`、`webdav`、`curl`、`python`
- Oracle 来源：`api`、`runtime`、`filesystem`、`report`
- 有效性证明：`counterexample`
- 有效性证据：真实提交拒绝操作并再次列出内容；本地反例主动破坏HTTP状态和清理调用，判据必须失败
- 超时：`20m`
- 敏感数据：密码随机生成并仅经stdin进入occ和curl；报告0600且无凭据或原始HTTP响应

前置条件：

- 显式指定专用测试 Docker socket和workspace和container_prefix和入口IP
- 已构建并应用包含本次 startup task 的测试镜像
- 两项回收站配置使用模块默认值

执行步骤：

- 核对运行容器归属、投影默认值和真实系统配置类型，不先覆盖默认值
- 创建随机临时用户，上传内容并删除进入回收站
- 单项删除和清空要求403且内容仍存在，MOVE还原后GET比对原始内容
- 运行容器内真实Expiration服务核对合成59和61和366天时间戳
- 通过occ切换disabled并检查不再过期，手动删除仍被拒绝
- 通过occ启用手动删除并验证单项删除和清空后的列表
- 恢复原配置、删除临时用户并写0600脱敏报告

可观察断言：

- 有效配置为boolean false和string 60逗号365
- 禁用删除返回403且不丢文件
- 还原后内容一致
- 启用删除后内容消失
- disabled下真实服务不判过期
- 测试配置恢复且临时用户删除

反例与故障路径：

- 真实提交单项DELETE和清空DELETE并要求403
- 401或其他错误不能被误判为禁止删除成功
- occ或HTTP故障必须触发清理且不能产生通过报告

清理：

- finally恢复两个原始系统配置并删除唯一临时用户
- 清理失败返回非零并保留失败报告

执行入口：

```bash
bash test-env/scripts/server-nextcloud-trashbin-e2e.sh
```

有效性验证入口：

```bash
bash test-env/scripts/server-nextcloud-trashbin-e2e.sh
python3 test-env/scripts/test_nextcloud_trashbin_e2e.py
```

## `NCT-T-003` E2E 判据、隔离和清理故障反例

- 级别：`unit`
- 覆盖需求：`NCT-R-007`、`NCT-R-008`、`NCT-R-009`
- 需求复核摘要：`sha256:5685df229639d45b78170e21d43f349a5f0c249f9e6b554810e0185692b4555e`
- 实现复核摘要：`sha256:594e5416f5c032335381dc9ddb03e12edd59845c37b84d608759a13512ad2aae`
- Fixture：Python E2E helper；只注入HTTP结果、外部命令记录和清理失败
- 目标能力：`python`
- Oracle 来源：`error-contract`、`return-value`
- 有效性证明：`fault-injection`
- 有效性证据：使用固定错误状态和抛错替身注入HTTP断言和清理故障，检查E2E判据拒绝或清理执行
- 超时：`1m`
- 敏感数据：只使用synthetic-secret合成密码

前置条件：

- 无。

执行步骤：

- 注入401和204等错误状态以及跨用户trash路径
- 注入HTTP断言失败和第一项配置恢复失败
- 检查curl参数与stdin的密码投影
- 指向生产socket确认在Docker调用前拒绝

可观察断言：

- 错误HTTP状态拒绝且只读取200 propstat
- 跨用户路径拒绝
- 清理仍尝试两项恢复和删除用户
- 密码只存在stdin
- 生产socket不调用Docker

反例与故障路径：

- 认证失败不能替代权限断言
- 第一项恢复失败也要继续清理并最终报错
- 生产socket必须在任何变更前拒绝

清理：

- 仅内存替身，不接触Docker

执行入口：

```bash
python3 test-env/scripts/test_nextcloud_trashbin_e2e.py
```

有效性验证入口：

```bash
python3 test-env/scripts/test_nextcloud_trashbin_e2e.py
```
