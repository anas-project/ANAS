<!-- Generated from cases.yml by cmd/gen-test-case-docs. DO NOT EDIT. -->

# 目录主邮箱与门户建号验收用例

> 需求来源：[`directory-primary-email.md`](../../../dev-docs/requirements/directory-primary-email.md)
>
> 实施计划：[`directory-primary-email.md`](../../../dev-docs/plans/archived/directory-primary-email.md)
> 本文由同目录 `cases.yml` 生成；修改用例后运行 `go run ./cmd/gen-test-case-docs`。

## 覆盖总览

| 用例 ID | 级别 | 需求 ID | 实现 |
| --- | --- | --- | --- |
| `MAIL-T-001` | unit | `MAIL-R-001`、`MAIL-R-002`、`MAIL-R-006` | internal/directorymail/address_test.go<br>modules/samba_dc/hook/mail_defaults_test.go |
| `MAIL-T-002` | unit | `MAIL-R-004`、`MAIL-R-006`、`MAIL-R-007` | modules/samba_dc/anchor_worker/test_mail_policy.py |
| `MAIL-T-003` | e2e | `MAIL-R-001`、`MAIL-R-004`、`MAIL-R-006`、`MAIL-R-007` | test-env/scripts/directory-primary-mail-native.py |
| `MAIL-T-004` | e2e | `MAIL-R-003`、`MAIL-R-005`、`MAIL-R-006` | test-env/playwright/user-portal-account-management.mjs |
| `MAIL-T-005` | e2e | `MAIL-R-002`、`MAIL-R-003` | test-env/scripts/directory-mail-creation-proof.py<br>test-env/scripts/user-portal-owner-isolated.py |

## `MAIL-T-001` 地址规则与生产初始化参数

- 级别：`unit`
- 覆盖需求：`MAIL-R-001`、`MAIL-R-002`、`MAIL-R-006`
- 需求复核摘要：`sha256:e1b8b75fece9acf3a17e2c49d60ea4c3b1ec071dd4c0cd239120fcb707675f05`
- 实现复核摘要：`sha256:8e9f0674dc598f42719afd3826d9c1e387dd7819c029e345efafb1275243c07a`
- Fixture：原生用例仅使用用户已授权 finance 测试栈的临时 OU/账号；凭据保留在同机私有文件
- 目标能力：`go`、`bash`
- Oracle 来源：`return-value`、`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：非法域名/用户名失败，后缀不改 AD 身份；实际初始化区块新建才传入 mail
- 超时：`10m`
- 敏感数据：密码/令牌/绑定凭据仅在 0700/0600 私有文件和同机输入处理，不进入 Git、截图或报告

前置条件：

- e2e 必须核对私有输入、候选源码和镜像基线；设置 PORTAL_MAIL_ROOT/PORTAL_ACCOUNT_ROOT；T-005 在选定 Samba DC 容器执行，可经标准输入传送同一源码；迁移实验先于生产 Worker 激活执行

执行步骤：

- 执行固定入口，核对正反例及目录回读结果

可观察断言：

- 非法域名/用户名失败，后缀不改 AD 身份；实际初始化区块新建才传入 mail

反例与故障路径：

- 非法域名/用户名失败，后缀不改 AD 身份；实际初始化区块新建才传入 mail

清理：

- 临时主体按私有稳定引用清理，恢复测试范围外字段；保留无凭据结果和源码摘要

执行入口：

```bash
go test ./internal/directorymail ./modules/samba_dc/hook
```

有效性验证入口：

```bash
go test ./internal/directorymail ./modules/samba_dc/hook
```

## `MAIL-T-002` 邮箱维护计划、冲突与写入失败

- 级别：`unit`
- 覆盖需求：`MAIL-R-004`、`MAIL-R-006`、`MAIL-R-007`
- 需求复核摘要：`sha256:5d5893d59d5b5dd2dd89d4a91f3bafcd941fd87d98280278eb6b4eef54d73d43`
- 实现复核摘要：`sha256:7e2d97b93afff4210f59708e066260fa48857d0267cdbed73ad9164372e469dc`
- Fixture：原生用例仅使用用户已授权 finance 测试栈的临时 OU/账号；凭据保留在同机私有文件
- 目标能力：`python3`
- Oracle 来源：`return-value`、`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：地址碰撞在写入前拒绝；身份移动/改名拒绝；未知写结果不重放、不宣称成功
- 超时：`10m`
- 敏感数据：密码/令牌/绑定凭据仅在 0700/0600 私有文件和同机输入处理，不进入 Git、截图或报告

