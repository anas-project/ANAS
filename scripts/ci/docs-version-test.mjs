import assert from 'node:assert/strict'
import { allowHistoricalDeadLinks, parseStableCoreTag, selectDocumentationVersions } from './docs-version-lib.mjs'

assert.deepEqual(parseStableCoreTag('v1.2.3'), {
  tag: 'v1.2.3',
  version: 'v1.2.3',
  major: 1,
  minor: 2,
  patch: 3
})

for (const invalidTag of [
  '1.2.3',
  'v01.2.3',
  'v1.2',
  'v1.2.3-rc.1',
  'module/nextcloud/34.0.2-r4'
]) {
  assert.equal(parseStableCoreTag(invalidTag), null, invalidTag)
}

assert.deepEqual(
  selectDocumentationVersions([
    'v0.1.0',
    'v0.3.1',
    'v1.0.0-rc.1',
    'v1.0.0',
    'v1.4.2',
    'v2.0.0',
    'module/nextcloud/34.0.2-r4'
  ]),
  [
    { tag: 'v2.0.0', version: 'v2.0.0', major: 2, minor: 0, patch: 0, current: true, path: '/' },
    { tag: 'v1.4.2', version: 'v1.4.2', major: 1, minor: 4, patch: 2, current: false, path: '/versions/1.x/' },
    { tag: 'v0.3.1', version: 'v0.3.1', major: 0, minor: 3, patch: 1, current: false, path: '/versions/0.x/' }
  ]
)

assert.deepEqual(selectDocumentationVersions([]), [])

assert.deepEqual(
  selectDocumentationVersions([
    'v1.9.0', 'v2.8.0', 'v3.7.0', 'v4.6.0', 'v5.5.0', 'v6.4.0',
    'v6.5.0', 'v7.0.0-rc.1'
  ]).map(({ version }) => version),
  ['v6.5.0', 'v5.5.0', 'v4.6.0', 'v3.7.0', 'v2.8.0']
)

assert.equal(
  allowHistoricalDeadLinks('export default defineConfig({\n  title: \'ANAS\'\n})'),
  'export default defineConfig({\n  ignoreDeadLinks: true,\n  title: \'ANAS\'\n})'
)
assert.equal(
  allowHistoricalDeadLinks('export default defineConfig({ ignoreDeadLinks: archive })'),
  'export default defineConfig({ ignoreDeadLinks: archive })'
)
assert.throws(() => allowHistoricalDeadLinks('export default {}'), /does not use defineConfig/)

console.log('documentation version selection tests passed')
