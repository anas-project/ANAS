// Checks every requirement matrix against the plan that delivers it.
//
// Pairing is by filename: docs/requirements/<topic>.md is delivered by
// docs/plans/<topic>.md. A requirement document without a matrix is skipped --
// not every document has adopted IDs yet -- but one that has a matrix and no
// paired plan is an error, because nothing would ever schedule its requirements.

import { readFileSync, readdirSync, existsSync } from 'node:fs'
import { join } from 'node:path'
import {
  parseRequirementMatrix,
  parseMilestoneAssignments,
  parseE2eRecords,
  checkRequirementCoverage
} from './requirement-coverage-lib.mjs'

const requirementsDir = 'docs/requirements'
const plansDir = 'docs/plans'

const failures = []
let checkedDocuments = 0
let checkedRequirements = 0

for (const name of readdirSync(requirementsDir).sort()) {
  if (!name.endsWith('.md') || name === 'index.md') continue

  const requirementPath = join(requirementsDir, name)
  const matrix = parseRequirementMatrix(readFileSync(requirementPath, 'utf8'))
  if (matrix.requirements.length === 0) continue

  const planPath = join(plansDir, name)
  if (!existsSync(planPath)) {
    failures.push(`${requirementPath}: 有需求矩阵但缺少配套计划 ${planPath}`)
    continue
  }

  const plan = readFileSync(planPath, 'utf8')
  const knownNumbers = new Set(matrix.requirements.map((requirement) => requirement.number))
  const { errors, checked, retired } = checkRequirementCoverage({
    matrix,
    assignments: parseMilestoneAssignments(plan, knownNumbers),
    e2eRecords: parseE2eRecords(plan)
  })

  checkedDocuments += 1
  checkedRequirements += checked

  if (errors.length > 0) {
    failures.push(`${requirementPath} ↔ ${planPath}`, ...errors.map((error) => `  - ${error}`))
    continue
  }

  const retiredNote = retired > 0 ? `，另有 ${retired} 项已废弃` : ''
  console.log(`${name}: ${checked} 项需求全部有归属，e2e 记录一致${retiredNote}`)
}

if (failures.length > 0) {
  console.error('\n需求覆盖校验失败：\n')
  for (const line of failures) console.error(line)
  console.error(
    '\n每条需求必须恰好归属一个里程碑，标注 e2e 的需求必须在实现检查表中有执行记录。' +
    '\n新增或废弃需求时同步更新计划文档的实现检查表。'
  )
  process.exit(1)
}

if (checkedDocuments === 0) {
  console.log('没有找到带需求矩阵的文档，跳过。')
} else {
  console.log(`\n需求覆盖校验通过：${checkedDocuments} 份文档，${checkedRequirements} 项需求。`)
}
