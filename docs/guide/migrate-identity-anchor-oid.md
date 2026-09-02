# 将 identity-anchor OID 迁移到 PEN 66678

本 Runbook 用于把**唯一一台**采用旧 GUID 派生 OID 的 Samba AD DC，离线迁移到 IANA 已分配的 PEN `66678`。迁移后的正式属性为：

| 项目 | 迁移前（永久退役） | 迁移后（正式） |
| --- | --- | --- |
| `attributeID` | `1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1` | `1.3.6.1.4.1.66678.1.2.1` |
| `schemaIDGUID` | `7108c5a7-2290-45e0-9eba-eef087be58e3` | `db3786ae-3261-4d44-a2a1-588bfe3e41c5` |
| 状态 | defunct legacy 对象，不得复用 | 当前 `anasIdentityAnchor` |

该流程会修改 AD schema，必须安排维护窗口。只适用于一个可写 DC、且该 DC 持有 Schema Master FSMO 的部署；多 DC 或复制拓扑必须另行设计迁移。

> 内部 OID 子分配不需要向 IANA 逐项登记。命名规则和退役记录见 [OID 注册表](../governance/oid-registry.md)。

## 迁移原则

- 使用精确的 `ghcr.io/anas-project/anas-samba-dc:4.23.6-r11` 镜像，禁止使用 `latest`。私有镜像仓库也必须解析到同一个 r11 镜像摘要。
- `--check` 只读；`--execute` 才会修改 schema 和对象值。
- 快照必须在所有 writer 停止后创建，并用 `anas snapshot verify` 校验成功。脚本的 `--snapshot-id` 只是证据标签，不会替你检查快照。
- `--backup-dir` 必须是挂载在 Samba 数据卷之外、预先存在且受保护的宿主目录；每次执行都使用一个尚不存在的新子目录。
- 任一步骤在 `--execute` 后失败，都恢复**整个 Samba 数据卷快照**。不得只恢复 `sam.ldb`、删除迁移标记或手工续改数据库。

## 1. 预备 r11 部署

先在停机前下载镜像并渲染候选部署，以缩短维护窗口。下面以 `/srv/anas` 为工作区示例；请替换成实际绝对路径。

```bash
docker pull ghcr.io/anas-project/anas-samba-dc:4.23.6-r11
docker pull ghcr.io/anas-project/anas-samba-dc-anchor:4.23.6-r11
anas module update samba_dc -w /srv/anas
anas render -w /srv/anas
```

记录 `anas render` 返回的候选 deployment ID，然后检查候选部署和 Compose 实际解析出的镜像：

```bash
anas deployments inspect REPLACE_WITH_R11_DEPLOYMENT_ID -w /srv/anas
ANAS_R11_MODULE_DIR=/srv/anas/.anas/deployments/REPLACE_WITH_R11_DEPLOYMENT_ID/modules/samba_dc
docker compose --project-directory "${ANAS_R11_MODULE_DIR}" \
  --env-file "${ANAS_R11_MODULE_DIR}/.env" \
  -f "${ANAS_R11_MODULE_DIR}/docker-compose.yml" config --images
docker image inspect --format '{{.Id}} {{json .RepoDigests}}' \
  ghcr.io/anas-project/anas-samba-dc:4.23.6-r11 \
  ghcr.io/anas-project/anas-samba-dc-anchor:4.23.6-r11
```

确认 deployment manifest 为 Samba DC r11，`config --images` 中 DC 与 anchor worker 都是精确的
`4.23.6-r11` tag；把本地 image ID/RepoDigest 记入受控执行证据，并在组织另有批准摘要时逐项比较。
`deployments inspect` 本身不提供运行时镜像摘要。此时**不要启动候选部署**：r11 的正常初始化会拒绝
未迁移的旧 schema，而任何 r11 之前的 legacy 镜像也不能在已迁移的数据上重新启动。使用私有镜像
仓库时，把上面两条公开镜像引用替换为 `config --images` 输出的实际引用。

同时完成以下准备：

1. 记录全部已启用 Consumer，以及每个 Consumer 中一个已知用户的账户映射和当前 anchor，供迁移后对比。
2. 确认工作区数据位于 `<workspace>/data/samba_dc/var`，且只有这一台可写 DC。
3. 在独立于 Samba 数据卷的持久存储上预建证据父目录，并限制为管理员可读：

   ```bash
   sudo install -d -m 0700 /mnt/anas-migration-evidence
   ```

