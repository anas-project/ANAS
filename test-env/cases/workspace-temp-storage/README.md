<!-- Generated from cases.yml by cmd/gen-test-case-docs. DO NOT EDIT. -->

# Workspace 临时存储需求用例与 finance 验收

> 需求来源：[`workspace-temp-storage.md`](../../../dev-docs/requirements/workspace-temp-storage.md)
>
> 实施计划：[`workspace-temp-storage.md`](../../../dev-docs/plans/archived/workspace-temp-storage.md)
> 本文由同目录 `cases.yml` 生成；修改用例后运行 `go run ./cmd/gen-test-case-docs`。

## 覆盖总览

| 用例 ID | 级别 | 需求 ID | 实现 |
| --- | --- | --- | --- |
| `TEMP-T-001` | contract | `TEMP-R-002` | internal/config/temporary_path_test.go |
| `TEMP-T-002` | unit | `TEMP-R-001`、`TEMP-R-003`、`TEMP-R-004`、`TEMP-R-005`、`TEMP-R-006`、`TEMP-R-007` | internal/runner/temp_storage_test.go<br>internal/runner/temp_storage_linux_test.go |
| `TEMP-T-003` | contract | `TEMP-R-008`、`TEMP-R-009`、`TEMP-R-040` | internal/runner/temp_lifecycle_regression_test.go<br>internal/runner/temporary_manifest_test.go<br>internal/compose/endpoint_test.go |
| `TEMP-T-004` | unit | `TEMP-R-010`、`TEMP-R-011`、`TEMP-R-012`、`TEMP-R-023`、`TEMP-R-035`、`TEMP-R-043` | internal/runner/temp_lifecycle_regression_test.go<br>internal/runner/temp_storage_test.go<br>internal/runner/temp_storage_security_test.go |
| `TEMP-T-005` | unit | `TEMP-R-017`、`TEMP-R-019`、`TEMP-R-020`、`TEMP-R-022`、`TEMP-R-039` | internal/runner/temp_storage_test.go<br>internal/runner/temp_storage_linux_test.go<br>internal/runner/temp_storage_reconcile_test.go |
| `TEMP-T-006` | contract | `TEMP-R-021`、`TEMP-R-023`、`TEMP-R-024`、`TEMP-R-025` | internal/runner/temp_storage_test.go<br>internal/runner/temp_storage_security_test.go |
| `TEMP-T-008` | unit | `TEMP-R-013`、`TEMP-R-014`、`TEMP-R-015`、`TEMP-R-016`、`TEMP-R-033`、`TEMP-R-035`、`TEMP-R-036`、`TEMP-R-037`、`TEMP-R-038`、`TEMP-R-039`、`TEMP-R-041`、`TEMP-R-044`、`TEMP-R-045` | internal/runner/temp_lifecycle_regression_test.go<br>internal/runner/temp_switch_test.go<br>internal/runner/temp_switch_fault_test.go<br>internal/runner/temp_storage_reconcile_test.go<br>internal/runner/application_adapter_test.go |
| `TEMP-T-009` | review | `TEMP-R-042` | test-env/scripts/review-workspace-temp-stop.py |
| `TEMP-T-010` | contract | `TEMP-R-021`、`TEMP-R-024`、`TEMP-R-038` | internal/api/httpapi/temporary_storage_dto_test.go<br>internal/runner/status_projection_test.go<br>internal/runner/temporary_runtime_projection_test.go<br>internal/runner/runtime_status.go<br>internal/runner/temporary_runtime_endpoint_test.go<br>internal/application/temporary_status_test.go<br>web/src/api/lifecycle.test.ts<br>web/src/lifecycle/temporary-storage.test.ts<br>web/src/lifecycle/temporary-storage-test-environment.ts<br>web/vite.config.ts |
| `TEMP-T-011` | unit | `TEMP-R-005`、`TEMP-R-009`、`TEMP-R-014`、`TEMP-R-016`、`TEMP-R-017`、`TEMP-R-022`、`TEMP-R-041`、`TEMP-R-045` | test-env/scripts/test-workspace-temp-storage-e2e.py |
| `TEMP-T-012` | e2e | `TEMP-R-005`、`TEMP-R-006` | test-env/scripts/server-workspace-temp-storage-e2e.sh<br>test-env/scripts/server-workspace-temp-storage-e2e.py<br>test-env/scripts/server-workspace-temp-storage-extended-e2e.sh<br>test-env/scripts/server-workspace-temp-storage-extended-e2e.py |
| `TEMP-T-013` | e2e | `TEMP-R-009`、`TEMP-R-010`、`TEMP-R-011`、`TEMP-R-012` | test-env/scripts/server-workspace-temp-storage-e2e.sh<br>test-env/scripts/server-workspace-temp-storage-e2e.py |
| `TEMP-T-014` | e2e | `TEMP-R-013`、`TEMP-R-014`、`TEMP-R-015`、`TEMP-R-033`、`TEMP-R-037`、`TEMP-R-038` | test-env/scripts/server-workspace-temp-storage-e2e.sh<br>test-env/scripts/server-workspace-temp-storage-e2e.py |
| `TEMP-T-015` | e2e | `TEMP-R-016`、`TEMP-R-035`、`TEMP-R-044` | test-env/scripts/server-workspace-temp-storage-e2e.sh<br>test-env/scripts/server-workspace-temp-storage-e2e.py<br>internal/runner/application_adapter_test.go |
| `TEMP-T-016` | e2e | `TEMP-R-017`、`TEMP-R-019`、`TEMP-R-020`、`TEMP-R-022` | test-env/scripts/server-workspace-temp-storage-e2e.sh<br>test-env/scripts/server-workspace-temp-storage-e2e.py |
| `TEMP-T-017` | e2e | `TEMP-R-023`、`TEMP-R-024`、`TEMP-R-025` | test-env/scripts/server-workspace-temp-storage-extended-e2e.sh<br>test-env/scripts/server-workspace-temp-storage-extended-e2e.py |
| `TEMP-T-018` | e2e | `TEMP-R-026`、`TEMP-R-027` | test-env/scripts/server-workspace-temp-storage-extended-e2e.sh<br>test-env/scripts/server-workspace-temp-storage-extended-e2e.py<br>internal/runner/temp_imported_runtime_test.go |
| `TEMP-T-019` | e2e | `TEMP-R-041` | test-env/scripts/server-workspace-temp-storage-e2e.sh<br>test-env/scripts/server-workspace-temp-storage-e2e.py<br>test-env/scripts/server-workspace-temp-storage-extended-e2e.sh<br>test-env/scripts/server-workspace-temp-storage-extended-e2e.py |
| `TEMP-T-020` | e2e | `TEMP-R-019`、`TEMP-R-039`、`TEMP-R-043` | test-env/scripts/server-workspace-temp-storage-extended-e2e.sh<br>test-env/scripts/server-workspace-temp-storage-extended-e2e.py |
| `TEMP-T-021` | e2e | `TEMP-R-029`、`TEMP-R-030` | test-env/playwright/workspace-temp-storage-collabora.spec.mjs<br>test-env/playwright/workspace-temp-storage-collabora.config.mjs<br>test-env/playwright/workspace-temp-storage-collabora-focus.mjs<br>test-env/playwright/workspace-temp-storage-collabora-focus.test.mjs<br>test-env/playwright/workspace-temp-storage-collabora-cleanup.mjs<br>test-env/scripts/server-workspace-temp-collabora-action.py<br>test-env/scripts/server-workspace-temp-storage-editing-e2e.sh<br>test-env/scripts/server-workspace-temp-storage-editing-e2e.py<br>test-env/server-workspace-temp-storage-editing.yml.in<br>test-env/server-workspace-temp-storage-editing-images.txt<br>modules/collabora/hook/container_start_test.go<br>modules/nextcloud/hook/office_startup_test.go<br>modules/nextcloud/nextcloud/root/usr/local/bin/anas-office-activate.sh<br>modules/nextcloud/nextcloud/root/usr/local/bin/anas-entrypoint.sh<br>modules/nextcloud/nextcloud/root/usr/local/bin/task.sh<br>internal/runner/temporary_editing_fixture_test.go<br>modules/samba_dc/hook/structure_script_test.go<br>modules/samba_dc/samba_dc/root/usr/local/bin/structure.sh |
| `TEMP-T-022` | unit | `TEMP-R-010`、`TEMP-R-030` | modules/collabora/hook/container_start_test.go |
| `TEMP-T-023` | unit | `TEMP-R-005`、`TEMP-R-006`、`TEMP-R-023`、`TEMP-R-024`、`TEMP-R-026`、`TEMP-R-027`、`TEMP-R-029`、`TEMP-R-030`、`TEMP-R-039`、`TEMP-R-041`、`TEMP-R-043` | internal/runner/temporary_editing_fixture_test.go<br>test-env/server-workspace-temp-storage-editing.yml.in<br>test-env/scripts/test-workspace-temp-storage-extended-e2e.py<br>test-env/scripts/test-workspace-temp-collabora-action.py<br>test-env/scripts/test-workspace-temp-storage-editing-e2e.py<br>test-env/playwright/workspace-temp-storage-collabora-cleanup.mjs<br>test-env/playwright/workspace-temp-storage-collabora-cleanup.test.mjs |
| `TEMP-T-024` | e2e | `TEMP-R-016`、`TEMP-R-036`、`TEMP-R-045` | test-env/scripts/server-workspace-temp-storage-extended-e2e.sh<br>test-env/scripts/server-workspace-temp-storage-extended-e2e.py |

