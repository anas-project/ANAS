import { readFileSync } from 'node:fs'
import type { DefaultTheme } from 'vitepress'

interface ModuleIndex {
  modules: Array<{
    title: string
    current: string
    status: string
    link_zh: string
    link_en: string
  }>
}

function readIndex(): ModuleIndex {
  try {
    return JSON.parse(readFileSync(new URL('../generated/module-docs.json', import.meta.url), 'utf8')) as ModuleIndex
  } catch {
    // The generated index exists only in the disposable documentation source
    // tree. Keeping this fallback makes config linting and archived builds safe.
    return { modules: [] }
  }
}

export function moduleSidebar(english: boolean): DefaultTheme.SidebarItem[] {
  return readIndex().modules.map((module) => ({
    text: `${module.title} ${module.current}`,
    link: english ? module.link_en : module.link_zh
  }))
}
