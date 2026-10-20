<!-- Generated from cases.yml by cmd/gen-test-case-docs. DO NOT EDIT. -->

# 分组共享清单验收用例

> 需求来源：[`identity-group-catalog.md`](../../../dev-docs/requirements/identity-group-catalog.md)
>
> 实施计划：[`identity-group-catalog.md`](../../../dev-docs/plans/archived/identity-group-catalog.md)
> 本文由同目录 `cases.yml` 生成；修改用例后运行 `go run ./cmd/gen-test-case-docs`。

## 覆盖总览

| 用例 ID | 级别 | 需求 ID | 实现 |
| --- | --- | --- | --- |
| `GROUPCAT-T-001` | unit | `GROUPCAT-R-001`、`GROUPCAT-R-002`、`GROUPCAT-R-003`、`GROUPCAT-R-005`、`GROUPCAT-R-006` | internal/groupcatalog/catalog_test.go<br>internal/runner/identity_groups_test.go<br>modules/user_portal/portal/administration_test.go |
| `GROUPCAT-T-002` | e2e | `GROUPCAT-R-002`、`GROUPCAT-R-004`、`GROUPCAT-R-005` | test-env/scripts/user-portal-owner-isolated.py<br>test-env/playwright/user-portal-account-management.mjs |

## `GROUPCAT-T-001` 分组声明、目录定位与只读共享契约

- 级别：`unit`
- 覆盖需求：`GROUPCAT-R-001`、`GROUPCAT-R-002`、`GROUPCAT-R-003`、`GROUPCAT-R-005`、`GROUPCAT-R-006`
- 需求复核摘要：`sha256:d03885c2080f2f375e7487422561dbad54baac6769ddd89326e87641db7c9a33`
- 实现复核摘要：`sha256:7ebbee2b14f24cf83ac0dd66df12bb15b73e4663bba9538c6958ff00a6ac7e3f`
- Fixture：固定 Module 声明和隔离 Go 目录替身
- 目标能力：`go`
- Oracle 来源：`ui`、`api`、`error-contract`
- 有效性证明：`counterexample`
- 有效性证据：对错误定位、写挂载、缺英文和伪前缀设反例断言
- 超时：`5m`
- 敏感数据：仅模拟密码，无真实凭据

前置条件：

- 本机临时 HTTPS 端口可用

执行步骤：

- 执行分组声明展开、定位和共享挂载反例

可观察断言：

- 所属名称过滤、重复定位拒绝、英文必填、只读挂载、外部前缀不获得受管身份

反例与故障路径：

- 可写和公开挂载拒绝；非本域同名系统组不匹配元数据

清理：

- 关闭隔离浏览器和本机测试服务

执行入口：

```bash
go test ./internal/groupcatalog ./internal/runner -run 'Test(IdentityCatalog|Expansion|RejectInvalidAndDuplicateCatalog)'
go test ./modules/user_portal/portal -run TestGroupDescription
```

有效性验证入口：

```bash
go test ./internal/groupcatalog ./internal/runner -run 'Test(IdentityCatalog|Expansion|RejectInvalidAndDuplicateCatalog)'
```

## `GROUPCAT-T-002` finance 共享制品及无信任隔离域原生建组

- 级别：`e2e`
- 覆盖需求：`GROUPCAT-R-002`、`GROUPCAT-R-004`、`GROUPCAT-R-005`
- 需求复核摘要：`sha256:e20316076465a4f2a2b2bdc5dc32b82a9cf1c095f5f3434786636eb24ca8f9aa`
- 实现复核摘要：`sha256:a41be5d808729f949dc7022ac0d43dc0822588b542d8918c3d41d9c54299a35e`
- Fixture：finance 指定测试服务和不发布端口、无网络、无生产信任的 owner.test 假域
- 目标能力：`docker`、`samba`、`playwright`、`ldaps`
- Oracle 来源：`runtime`、`ui`、`api`、`report`
- 有效性证明：`counterexample`
- 有效性证据：实际报告必须包含隔离域执行和 finance 冻结制品/只读挂载证据，越界定位及伪前缀反例须拒绝
- 超时：`20m`
- 敏感数据：随机密码与私钥仅存在私有输入和内存，不进入报告或截图

前置条件：

- 完整恢复点已验证；候选镜像与原生测试二进制已固定；不在 finance 域创建高权测试身份

执行步骤：

- 核对冻结清单及消费者只读挂载
- 运行隔离域建组/描述/系统组不变及原生权限验证
- 运行 finance 普通用户分组正反例和双语界面

可观察断言：

- 同一清单实际创建受管组和英文目录介绍；重复执行保留 GUID；不修改内置组；伪前缀和实际权限边界仍拒绝

反例与故障路径：

- 目录之外的定位拒绝；未注册 APP_ 组不得获得受管权限

清理：

- 删除专用无网络假域容器/卷和普通测试主体；保留脱敏证据

执行入口：

```bash
python3 test-env/scripts/user-portal-owner-isolated.py --image ${GROUPCAT_TEST_IMAGE} --native-binary ${GROUPCAT_NATIVE_BINARY} --group-catalog ${GROUPCAT_CATALOG} --evidence-dir ${GROUPCAT_EVIDENCE_DIR}
node test-env/playwright/user-portal-account-management.mjs
```

有效性验证入口：

```bash
python3 test-env/scripts/user-portal-owner-isolated.py --image ${GROUPCAT_TEST_IMAGE} --native-binary ${GROUPCAT_NATIVE_BINARY} --group-catalog ${GROUPCAT_CATALOG} --evidence-dir ${GROUPCAT_EVIDENCE_DIR}
```