## `TEMP-T-001` 显式绝对路径配置与规范化

- 级别：`contract`
- 覆盖需求：`TEMP-R-002`
- 需求复核摘要：`sha256:1651bd35d45def7c4c685a57c9be7b9887419d7b47fbb3fa24736e64823018c3`
- 实现复核摘要：`sha256:b14d7762b424f71c30502798b9ab5964c4888ba33ecb844d974149b00149091e`
- Fixture：配置结构与绝对路径表
- 目标能力：`go`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`fault-injection`
- 有效性证据：相对路径、NUL和换行必须拒绝
- 超时：`10m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- 无。

执行步骤：

- 运行实现命令验证：显式外部绝对根按规范解析；清理冗余分隔符
- 执行反例：相对路径、NUL和换行必须拒绝

可观察断言：

- 显式外部绝对根按规范解析；清理冗余分隔符

反例与故障路径：

- 相对路径、NUL和换行必须拒绝

清理：

- 临时目录和子进程由测试清理；只读审阅不操作服务

执行入口：

```bash
go test ./internal/config -run TestTemporary -count=1
```

有效性验证入口：

```bash
go test ./internal/config -run TestTemporary -count=1
```

## `TEMP-T-002` 根边界、身份与缺失磁盘

- 级别：`unit`
- 覆盖需求：`TEMP-R-001`、`TEMP-R-003`、`TEMP-R-004`、`TEMP-R-005`、`TEMP-R-006`、`TEMP-R-007`
- 需求复核摘要：`sha256:a023d2a3b639395d51c107ccde1dc932d555f93d548a801abdaf4b2b99fb82c2`
- 实现复核摘要：`sha256:edb868ef98f70731cc9c4060835b572f85b65e326501fdbaae80c60457e09390`
- Fixture：临时workspace与文件系统探针，Linux描述符删除实测
- 目标能力：`go`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`fault-injection`
- 有效性证据：根替换与内容符号链接不得触及外部sentinel
- 超时：`10m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- 无。

执行步骤：

- 运行实现命令验证：根身份改变不分配或回退；克隆签发新身份；业务路径拒绝
- 执行反例：根替换与内容符号链接不得触及外部sentinel

可观察断言：

- 根身份改变不分配或回退；克隆签发新身份；业务路径拒绝

反例与故障路径：

- 根替换与内容符号链接不得触及外部sentinel

清理：

- 临时目录和子进程由测试清理；只读审阅不操作服务

执行入口：

```bash
go test ./internal/runner -run TestTemporary -count=1
```

有效性验证入口：

```bash
go test ./internal/runner -run TestTemporary -count=1
```

## `TEMP-T-003` 声明冻结、单容器与Module环境隔离

- 级别：`contract`
- 覆盖需求：`TEMP-R-008`、`TEMP-R-009`、`TEMP-R-040`
- 需求复核摘要：`sha256:54a8dc0ad1e9bef9429f8bde134974e89807dcdd0b299ef0a106c098a6bb2121`
- 实现复核摘要：`sha256:f010620a79bd5365ac11c2b5fc283bc6a3f4d5ffa8565c674f255a7e314ff470`
- Fixture：合成Module manifest和渲染Compose
- 目标能力：`go`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`fault-injection`
- 有效性证据：缺字段、未知键、共享副本、重叠mount、伪造环境均拒绝
- 超时：`10m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- 无。

执行步骤：

- 运行实现命令验证：只有声明service获得长格式bind；源使用占位符且create_host_path=false；声明冻结
- 执行反例：缺字段、未知键、共享副本、重叠mount、伪造环境均拒绝

可观察断言：

- 只有声明service获得长格式bind；源使用占位符且create_host_path=false；声明冻结

反例与故障路径：

- 组可写0770/0775声明在校验时拒绝，历史冻结声明也不能绕过启动前预检
- 缺字段、未知键、共享副本、重叠mount、伪造环境均拒绝

清理：

- 临时目录和子进程由测试清理；只读审阅不操作服务

执行入口：

```bash
go test ./internal/runner -run TestTemporary -count=1
go test ./internal/compose -run TestTemporary -count=1
```

有效性验证入口：

```bash
go test ./internal/runner -run TestTemporary -count=1
go test ./internal/compose -run TestTemporary -count=1
```

## `TEMP-T-004` 先登记、权限与预检

- 级别：`unit`
- 覆盖需求：`TEMP-R-010`、`TEMP-R-011`、`TEMP-R-012`、`TEMP-R-023`、`TEMP-R-035`、`TEMP-R-043`
- 需求复核摘要：`sha256:c9fe3a45177acd7ff7ca31af6907e654c6c6e2e30ec521674074e016a7f83767`
- 实现复核摘要：`sha256:cd208be2311053ead2d52af58a158d21be7077e68b7a0b76d7ad51a514ece7ce`
- Fixture：持久registry和Docker观察替身、真实目录权限
- 目标能力：`go`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`fault-injection`
- 有效性证据：UID/GID或容量失败、可读marker、marker硬链接和symlink拒绝
- 超时：`10m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- 无。

执行步骤：

- 运行实现命令验证：启动前已有durable租约；分配和GC共用workspace锁；预检失败无启动或停止
- 执行反例：UID/GID或容量失败、可读marker、marker硬链接和symlink拒绝

可观察断言：

- 定向启动不受未选中模块临时盘故障影响；定向重启在停止前拒绝选中模块的容量不足及非法冻结权限
- 启动前已有durable租约；分配和GC共用workspace锁；预检失败无启动或停止

反例与故障路径：

- UID/GID或容量失败、可读marker、marker硬链接和symlink拒绝

清理：

- 临时目录和子进程由测试清理；只读审阅不操作服务

执行入口：

```bash
go test ./internal/runner -run TestTemporary -count=1
```

有效性验证入口：

```bash
go test ./internal/runner -run TestTemporary -count=1
```

## `TEMP-T-005` 实际引用、异常对账与GC

- 级别：`unit`
- 覆盖需求：`TEMP-R-017`、`TEMP-R-019`、`TEMP-R-020`、`TEMP-R-022`、`TEMP-R-039`
- 需求复核摘要：`sha256:d757887644110801539512c0c1789e6c1668dab956c8027bf946cf34e3d5b5cc`
- 实现复核摘要：`sha256:3d4daf80e88b26d1cf609db3d54f1aa27186c2141e453df7d7764f1fa03146bd`
- Fixture：运行/停止容器inventory、host mount和Linux目录FD
- 目标能力：`go`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`fault-injection`
- 有效性证据：Docker失败、错daemon、路径替换、未提交transition均保留
- 超时：`10m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- 无。

执行步骤：

- 运行实现命令验证：当前和历史根仅回收已核验lease；GC重新inventory；显式workspace和锁强制
- 执行反例：Docker失败、错daemon、路径替换、未提交transition均保留

可观察断言：

- 当前和历史根仅回收已核验lease；GC重新inventory；显式workspace和锁强制
- 删除后登记前中断，仅在原根/祖先身份不变、叶ENOENT及同daemon无引用时登记deleted；dry-run不写

反例与故障路径：

- Docker失败、错daemon、路径替换、未提交transition均保留
- 根/祖先缺失、symlink或替换、引用未知不能把released丢失目录误记deleted

清理：

- 临时目录和子进程由测试清理；只读审阅不操作服务

执行入口：

```bash
go test ./internal/runner -run TestTemporary -count=1
```

有效性验证入口：

```bash
go test ./internal/runner -run TestTemporary -count=1
```

## `TEMP-T-006` 容量状态、恢复与GC只读预览

- 级别：`contract`
- 覆盖需求：`TEMP-R-021`、`TEMP-R-023`、`TEMP-R-024`、`TEMP-R-025`
- 需求复核摘要：`sha256:703635d6fc38345b42924152c78a2a6634ad131952da94f16434e5765bf9ccd9`
- 实现复核摘要：`sha256:c5538390deb2c9eb7264a029d1bf588f5588cc41afa16b2ac7d40bd377e02924`
- Fixture：容量探针可切换与真实registry、文本formatter
- 目标能力：`go`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`fault-injection`
- 有效性证据：低容量不分配；查询失败不当空闲；活动内容不删除
- 超时：`10m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- 无。

