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
        { text: '特权 helper', link: '/operations/runbooks/privileged-helper' }
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
        { text: 'Module 时区与语言', link: '/reference/module-localization' },
        { text: 'Module 环境变量', link: '/reference/module-environment-variables' },
        { text: 'Module IAM / OIDC 支持', link: '/reference/module-iam-support' },
        { text: 'CLI JSON 契约', link: '/reference/contracts/' },
        { text: 'relational_database Contract', link: '/reference/contracts/relational_database' },
        { text: 'identity Contract', link: '/reference/contracts/identity' },
        { text: 'certificate Contract', link: '/reference/contracts/certificate' }
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
        { text: 'Module 文档规范', link: '/developer/module-documentation' },
        { text: 'Contract 文档规范', link: '/developer/contract-documentation' },
        { text: 'Module 升级 SOP', link: '/developer/module-upgrade-sop' },
        { text: '测试', link: '/developer/testing' },
        { text: '镜像发布', link: '/developer/release' },
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
        { text: 'Changelog 方案', link: '/research/changelog-plan-2026-08-18' },
        { text: 'samba-tool 用户与组管理', link: '/research/samba-tool-user-group-admin-guide-2026-08-17' },
        { text: 'Web API 与管理前端规划', link: '/research/web-api-admin-console-plan-2026-08-16' },
        { text: '应用研究 Module 规范', link: '/research/application-research-module-spec' },
        { text: 'S3 兼容存储调研', link: '/research/self-hosted-open-source-s3-compatible-storage-research-2026-08-15' },
        { text: '自部署 Git 服务调研', link: '/research/self-hosted-open-source-git-services-research-2026-08-15' },
        { text: 'Super Productivity 同类调研', link: '/research/super-productivity-alternatives-research-2026-08-15' },
        { text: '自部署笔记应用调研', link: '/research/self-hosted-open-source-notes-research-2026-08-13' },
        { text: '中国大陆镜像与 CNB', link: '/research/china-mainland-mirrors-and-cnb-distribution-2026-08-11' },
        { text: '自部署 Kanban 调研', link: '/research/self-hosted-open-source-kanban-research-2026-08-10' }
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
        { text: 'CLI contract', link: '/en/reference/cli-contract' },
        { text: 'Module catalog', link: '/en/reference/modules' },
        { text: 'Module timezone and language', link: '/en/reference/module-localization' },
        { text: 'Module IAM and OIDC support', link: '/en/reference/module-iam-support' },
        { text: 'relational_database Contract', link: '/en/reference/contracts/relational_database' },
        { text: 'identity Contract', link: '/en/reference/contracts/identity' },
        { text: 'certificate Contract', link: '/en/reference/contracts/certificate' }
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
        { text: 'Module documentation standard', link: '/en/developer/module-documentation' },
        { text: 'Contract documentation standard', link: '/en/developer/contract-documentation' },
        { text: 'Module upgrade SOP', link: '/en/developer/module-upgrade-sop' },
        { text: 'Testing', link: '/en/developer/testing' },
        { text: 'Image releases', link: '/en/developer/release' },
        { text: 'Documentation standard', link: '/en/developer/documentation-standard' },
        { text: 'Documentation site', link: '/en/developer/documentation' }
      ]
    }
  ],
  '/en/architecture/': [
    {
      text: 'Architecture',
      items: [
        { text: 'Overview', link: '/en/architecture/' }
      ]
    }
  ],
  '/en/research/': [
    {
      text: 'Research',
      items: [
        { text: 'Overview', link: '/en/research/' },
        { text: 'samba-tool user and group management', link: '/en/research/samba-tool-user-group-admin-guide-2026-08-17' }
      ]
    }
  ]
}
