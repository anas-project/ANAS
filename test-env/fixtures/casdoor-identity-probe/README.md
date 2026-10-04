# Casdoor 固定源码身份键探针

执行 `bash test-env/scripts/test-casdoor-identity-source.sh [pinned-source.tar.gz]`。
脚本校验 Dockerfile 固定源码归档的 SHA-256，应用原四个补丁，先验证历史配置能力的实际边界，
再应用 Dockerfile 的生产补丁 0005/0006 验证统一主体和撤权。两轮使用实际上游函数、隔离内存 SQLite 和临时
签名密钥，不连接部署；临时目录保留在输出所示路径。

- `identity_probe_test.go.in`：基线探针，预期当前 UserInfo/Logout Token 仍取内部 ID、SAML NameID
  取用户名/邮箱，JWT-Custom 缺锚点不会报错。
- `identity_candidate_test.go.in`：保留原测试名的生产主体补丁检查，包括签名 JWT、UserInfo、签名 Logout Token、SAML 模板、缺锚点
  拒绝与恢复管理员检查。模板扩展名避免被根仓或嵌套 helper 的普通 `go test ./...` 误编译。

- `revocation_test.go.in`：捕获后刷新记录轮换仍被撤销，后续授权和其他 client 保留，重放幂等。

源码测试不计作登录、refresh grant、SAML 签名断言或实机 E2E。Consumer 投影与实机证据分别记录，
详见 [能力核实](../../../docs/research/casdoor-directory-subject.md)和
[目录身份键计划](../../../dev-docs/plans/directory-identity-key.md)。
