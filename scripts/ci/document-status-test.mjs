import assert from 'node:assert/strict'
import { parseDocumentStatus, checkDocumentStatus } from './document-status-lib.mjs'

assert.deepEqual(
  parseDocumentStatus('---\ndoc_type: plan\nstatus: implementing\n---\n\n# 标题\n'),
  { source: 'frontmatter', value: 'implementing' }
)
assert.deepEqual(
  parseDocumentStatus('# 标题\n\n> 状态：**当前模型**。已实现。\n'),
  { source: 'inline', value: '**当前模型**。已实现。' }
)
assert.deepEqual(
  parseDocumentStatus('# Title\n\n> Status: **historical**. Legacy.\n'),
  { source: 'inline', value: '**historical**. Legacy.' }
)
// The inline form may sit under an admonition, which is how several architecture
// documents already read.
assert.equal(
  parseDocumentStatus('# 标题\n\n> [!NOTE]\n> 说明。\n\n状态：历史记录\n').source,
  'inline'
)
// Drafts commonly open with emphasis before the marker.
assert.deepEqual(
  parseDocumentStatus('# 草案\n\n> **状态：§1–§7 已实现**，其余仍是草案。\n'),
  { source: 'inline', value: '§1–§7 已实现**，其余仍是草案。' }
)
// Frontmatter wins when both are present.
assert.deepEqual(
  parseDocumentStatus('---\nstatus: current\n---\n# 标题\n状态：提案\n'),
  { source: 'frontmatter', value: 'current' }
)
// Too far down does not count as "up front".
assert.equal(parseDocumentStatus('# 标题\n' + '\n'.repeat(20) + '状态：提案\n').source, null)
assert.equal(parseDocumentStatus('# 标题\n\n正文，没有状态。\n').source, null)
// A frontmatter block without a status field falls through to the inline form.
assert.equal(parseDocumentStatus('---\ndoc_type: plan\n---\n# 标题\n状态：提案\n').source, 'inline')

assert.deepEqual(
  checkDocumentStatus([{ path: 'a.md', markdown: '# T\n\n状态：当前模型\n' }]),
  { errors: [], checked: 1 }
)
assert.match(
  checkDocumentStatus([{ path: 'b.md', markdown: '# T\n\n正文。\n' }]).errors.join('\n'),
  /b\.md: 开头未声明状态/
)

console.log('document status tests passed')
