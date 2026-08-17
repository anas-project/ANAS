import { spawn } from 'node:child_process'
import path from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'
import { rm } from 'node:fs/promises'
import { prepareDocumentationSource } from './docs-source.mjs'

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const repositoryRoot = path.resolve(scriptDirectory, '../..')
const vitepressBinary = path.join(repositoryRoot, 'node_modules/.bin/vitepress')
const { sourceDirectory } = await prepareDocumentationSource(repositoryRoot)

const child = spawn(vitepressBinary, ['dev', 'docs', ...process.argv.slice(2)], {
  cwd: sourceDirectory,
  env: process.env,
  stdio: 'inherit'
})

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.once(signal, () => child.kill(signal))
}

const exitCode = await new Promise((resolve, reject) => {
  child.once('error', reject)
  child.once('close', (code) => resolve(code ?? 1))
})

await rm(sourceDirectory, { recursive: true, force: true })
process.exitCode = exitCode
