// Requires a status declaration on every architecture, requirement and plan
// document. Index pages are excluded: they list documents rather than describe
// a design, so they have no implementation status of their own.

import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { checkDocumentStatus, parseDocumentStatus } from './document-status-lib.mjs'
import { requirementScopes } from './requirement-docs-lib.mjs'

// Archived plans are included: a finished plan still has to say so at the top,
// and its directory is exactly where a reader is most likely to mistake a
// historical baseline for current instructions.
const directories = ['docs/architecture']
for (const { requirementsDir, plansDir, archivedPlansDir } of requirementScopes()) {
  directories.push(requirementsDir, plansDir, archivedPlansDir)
}

const documents = []

for (const directory of directories) {
  if (!existsSync(directory)) continue
  for (const name of readdirSync(directory).sort()) {
    if (!name.endsWith('.md') || name === 'index.md') continue
    const path = join(directory, name)
    documents.push({ path, markdown: readFileSync(path, 'utf8') })
  }
}

const { errors, checked } = checkDocumentStatus(documents)

if (errors.length > 0) {
  console.error('\n文档状态校验失败：\n')
  for (const error of errors) console.error(`  - ${error}`)
  console.error(
    '\n设计、需求与计划文档必须在开头声明状态，读者才能分辨「已经跑着的机制」和「设想」。' +
    '\n见 docs/developer/documentation-standard.md 的准确性与状态一节。'
  )
  process.exit(1)
}

const counts = new Map()
for (const { markdown } of documents) {
  const { source } = parseDocumentStatus(markdown)
  counts.set(source, (counts.get(source) ?? 0) + 1)
}

const shape = [...counts].map(([source, count]) => `${source} ${count}`).join('，')
console.log(`文档状态校验通过：${checked} 份文档（${shape}）。`)
