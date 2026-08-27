<!-- Generated from cases.yml by cmd/gen-test-case-docs. DO NOT EDIT. -->

# 文档驱动测试自动化 M0 用例

> 需求来源：[`document-driven-test-automation.md`](../../../docs/requirements/document-driven-test-automation.md)
>
> 实施计划：[`document-driven-test-automation.md`](../../../docs/plans/document-driven-test-automation.md)
> 本文由同目录 `cases.yml` 生成；修改用例后运行 `go run ./cmd/gen-test-case-docs`。

## 覆盖总览

| 用例 ID | 级别 | 需求 ID | 实现 |
| --- | --- | --- | --- |
| `TESTAUTO-T-001` | unit | `TESTAUTO-R-002`、`TESTAUTO-R-003`、`TESTAUTO-R-004` | internal/testcasecatalog/catalog_test.go |
| `TESTAUTO-T-002` | unit | `TESTAUTO-R-005`、`TESTAUTO-R-006` | internal/testcasecatalog/catalog_test.go |
| `TESTAUTO-T-003` | unit | `TESTAUTO-R-007` | internal/testcasecatalog/catalog_test.go |
| `TESTAUTO-T-004` | unit | `TESTAUTO-R-008` | internal/testcasecatalog/catalog_test.go |

## `TESTAUTO-T-001` 用例 schema、稳定 ID 与需求覆盖

- 级别：`unit`
- 覆盖需求：`TESTAUTO-R-002`、`TESTAUTO-R-003`、`TESTAUTO-R-004`
- 需求复核摘要：`sha256:cddf7bd5e986bd30b8cd12b0345a9539abadf5177a057bccacf032e10840ea0c`
- Fixture：临时仓库中的需求矩阵、计划和 cases.yml
- 目标能力：`go`、`filesystem`
- 超时：`1m`
- 敏感数据：不使用敏感数据

前置条件：

- 无。

执行步骤：

- 加载需求矩阵和严格 YAML catalog 后执行结构、ID 与 scope 校验

可观察断言：

- 严格 schema 接受完整用例并拒绝未知字段
- 稳定用例 ID 使用需求前缀且不能跨 catalog 重复
- 纳入 scope 的自动验证需求必须由 active 用例覆盖

反例与故障路径：

- 删除必填字段、使用未知字段或让 e2e 需求仅由 unit 用例覆盖

清理：

- Go 测试框架删除临时仓库

执行入口：

```bash
go test ./internal/testcasecatalog
```

## `TESTAUTO-T-002` 用例到实现文件和执行命令的双向发现

- 级别：`unit`
- 覆盖需求：`TESTAUTO-R-005`、`TESTAUTO-R-006`
- 需求复核摘要：`sha256:76f8dc8d79ef0f639a6e5c925c2ee276edc8024aa39d72dbae6eb5686b5b39d3`
- Fixture：带 TEST_CASES 标记和可发现命令的临时仓库
- 目标能力：`go`、`filesystem`
- 超时：`1m`
- 敏感数据：不使用敏感数据

前置条件：

- 无。

执行步骤：

- 扫描实现文件标记并解析 catalog 中的每个执行命令

可观察断言：

- catalog 引用的实现文件存在且反向声明同一用例 ID
- npm、Go 和脚本命令必须解析到仓库中的真实入口
- 实现文件声明不存在或未反向引用的用例 ID 时校验失败

反例与故障路径：

- 删除 TEST_CASES 标记并加入不存在的用例 ID

清理：

- Go 测试框架删除临时仓库

执行入口：

```bash
go test ./internal/testcasecatalog
```

## `TESTAUTO-T-003` 人类可读用例文档生成和陈旧检查

- 级别：`unit`
- 覆盖需求：`TESTAUTO-R-007`
- 需求复核摘要：`sha256:cd6c1645cdf9ea27694d84edbb6c906a466bd3acc7adb963147a89f99f7da09f`
- Fixture：可生成 README 的临时用例 catalog
- 目标能力：`go`、`filesystem`
- 超时：`1m`
- 敏感数据：不使用敏感数据

前置条件：

- 无。

执行步骤：

- 生成 README、执行 --check，再注入一次手工漂移

可观察断言：

- README 包含生成来源、覆盖总览、断言、反例、清理和执行入口
- README 与 cases.yml 一致时 --check 通过

反例与故障路径：

- 手工修改生成 README 后 --check 必须失败

清理：

- Go 测试框架删除临时仓库

执行入口：

```bash
go test ./internal/testcasecatalog
```

## `TESTAUTO-T-004` 需求变更触发用例复核

- 级别：`unit`
- 覆盖需求：`TESTAUTO-R-008`
- 需求复核摘要：`sha256:5ac5cd27bd4e9a3828c722a52a56590588a2249ced5fb1164ed01496bf649d07`
- Fixture：已记录逐用例需求摘要的临时 catalog
- 目标能力：`go`、`sha256`、`filesystem`
- 超时：`1m`
- 敏感数据：不使用敏感数据

前置条件：

- 无。

执行步骤：

- 修改用例引用的需求正文并分别执行 --check 与 --print-digests

可观察断言：

- 摘要只由该用例引用的需求 ID、正文和验证方式计算
- --print-digests 能给出显式复核所需的新摘要但不改写 catalog

反例与故障路径：

- 修改需求正文而不更新摘要时 --check 必须失败并要求复核

清理：

- Go 测试框架删除临时仓库

执行入口：

```bash
go test ./internal/testcasecatalog
```
