import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

// Compare to the working tree, not HEAD: unrelated local edits are irrelevant.
const root = fileURLToPath(new URL('.', import.meta.url))
const temporary = mkdtempSync(join(tmpdir(), 'anas-api-check-'))
try {
  const output = join(temporary, 'schema.d.ts')
  execFileSync(process.execPath, [join(root, 'node_modules/openapi-typescript/bin/cli.js'), join(root, '../api/openapi.yaml'), '-o', output], { stdio: 'inherit' })
  if (!readFileSync(output).equals(readFileSync(join(root, 'src/api/schema.d.ts')))) {
    throw new Error('API types are stale; run npm --prefix web run generate:api')
  }
} finally {
  rmSync(temporary, { recursive: true, force: true })
}
