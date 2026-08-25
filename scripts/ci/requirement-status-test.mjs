import assert from 'node:assert/strict'
import { documentStatus, milestoneState, renderIndex, missingRows } from './requirement-status-lib.mjs'

const matrix = `
# 要求

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| \`DEMO-R-001\` | 甲 | 单元 |
| \`DEMO-R-002\` | 乙 | 单元 |
| \`DEMO-R-003\` | 丙 | 审阅 |
| \`DEMO-R-004\` | 丁（已废弃） | 审阅 |
`

const plan = (m2) => `
# 计划

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1 | R-001—R-002 | 已完成 |
| M2 | R-003 | ${m2} |
`

// The leading word is the state; prose after the separator is for the reader.
assert.equal(milestoneState('已完成'), '已完成')
assert.equal(milestoneState('实施中；只读部分已落地，invoke 待办'), '实施中')
assert.equal(milestoneState('阻塞；等待独立宿主'), '阻塞')
assert.equal(milestoneState('只读 list/detail 已完成'), null)
assert.equal(milestoneState(''), null)
assert.equal(milestoneState(undefined), null)

// Retired requirements leave the denominator: their numbers are reserved, but
// nobody still owes the work.
assert.deepEqual(documentStatus(matrix, plan('未开始')), {
  kind: 'ok', label: '2/3 已完成', done: 2, total: 3
})
assert.equal(documentStatus(matrix, plan('已完成')).label, '3/3 已完成')

// A milestone in flight is not complete, however much of it landed.
assert.equal(documentStatus(matrix, plan('实施中；一半已落地')).label, '2/3 已完成')

// A status cell the vocabulary does not cover names the offending milestone
// rather than silently counting it as unfinished.
const bad = documentStatus(matrix, plan('差不多快好了'))
assert.equal(bad.kind, 'bad-state')
assert.deepEqual(bad.unknown, ['M2'])

// A requirement no milestone claims is a coverage failure, not a zero.
const orphan = documentStatus(matrix, `
| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1 | R-001 | 已完成 |
`)
assert.equal(orphan.kind, 'unassigned')
assert.equal(orphan.unassigned, 2)

// Documents that have not adopted IDs, and documents whose plan is missing, are
// reported as such instead of as 0%.
assert.equal(documentStatus('# 没有矩阵\n\n正文。', plan('已完成')).kind, 'no-matrix')
assert.equal(documentStatus(matrix, null).label, '3 项需求，无配套计划')

// Only rows the generator has a figure for are rewritten; anything else -- prose,
// headers, a link to a document that was not scanned -- survives byte for byte.
const index = `# 索引

| 文档 | 范围 | 状态 |
| --- | --- | --- |
| [甲](alpha.md) | 甲的范围 | 手写 |
| [乙](beta.md) | 乙的范围 | 手写 |

尾注。`
const rendered = renderIndex(index, new Map([['alpha.md', '2/3 已完成']]))
assert.match(rendered, /\| \[甲\]\(alpha\.md\) \| 甲的范围 \| 2\/3 已完成 \|/)
assert.match(rendered, /\| \[乙\]\(beta\.md\) \| 乙的范围 \| 手写 \|/)
assert.match(rendered, /尾注。$/)

// Rendering is idempotent: running the generator twice changes nothing.
assert.equal(renderIndex(rendered, new Map([['alpha.md', '2/3 已完成']])), rendered)

// The generator fills a status but never invents a row, so a document nobody
// listed has to be reported rather than quietly skipped.
assert.deepEqual(missingRows(index, ['alpha.md', 'beta.md']), [])
assert.deepEqual(missingRows(index, ['alpha.md', 'gamma.md']), ['gamma.md'])
assert.deepEqual(missingRows('# 空索引\n\n没有表格。', ['alpha.md']), ['alpha.md'])

console.log('requirement status tests passed')
