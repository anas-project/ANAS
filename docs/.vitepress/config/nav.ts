import type { DefaultTheme } from 'vitepress'

export const zhNav: DefaultTheme.NavItem[] = [
  { text: '快速开始', link: '/getting-started/' },
  { text: '使用指南', link: '/guide/' },
  { text: '运维', link: '/operations/' },
  { text: '参考', link: '/reference/' },
  { text: '开发', link: '/developer/' },
  { text: '架构', link: '/architecture/' },
  {
    text: '项目资料',
    items: [
      { text: '研究与选型', link: '/research/' },
      { text: '项目治理', link: '/governance/' }
    ]
  }
]

export const enNav: DefaultTheme.NavItem[] = [
  { text: 'Getting Started', link: '/en/getting-started/' },
  { text: 'User Guide', link: '/en/guide/' },
  { text: 'Operations', link: '/en/operations/' },
  { text: 'Reference', link: '/en/reference/' },
  { text: 'Development', link: '/en/developer/' },
  { text: 'Architecture', link: '/en/architecture/' },
  {
    text: 'Project',
    items: [
      { text: 'Research', link: '/en/research/' },
      { text: 'Governance', link: '/en/governance/' }
    ]
  }
]