执行步骤：

- 运行实现命令验证：低空间显示问题，恢复清除；Btrfs inode为不适用；dry-run不写registry且与对账候选一致
- 执行反例：低容量不分配；查询失败不当空闲；活动内容不删除

可观察断言：

- 低空间显示问题，恢复清除；Btrfs inode为不适用；dry-run不写registry且与对账候选一致

反例与故障路径：

- 低容量不分配；查询失败不当空闲；活动内容不删除

清理：

- 临时目录和子进程由测试清理；只读审阅不操作服务

执行入口：

```bash
go test ./internal/runner -run TestTemporary -count=1
```

有效性验证入口：

```bash
go test ./internal/runner -run TestTemporary -count=1
```

## `TEMP-T-008` 全量切换、不可变历史与失败恢复

- 级别：`unit`
- 覆盖需求：`TEMP-R-013`、`TEMP-R-014`、`TEMP-R-015`、`TEMP-R-016`、`TEMP-R-033`、`TEMP-R-035`、`TEMP-R-036`、`TEMP-R-037`、`TEMP-R-038`、`TEMP-R-039`、`TEMP-R-041`、`TEMP-R-044`、`TEMP-R-045`
- 需求复核摘要：`sha256:d8f76774e4a2f1ae1daaf651b5f6912f655856bcf4ad10929966f10d56b451d9`
- 实现复核摘要：`sha256:3f0278c3e866be1fd7b885275786473fd8d5b2eb5404e7b1b6b2026343fe64bc`
- Fixture：冻结deployment、切换journal与故障注入Compose
- 目标能力：`go`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`fault-injection`
- 有效性证据：未知phase、pending before_stop、candidate引用未释放均不得盲目恢复
- 超时：`10m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- 无。

执行步骤：

- 运行实现命令验证：全停启预览绑定digest；历史clone保持源制品且重建授权；停止与恢复阶段持久化
- 执行反例：未知phase、pending before_stop、candidate引用未释放均不得盲目恢复

可观察断言：

- 已提交切换清理失败保留cleanup_deferred及旧租约，后续stop/start/restart/apply和下一次切换可继续；未核验目标挂载仍拒绝恢复
- 全停启预览绑定digest；历史clone保持源制品且重建授权；停止与恢复阶段持久化
- 候选停止只释放候选deployment；旧root held租约仍active并由旧启动复用原Path和哨兵
- 真实 Main apply JSON 保留 down exit98、phase、project 首因，previous_restore 成功或失败分别报告，adapter保留既有退出码

反例与故障路径：

- 未知phase、pending before_stop、candidate引用未释放均不得盲目恢复
- candidate停止不能跨root释放旧租约；删除与登记提交中断只能按已证明的absence恢复
- Main 或共享应用 adapter 不得吞掉 primary/recovery detail；补偿成功不能覆盖停止首因

清理：

- 临时目录和子进程由测试清理；只读审阅不操作服务

执行入口：

```bash
go test ./internal/runner -run TestTemporary -count=1
go test ./internal/runner -run TestApplicationCLIError -count=1
```

有效性验证入口：

```bash
go test ./internal/runner -run TestTemporary -count=1
```

## `TEMP-T-009` 正常停止调用链人工审阅

- 级别：`review`
- 覆盖需求：`TEMP-R-042`
- 需求复核摘要：`sha256:4e1179375bc71c56b4771a889d224a09ffa501480bb1a4fb526068c2727198b2`
- 实现复核摘要：`sha256:253cca01294930248a7cc244697edbc0de9378f0f7edeb92b0d1a47bccb66642`
- Fixture：当前正常生命周期、切换和Collabora入口源代码
- 目标能力：`python`
- Oracle 来源：`filesystem`
- 有效性证明：`manual`
- 人工复核理由：正常停止契约涉及应用信号与现有Hook调用链；只读脚本定位当前源代码，人工核对调用责任；实际保存仍由TEMP-T-021实机证明。
- 超时：`10m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- 无。

执行步骤：

- 运行实现命令验证：停机复用before_stop/Compose down；180s等待；Go Hook exec使coolwsd接收信号
- 执行反例：不能把健康探针或保存日志替代正常停止语义

可观察断言：

- 停机复用before_stop/Compose down；180s等待；Go Hook exec使coolwsd接收信号

反例与故障路径：

- 不能把健康探针或保存日志替代正常停止语义

清理：

- 临时目录和子进程由测试清理；只读审阅不操作服务

执行入口：

```bash
python3 test-env/scripts/review-workspace-temp-stop.py
```

## `TEMP-T-010` CLI HTTP与控制台状态和切换预览

- 级别：`contract`
- 覆盖需求：`TEMP-R-021`、`TEMP-R-024`、`TEMP-R-038`
- 需求复核摘要：`sha256:7af4176adcc00e48caae128097f81bcb4e32005dae13d790c5dffbc20fdd96cb`
- 实现复核摘要：`sha256:aaa59f53f48444269ea9f3e61a5fcfaee0dc31a89701b5203e4ba19f4542f2b7`
- Fixture：DTO与Vue实际渲染、真实InspectRuntime冻结制品及Docker endpoint子进程、部分runtime probe失败
- 目标能力：`go`、`node`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`fault-injection`
- 有效性证据：宿主路径不进入HTTP；健康成功不能掩盖storage unknown；HTML文本转义
- 超时：`10m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- 无。

执行步骤：

- 运行实现命令验证：低空间覆盖healthy；按applied根检查；每个声明租约缺失均显示；失败保留已取得Module信息
- 真实InspectRuntime的Compose检测及ps保留进程Docker endpoint、context/config/TLS及空/未设区别，拒绝Module选择项和ambient Secret；取消仍生效
- 正常Linux context/info/全量ps返回有效响应并核验精确4/5次调用顺序；非Linux存储不支持时只查询Compose，未知调用拒绝
- 执行反例：宿主路径不进入HTTP；健康成功不能掩盖storage unknown；HTML文本转义

可观察断言：

- 低空间覆盖healthy；按applied根检查；每个声明租约缺失均显示；失败保留已取得Module信息
- 私有进程endpoint真实被查询，workspace或Module环境不能改指默认或其它daemon；未设项保持默认选择语义
- 刷新成功清除旧查询错误；A→B→A切换后旧响应和失败不覆盖当前状态或解除新查询加载状态

反例与故障路径：

- 宿主路径不进入HTTP；健康成功不能掩盖storage unknown；HTML文本转义
- 受限查询丢失进程endpoint、Module覆盖Docker选择项或继承ambient Secret时拒绝通过；已取消上下文不能继续查询
- 旧workspace查询延迟成功或失败时，不展示旧Module、不显示旧错误且保持当前查询加载状态

清理：

- 临时目录和子进程由测试清理；只读审阅不操作服务

执行入口：

```bash
go test ./internal/api/httpapi ./internal/application ./internal/runner -run 'TestTemporary|TestStatus|TestReadOnlyQueryPayloads' -count=1
npm run test:workspace-temp-storage-ui
```

有效性验证入口：

```bash
go test ./internal/api/httpapi ./internal/application ./internal/runner -run 'TestTemporary|TestStatus|TestReadOnlyQueryPayloads' -count=1
npm run test:workspace-temp-storage-ui
```

## `TEMP-T-011` 远端核心判据反例

- 级别：`unit`
- 覆盖需求：`TEMP-R-005`、`TEMP-R-009`、`TEMP-R-014`、`TEMP-R-016`、`TEMP-R-017`、`TEMP-R-022`、`TEMP-R-041`、`TEMP-R-045`
- 需求复核摘要：`sha256:d3019f8345a0f9204c7132fa398ee69d96987c0f217db023203d0db7970f5130`
- 实现复核摘要：`sha256:2b0e47d773d6bc01037d5ff32aca6056087fe333b126daac6bfff5a57a6f6d3a`
- Fixture：Python隔离目录、内存反例和合成Compose shim，不调用Docker或真实mount
- 目标能力：`python`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`fault-injection`
- 有效性证据：生产socket、跨run、恢复结果混淆、宿主bind误判、公开诊断泄露和失败清理假成功均有本地反例
- 超时：`10m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- 无。

