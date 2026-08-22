# compute Contract

为一次性隔离工作负载提供 Provider 中立的 VM 生命周期与仅通过 stdin 注入 Secret 的执行原语。

<!-- generated:contract-reference:start -->
## 生成的 Contract 参考

> 本节由 `contract.yml`、schemas、Module manifests 与 `documentation.yml` 生成，请勿手工编辑。

- Version / 版本：`1.0.0`
- Status / 状态：`proposal`（reviewed 2026-08-22）
- Interfaces / 接口：`incus_vm`
- Resource identity / 资源标识：`provider_project`, `instance_id`
- Resource schema / 资源 Schema：`schemas/instance.yml`

### Operations / 操作

| Operation | Required | Request schema | Result schema |
| --- | --- | --- | --- |
| `create` | `true` | `schemas/create-request.yml` | `schemas/instance-result.yml` |
| `delete` | `true` | `schemas/instance-request.yml` | `schemas/delete-result.yml` |
| `exec_stdin` | `true` | `schemas/exec-request.yml` | `schemas/exec-result.yml` |
| `inspect` | `true` | `schemas/instance-request.yml` | `schemas/instance-result.yml` |
| `list_managed` | `true` | `schemas/list-request.yml` | `schemas/list-result.yml` |
| `start` | `true` | `schemas/instance-request.yml` | `schemas/instance-result.yml` |
| `stop` | `true` | `schemas/instance-request.yml` | `schemas/instance-result.yml` |

### Schemas / 字段

| Schema | Type | Required fields | All fields |
| --- | --- | --- | --- |
| `schemas/create-request.yml` | `object` | `provider`, `interface`, `spec` | `interface`, `provider`, `spec` |
| `schemas/delete-result.yml` | `object` | `deleted` | `deleted` |
| `schemas/exec-request.yml` | `object` | `provider`, `interface`, `provider_project`, `instance_id`, `command`, `stdin` | `command`, `instance_id`, `interface`, `provider`, `provider_project`, `stdin` |
| `schemas/exec-result.yml` | `object` | `exit_code` | `exit_code` |
| `schemas/instance-request.yml` | `object` | `provider`, `interface`, `provider_project`, `instance_id` | `instance_id`, `interface`, `provider`, `provider_project` |
| `schemas/instance-result.yml` | `object` | `provider_project`, `instance_id`, `state` | `instance_id`, `provider_project`, `state` |
| `schemas/instance.yml` | `object` | `provider_project`, `instance_id`, `image`, `cpu`, `memory_mib`, `disk_gib`, `lifecycle` | `cpu`, `disk_gib`, `image`, `instance_id`, `lifecycle`, `memory_mib`, `provider_project`, `workload_id` |
| `schemas/list-request.yml` | `object` | `provider`, `interface`, `provider_project`, `managed_prefix` | `interface`, `managed_prefix`, `provider`, `provider_project` |
| `schemas/list-result.yml` | `object` | `instances` | `instances` |

### 当前 Provider 与 Consumer

| Role | Module | Version constraint | Interface | Implementation |
| --- | --- | --- | --- | --- |
| - | - | - | - | - |
<!-- generated:contract-reference:end -->
实现与安全边界见[技术文档](docs/technical.md)。
