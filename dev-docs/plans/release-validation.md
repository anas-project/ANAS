---
doc_type: plan
status: implementing
created: 2026-11-02
updated: 2026-11-02
---

# 发布状态与依赖验证实施计划

规范：[发布状态与依赖验证](../requirements/release-validation.md)。沿用现有分支、双 Registry 和版本计算流程，不增加服务。

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：依赖提醒与验收审查门禁 | RELPUB-R-001 | 实施中；扫描和前置阻断已实现，本机反例通过；线上候选与人工依赖审查尚未收口 |
| M2：仅 release 的发布范围与目录保持 | RELPUB-R-002 | 实施中；镜像/mirror/Module 范围过滤及旧目录合并已接线，shell 回归通过；线上发布未执行 |

## 本轮候选与限制

用户选择当前提交中的12个release Module，加Forgejo/Incus已验收的amd64候选，并同时发布Core。
原基线`6e6a777e`已推master以运行CI；未将原工作区的其他并行修改带入。Core下一自动版本计算为`0.1.2`。
Module正式revision仍由上次成功边界`image-release/46-2`计算，不把本地开发修订当作已发布身份。

拟发布14项：collabora、ddns_go、ddns_updater、eturnal、forgejo、incus、lam、lego、mariadb、meshcentral、oauth2_proxy、samba_dc、samba_fs、traefik。
本次排除12项：ai_agent、casdoor、freeradius、immich、llng、mail、netbird、nextcloud、postgres、user_portal、versitygw、vikunja。
Forgejo/Incus在依赖审查结束前保留developing；不得为了凑足14个自动改状态或放宽新门禁。

## RELPUB-R-001：当前审查结果

`check-release-modules`复用现有inventory校验与YAML/semver依赖，无新增第三方依赖。
输出release及排除名单、非release依赖链、条件与Provider类型；遍历防环，Provider依照接口及Contract版本匹配。
这是所有可能Provider的保守检查，不猜测某台部署的实际选择；`--list`仅用于状态筛选，不作为放行结果。
前置工作流在revision写入和制品上传之前执行，未核实依赖则失败并写GitHub summary。

对原基线12个release Module实际扫描，需审查的developing依赖为casdoor、llng、nextcloud、postgres。
例如collabora直接依赖nextcloud；meshcentral/oauth2_proxy的IAM候选包含casdoor/llng；间接数据库依赖包含postgres。
当前记录核对：Casdoor旧生命周期通过但存在撤权积压缺陷、LDAP并发修复未完成实机复验；LLNG目录事件/适用消费者/SAML/恢复范围仍有未结项；Nextcloud新增回收站真实验收未执行。
PostgreSQL已有原生认证、扩展升级及一致恢复通过基线，但需确认当前版本连续性；user_portal的计划完成不证明当前提交包含实测源码，相关iframe会话检查等仍在未提交修改中。
不能据“已发布旧包”自动放行当前源码，也不自动把这些依赖加入本批发布。

本机扫描按预期返回非零并保留机器报告，属于新门禁成功拦截，不是依赖已验收通过。
未向image-release推送候选，未上传新制品，Core发布独立等待线上CI通过。

## CI发现与修复

原基线线上CI：[41408071627](https://github.com/anas-project/ANAS/actions/runs/41408071627)。
Web、脚本和漏洞扫描通过；文档因四份relational_database生成页陈旧失败，已在隔离候选从现有源文件重新生成。
Go的Incus连接包测试在Linux默认/tmp下失败：生产检查正确拒绝可写祖先目录；原测试把/tmp当安全根。
测试改用经同一安全检查验证的私有home临时目录，并保留0600、symlink和内容反例；生产安全策略不改，本机Hook全包通过。后续线上CI仍需重验。

## 证据与下一步

原始CI日志、当前扫描JSON和回归日志暂存Mac `/tmp/anas-online-ci-*`、`/tmp/anas-release-dependency-*`，不是共享持久档案。
既有GHCR stable目录只读核对digest为`sha256:2997468033d84ac40f8bc262e8188a8c5eb146bf36e17b1cc822b1c3e30a34fd`，source_commit为6171f59d。
新的catalog仅替换本次实际发布条目，保留其他已发布版本；两Registry既有stable不一致或取回失败时中止，不视为空目录。
Incus的正式发布还需把ln已签收的完整Runner目录与字节接入打包任务；源码catalog仍为空，不能直接重新打包冒充已签收版本。本次门禁阻止提前上传该空目录候选。
下一步：完成工作流与新门禁的线上验证，向用户结算开发依赖的实际发布标准，再推进获准的Module发布；Core可独立推进。
