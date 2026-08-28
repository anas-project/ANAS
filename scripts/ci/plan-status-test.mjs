import assert from 'node:assert/strict'
import { planStatus, planStates, renderIndex, missingRows, staleRows } from './plan-status-lib.mjs'

const plan = (status) => `---\ndoc_type: plan\nstatus: ${status}\ncreated: 2026-08-16\nupdated: 2026-08-28\n---\n\n# Plan\n\n> 状态：给人读的细节写在这里。\n`

assert.deepEqual(planStates, ['proposed', 'implementing', 'partial', 'done'])

for (const [status, label] of [
  ['proposed', '提案'],
  ['implementing', '实施中'],
  ['partial', '部分实施'],
  ['done', '已完成']
]) {
  assert.deepEqual(planStatus(plan(status)), { kind: 'ok', status, label })
}

// The body's quote block is prose for the reader. It must never become a second
// machine-readable source: that is how seven documents ended up with seven
// shapes and no two of them obliged to agree.
assert.equal(
  planStatus('---\ndoc_type: plan\nstatus: partial\n---\n\n# Plan\n\n> 状态：**已完成**。\n').status,
  'partial'
)

// Each rejection is a distinct kind, because the fix differs: add frontmatter,
// add the field, or correct the value.
assert.equal(planStatus('# Plan\n\n> 状态：实施中\n').kind, 'no-frontmatter')
assert.equal(planStatus('---\ndoc_type: plan\ncreated: 2026-08-16\n---\n\n# Plan\n').kind, 'no-status')
assert.deepEqual(planStatus(plan('completed')), { kind: 'bad-state', status: 'completed' })
assert.deepEqual(planStatus(plan('not_started')), { kind: 'bad-state', status: 'not_started' })

const index = `# 实施计划

| 文档 | 范围 | 状态 |
| --- | --- | --- |
| [Alpha](alpha.md) | 甲 | 未开始 |
| [Beta](beta.md) | 乙 | 实施中 |

## 已归档

| 文档 | 结论去向 | 状态 |
| --- | --- | --- |
| [Gamma](archived/gamma.md) | [指南](../../docs/guide/index.md) | 手写的东西 |
`

const rendered = renderIndex(index, new Map([
  ['alpha.md', '提案'],
  ['gamma.md', '已完成（已归档）']
]))
assert.match(rendered, /\| \[Alpha\]\(alpha\.md\) \| 甲 \| 提案 \|/)
assert.match(rendered, /\| \[Gamma\]\(archived\/gamma\.md\) \|.*\| 已完成（已归档） \|/)
// A row without a computed value keeps whatever it had; blanking it would read
// as "unknown" rather than "not generated".
assert.match(rendered, /\| \[Beta\]\(beta\.md\) \| 乙 \| 实施中 \|/)
assert.equal(renderIndex(rendered, new Map([['alpha.md', '提案'], ['gamma.md', '已完成（已归档）']])), rendered)

// A plan with no row would vanish from the index silently, and a row whose plan
// was archived or deleted would point at nothing. Both are caught.
assert.deepEqual(missingRows(index, ['alpha.md', 'delta.md']), ['delta.md'])
assert.deepEqual(missingRows(index, ['gamma.md']), [])
assert.deepEqual(staleRows(index, ['alpha.md', 'beta.md', 'gamma.md']), [])
assert.deepEqual(staleRows(index, ['alpha.md', 'beta.md']), ['gamma.md'])

console.log('plan status tests passed')
