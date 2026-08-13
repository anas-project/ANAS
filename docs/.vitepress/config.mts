import { defineConfig } from 'vitepress'
import { enNav, zhNav } from './config/nav'
import { enSidebar, zhSidebar } from './config/sidebar'

const repository = process.env.GITHUB_REPOSITORY?.split('/')[1]
const base = process.env.DOCS_BASE ?? (repository ? `/${repository}/` : '/')

export default defineConfig({
  title: 'ANAS',
  description: 'Composable NAS service launcher',
  base,
  cleanUrls: true,
  lastUpdated: true,
  locales: {
    root: {
      label: '简体中文',
      lang: 'zh-CN',
      title: 'ANAS 文档',
      description: '可组合的 NAS 服务启动器'
    },
    en: {
      label: 'English',
      lang: 'en-US',
      link: '/en/',
      title: 'ANAS Documentation',
      description: 'Composable NAS service launcher'
    }
  },
  markdown: {
    lineNumbers: true
  },
  themeConfig: {
    search: {
      provider: 'local',
      options: {
        locales: {
          root: {
            translations: {
              button: {
                buttonText: '搜索文档',
                buttonAriaLabel: '搜索文档'
              },
              modal: {
                noResultsText: '没有找到相关结果',
                resetButtonTitle: '清除查询',
                footer: {
                  selectText: '选择',
                  navigateText: '切换',
                  closeText: '关闭'
                }
              }
            }
          },
          en: {
            translations: {
              button: {
                buttonText: 'Search',
                buttonAriaLabel: 'Search documentation'
              }
            }
          }
        }
      }
    },
    socialLinks: [
      { icon: 'github', link: 'https://github.com/anas-project/ANAS' }
    ],
    locales: {
      root: {
        nav: zhNav,
        sidebar: zhSidebar,
        outline: { label: '本页目录', level: [2, 3] },
        docFooter: { prev: '上一页', next: '下一页' },
        lastUpdated: { text: '最后更新于' },
        editLink: {
          pattern: 'https://github.com/anas-project/ANAS/edit/master/docs/:path',
          text: '在 GitHub 上编辑此页'
        },
        langMenuLabel: '切换语言',
        returnToTopLabel: '返回顶部',
        sidebarMenuLabel: '目录'
      },
      en: {
        nav: enNav,
        sidebar: enSidebar,
        outline: { label: 'On this page', level: [2, 3] },
        docFooter: { prev: 'Previous', next: 'Next' },
        lastUpdated: { text: 'Last updated' },
        editLink: {
          pattern: 'https://github.com/anas-project/ANAS/edit/master/docs/:path',
          text: 'Edit this page on GitHub'
        },
        langMenuLabel: 'Change language',
        returnToTopLabel: 'Return to top',
        sidebarMenuLabel: 'Menu'
      }
    }
  }
})