执行步骤：

- 运行实现命令验证：路径/socket、重建和顺序；down一次失败保留首因/独立恢复；无Docker引用的宿主bind保留/卸载回收
- 执行反例：生产socket、跨run、错误恢复/挂载inode、仍有Docker引用和诊断symlink拒绝；清理非零不遮蔽首因

可观察断言：

- 隔离路径/socket、重建、mount和顺序拒绝错误输入；down shim只失败一次且实际旧挂载保持
- 私有namespace重复核验、mountinfo与inode身份；Docker引用不为空不得执行宿主挂载GC；卸载后才删除租约且保留源哨兵
- 原始故障/超时诊断以0700/0600保留；公开错误allowlist；诊断写失败不覆盖首因；每个清理停止均尝试并记录失败
- 历史rollback必须实际调用支持的CLI参数；旧deployment、旧租约或被改写历史均不能作为成功结果
- host bind只接受精确释放拒绝；清理只跳过成功清单证明已不存在的容器，存在须核验完整ID/project/working_dir并按ID停止，未知或异主拒绝

反例与故障路径：

- 生产socket、跨run路径、共享挂载、未重建或错序拒绝
- stop_failed首因、down98项目、previous_restore成功任一缺失拒绝；错误inode、缺host_mount_reference/owner或哨兵变化拒绝
- 诊断路径symlink、异常CLI error.code和超时原文不得进入公开报告；停止失败不得宣称清理完成

清理：

- 临时目录和子进程由测试清理；只读审阅不操作服务

执行入口：

```bash
python3 test-env/scripts/test-workspace-temp-storage-e2e.py
```

有效性验证入口：

```bash
python3 test-env/scripts/test-workspace-temp-storage-e2e.py
```

## `TEMP-T-012` 真实路径攻击与外盘身份

- 级别：`e2e`
- 覆盖需求：`TEMP-R-005`、`TEMP-R-006`
- 需求复核摘要：`sha256:5da68417d32ddbe442827f5cc8c364963602cccaf11a277133ebc70bbad28551`
- 实现复核摘要：`sha256:55d9a510e6faf02bdd275080d3f565eb1c7ae6fe97881c20d0aa0610277b812e`
- Fixture：本run外部ext4 loopback及独立workspace
- 目标能力：`linux`、`isolated-docker`、`python`
- Oracle 来源：`runtime`、`filesystem`、`report`
- 有效性证明：`counterexample`
- 有效性证据：缺盘同名目录不得成为替代位置；不清未登记文件
- 超时：`90m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- ssh whl@finance.hlong.wang；独立run目录、Docker/containerd/net/socket/端口；串行执行
- 源码摘要和run-id必填；磁盘故障需与两个daemon共享私有mount namespace

执行步骤：

- 运行实现命令验证：symlink/替换保留外部sentinel；卸盘和换盘拒绝；原盘重挂身份稳定
- 执行反例：缺盘同名目录不得成为替代位置；不清未登记文件

可观察断言：

- symlink/替换保留外部sentinel；卸盘和换盘拒绝；原盘重挂身份稳定
- 历史外盘缺失的rollback预检及原盘重挂后的新租约由同一filesystem阶段的TEMP-T-019独立核验

反例与故障路径：

- 缺盘同名目录不得成为替代位置；不清未登记文件

清理：

- 成功停止并释放仅本run测试容器、受管目录和loopback；保留0600脱敏报告
- 失败尝试停止本run消费者，保留未能证明可清理的目录、loopback和恢复材料并报告结果

执行入口：

```bash
bash test-env/scripts/server-workspace-temp-storage-extended-e2e.sh filesystem
```

有效性验证入口：

```bash
python3 test-env/scripts/test-workspace-temp-storage-e2e.py
```

## `TEMP-T-013` 真实声明挂载与权限隔离

- 级别：`e2e`
- 覆盖需求：`TEMP-R-009`、`TEMP-R-010`、`TEMP-R-011`、`TEMP-R-012`
- 需求复核摘要：`sha256:59add12fcc4dd9e5e30b159606060d7fea7116d881c99252624eb117c6ccf65f`
- 实现复核摘要：`sha256:8af8d3f1d9e8ea85aca0f7e870acd992830e4e25d0fecd8b6b12a6cde17f74e5`
- Fixture：三个真实Compose Module，两个受管消费者和一个无声明Module
- 目标能力：`linux`、`isolated-docker`、`python`
- Oracle 来源：`runtime`、`filesystem`、`report`
- 有效性证明：`counterexample`
- 有效性证据：错误UID/GID不得以0777绕过；非声明Module无临时bind
- 超时：`90m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- ssh whl@finance.hlong.wang；独立run目录、Docker/containerd/net/socket/端口；串行执行
- 源码摘要和run-id必填；磁盘故障需与两个daemon共享私有mount namespace

执行步骤：

- 运行实现命令验证：实际3个目录隔离；非root容器可写；所有权750精确；状态对应实际mount
- 执行反例：错误UID/GID不得以0777绕过；非声明Module无临时bind

可观察断言：

- 实际3个目录隔离；非root容器可写；所有权750精确；状态对应实际mount

反例与故障路径：

- 错误UID/GID不得以0777绕过；非声明Module无临时bind

清理：

- 成功停止并释放仅本run测试容器、受管目录和loopback；保留0600脱敏报告
- 失败尝试停止本run消费者，保留未能证明可清理的目录、loopback和恢复材料并报告结果

执行入口：

```bash
bash test-env/scripts/server-workspace-temp-storage-e2e.sh core
```

有效性验证入口：

```bash
python3 test-env/scripts/test-workspace-temp-storage-e2e.py
```

## `TEMP-T-014` 依赖顺序全量重建及旧根清理

- 级别：`e2e`
- 覆盖需求：`TEMP-R-013`、`TEMP-R-014`、`TEMP-R-015`、`TEMP-R-033`、`TEMP-R-037`、`TEMP-R-038`
- 需求复核摘要：`sha256:3a0f287d43ec620fa96a6b14004ad716acc0f6d2802e7ff712ce5f08327befbb`
- 实现复核摘要：`sha256:8af8d3f1d9e8ea85aca0f7e870acd992830e4e25d0fecd8b6b12a6cde17f74e5`
- Fixture：Docker events与不可变制品摘要、未受管sentinel
- 目标能力：`linux`、`isolated-docker`、`python`
- Oracle 来源：`runtime`、`filesystem`、`report`
- 有效性证明：`counterexample`
- 有效性证据：不复制临时sentinel；未受管根文件保留；同路径文本不触发额外重启
- 超时：`90m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- ssh whl@finance.hlong.wang；独立run目录、Docker/containerd/net/socket/端口；串行执行
- 源码摘要和run-id必填；磁盘故障需与两个daemon共享私有mount namespace

执行步骤：

- 运行实现命令验证：逆序destroy/正序start包含无声明Module；全部ID更新；旧artifact不变；旧树释放后删除
- 执行反例：不复制临时sentinel；未受管根文件保留；同路径文本不触发额外重启

可观察断言：

- 逆序destroy/正序start包含无声明Module；全部ID更新；旧artifact不变；旧树释放后删除

反例与故障路径：

- 不复制临时sentinel；未受管根文件保留；同路径文本不触发额外重启

清理：

- 成功停止并释放仅本run测试容器、受管目录和loopback；保留0600脱敏报告
- 失败尝试停止本run消费者，保留未能证明可清理的目录、loopback和恢复材料并报告结果

执行入口：

```bash
bash test-env/scripts/server-workspace-temp-storage-e2e.sh core
```

有效性验证入口：

```bash
python3 test-env/scripts/test-workspace-temp-storage-e2e.py
```

## `TEMP-T-015` 预检拒绝与停止启动失败补偿

- 级别：`e2e`
- 覆盖需求：`TEMP-R-016`、`TEMP-R-035`、`TEMP-R-044`
- 需求复核摘要：`sha256:5951ef7be29e4231934b04d8cd37c88d377273473ccf89a203bcbc34df624061`
- 实现复核摘要：`sha256:03ab89d3f44f1ba0518b138f1c574bebb0b70026fed401e3014a80450c07fccc`
- Fixture：真实Compose one-shot up/down故障及无关unhealthy容器
- 目标能力：`linux`、`isolated-docker`、`python`、`go`
- Oracle 来源：`runtime`、`filesystem`、`report`
- 有效性证明：`counterexample`
- 有效性证据：真实shim故障一次及首因/独立恢复判据有本地反例；恢复须核对实际旧active、mount和sentinel
- 超时：`90m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- ssh whl@finance.hlong.wang；独立run目录、Docker/containerd/net/socket/端口；串行执行
- 源码摘要和run-id必填；磁盘故障需与两个daemon共享私有mount namespace

