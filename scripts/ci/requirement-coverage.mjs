// Checks every requirement matrix against the plan that delivers it.
//
// Pairing is by filename: dev-docs/requirements/<topic>.md is delivered by
// dev-docs/plans/<topic>.md, and a Module-private topic pairs the same way under
// modules/<name>/dev-docs/. A requirement document without a matrix is skipped --
// not every document has adopted IDs yet -- but one that has a matrix and no
// paired plan is an error, because nothing would ever schedule its requirements.

import { readFileSync, existsSync } from 'node:fs'
import {
  parseRequirementMatrix,
  parseMilestoneAssignments,
  parseE2eRecords,
  checkRequirementCoverage
} from './requirement-coverage-lib.mjs'
import { collectRequirementDocuments, requirementScopes } from './requirement-docs-lib.mjs'

const { documents, errors: locationErrors } = collectRequirementDocuments(requirementScopes())

const failures = [...locationErrors]
let checkedDocuments = 0
let checkedRequirements = 0

const archivedTopics = []

for (const { requirementPath, planPath, archived } of documents) {
  const matrix = parseRequirementMatrix(readFileSync(requirementPath, 'utf8'))
  if (matrix.requirements.length === 0) continue

  // An archived plan's assignment table is a record of what was delivered, not a
  // schedule, so it is not held to the full consistency rules: a regression note
  // added to a finished matrix should not force edits to the plan's e2e records.
  //
  // Assignment coverage is still checked. Skipping it entirely meant a
  // requirement added to an archived matrix belonged to no milestone and nobody
  // was told -- exactly the drift this gate exists to catch. `archivedOnly`
  // keeps the coverage half and drops the e2e-record half.
  if (archived) {
    if (!existsSync(planPath)) {
      failures.push(`${requirementPath}: 有需求矩阵但缺少配套计划 ${planPath}`)
      continue
    }
    const archivedPlan = readFileSync(planPath, 'utf8')
    const archivedNumbers = new Set(matrix.requirements.map((requirement) => requirement.number))
    const { errors, checked } = checkRequirementCoverage({
      matrix,
      assignments: parseMilestoneAssignments(archivedPlan, archivedNumbers),
      e2eRecords: parseE2eRecords(archivedPlan),
      coverageOnly: true
    })
    if (errors.length > 0) {
      failures.push(`${requirementPath} ↔ ${planPath}（已归档）`, ...errors.map((error) => `  - ${error}`))
      continue
    }
    archivedTopics.push(`${requirementPath} ↔ ${planPath}（${checked} 项已归属）`)
    continue
  }

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
  console.log(`${requirementPath}: ${checked} 项需求全部有归属，e2e 记录一致${retiredNote}`)
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

if (archivedTopics.length > 0) {
  console.log(`\n已归档主题（计划已完成，只校验归属，不校验 e2e 记录）：${archivedTopics.length} 份`)
  for (const line of archivedTopics) console.log(`  ${line}`)
}

if (checkedDocuments === 0) {
  console.log('没有找到带需求矩阵的文档，跳过。')
} else {
  console.log(`\n需求覆盖校验通过：${checkedDocuments} 份文档，${checkedRequirements} 项需求。`)
}
