---
layout: home
title: ANAS 文档
titleTemplate: false

hero:
  name: ANAS
  text: 可组合的 NAS 服务启动器
  tagline: 从首次部署到备份恢复，以及 Module 开发和系统设计。
  actions:
    - theme: brand
      text: 快速开始
      link: /getting-started/quick-start
    - theme: alt
      text: 使用指南
      link: /guide/
    - theme: alt
      text: 开发者文档
      link: /developer/

features:
  - title: 新用户
    details: 安装 ANAS，创建 workspace，并完成第一次部署。
    link: /getting-started/
  - title: 管理员
    details: 管理配置、服务生命周期、存储、备份与故障恢复。
    link: /operations/
  - title: 开发者
    details: 理解仓库结构、Module 契约、CLI 接口和架构设计。
    link: /developer/
---

## 从哪里开始

- 第一次使用：从[快速开始](/getting-started/quick-start)开始。
- 已经运行 ANAS：查看[使用指南](/guide/)和[运维文档](/operations/)。
- 开发 Module 或 Runner：查看[开发者指南](/developer/)和[架构设计](/architecture/)。

`docs/` 中只保留持续维护且可以公开发布的内容。带主机地址、临时凭据或单次验证过程的记录应放在受控的 Issue、CI artifact 或外部私有系统中；稳定结论再整理进当前指南、参考或设计文档。