执行步骤：

- 运行实现命令验证：目标无效时ID保持；up/down故障恢复实际旧mount/内容；down保留stop_failed首因并独立报告previous_restore成功；原有健康问题不阻止切换
- 执行反例：down98必须来自consumer项目且只失败一次；恢复不能替代首因；旧active/挂载/哨兵/运行任一不符拒绝

可观察断言：

- 目标无效时ID保持；up/down故障恢复实际旧mount/内容；stop_failed首因与previous_restore成功独立；原有健康问题不额外阻止切换

反例与故障路径：

- 故障必须产生非零且恢复旧sentinel；健康失败不能新增回滚门槛
- 首因被恢复输出覆盖、down phase/项目/exit98不符或previous_restore不成功均拒绝

清理：

- 成功停止并释放仅本run测试容器、受管目录和loopback；保留0600脱敏报告
- 失败尝试停止本run消费者，保留未能证明可清理的目录、loopback和恢复材料并报告结果

执行入口：

```bash
bash test-env/scripts/server-workspace-temp-storage-e2e.sh core
go test ./internal/runner -run TestTemporaryCLIApplyJSON -count=1
```

有效性验证入口：

```bash
python3 test-env/scripts/test-workspace-temp-storage-e2e.py
```

## `TEMP-T-016` 容器及宿主挂载引用阻止GC

- 级别：`e2e`
- 覆盖需求：`TEMP-R-017`、`TEMP-R-019`、`TEMP-R-020`、`TEMP-R-022`
- 需求复核摘要：`sha256:019d0e3ad52afec9d7b1dae6c76671e7897648ff9e181518aee1ebdc999a9145`
- 实现复核摘要：`sha256:8af8d3f1d9e8ea85aca0f7e870acd992830e4e25d0fecd8b6b12a6cde17f74e5`
- Fixture：运行/停止容器、不可用endpoint和私有namespace内登记树的残留host bind
- 目标能力：`linux`、`isolated-docker`、`python`
- Oracle 来源：`runtime`、`filesystem`、`report`
- 有效性证明：`counterexample`
- 有效性证据：仅进程退出/空Docker inventory不能授权删除；host blocker、同盘bind身份和卸载后回收顺序有本地反例
- 超时：`90m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- ssh whl@finance.hlong.wang；独立run目录、Docker/containerd/net/socket/端口；串行执行
- 源码摘要和run-id必填；磁盘故障需与两个daemon共享私有mount namespace

执行步骤：

- 运行实现命令验证：运行/停止bind及query失败保留；Docker容器清空后host bind仍阻止GC；卸载后回收租约且保留源哨兵
- 执行反例：仅进程退出和无Docker容器不能证明释放；缺socket、错误namespace/mountinfo/inode不可授权清理

可观察断言：

- 正常down后host bind仍在时必须返回producer的精确stop_failed释放拒绝；Docker零容器也不得GC，卸载后先安全release再GC删除原树
- 运行与停止bind仍持引用均保留；query失败保留；显式-w限制
- 当前/holder/daemon共享private mount namespace；清空Docker后实际host bind、owner和sentinel保留；卸载后显式GC只删除登记树

反例与故障路径：

- 仅进程退出不能视为释放；缺失Docker socket不可授权清理
- 同文件系统bind必须以mountinfo和inode证明；没有host_mount_reference阻碍或仍有Docker容器不能算宿主挂载场景通过

清理：

- 成功停止并释放仅本run测试容器、受管目录和loopback；保留0600脱敏报告
- 失败尝试停止本run消费者，保留未能证明可清理的目录、loopback和恢复材料并报告结果

执行入口：

```bash
bash test-env/scripts/server-workspace-temp-storage-e2e.sh core
```

有效性验证入口：

```bash
python3 test-env/scripts/test-workspace-temp-storage-e2e.py
```

## `TEMP-T-017` 字节和inode耗尽及运行恢复

- 级别：`e2e`
- 覆盖需求：`TEMP-R-023`、`TEMP-R-024`、`TEMP-R-025`
- 需求复核摘要：`sha256:20649bb9d937b0a4d35d1119ca4626fd01d8b93e797647aefe5d477a876659b8`
- 实现复核摘要：`sha256:4d26a0f0df1f756a26d607fa44faf9567406089c16881bc81659336bfabf5732`
- Fixture：192MiB ext4少inode loopback，私有mount namespace
- 目标能力：`linux`、`isolated-docker`、`python`
- Oracle 来源：`runtime`、`filesystem`、`report`
- 有效性证明：`counterexample`
- 有效性证据：不足拒绝新使用者且现有sentinel不删除；健康probe不掩盖存储失败
- 超时：`90m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- ssh whl@finance.hlong.wang；独立run目录、Docker/containerd/net/socket/端口；串行执行
- 源码摘要和run-id必填；磁盘故障需与两个daemon共享私有mount namespace

执行步骤：

- 运行实现命令验证：实际填满字节与inode出现问题；释放测试填充后清除；恢复期间容器ID不变
- 执行反例：不足拒绝新使用者且现有sentinel不删除；健康probe不掩盖存储失败

可观察断言：

- 实际填满字节与inode出现问题；释放测试填充后清除；恢复期间容器ID不变

反例与故障路径：

- 不足拒绝新使用者且现有sentinel不删除；健康probe不掩盖存储失败

清理：

- 成功停止并释放仅本run测试容器、受管目录和loopback；保留0600脱敏报告
- 失败尝试停止本run消费者，保留未能证明可清理的目录、loopback和恢复材料并报告结果

执行入口：

```bash
bash test-env/scripts/server-workspace-temp-storage-extended-e2e.sh filesystem
```

有效性验证入口：

```bash
python3 test-env/scripts/test-workspace-temp-storage-extended-e2e.py
```

## `TEMP-T-018` 四种备份、快照及克隆隔离