4. 确认 Btrfs 快照存储有足够空间，并准备好完整恢复命令。

## 2. 宿主级停写

停止整个工作区：

```bash
anas stop -w /srv/anas
```

在宿主机上确认以下对象均已停止，而不只是 Compose 中的 Samba 容器：

- `samba_dc`、anchor worker、事件初始化任务和所有 Consumer；
- 任何通过 LDAP、`samba-tool` 或离线 LDB 访问该数据的临时容器、计划任务和人工会话；
- 会读取或写入 `<workspace>/data/samba_dc/var` 的备份、同步或维护进程。

迁移脚本只能检查它所在容器内的状态，不能证明宿主机已经完全停写。确认静止后保持所有服务停止，继续只读预检。

## 3. 设置维护变量

```bash
ANAS_WORKSPACE_PATH=/srv/anas
ANAS_SAMBA_DATA_PATH="${ANAS_WORKSPACE_PATH}/data/samba_dc/var"
ANAS_EVIDENCE_PARENT=/mnt/anas-migration-evidence
ANAS_EVIDENCE_NAME=pen66678-REPLACE_WITH_UNIQUE_RUN_ID
ANAS_R11_DEPLOYMENT_ID=REPLACE_WITH_R11_DEPLOYMENT_ID
ANAS_MIGRATION_IMAGE=ghcr.io/anas-project/anas-samba-dc:4.23.6-r11
ANAS_R11_MODULE_DIR="${ANAS_WORKSPACE_PATH}/.anas/deployments/${ANAS_R11_DEPLOYMENT_ID}/modules/samba_dc"
ANAS_CONTAINER_PREFIX=$(sed -n 's/^CONTAINER_PREFIX=//p' "${ANAS_R11_MODULE_DIR}/.env" | head -n 1)
ANAS_COMPOSE_PROJECT="${ANAS_CONTAINER_PREFIX}samba_dc"
ANAS_DC_CONTAINER="${ANAS_CONTAINER_PREFIX}samba_dc"
ANAS_ANCHOR_CONTAINER="${ANAS_CONTAINER_PREFIX}samba_dc_anchor"
```

`ANAS_EVIDENCE_NAME` 对应的子目录必须尚不存在。证据中包含目录 DN 和稳定标识符，应按敏感目录数据保护，不得复制到公开工单或日志服务。

## 4. 只读预检

通过一次性、无网络的 r11 容器挂载已停止的 Samba 数据卷：

```bash
docker run --rm --network none --user 0 \
  --entrypoint /usr/local/bin/migrate-identity-anchor-oid.sh \
  --mount type=bind,src="${ANAS_SAMBA_DATA_PATH}",dst=/var/lib/samba \
  "${ANAS_MIGRATION_IMAGE}" --check
```

只在输出明确显示为脚本支持的**完整旧状态**时继续。若输出为以下任一情况，则停止：

- 已经是完整的新状态：无需再次执行迁移；
- 部分迁移、混合 OID、未知 GUID、重复值、class link 不一致或数据库不可读：不得自动修补，应保留现场并调查；
- 检测到运行中的 Samba 或 writer：回到宿主级停写检查。

## 5. 创建真实冷快照并执行一次迁移

预检通过且宿主仍完全停写时，创建真实冷快照：

```bash
anas snapshot create --label "before PEN 66678 identity-anchor OID migration" -w "${ANAS_WORKSPACE_PATH}"
ANAS_SNAPSHOT_ID=REPLACE_WITH_VERIFIED_SNAPSHOT_ID
anas snapshot show "${ANAS_SNAPSHOT_ID}" -w "${ANAS_WORKSPACE_PATH}"
anas snapshot verify "${ANAS_SNAPSHOT_ID}" -w "${ANAS_WORKSPACE_PATH}"
anas snapshot pin "${ANAS_SNAPSHOT_ID}" -w "${ANAS_WORKSPACE_PATH}"
```

只有 `snapshot verify` 成功后才能继续。记录工具实际返回的 snapshot ID；不要自造或复用一个标签作为 ID。快照创建和验证期间也不得启动任何 writer。