前置条件：

- e2e 必须核对私有输入、候选源码和镜像基线；设置 PORTAL_MAIL_ROOT/PORTAL_ACCOUNT_ROOT；T-005 在选定 Samba DC 容器执行，可经标准输入传送同一源码；迁移实验先于生产 Worker 激活执行

执行步骤：

- 执行固定入口，核对正反例及目录回读结果

可观察断言：

- 地址碰撞在写入前拒绝；身份移动/改名拒绝；未知写结果不重放、不宣称成功

反例与故障路径：

- 地址碰撞在写入前拒绝；身份移动/改名拒绝；未知写结果不重放、不宣称成功

清理：

- 临时主体按私有稳定引用清理，恢复测试范围外字段；保留无凭据结果和源码摘要

执行入口：

```bash
python3 modules/samba_dc/anchor_worker/test_mail_policy.py
```

有效性验证入口：

```bash
python3 modules/samba_dc/anchor_worker/test_mail_policy.py
```

## `MAIL-T-003` finance 原生迁移与最小邮箱权限

- 级别：`e2e`
- 覆盖需求：`MAIL-R-001`、`MAIL-R-004`、`MAIL-R-006`、`MAIL-R-007`
- 需求复核摘要：`sha256:db78b9493ea6c7b62bb7974ee997948e4907ef6bb30f0a80547f812587626eb3`
- 实现复核摘要：`sha256:8dca9d755ce99a6b2ee15d42c948d8cdc44b4eade9fcd9d8c357982e8c97b5d6`
- Fixture：原生用例仅使用用户已授权 finance 测试栈的临时 OU/账号；凭据保留在同机私有文件
- 目标能力：`samba`、`docker`、`python3-ldap`、`private-input`
- Oracle 来源：`return-value`、`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：临时 OU 默认地址及后缀迁移；冲突无部分写入；越界 mail ACL 和只读写入拒绝；身份/姓名/密码状态/组不变
- 超时：`10m`
- 敏感数据：密码/令牌/绑定凭据仅在 0700/0600 私有文件和同机输入处理，不进入 Git、截图或报告

前置条件：

- e2e 必须核对私有输入、候选源码和镜像基线；设置 PORTAL_MAIL_ROOT/PORTAL_ACCOUNT_ROOT；T-005 在选定 Samba DC 容器执行，可经标准输入传送同一源码；迁移实验先于生产 Worker 激活执行

执行步骤：

- 执行固定入口，核对正反例及目录回读结果

可观察断言：

- 临时 OU 默认地址及后缀迁移；冲突无部分写入；越界 mail ACL 和只读写入拒绝；身份/姓名/密码状态/组不变

反例与故障路径：

- 临时 OU 默认地址及后缀迁移；冲突无部分写入；越界 mail ACL 和只读写入拒绝；身份/姓名/密码状态/组不变

清理：

- 临时主体按私有稳定引用清理，恢复测试范围外字段；保留无凭据结果和源码摘要

执行入口：

```bash
python3 test-env/scripts/directory-primary-mail-native.py
```

有效性验证入口：

```bash
python3 test-env/scripts/directory-primary-mail-native.py
```

## `MAIL-T-004` 门户真实创建与系统邮箱只读展示

- 级别：`e2e`
- 覆盖需求：`MAIL-R-003`、`MAIL-R-005`、`MAIL-R-006`
- 需求复核摘要：`sha256:5faf909d01a66e43edcec804eb6d6d4e56cb61d2c9e81bb56723544896eb6095`
- 实现复核摘要：`sha256:15c55f5146fd0c33a832baebd0f54bebd1674170437eb70a0f13e085ebef6a61`
- Fixture：原生用例仅使用用户已授权 finance 测试栈的临时 OU/账号；凭据保留在同机私有文件
- 目标能力：`playwright-chromium`、`casdoor`、`samba`、`private-input`
- Oracle 来源：`return-value`、`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：门户不接受任意邮箱/DN；创建时邮箱/临时密码/身份同时生效；重名不覆盖；普通用户无建号权；此入口不代替 LAM 浏览器验收
- 超时：`10m`
- 敏感数据：密码/令牌/绑定凭据仅在 0700/0600 私有文件和同机输入处理，不进入 Git、截图或报告