- 级别：`e2e`
- 覆盖需求：`TEMP-R-026`、`TEMP-R-027`
- 需求复核摘要：`sha256:1b70486ef4aa52d6fb245fa366c86d0da057597fe3fb84482e40e1dae8f723b3`
- 实现复核摘要：`sha256:2d8b7c05edc78cecfc8279c127e9e7c73eb82709e827f1bb2a5ce130be9b0624`
- Fixture：本run Btrfs loopback、workspace/tmp 实际挂载、真实业务和临时sentinel
- 目标能力：`linux`、`isolated-docker`、`python`、`go`
- Oracle 来源：`runtime`、`filesystem`、`report`
- 有效性证明：`counterexample`
- 有效性证据：有效显式绝对workspace/tmp才能进入备份；新目标真实空init后才restore且无源生命周期调用；初始化、结构校验和快照字段反例严格拒绝；源active租约不可复用，目标外盘重新验证
- 超时：`90m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- ssh whl@finance.hlong.wang；独立run目录、Docker/containerd/net/socket/端口；串行执行
- 源码摘要和run-id必填；磁盘故障需与两个daemon共享私有mount namespace

执行步骤：

- 显式配置 workspace/tmp 绝对路径并核对实际挂载，在备份源内写临时sentinel，避免外部目录自然不在备份范围造成假通过
- snapshot 使用默认暂停备份，send/send-file/copy 使用 no-stop；每次备份后原容器ID、deployment、workspace身份、租约路径、临时内容和运行状态保持
- restore目标先核验本run范围内不存在且无符号链接，创建0700空目录并真实空init；确认.anas及无temp/active授权，再restore并要求结构校验成功；不复用目标
- 运行实现命令验证：snapshot/send/send-file/copy恢复业务；lease内容排除；clone新身份和bind且源不变
- 通过真实Main配置apply回归，核验无registry的外来active先脱离源运行授权，源运行或无容器均不执行源Hook/down/up，目标生成新部署和租约
- 执行反例：实际挂载仍在外部根时拒绝；不可复用源active租约或清源目录；目标外盘重新验证

可观察断言：

- 源实际挂载位于 workspace/tmp，snapshot/send/send-file/copy恢复业务并排除源范围内临时sentinel和租约；clone新身份和bind且源不变
- snapshot 暂停复启必须仍使用原容器和有效租约，不能通过重新分配目录掩盖恢复核验失败
- 目标init和restore不控制源容器或租约；每次还原后完整源身份、容器、active、挂载、运行和内容保持
- 每次目标apply/stop/GC及完整copytree clone前后立即核对完整源baseline；不能依靠下一轮备份间接发现变化
- workspace快照须读取实际snapshot.id/label/complete且problems为空，再restore启动并核验新租约和业务内容
- 快照create前后原IDs/active/identity/root/租约/内容保持；restore有意重建并保持本地identity/历史active/root，全新ID/租约等于实际bind且不恢复历史临时内容；显式temp gc删除旧树并保持新IDs/bind/owner marker
- 无源registry不能授予源project运行权限；导入生命周期/历史预览及直接入口要求本地配置apply，源ID/active/租约/内容保持；同workspace快照和有效本地租约不脱离

反例与故障路径：

- 不可复用源active租约或清源目录；目标外盘重新验证，symlink必须为temp_preflight_failed且源ID保持，usage不得冒充拒绝
- 源实际挂载位于外部根时不得继续备份或记录排除通过；空字符串不得冒充合法显式路径
- init失败、目标无.anas或已有temp/active授权不能restore；结构verify失败或快照字段错误不能记为通过
- 受管临时制品缺冻结env、缺DATA_PATH或绑定非法必须拒绝；无temp旧制品兼容不得成为受管制品旁路
- 源被重建但Running和哨兵内容相同也必须拒绝clone通过；快照恢复旧ID、外来active/identity、遗漏登记或历史内容均拒绝

清理：

- 成功停止并释放仅本run测试容器、受管目录和loopback；保留0600脱敏报告
- 失败尝试停止本run消费者，保留未能证明可清理的目录、loopback和恢复材料并报告结果

执行入口：

```bash
bash test-env/scripts/server-workspace-temp-storage-extended-e2e.sh backup
go test ./internal/runner -run 'TestTemporaryImported|TestTemporaryWorkspaceBinding' -count=1
```

有效性验证入口：

```bash
python3 test-env/scripts/test-workspace-temp-storage-extended-e2e.py
go test ./internal/runner -run 'TestTemporaryImported|TestTemporaryWorkspaceBinding' -count=1
```

## `TEMP-T-019` 历史配置A B回滚与历史外盘缺失

- 级别：`e2e`
- 覆盖需求：`TEMP-R-041`
- 需求复核摘要：`sha256:4b94e78e6788a21b0763398f08f28a3d6d64ee005fc6c180a4d432a2188a8619`
- 实现复核摘要：`sha256:adddd85d0b8b4d4148861edc6ae5de56ffd9f0cd2ecbd9cfcff6001cef2bb3ce`
- Fixture：历史冻结deployment、已清理的A目录及本run可卸载外盘
- 目标能力：`linux`、`isolated-docker`、`python`
- Oracle 来源：`runtime`、`filesystem`、`report`
- 有效性证明：`counterexample`
- 有效性证据：真实CLI不传-y；当前运行→卸载→预检拒绝保持→原盘重挂→新租约回滚顺序有反例，已释放内容不可复用
- 超时：`90m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- ssh whl@finance.hlong.wang；独立run目录、Docker/containerd/net/socket/端口；串行执行
- 源码摘要和run-id必填；磁盘故障需与两个daemon共享私有mount namespace

执行步骤：

- 运行实现命令验证：B回滚历史A生成新的deployment与目录，原历史摘要保持
- 当前rootA运行时卸载历史外盘，真实 rollback 要以 temp_preflight_failed 拒绝并保留IDs/active/内容；原盘重挂后回滚新deployment和新租约
- 执行反例：已释放A租约和临时sentinel不可恢复或复用

可观察断言：

- B回滚历史A生成新的deployment与目录，原历史摘要保持
- 历史外盘缺失不得停止当前运行实例；原盘重挂后使用新租约且不恢复历史临时sentinel

反例与故障路径：

- 已释放A租约和临时sentinel不可恢复或复用
- 缺少真实rollback调用、传不支持的-y造成usage拒绝或改变当前ID/active/哨兵，均不能记录历史缺盘通过

清理：

- 成功停止并释放仅本run测试容器、受管目录和loopback；保留0600脱敏报告
- 失败尝试停止本run消费者，保留未能证明可清理的目录、loopback和恢复材料并报告结果

执行入口：

```bash
bash test-env/scripts/server-workspace-temp-storage-e2e.sh core
bash test-env/scripts/server-workspace-temp-storage-extended-e2e.sh filesystem
```

有效性验证入口：

```bash
python3 test-env/scripts/test-workspace-temp-storage-e2e.py
python3 test-env/scripts/test-workspace-temp-storage-extended-e2e.py
```

## `TEMP-T-020` 阶段中断与并发互斥

- 级别：`e2e`
- 覆盖需求：`TEMP-R-019`、`TEMP-R-039`、`TEMP-R-043`
- 需求复核摘要：`sha256:37d7276925c82a26cbd52a180554f5fe7807ec7c9db84f2b382c71e6ced9372f`
- 实现复核摘要：`sha256:4df88708db2adf1674748c8f9908d09145d27df7bc26f2bc05cd4590970a2dcf`
- Fixture：真实CLI SIGKILL与Docker边界阻塞、并发GC/lifecycle
- 目标能力：`linux`、`isolated-docker`、`python`
- Oracle 来源：`runtime`、`filesystem`、`report`
- 有效性证明：`counterexample`
- 有效性证据：未对账GC必须拒绝并保持旧sentinel；不能凭phase猜已释放
- 超时：`90m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- ssh whl@finance.hlong.wang；独立run目录、Docker/containerd/net/socket/端口；串行执行
- 源码摘要和run-id必填；磁盘故障需与两个daemon共享私有mount namespace

执行步骤：

- 运行实现命令验证：停止、启动、precommit、partialcleanup中断后按真实引用对账；另CLI等待同一锁
- 执行反例：未对账GC必须拒绝并保持旧sentinel；不能凭phase猜已释放

可观察断言：

- 停止、启动、precommit、partialcleanup中断后按真实引用对账；另CLI等待同一锁
- 未提交GC必须为temp_recovery_required/4；committed partialcleanup GC须ok/0且实际删除旧树，再启动保持新运行；捕获真实JSON而不是只看非零
- 选中阻塞shim先核验直接CLI父进程/session及继承组，再仅对自身setpgid；记录并复核PPID/starttime/session/独立PGID，SIGKILL不误杀其它组

反例与故障路径：

- 未对账GC必须拒绝并保持旧sentinel；不能凭phase猜已释放
- usage、Compose缺失或未知Docker错误不得冒充未提交恢复拒绝；成功返回但旧树仍在不得作为已提交清理证据

清理：

- 成功停止并释放仅本run测试容器、受管目录和loopback；保留0600脱敏报告
- 失败尝试停止本run消费者，保留未能证明可清理的目录、loopback和恢复材料并报告结果

执行入口：

```bash
bash test-env/scripts/server-workspace-temp-storage-extended-e2e.sh concurrency
```

有效性验证入口：

```bash
python3 test-env/scripts/test-workspace-temp-storage-extended-e2e.py
```

## `TEMP-T-021` Nextcloud真实编辑与生命周期往返

- 级别：`e2e`
- 覆盖需求：`TEMP-R-029`、`TEMP-R-030`
- 需求复核摘要：`sha256:f0d5a471485af22b34598c07adb9443bcfaf9094f7db0af3bf9d70b023bda88d`
- 实现复核摘要：`sha256:bd7a0e94a226275ff29ab18d05e33dac19210c665ed2206b636a8f8787644e0e`
- Fixture：固定Collabora镜像和最小Nextcloud LLNG编辑栈
- 目标能力：`go`、`linux`、`isolated-docker`、`playwright`、`nextcloud`、`python`
- Oracle 来源：`runtime`、`filesystem`、`report`、`ui`、`api`
- 有效性证明：`counterexample`
- 有效性证据：初始化等待须固定实例和截止时间，每个子命令重新计算预算；退出/OOM/重启/替换和模板/权限/租约/挂载错误不能重试为通过；探针或UI通知不能替代真实保存文件
- 超时：`90m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- 浏览器与固定动作socket布局、必需环境和隔离启动步骤见同目录 [RUNBOOK.md](RUNBOOK.md)；本地反例不能替代finance验收
- 最小八Module栈与离线Hook/浏览器工具容器入口见同目录 [EDITING-STACK.md](EDITING-STACK.md)；仅接受本run私有mount/net namespace
- ssh whl@finance.hlong.wang；独立run目录、Docker/containerd/net/socket/端口；串行执行
- 源码摘要和run-id必填；磁盘故障需与两个daemon共享私有mount namespace
- 官方固定app下载仅经本run私有gateway代理，NO_PROXY覆盖测试域及本轮子网；代理不包含凭据

