import type { DefaultTheme } from 'vitepress'
import { moduleSidebar } from './module-docs'

export const zhSidebar: DefaultTheme.Sidebar = {
  '/getting-started/': [
    {
      text: '快速开始',
      items: [
        { text: '概览', link: '/getting-started/' },
        { text: '安装与要求', link: '/getting-started/installation' },
        { text: '首次部署', link: '/getting-started/quick-start' }
      ]
    }
  ],
  '/guide/': [
    {
      text: '使用指南',
      items: [
        { text: '概览', link: '/guide/' },
        { text: '配置', link: '/guide/configuration' },
        { text: '服务生命周期', link: '/guide/service-lifecycle' },
        { text: '备份与恢复', link: '/guide/backup-and-restore' },
        { text: '完整任务指南', link: '/guide/usage' }
      ]
    }
  ],
  '/operations/': [
    {
      text: '运维',
      items: [
        { text: '概览', link: '/operations/' },
        { text: '存储', link: '/operations/storage' },
        { text: '网络', link: '/operations/networking' },
        { text: '故障排查', link: '/operations/troubleshooting' },
        { text: 'Samba', link: '/operations/samba' },
        { text: 'Traefik', link: '/operations/traefik' }
      ]
    },
    {
      text: 'Runbook',
      collapsed: true,
      items: [
        { text: '挂载与格式化', link: '/operations/runbooks/mount' },
        { text: '特权 helper', link: '/operations/runbooks/privileged-helper' },
        { text: 'samba-tool 用户与组管理', link: '/operations/runbooks/samba-tool-user-management' }
      ]
    }
  ],
  '/reference/': [
    {
      text: '参考',
      items: [
        { text: '概览', link: '/reference/' },
        { text: '配置结构', link: '/reference/configuration' },
        { text: 'Module 目录', link: '/reference/modules' },
        { text: 'Module 专属命令', link: '/reference/module-commands' },
        { text: 'Module 时区与语言', link: '/reference/module-localization' },
        { text: 'Module 环境变量', link: '/reference/module-environment-variables' },
        { text: 'Module IAM / OIDC 支持', link: '/reference/module-iam-support' },
        { text: 'CLI JSON 契约', link: '/reference/contracts/' },
        { text: 'object_storage Contract', link: '/reference/module-contracts/object_storage' },
        { text: 'relational_database Contract', link: '/reference/module-contracts/relational_database' },
        { text: 'identity Contract', link: '/reference/module-contracts/identity' },
        { text: 'certificate Contract', link: '/reference/module-contracts/certificate' },
        { text: 'compute Contract', link: '/reference/module-contracts/compute' }
      ]
    },
    {
      text: 'Module',
      collapsed: true,
      items: moduleSidebar(false)
    }
  ],
  '/developer/': [
    {
      text: '开发者指南',
      items: [
        { text: '概览', link: '/developer/' },
        { text: '仓库结构', link: '/developer/repository-layout' },
        { text: 'Module 开发', link: '/developer/module-development' },
        { text: 'Capability 开发标准', link: '/developer/capability-development' },
        { text: 'Module 设计检查表', link: '/developer/module-design-checklist' },
        { text: 'Module 文档规范', link: '/developer/module-documentation' },
        { text: 'Contract 文档规范', link: '/developer/contract-documentation' },
        { text: 'Module 升级 SOP', link: '/developer/module-upgrade-sop' },
        { text: 'Module 升级检查表', link: '/developer/module-upgrade-checklist' },
        { text: '测试', link: '/developer/testing' },
        { text: '镜像发布', link: '/developer/release' },
        { text: '中国大陆构建与发行', link: '/developer/china-mainland-build-and-distribution' },
        { text: '需求编写规范', link: '/developer/requirement-authoring' },
        { text: '应用研究文档规范', link: '/developer/research-document-standard' },
        { text: 'Changelog 规范', link: '/developer/changelog-standard' },
        { text: '文档写作标准', link: '/developer/documentation-standard' },
        { text: '文档站点', link: '/developer/documentation' }
      ]
    }
  ],
  '/architecture/': [
    {
      text: '架构与设计',
      items: [
        { text: '设计索引', link: '/architecture/' },
        { text: 'Module、Contract 与 Resource', link: '/architecture/module-contract-resource-design' },
        { text: '管理员账号系统', link: '/architecture/admin-account-system' },
        { text: 'Samba AD 用户与组规范', link: '/architecture/samba-ad-user-planning' },
        { text: 'IAM 能力', link: '/architecture/iam-capability-design' },
        { text: '应用目录', link: '/architecture/app-catalog-design' },
        { text: '动态 DNS', link: '/architecture/dynamic-dns-capability-design' },
        { text: 'Object Storage 能力', link: '/architecture/object-storage-capability-design' },
        { text: 'Forgejo Module', link: '/architecture/forgejo-module-design' },
        { text: 'AI Agent 编排', link: '/architecture/ai-agent-orchestration-design' },
        { text: '凭据轮换', link: '/architecture/credential-rotation' },
        { text: '运行时与发布状态', link: '/architecture/runtime-release-state-design' },
        { text: '配置状态生命周期', link: '/architecture/config-state-lifecycle' }
      ]
    }
  ],
  '/research/': [
    {
      text: '研究与选型',
      items: [
        { text: '索引', link: '/research/' },
        { text: 'IAM 登出与会话同步', link: '/research/iam-logout-application-session-sync' },
        { text: '自部署 IAM 与 ANAS 适配', link: '/research/self-hosted-open-source-iam-research' },
        { text: 'Nextcloud 搜索方案', link: '/research/nextcloud-search-solution-research' },
        { text: 'BIND 9 Web 管理工具', link: '/research/bind9-open-source-web-management-research' },
        { text: '自部署邮件服务', link: '/research/self-hosted-open-source-mail-services-research' },
        { text: '自部署邮件转发', link: '/research/self-hosted-open-source-email-forwarding-research' },
        { text: '看板与 AI Agent', link: '/research/kanban-ai-agent-integration-research' },
        { text: 'S3 兼容存储', link: '/research/self-hosted-open-source-s3-compatible-storage-research' },
        { text: '自部署 Git 服务', link: '/research/self-hosted-open-source-git-services-research' },
        { text: 'Super Productivity Nextcloud SSO', link: '/research/super-productivity-nextcloud-sso-sync-research' },
        { text: 'Super Productivity 同类项目', link: '/research/super-productivity-alternatives-research' },
        { text: '自部署笔记应用', link: '/research/self-hosted-open-source-notes-research' },
        { text: '自部署 Kanban 应用', link: '/research/self-hosted-open-source-kanban-research' }
      ]
    }
  ],
  '/requirements/': [
    {
      text: '需求与验收标准',
      items: [
        { text: '索引', link: '/requirements/' },
        { text: 'IAM Provider 准入要求', link: '/requirements/iam-provider' },
        { text: 'Changelog 要求', link: '/requirements/changelog' },
        { text: 'Web API 与管理前端要求', link: '/requirements/web-api-admin-console' },
        { text: 'Forgejo Module 集成要求', link: '/requirements/forgejo-module' },
        { text: 'Vikunja Module 集成要求', link: '/requirements/vikunja-module' },
        { text: 'VersityGW S3 Module', link: '/requirements/versitygw-module' },
        { text: 'Incus compute Provider', link: '/requirements/incus-module' },
        { text: 'Module IAM 双向登出', link: '/requirements/module-iam-bidirectional-logout' },
        { text: '需求 ID 矩阵采用范围', link: '/requirements/requirement-id-adoption' },
        { text: '文档驱动测试自动化', link: '/requirements/document-driven-test-automation' }
      ]
    }
  ],
  '/plans/': [
    {
      text: '实施计划',
      items: [
        { text: '索引', link: '/plans/' },
        { text: '应用域与 AD 域分离', link: '/plans/domain-separation' },
        { text: 'Changelog', link: '/plans/changelog' },
        { text: 'Web API 与管理前端', link: '/plans/web-api-admin-console' },
        { text: 'workspace 与备份', link: '/plans/workspace-backup' },
        { text: 'Forgejo Module', link: '/plans/forgejo-module' },
        { text: 'VersityGW S3 Module', link: '/plans/versitygw-module' },
        { text: 'Incus compute Provider', link: '/plans/incus-module' },
        { text: '需求 ID 矩阵采用', link: '/plans/requirement-id-adoption' },
        { text: '文档驱动测试自动化', link: '/plans/document-driven-test-automation' }
      ]
    }
  ],
  '/reviews/': [
    {
      text: '评审与历史快照',
      items: [
        { text: '索引', link: '/reviews/' },
        { text: 'Vikunja Module 设计符合性', link: '/reviews/2026-08-21-vikunja-module-design-compliance' },
        { text: 'Module 分类与访问边界', link: '/reviews/2026-08-19-module-classification' },
        { text: 'Module 镜像升级评估', link: '/reviews/2026-07-29-module-image-upgrade' },
        { text: '设计问题审查', link: '/reviews/2026-07-19-design-review' }
      ]
    }
  ],
  '/governance/': [
    {
      text: '项目治理',
      items: [
        { text: '概览', link: '/governance/' },
        { text: 'IANA PEN 申请', link: '/governance/iana-pen-application' }
      ]
    }
  ]
}

