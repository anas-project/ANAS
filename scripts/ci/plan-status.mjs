// Generates the status column of dev-docs/plans/index.md from each plan's own
// frontmatter, so nobody maintains it by hand.
//
// Run with --check in CI to fail when the checked-in column differs from the
// documents, the same contract requirement-status and gen-module-docs use.

import { readFileSync, writeFileSync, readdirSync, existsSync } from 'node:fs'
import { join } from 'node:path'
import { planStatus, planStates, renderIndex, missingRows, staleRows } from './plan-status-lib.mjs'
import { requirementScopes, repositoryScopeLabel, archivedPlansDirName } from './requirement-docs-lib.mjs'

const scope = requirementScopes().find((candidate) => candidate.label === repositoryScopeLabel)
if (scope === undefined) {
  console.error('找不到仓库级 dev-docs 作用域。')
  process.exit(1)
}

const indexPath = join(scope.plansDir, 'index.md')
const rows = new Map()
// Every plan file that exists, whatever its status parsed to. Staleness is about
// whether the file is there; reporting a row as stale because its status was
// rejected would bury the real error under a wrong one.
const present = new Set()
const errors = []

// Archived plans keep their row in the index. Dropping them would answer "where
// did that plan go" with silence, which is the same failure as a stale column.
const sources = [
  { dir: scope.plansDir, archived: false },
  { dir: scope.archivedPlansDir, archived: true }
]

for (const { dir, archived } of sources) {
  if (!existsSync(dir)) continue
  for (const name of readdirSync(dir).sort()) {
    if (!name.endsWith('.md') || name === 'index.md') continue

    const path = join(dir, name)
    present.add(name)
    const status = planStatus(readFileSync(path, 'utf8'))
    if (status.kind === 'no-frontmatter' || status.kind === 'no-status') {
      errors.push(`${path}: 缺少 frontmatter 的 status 字段；取值为 ${planStates.join(' / ')} 之一`)
      continue
    }
    if (status.kind === 'bad-state') {
      errors.push(`${path}: status ${status.status} 不在 ${planStates.join(' / ')} 之内`)
      continue
    }
    // A finished plan that is still in the active directory is the thing rule 5
    // of the documentation standard forbids: a file that reads as in-flight when
    // it is not. Catch it here rather than trusting everyone to remember.
    if (status.status === 'done' && !archived) {
      errors.push(`${path}: status done 的计划应移入 ${archivedPlansDirName}/，不留在活跃计划目录`)
      continue
    }
    if (archived && status.status !== 'done') {
      errors.push(`${path}: 已归档但 status 是 ${status.status}；归档目录只放 done`)
      continue
    }

    rows.set(name, archived ? `${status.label}（已归档）` : status.label)
  }
}

const current = readFileSync(indexPath, 'utf8')
for (const name of missingRows(current, rows.keys())) {
  errors.push(`${indexPath}: 缺少 ${name} 的条目；生成器只填状态列，条目的标题和范围要人写`)
}
for (const name of staleRows(current, present)) {
  errors.push(`${indexPath}: ${name} 的条目指向一份不存在的计划`)
}

if (errors.length > 0) {
  console.error('计划状态生成失败：\n')
  for (const error of errors) console.error(`  - ${error}`)
  process.exit(1)
}

const next = renderIndex(current, rows)

if (process.argv.includes('--check')) {
  if (current !== next) {
    console.error(`${indexPath} 的状态列已过期；运行 npm run docs:plan-status 重新生成。`)
    process.exit(1)
  }
  console.log(`${indexPath}: ${rows.size} 份计划的状态列与文档一致。`)
} else {
  if (current !== next) writeFileSync(indexPath, next)
  for (const [name, label] of rows) console.log(`  ${name.padEnd(42)} ${label}`)
  console.log(`\n已写入 ${indexPath}。`)
}