执行步骤：

- 启动和三次生命周期动作在同一owned容器上有界等待初始化，再完成coolwsd、租约/挂载/marker、模板与discovery核验
- 以真实builtin registry验证完整渲染配置的导入/规范化，保留八Module和合法隔离字段
- 实际执行Samba生产能力组段，验证可选名单缺失或为空时继续初始化，非空时建立所声明能力组
- 等待真实文档加载和可编辑状态；仅对可见欢迎窗口普通点击第三页锚点及真实Close按钮，第三页已可见时直接Close；设置窗口普通点击父frame取消按钮，控件与移除等待各30秒；正常画布点击5秒最多3次，仅真实TimeoutError且已知窗口迟到时关闭后重试，仍只读确认Core输入焦点
- 默认容器浏览器使用1 GiB内存、256个进程、256 MiB shm及512 MiB tmpfs预算，依赖容器保留768 MiB并串行执行；用户明确指定本机Chrome时使用独立临时配置和loopback SSH隧道，记录实际版本与两端源码摘要，不记作固定工具镜像通过
- 重开后通过真实CtrlA建立选区，等待两端文档selection handle attached再CtrlC，仍独立核验原生剪贴板文本及WebDAV XML
- 仅本次独立Playwright context授予剪贴板读写权限，不限制单一origin，以支持Nextcloud内跨源编辑iframe；不替代真实复制内容断言
- 等待Nextcloud后台官方Office激活标记；404、非discovery与激活失败只能在固定截止内重试，测试只读该标记，不代为激活应用
- 运行实现命令验证：真实打开、编辑、CtrlS、WebDAV ODT XML含保存文本；关闭重开；停启重建切换后内容一致
- 执行反例：探针或UI通知不能替代真实保存文件；初始化权限失败必须拒绝

可观察断言：

- up -d成功不能替代初始化完成；只等待固定初始化入口尚未exec或discovery未就绪，health starting可继续观察，退出/OOM/重启/ID或PID替换及真实存储错误立即失败，最终完整校验仍须通过
- 模板仅通过global.host_ip/dns_server声明宿主地址，不注入重复HOST_IP或六个runner派生字段；实际导入保留私有Docker/netns/SAMBA及禁用可选应用配置
- Samba可选能力组名单没有发布者时按空处理；实际Bash缺失、空及非空用例通过，其他必需环境仍缺失即拒绝
- 真实打开、编辑、CtrlS、WebDAV ODT XML含保存文本；关闭重开；停启重建切换后内容一致
- 导航只等待DOMContentLoaded，避免附属资源阻塞load；编辑frame、Core已加载且可编辑、焦点和真实保存仍必须全部验证，不能把DOM就绪当成编辑通过
- 私有mount/net与八Module已安装状态、模板同FS和UID/bind核验；只有真实浏览器和三动作报告齐全才记R029/R030通过
- 隔离工具容器只增加读取本run源目录必需的DAC_OVERRIDE；浏览器无Docker/PID权限，源码只读
- 文档DELETE与context关闭的清理结果独立保存0600固定code报告；清理失败不覆盖编辑或生命周期首因
- CLI失败仅记录固定error.code、白名单primary的phase/module/exit_code和独立recovery结果；不记录原文、argv、凭据或完整project
- 固定生命周期动作失败也独立写本run 0600失败receipt，不覆写已存在记录或跟随符号链接，报告写失败保留动作首因
- 固定原生健康探针通过同一Go入口降为1001:1001执行；探针不初始化或清理活动模板、child-roots或cache
- 迟到已知窗口只能经普通欢迎Close或设置Cancel按钮移除后重试正常canvas点击；每次5秒且最多3次，未知错误、无已知窗口、关闭失败及上限保留原点击首因，点击成功不替代Core焦点及保存/重开/三动作内容验证
- 欢迎第三页已有Close时直接普通点击，否则真实第三页锚点点击后Close；设置Cancel位于父编辑frame，每个控件和移除等待均30秒有界，不使用内层Escape或隐藏DIV强制点击

反例与故障路径：

- 初始化未完成不能记通过；有界等待不能吞任意ProbeFailure、忽略超时或接受退出/OOM/ID变化、错误PID/UID、租约/挂载/marker及模板错误
- 相同值的HOST_IP重复声明仍按runtime-key冲突拒绝；六个runner派生字段逐一注入须拒绝，不放宽生产配置契约
- 探针或UI通知不能替代真实保存文件；初始化权限失败必须拒绝
- 准备/启动报告不得冒充编辑验收；凭据仅标准输入验证及临时浏览器环境，不进入报告或argv
- DELETE非204/404、context关闭或报告写入失败不可虚报通过；失败信息不写凭据原文
- 畸形/未知CLI错误和foreign project不得伪造Module首因，nonCLI错误不得解析成CLI detail，成功凭据输出只保留内存
- 动作失败不得缺失独立失败receipt；既有记录、symlink和写报告故障不得覆盖首因或写凭据
- 迟到窗口关闭失败不得覆盖canvas首因；没有已知窗口或非TimeoutError不得重试，不得force click、修改DOM或放宽内容断言
- 欢迎第三页切换、Close或设置Cancel失败以及窗口移除失败均不得伪造关闭成功；未知弹窗不可当作已知窗口重试

清理：

- 成功停止并释放仅本run测试容器、受管目录和loopback；保留0600脱敏报告
- 失败尝试停止本run消费者，保留未能证明可清理的目录、loopback和恢复材料并报告结果

执行入口：

```bash
go test ./internal/runner -run TestTemporaryEditingFixture -count=1
go test ./modules/samba_dc/hook -run TestStructure -count=1
npm run e2e:workspace-temp-storage-collabora
npm run test:workspace-temp-storage-editor-focus
go test ./modules/nextcloud/hook -run TestOfficeActivation -count=1
go test ./modules/collabora/hook -run TestCollabora -count=1
```

有效性验证入口：

```bash
go test ./internal/runner -run TestTemporaryEditingFixture -count=1
go test ./modules/nextcloud/hook -run TestOfficeActivation -count=1
go test ./modules/collabora/hook -run TestCollabora -count=1
python3 test-env/scripts/test-workspace-temp-collabora-action.py
python3 test-env/scripts/test-workspace-temp-storage-editing-e2e.py
npm run test:workspace-temp-storage-editor-focus
```

## `TEMP-T-022` Collabora模板初始化权限和链接

- 级别：`unit`
- 覆盖需求：`TEMP-R-010`、`TEMP-R-030`
- 需求复核摘要：`sha256:e9101aec512281b8cb41c0181b6f106d200fe93ff4e9a4d71728d48e02ea0482`
- 实现复核摘要：`sha256:7f14e6225429545da7419a7461fa3b6c0131a2804dfc07ab0e930b474f5a86ef`
- Fixture：临时模板目录含hardlink/symlink/FIFO
- 目标能力：`go`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`fault-injection`
- 有效性证据：特殊文件、symlink根、不完整已完成模板、附加探针参数及降权故障均拒绝；探针不可进入清理初始化
- 超时：`10m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- 无。

执行步骤：

- 运行实现命令验证：复制保留权限owner/link；仅清child-roots/cache；相同镜像模板复用
- 验证固定健康探针只降权执行coolwsd --probe，拒绝额外参数，降权失败禁止exec且保留首因
- 执行反例：特殊文件、symlink根、不完整已完成模板拒绝

可观察断言：

- 复制保留权限owner/link；仅清child-roots/cache；相同镜像模板复用
- 启动与健康探针共享清空补充组、GID1001、UID1001、原生exec顺序；探针不进入初始化；底层失败保留

反例与故障路径：

- 特殊文件、symlink根、不完整已完成模板拒绝
- 探针附加命令或参数拒绝；每步降权失败不得执行原生程序或被记为健康

清理：

- 临时目录和子进程由测试清理；只读审阅不操作服务

执行入口：

```bash
go test ./modules/collabora/hook -run TestCollabora -count=1
```

有效性验证入口：

```bash
go test ./modules/collabora/hook -run TestCollabora -count=1
```

## `TEMP-T-023` 扩展远端判据与清理反例

- 级别：`unit`
- 覆盖需求：`TEMP-R-005`、`TEMP-R-006`、`TEMP-R-023`、`TEMP-R-024`、`TEMP-R-026`、`TEMP-R-027`、`TEMP-R-029`、`TEMP-R-030`、`TEMP-R-039`、`TEMP-R-041`、`TEMP-R-043`
- 需求复核摘要：`sha256:d9e4dced033ac08d555fc8e800a46654f819723f319e6c56c360832af4b29e75`
- 实现复核摘要：`sha256:b56eedd201cfc0d04bc6c9fb0aff66afd06e46a40cebd16634ea76876bd0508b`
- Fixture：真实builtin registry编辑配置导入及Python路径、状态、恢复、workspace/tmp实际备份范围、cleanup/凭据/report反例
- 目标能力：`go`、`python`、`node`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`fault-injection`
- 有效性证据：清理失败仍尝试所有run内停止且不能虚报完成
- 超时：`10m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- 无。

