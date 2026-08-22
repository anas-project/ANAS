import assert from 'node:assert/strict'
import {
  parseRequirementMatrix,
  parseMilestoneAssignments,
  parseE2eRecords,
  expandIdCell,
  checkRequirementCoverage
} from './requirement-coverage-lib.mjs'

const matrixDoc = `
# 要求

正文里出现 CONSOLE-R-999 不应被当成矩阵行。

| ID | 要求 | 验证 |
| --- | --- | --- |
| \`CONSOLE-R-001\` | 甲 | 单元 |
| \`CONSOLE-R-002\` | 乙 | e2e |
| \`CONSOLE-R-003\` | 丙 | 审阅 |

### 另一组

| ID | 要求 | 验证 |
| --- | --- | --- |
| \`CONSOLE-R-010\` | 丁 | e2e |
| \`CONSOLE-R-011\` | 戊（已废弃） | 审阅 |
`

const matrix = parseRequirementMatrix(matrixDoc)
assert.deepEqual(matrix.prefixes, ['CONSOLE'])
assert.deepEqual(
  matrix.requirements.map((requirement) => requirement.id),
  ['CONSOLE-R-001', 'CONSOLE-R-002', 'CONSOLE-R-003', 'CONSOLE-R-010', 'CONSOLE-R-011']
)
assert.equal(matrix.requirements[1].verification, 'e2e')
assert.equal(matrix.requirements[4].retired, true)
assert.equal(matrix.requirements[0].retired, false)

// Ranges expand only over IDs the matrix declares, so a range spanning a gap
// does not invent requirements.
const known = new Set(['001', '002', '003', '010', '011'])
assert.deepEqual(expandIdCell('R-001—R-003', known).map((entry) => entry.number), ['001', '002', '003'])
assert.deepEqual(expandIdCell('R-001-R-003', known).map((entry) => entry.number), ['001', '002', '003'])
assert.deepEqual(expandIdCell('R-003—R-011', known).map((entry) => entry.number), ['003', '010', '011'])
assert.deepEqual(expandIdCell('R-001、R-010', known).map((entry) => entry.number), ['001', '010'])
assert.deepEqual(expandIdCell('R-001—R-002、R-010', known).map((entry) => entry.number), ['001', '002', '010'])
assert.equal(expandIdCell('R-003—R-001', known)[0].invalidRange, 'R-003—R-001')
assert.deepEqual(expandIdCell('无', known), [])

const planDoc = `
# 计划

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1 | R-001—R-003 | 已完成 |
| M2 | R-010 | 未开始 |

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-002 | a.sh | ln | 2026-08-21 | 通过 |
| R-010 | b.sh | | | |
`

const assignments = parseMilestoneAssignments(planDoc, known)
assert.deepEqual(assignments.map((row) => row.milestone), ['M1', 'M2'])
assert.deepEqual(assignments[0].entries.map((entry) => entry.number), ['001', '002', '003'])
assert.deepEqual(parseE2eRecords(planDoc), ['002', '010'])

const clean = checkRequirementCoverage({
  matrix,
  assignments,
  e2eRecords: parseE2eRecords(planDoc)
})
assert.deepEqual(clean.errors, [])
assert.equal(clean.checked, 4)
assert.equal(clean.retired, 1)

// Each failure mode must actually be caught -- a checker that always passes is
// worse than no checker, because it makes the drift look reviewed.
function errorsFor(overrides) {
  return checkRequirementCoverage({
    matrix,
    assignments,
    e2eRecords: parseE2eRecords(planDoc),
    ...overrides
  }).errors
}

assert.match(
  errorsFor({ assignments: assignments.filter((row) => row.milestone !== 'M2') }).join('\n'),
  /CONSOLE-R-010 没有归属任何里程碑/
)
assert.match(
  errorsFor({ assignments: [...assignments, { milestone: 'M3', entries: [{ number: '001' }] }] }).join('\n'),
  /CONSOLE-R-001 归属了 2 个里程碑/
)
assert.match(
  errorsFor({ assignments: [...assignments, { milestone: 'M3', entries: [{ number: '404' }] }] }).join('\n'),
  /M3 引用了矩阵中不存在的 R-404/
)
assert.match(
  errorsFor({ assignments: [...assignments, { milestone: 'M3', entries: [{ number: '011' }] }] }).join('\n'),
  /CONSOLE-R-011 已废弃但仍归属/
)
assert.match(
  errorsFor({ e2eRecords: ['010'] }).join('\n'),
  /CONSOLE-R-002 的验证方式是 e2e，但实现检查表的 e2e 记录里没有它/
)
assert.match(
  errorsFor({ e2eRecords: ['002', '010', '001'] }).join('\n'),
  /CONSOLE-R-001 出现在 e2e 记录里，但矩阵标注的验证方式是「单元」/
)
assert.match(
  errorsFor({ e2eRecords: ['002', '010', '404'] }).join('\n'),
  /e2e 记录引用了矩阵中不存在的 R-404/
)
assert.match(
  errorsFor({
    assignments: [...assignments, { milestone: 'M3', entries: [{ number: '003', invalidRange: 'R-003—R-001' }] }]
  }).join('\n'),
  /M3 的区间 R-003—R-001 起止颠倒/
)

const duplicated = parseRequirementMatrix(`
| ID | 要求 | 验证 |
| --- | --- | --- |
| \`CONSOLE-R-001\` | 甲 | 单元 |
| \`CONSOLE-R-001\` | 甲的重复行 | 单元 |
`)
assert.match(
  checkRequirementCoverage({
    matrix: duplicated,
    assignments: [{ milestone: 'M1', entries: [{ number: '001' }] }],
    e2eRecords: []
  }).errors.join('\n'),
  /需求矩阵重复定义 CONSOLE-R-001/
)

const mixed = parseRequirementMatrix(`
| ID | 要求 | 验证 |
| --- | --- | --- |
| \`CONSOLE-R-001\` | 甲 | 单元 |
| \`BACKUP-R-002\` | 乙 | 单元 |
`)
assert.match(
  checkRequirementCoverage({
    matrix: mixed,
    assignments: [{ milestone: 'M1', entries: [{ number: '001' }, { number: '002' }] }],
    e2eRecords: []
  }).errors.join('\n'),
  /混用了多个 ID 前缀/
)

// A document with no matrix is skipped rather than reported as broken.
assert.deepEqual(parseRequirementMatrix('# 没有矩阵的文档\n\n正文。').requirements, [])
assert.deepEqual(
  checkRequirementCoverage({ matrix: { prefixes: [], requirements: [] }, assignments: [], e2eRecords: [] }),
  { errors: [], checked: 0 }
)

console.log('requirement coverage tests passed')
