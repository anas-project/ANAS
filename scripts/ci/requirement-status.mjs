// Generates the status column of docs/requirements/index.md from the documents
// themselves, so nobody maintains a completion figure by hand.
//
// Run with --check in CI to fail when the checked-in column differs from the
// calculation, the same contract gen-module-docs uses for its parameter tables.

import { readFileSync, writeFileSync, readdirSync, existsSync } from 'node:fs'
import { join } from 'node:path'
import { documentStatus, renderIndex, missingRows, milestoneStates } from './requirement-status-lib.mjs'

const requirementsDir = 'docs/requirements'
const plansDir = 'docs/plans'
const indexPath = join(requirementsDir, 'index.md')

const rows = new Map()
const errors = []

for (const name of readdirSync(requirementsDir).sort()) {
  if (!name.endsWith('.md') || name === 'index.md') continue
  const planPath = join(plansDir, name)
  const status = documentStatus(
    readFileSync(join(requirementsDir, name), 'utf8'),
    existsSync(planPath) ? readFileSync(planPath, 'utf8') : null
  )
  if (status.kind === 'bad-state') {
    errors.push(
      `${planPath}: 里程碑 ${status.unknown.join('、')} 的状态列不以 ` +
      `${milestoneStates.join(' / ')} 之一开头；细节写在分号之后`
    )
    continue
  }
  if (status.kind === 'unassigned') {
    errors.push(
      `${join(requirementsDir, name)}: ${status.unassigned} 项需求没有里程碑归属，` +
      '先跑 npm run docs:check-requirements'
    )
    continue
  }
  rows.set(name, status.label)
}

const current = readFileSync(indexPath, 'utf8')
for (const name of missingRows(current, rows.keys())) {
  errors.push(`${indexPath}: 缺少 ${name} 的条目；生成器只填状态列，条目的标题和范围要人写`)
}

if (errors.length > 0) {
  console.error('需求状态生成失败：\n')
  for (const error of errors) console.error(`  - ${error}`)
  process.exit(1)
}

const next = renderIndex(current, rows)

if (process.argv.includes('--check')) {
  if (current !== next) {
    console.error(`${indexPath} 的状态列已过期；运行 npm run docs:requirement-status 重新生成。`)
    process.exit(1)
  }
  console.log(`${indexPath}: ${rows.size} 份文档的状态列与计算一致。`)
} else {
  if (current !== next) writeFileSync(indexPath, next)
  for (const [name, label] of rows) console.log(`  ${name.padEnd(40)} ${label}`)
  console.log(`\n已写入 ${indexPath}。`)
}