执行步骤：

- 验证初始化等待固定300秒上限及每次命令的剩余预算，正常pending后仍须完整安全核验
- 用真实导入器验证完整编辑模板，分别注入重复HOST_IP及六个runner派生字段以核验拒绝
- 验证真实浏览器命令的1 GiB内存及512 MiB tmpfs预算、只读源码挂载和无daemon访问，依赖及256 MiB shm保持独立
- 运行实现命令验证：跨run、缺失问题、新身份/路径错误和未提交GC判据拒绝
- 执行反例：清理失败仍尝试所有run内停止且不能虚报完成

可观察断言：

- 仅允许正常初始化阶段有限重试，同一owned ID/PID及零重启次数固定；300秒截止时间下每子命令重算剩余预算，ready时仍核PID1 coolwsd/1001、lease/bind/marker、同FS模板版本及真实discovery
- 八Module渲染配置以真实builtin registry导入/规范化成功，合法host/DNS和隔离字段保留，读取不改写源文件
- 跨run、缺失问题、新身份/路径错误和未提交GC判据拒绝
- 备份探测必须使用显式 workspace/tmp 绝对路径并核对实际源挂载；外部源挂载不能证明临时排除
- 历史缺盘rollback必须以存储预检错误拒绝且当前实例保持，先原盘重挂再成功回滚；snapshot默认pause复启原租约
- 编辑工具容器/模板输入/Hook产物须冻结；源摘要/三动作/浏览器证据不齐或凭据泄露拒绝
- 仅增加DAC_OVERRIDE以读私有源目录；删除/context/报告写失败保留首因并独立记录固定code，报告0600无凭据
- 下载代理只能指向私有gateway，无用户名密码，测试域与本轮内部子网必须绕过代理
- 编辑CLI故障保留白名单首因及独立recovery，错误原文/argv/凭据/完整project均丢弃；未知或畸形输入不得生成虚假首因
- 固定动作失败写0600独占创建receipt，真实本地合成CLI退出1及Compose首因exit98可安全投影；不冒称真实ANAS或finance验收

反例与故障路径：

- 固定初始化入口/discovery未就绪只可等待，退出/OOM/重启/ID或PID替换、unhealthy、超时、未知执行错误或最终安全核验失败均不能变为通过，不能统一捕获ProbeFailure后重试或让连续子命令各复用旧预算
- 重复global.host_ip/env.HOST_IP以及INTERFACE/DEFAULT_GATEWAY_IP/HOST_SUBNET_MASK/LOCAL_DNS_SERVER/HOST_DNS_SERVER/SERVER_NAME输入各自触发真实导入拒绝，不能靠mock或删除安全校验使模板通过
- 清理失败仍尝试所有run内停止且不能虚报完成
- 实际源挂载位于外部根时拒绝继续备份，不能仅凭配置值记录排除通过
- 历史rollback的usage错误不得冒充缺盘拒绝；无-y真实调用、拒绝保持/原盘重挂顺序、复启原ID/身份/租约/哨兵任一不符拒绝
- restore目录须本run新0700目标并真实init先于restore；已有目录、dangling symlink及越界须CLI前拒绝，init失败/缺.anas/源授权拒绝，不调用源生命周期
- restore成功需正确target/backup/mode及verify.ok/checked/problems；外部根usage不得冒充temp_preflight_failed；snapshot须nested id、label、complete与健康问题为空，错误字段不得成为证据
- 错误私有网卡/镜像、派生Hook变化、外部登录URL、错误安装或同FS/UID/bind核验不得启动或记通过
- DELETE500、dispose异常、写报告失败不可变为通过或覆盖此前编辑/生命周期首因
- CLI凭据字段、unknown code/phase/recovery、foreign project、畸形JSON/UTF8和nonCLI输出不得进入结构化首因，成功凭据不写failure
- 每种restore/copytree clone须立即核验完整源baseline；snapshot restore须有意重建且保持本地授权，不接受仅Running与内容一致
- 生成shim的真实Python子进程证明继承CLI组与已有自组均安全独立，父/兄弟组不变；异源session拒绝；只在实际block边界调用，不放宽PID复用守卫
- snapshot start仅释放旧租约，须实际显式GC；返回ok仍替换新容器、改变所属Module bind或owner marker、留下旧树或丢新登记均拒绝
- 未提交中断GC只接受temp_recovery_required/4并保留内容；已提交GC须ok/0且旧树实际删除，usage/未知Docker故障不能冒充拒绝或清理完成
- 失败动作receipt拒绝覆盖或跟随symlink；报告创建失败仍保留原动作失败，不写原始stdout/stderr或凭据
- 源ID变化即使Running和内容相同也不能通过；快照不重建、改本地授权或显式GC后保留历史临时树拒绝；GC泛化非零和错bind的usage/未知错误拒绝

清理：

- 临时目录和子进程由测试清理；只读审阅不操作服务

执行入口：

```bash
go test ./internal/runner -run TestTemporaryEditingFixture -count=1
python3 test-env/scripts/test-workspace-temp-storage-extended-e2e.py
python3 test-env/scripts/test-workspace-temp-collabora-action.py
python3 test-env/scripts/test-workspace-temp-storage-editing-e2e.py
npm run test:workspace-temp-storage-browser-cleanup
```

有效性验证入口：

```bash
go test ./internal/runner -run TestTemporaryEditingFixture -count=1
python3 test-env/scripts/test-workspace-temp-storage-extended-e2e.py
python3 test-env/scripts/test-workspace-temp-collabora-action.py
python3 test-env/scripts/test-workspace-temp-storage-editing-e2e.py
npm run test:workspace-temp-storage-browser-cleanup
```

## `TEMP-T-024` 挂载核验拒绝提交与清理重试

- 级别：`e2e`
- 覆盖需求：`TEMP-R-016`、`TEMP-R-036`、`TEMP-R-045`
- 需求复核摘要：`sha256:0fed7b9dc8cc838d1aee6b1b23da3c9c1d632a7f84404053726b8a5f57cc62bb`
- 实现复核摘要：`sha256:4bb8bfa46c2812a3331bef4e5c40350c1baf83d9da3af3c145f21d92f3ce9aca`
- Fixture：真实Docker和one-shot inspect/cleanup故障
- 目标能力：`linux`、`isolated-docker`、`python`
- Oracle 来源：`runtime`、`filesystem`、`report`
- 有效性证明：`counterexample`
- 有效性证据：错误mount不能以启动成功代替；cleanup失败不可回滚成功的新路径
- 超时：`90m`
- 敏感数据：使用合成数据；凭据不进入报告或命令错误；E2E原始材料0600，报告仅含稳定ID、摘要、结果和清理状态。

前置条件：

- ssh whl@finance.hlong.wang；独立run目录、Docker/containerd/net/socket/端口；串行执行
- 源码摘要和run-id必填；磁盘故障需与两个daemon共享私有mount namespace

执行步骤：

- 运行实现命令验证：错误实际mount拒绝commit并恢复旧运行；旧树清理失败新运行保持且重试登记
- 执行反例：错误mount不能以启动成功代替；cleanup失败不可回滚成功的新路径

可观察断言：

- 错误实际mount拒绝commit并恢复旧运行；旧树清理失败新运行保持且重试登记
- 错bind injection必须被Docker实际inspect证明并在逐Module启动核验返回start_failed/1；usage、Compose缺失、未知存储错误不能冒充故障证据

反例与故障路径：

- 错误mount不能以启动成功代替；cleanup失败不可回滚成功的新路径

清理：

- 成功停止并释放仅本run测试容器、受管目录和loopback；保留0600脱敏报告
- 失败尝试停止本run消费者，保留未能证明可清理的目录、loopback和恢复材料并报告结果

执行入口：

```bash
bash test-env/scripts/server-workspace-temp-storage-extended-e2e.sh faults
```

有效性验证入口：

```bash
python3 test-env/scripts/test-workspace-temp-storage-extended-e2e.py
```