export const enSidebar: DefaultTheme.Sidebar = {
  '/en/getting-started/': [
    {
      text: 'Getting Started',
      items: [
        { text: 'Overview', link: '/en/getting-started/' },
        { text: 'Installation', link: '/en/getting-started/installation' },
        { text: 'First deployment', link: '/en/getting-started/quick-start' }
      ]
    }
  ],
  '/en/guide/': [
    {
      text: 'User Guide',
      items: [
        { text: 'Overview', link: '/en/guide/' },
        { text: 'Configuration', link: '/en/guide/configuration' },
        { text: 'Service lifecycle', link: '/en/guide/service-lifecycle' },
        { text: 'Backup and restore', link: '/en/guide/backup-and-restore' },
        { text: 'Complete task guide', link: '/en/guide/usage' }
      ]
    }
  ],
  '/en/operations/': [
    {
      text: 'Operations',
      items: [
        { text: 'Overview', link: '/en/operations/' },
        { text: 'Storage', link: '/en/operations/storage' },
        { text: 'Networking', link: '/en/operations/networking' },
        { text: 'Troubleshooting', link: '/en/operations/troubleshooting' },
        { text: 'samba-tool user and group management', link: '/en/operations/runbooks/samba-tool-user-management' },
        { text: 'Traefik', link: '/en/operations/traefik' }
      ]
    }
  ],
  '/en/reference/': [
    {
      text: 'Reference',
      items: [
        { text: 'Overview', link: '/en/reference/' },
        { text: 'Configuration structure', link: '/en/reference/configuration' },
        { text: 'CLI JSON contracts', link: '/en/reference/contracts/' },
        { text: 'Module catalog', link: '/en/reference/modules' },
        { text: 'Module-specific commands', link: '/en/reference/module-commands' },
        { text: 'Module environment variables', link: '/en/reference/module-environment-variables' },
        { text: 'Module timezone and language', link: '/en/reference/module-localization' },
        { text: 'Module IAM and OIDC support', link: '/en/reference/module-iam-support' },
        { text: 'object_storage Contract', link: '/en/reference/module-contracts/object_storage' },
        { text: 'relational_database Contract', link: '/en/reference/module-contracts/relational_database' },
        { text: 'identity Contract', link: '/en/reference/module-contracts/identity' },
        { text: 'certificate Contract', link: '/en/reference/module-contracts/certificate' },
        { text: 'compute Contract', link: '/en/reference/module-contracts/compute' }
      ]
    },
    {
      text: 'Modules',
      collapsed: true,
      items: moduleSidebar(true)
    }
  ],
  '/en/developer/': [
    {
      text: 'Developer Guide',
      items: [
        { text: 'Overview', link: '/en/developer/' },
        { text: 'Repository layout', link: '/en/developer/repository-layout' },
        { text: 'Module development', link: '/en/developer/module-development' },
        { text: 'Module design checklist', link: '/en/developer/module-design-checklist' },
        { text: 'Module documentation standard', link: '/en/developer/module-documentation' },
        { text: 'Contract documentation standard', link: '/en/developer/contract-documentation' },
        { text: 'Module upgrade SOP', link: '/en/developer/module-upgrade-sop' },
        { text: 'Module upgrade checklist', link: '/en/developer/module-upgrade-checklist' },
        { text: 'Testing', link: '/en/developer/testing' },
        { text: 'Image releases', link: '/en/developer/release' },
        { text: 'Changelog standard', link: '/en/developer/changelog-standard' },
        { text: 'Documentation standard', link: '/en/developer/documentation-standard' },
        { text: 'Documentation site', link: '/en/developer/documentation' }
      ]
    }
  ],
  '/en/architecture/': [
    {
      text: 'Architecture',
      items: [
        { text: 'Overview', link: '/en/architecture/' },
        { text: 'Core implementation standard', link: '/en/architecture/core-implementation-standard' },
        { text: 'Module, Contract, Resource', link: '/en/architecture/module-contract-resource-design' },
        { text: 'IAM capability design', link: '/en/architecture/iam-capability-design' },
        { text: 'Object storage capability design', link: '/en/architecture/object-storage-capability-design' }
      ]
    }
  ],
  '/en/research/': [
    {
      text: 'Research',
      items: [
        { text: 'Overview', link: '/en/research/' }
      ]
    }
  ],
  '/en/requirements/': [
    {
      text: 'Requirements',
      items: [{ text: 'Overview', link: '/en/requirements/' }]
    }
  ],
  '/en/plans/': [
    {
      text: 'Implementation plans',
      items: [{ text: 'Overview', link: '/en/plans/' }]
    }
  ],
  '/en/reviews/': [
    {
      text: 'Reviews',
      items: [{ text: 'Overview', link: '/en/reviews/' }]
    }
  ]
}
