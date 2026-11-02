---
doc_type: requirement
status: current
created: 2026-11-02
updated: 2026-11-02
---

# 发布状态与依赖验证

本主题约束既有 `image-release` 流程；不另建发布系统。Core 不内嵌 Module，仍走 `anas-release` 的独立门禁。

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `RELPUB-R-001` | Module 发布前检查 release Module 的直接及间接依赖、备选依赖、Capability 和版本/interface 兼容的 Contract Provider；条件依赖与 Provider 候选明确标注。发现 developing 或其他非 release 依赖时提醒用户并核对配对需求/计划、实测源码及制品基线、未完成项；未核实放行前阻止该批制品上传。不得将计划 done、上层 release 或已有线上包当作被依赖组件已达标的充分证据 | 单元、工作流检查、当前源码报告及人工审查 |
| `RELPUB-R-002` | 既有发布流程只为 status=release 的 Module 新发布 bundle、派生镜像及所需 mirror，手工指定 Module 同样不能绕过。发布清单区分本次发布与排除项；更新 stable 发现目录时保留本次未发布组件的既有版本，不能改指向未发布版本或静默删除 | 单元及工作流 shell 回归 |