前置条件：

- e2e 必须核对私有输入、候选源码和镜像基线；设置 PORTAL_MAIL_ROOT/PORTAL_ACCOUNT_ROOT；T-005 在选定 Samba DC 容器执行，可经标准输入传送同一源码；迁移实验先于生产 Worker 激活执行

执行步骤：

- 执行固定入口，核对正反例及目录回读结果

可观察断言：

- 门户不接受任意邮箱/DN；创建时邮箱/临时密码/身份同时生效；重名不覆盖；普通用户无建号权；此入口不代替 LAM 浏览器验收

反例与故障路径：

- 门户不接受任意邮箱/DN；创建时邮箱/临时密码/身份同时生效；重名不覆盖；普通用户无建号权；此入口不代替 LAM 浏览器验收

清理：

- 临时主体按私有稳定引用清理，恢复测试范围外字段；保留无凭据结果和源码摘要

执行入口：

```bash
node test-env/playwright/user-portal-account-management.mjs
```

有效性验证入口：

```bash
node test-env/playwright/user-portal-account-management.mjs
```

## `MAIL-T-005` Samba 原生初始化邮箱参数与用户类创建授权

- 级别：`e2e`
- 覆盖需求：`MAIL-R-002`、`MAIL-R-003`
- 需求复核摘要：`sha256:6ec3f65bd75e3740808e3a2a7f265231a19ea9f0aabe2a1d033b18d562ed9d3f`
- 实现复核摘要：`sha256:0789adef77850ad5ee1bd868e79284f6f1f3b3bc34167d1a77b7d29a2c8acf83`
- Fixture：finance 临时 OU 验证普通用户类授权；完全断网假域执行镜像内生产管理员创建分支，所有高权主体仅在假域
- 目标能力：`samba`、`private-input`、`docker`、`go`、`openssl`
- Oracle 来源：`return-value`、`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：镜像内生产管理员分支直接写主邮箱，LDAPS立即可读，独立后缀与既有账号不覆写；CC user 单次 Add 可含密码/mail/锚点且不能创建组
- 超时：`10m`
- 敏感数据：密码/令牌/绑定凭据仅在 0700/0600 私有文件和同机输入处理，不进入 Git、截图或报告

前置条件：

- e2e 必须核对私有输入、候选源码和镜像基线；设置 PORTAL_MAIL_ROOT/PORTAL_ACCOUNT_ROOT；T-005 在选定 Samba DC 容器执行，可经标准输入传送同一源码；迁移实验先于生产 Worker 激活执行

执行步骤：

- 执行固定入口，核对正反例及目录回读结果

可观察断言：

- 镜像内生产管理员分支直接写主邮箱，LDAPS立即可读，独立后缀与既有账号不覆写；CC user 单次 Add 可含密码/mail/锚点且不能创建组

反例与故障路径：

- 镜像内生产管理员分支直接写主邮箱，LDAPS立即可读，独立后缀与既有账号不覆写；CC user 单次 Add 可含密码/mail/锚点且不能创建组

清理：

- 临时主体按私有稳定引用清理，恢复测试范围外字段；保留无凭据结果和源码摘要

执行入口：

```bash
python3 test-env/scripts/directory-mail-creation-proof.py
python3 test-env/scripts/user-portal-owner-isolated.py --image "$PORTAL_OWNER_IMAGE" --native-binary "$PORTAL_OWNER_BINARY" --evidence-dir "$PORTAL_OWNER_EVIDENCE"
```

有效性验证入口：

```bash
python3 test-env/scripts/directory-mail-creation-proof.py
```