把外部证据父目录以相同的宿主和容器路径挂载，并显式传入全新子目录：

```bash
docker run --rm --network none --user 0 \
  --entrypoint /usr/local/bin/migrate-identity-anchor-oid.sh \
  --mount type=bind,src="${ANAS_SAMBA_DATA_PATH}",dst=/var/lib/samba \
  --mount type=bind,src="${ANAS_EVIDENCE_PARENT}",dst="${ANAS_EVIDENCE_PARENT}" \
  "${ANAS_MIGRATION_IMAGE}" --execute \
  --snapshot-id "${ANAS_SNAPSHOT_ID}" \
  --backup-dir "${ANAS_EVIDENCE_PARENT}/${ANAS_EVIDENCE_NAME}"
```

脚本会先导出并校验旧值，然后依次移除旧 class link、将旧 schema 对象设为 defunct 并改名、创建新 OID/GUID 对象、恢复值并重建 User/Group class link。各阶段都有可审计的检查点，但**不支持在失败后就地续跑**。

执行完成后，保持所有正式服务停止，再运行一次相同的只读检查：

```bash
docker run --rm --network none --user 0 \
  --entrypoint /usr/local/bin/migrate-identity-anchor-oid.sh \
  --mount type=bind,src="${ANAS_SAMBA_DATA_PATH}",dst=/var/lib/samba \
  "${ANAS_MIGRATION_IMAGE}" --check

sha256sum -c "${ANAS_EVIDENCE_PARENT}/${ANAS_EVIDENCE_NAME}/SHA256SUMS"
```

完整状态的 `--check` 会验证：

- 正式和 legacy schema 对象的 OID、GUID、不可变字段及 defunct 状态；
- User 与 Group 对 `anasIdentityAnchor` 的 class link；
- 每个带文本 anchor 的对象恰有一个 `objectGUID` 和一个 `mS-DS-ConsistencyGuid`；
- 文本 UUID 与二进制 GUID 的 Windows `bytes_le` 表示一致，且文本 anchor 全局唯一。

## 6. 启动 r11 并验证写权限

先只启动候选部署中的 DC 和初始化服务，Consumer 仍保持停止：

```bash
docker compose --project-name "${ANAS_COMPOSE_PROJECT}" \
  --project-directory "${ANAS_R11_MODULE_DIR}" \
  --env-file "${ANAS_R11_MODULE_DIR}/.env" \
  -f "${ANAS_R11_MODULE_DIR}/docker-compose.yml" \
  up -d anas_samba_dc_events_init anas_samba_dc
```

确认 DC 健康、初始化成功且 schema ready marker 存在：

```bash
docker exec "${ANAS_DC_CONTAINER}" test -f /run/anas-identity-schema.ready
for _ in $(seq 1 90); do
  ANAS_DC_HEALTH=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "${ANAS_DC_CONTAINER}")
  [ "${ANAS_DC_HEALTH}" = healthy ] && break
  sleep 2
done
test "${ANAS_DC_HEALTH}" = healthy
docker logs --tail 100 "${ANAS_DC_CONTAINER}"
```

检查 `OU=People` 与 `OU=Groups` 的 ACL：必须包含新 schemaIDGUID，且不再包含旧 GUID。

```bash
ANAS_USERS_DN=$(docker exec "${ANAS_DC_CONTAINER}" printenv SAMBA_DC_BASE_USERS_DN)
ANAS_GROUPS_DN=$(docker exec "${ANAS_DC_CONTAINER}" printenv SAMBA_DC_BASE_GROUPS_DN)

docker exec "${ANAS_DC_CONTAINER}" samba-tool dsacl get --objectdn="${ANAS_USERS_DN}" | grep -F 'db3786ae-3261-4d44-a2a1-588bfe3e41c5'
docker exec "${ANAS_DC_CONTAINER}" samba-tool dsacl get --objectdn="${ANAS_GROUPS_DN}" | grep -F 'db3786ae-3261-4d44-a2a1-588bfe3e41c5'
```

人工确认两份 ACL 输出都不含 `7108c5a7-2290-45e0-9eba-eef087be58e3`。随后启动 anchor worker：

