import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { collectRequirementDocuments, requirementScopes } from './requirement-docs-lib.mjs'

const root = mkdtempSync(join(tmpdir(), 'anas-requirement-docs-'))

function write(relativePath, content = '# doc\n') {
  const path = join(root, relativePath)
  mkdirSync(join(path, '..'), { recursive: true })
  writeFileSync(path, content)
}

try {
  write('dev-docs/requirements/index.md')
  write('dev-docs/requirements/alpha.md')
  write('dev-docs/plans/alpha.md')
  write('modules/demo/dev-docs/requirements/demo-only.md')
  write('modules/demo/dev-docs/plans/demo-only.md')
  write('modules/quiet/module.yml', 'name: quiet\n')

  const scopes = requirementScopes(root)
  assert.deepEqual(scopes.map((scope) => scope.label), ['dev-docs', 'modules/demo'])

  const { documents, errors } = collectRequirementDocuments(scopes)
  assert.deepEqual(errors, [])
  // Module-private topics are scanned with the same pairing rule, and index.md
  // is not a requirement document.
  assert.deepEqual(documents.map((document) => document.topic), ['alpha', 'demo-only'])
  assert.equal(documents[1].planPath, join(root, 'modules/demo/dev-docs/plans/demo-only.md'))

  // REQID-R-006: the same topic in two locations is a migration that never
  // finished, and the two copies would drift without either looking wrong.
  write('modules/demo/dev-docs/requirements/alpha.md')
  const duplicated = collectRequirementDocuments(requirementScopes(root))
  assert.equal(duplicated.documents.filter((document) => document.topic === 'alpha').length, 1)
  assert.match(duplicated.errors.join('\n'), /主题 alpha 已经由 .*dev-docs\/requirements\/alpha\.md 承载/)
} finally {
  rmSync(root, { recursive: true, force: true })
}

console.log('requirement document location tests passed')
