import { execFileSync } from 'node:child_process'
import type { DefaultTheme } from 'vitepress'

interface VersionLink {
  version: string
  path: string
  current: boolean
}

const stableVersionPattern = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/

function detectedVersion(): string {
  try {
    const tags = execFileSync('git', ['tag', '--list', 'v*', '--sort=-version:refname'], {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore']
    })
    return tags.split('\n').find((tag) => stableVersionPattern.test(tag)) ?? 'development'
  } catch {
    return 'development'
  }
}

function versionLinks(currentVersion: string): VersionLink[] {
  const serializedLinks = process.env.DOCS_VERSION_LINKS
  if (!serializedLinks) return [{ version: currentVersion, path: '/', current: true }]

  const links = JSON.parse(serializedLinks) as VersionLink[]
  if (!Array.isArray(links) || links.some((link) =>
    typeof link.version !== 'string' || typeof link.path !== 'string' || typeof link.current !== 'boolean'
  )) {
    throw new Error('DOCS_VERSION_LINKS must be a JSON array of version links')
  }
  return links
}

function normalizeBase(base: string): string {
  const withLeadingSlash = base.startsWith('/') ? base : `/${base}`
  return withLeadingSlash.endsWith('/') ? withLeadingSlash : `${withLeadingSlash}/`
}

function linkAtRoot(rootBase: string, versionPath: string): string {
  return versionPath === '/' ? rootBase : `${rootBase.slice(0, -1)}${versionPath}`
}

export function documentationVersion(base: string) {
  const currentVersion = process.env.DOCS_VERSION ?? detectedVersion()
  const links = versionLinks(currentVersion)
  const rootBase = normalizeBase(process.env.DOCS_ROOT_BASE ?? base)
  const siteOrigin = process.env.DOCS_SITE_ORIGIN ?? 'https://anas-project.github.io'
  const archive = process.env.DOCS_IS_ARCHIVE === 'true'

  function nav(language: 'zh' | 'en'): DefaultTheme.NavItem {
    const text = archive
      ? (language === 'zh' ? `历史版 ${currentVersion}` : `Archived ${currentVersion}`)
      : (language === 'zh' ? `稳定版 ${currentVersion}` : `Stable ${currentVersion}`)

    return {
      text,
      items: links.map((link) => ({
        text: link.current
          ? `${link.version}${language === 'zh' ? '（最新版）' : ' (latest)'}`
          : link.version,
        link: archive ? new URL(linkAtRoot(rootBase, link.path), siteOrigin).href : link.path,
        target: archive ? '_self' : undefined,
        noIcon: archive
      }))
    }
  }

  return { currentVersion, archive, nav }
}