```bash
docker compose --project-name "${ANAS_COMPOSE_PROJECT}" \
  --project-directory "${ANAS_R11_MODULE_DIR}" \
  --env-file "${ANAS_R11_MODULE_DIR}/.env" \
  -f "${ANAS_R11_MODULE_DIR}/docker-compose.yml" \
  up -d anas_samba_dc_anchor

for _ in $(seq 1 90); do
  ANAS_ANCHOR_HEALTH=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "${ANAS_ANCHOR_CONTAINER}")
  [ "${ANAS_ANCHOR_HEALTH}" = healthy ] && break
  sleep 2
done
test "${ANAS_ANCHOR_HEALTH}" = healthy
```

在 Consumer 仍停止时，用一个已批准的、禁用且不属于任何 `APP_*` 组的临时用户完成真实权限探针：

1. 通过正常管理接口在 `OU=People` 创建临时用户；
2. 等待 worker 写入 `anasIdentityAnchor` 和 `mS-DS-ConsistencyGuid`；
3. 查询并确认文本 UUID 与二进制 GUID 一致，同时 worker 仍为 `healthy`；
4. 删除临时用户并确认目录对象已删除；事件日志是 append-only，不得删事件。Consumer 启动后确认该
   用户的 Add/Delete 事件已消费，且 Consumer 中没有留下测试账户。

这个探针验证的是 `svc_anchor` 对新 GUID 的实际写权限；只读取 ACL 不能代替它。

## 7. 激活部署并验证 Consumer

DC、ACL 和 worker 验证通过后，激活已渲染的同一个 r11 候选部署：

```bash
anas apply --deployment "${ANAS_R11_DEPLOYMENT_ID}" -w "${ANAS_WORKSPACE_PATH}"
anas status -w "${ANAS_WORKSPACE_PATH}"
```

启动后再次确认 DC ready marker 和 anchor worker 健康。然后逐个验证迁移前登记的 Consumer：

- 已知用户仍映射到原账户，anchor 未变化，也没有产生重复账户；
- 登录、组授权和退出登录正常；
- Consumer 的同步/认证日志没有 schema、ACL、重复 anchor 或未知用户错误；
- 迁移期间的目录事件已被正常消费，没有持续重试或积压。

全部检查通过后才结束维护窗口。保留已 pin 的快照和外部证据目录，直到达到组织规定的观察期和保留期。

## 失败与整卷恢复

若 `--execute` 开始后的任一步骤失败，立即停止，不要再次运行 `--execute`。保持 Consumer 停止；如果候选 Compose 项目已经启动，先关闭它，然后恢复已校验的真实快照：

```bash
docker compose --project-name "${ANAS_COMPOSE_PROJECT}" \
  --project-directory "${ANAS_R11_MODULE_DIR}" \
  --env-file "${ANAS_R11_MODULE_DIR}/.env" \
  -f "${ANAS_R11_MODULE_DIR}/docker-compose.yml" down

anas stop -w "${ANAS_WORKSPACE_PATH}"
anas snapshot verify "${ANAS_SNAPSHOT_ID}" -w "${ANAS_WORKSPACE_PATH}"
anas snapshot restore "${ANAS_SNAPSHOT_ID}" --dry-run -w "${ANAS_WORKSPACE_PATH}"
anas snapshot restore "${ANAS_SNAPSHOT_ID}" -w "${ANAS_WORKSPACE_PATH}" -y
```

若候选 Compose 项目从未启动，则跳过第一条 `docker compose ... down`。

`snapshot restore` 会把 workspace data、活动制品、配置、lock 和 Secret 一起还原到同一个恢复点，这是保持目录和凭据一致性的必要行为。恢复后先用与恢复状态匹配的部署做离线检查，再决定启动。快照恢复会保持服务停止；只有确认整个 Samba 数据卷回到一致状态后才能恢复原 legacy 服务。外部证据目录不会随数据卷回滚，应原样保留供调查。

迁移实现位于 [`modules/samba_dc/samba_dc/root/usr/local/bin/migrate-identity-anchor-oid.sh`](https://github.com/anas-project/ANAS/blob/master/modules/samba_dc/samba_dc/root/usr/local/bin/migrate-identity-anchor-oid.sh)。schema 设计和限制见 [Samba identity anchor 架构](../architecture/samba-identity-anchor.md)。
