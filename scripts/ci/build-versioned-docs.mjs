import { spawn, spawnSync } from 'node:child_process'
import { mkdtemp, mkdir, readFile, readdir, rename, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'
import { allowHistoricalDeadLinks, selectDocumentationVersions } from './docs-version-lib.mjs'

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const repositoryRoot = path.resolve(scriptDirectory, '../..')
const vitepressBinary = path.join(repositoryRoot, 'node_modules/.bin/vitepress')
const outputDirectory = path.join(repositoryRoot, 'docs/.vitepress/dist')
const stagingDirectory = await mkdtemp(path.join(repositoryRoot, 'docs/.vitepress/versioned-dist-'))
const temporaryDirectories = []

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: repositoryRoot,
    encoding: 'utf8',
    stdio: 'inherit',
    ...options
  })

  if (result.error) throw result.error
  if (result.status !== 0) {
    throw new Error(`${command} exited with status ${result.status}`)
  }
}

function readStableTags() {
  const result = spawnSync('git', ['tag', '--list', 'v*'], {
    cwd: repositoryRoot,
    encoding: 'utf8'
  })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error(result.stderr.trim() || 'could not read Git tags')
  return result.stdout.split('\n').filter(Boolean)
}

function normalizeBase(base) {
  const withLeadingSlash = base.startsWith('/') ? base : `/${base}`
  return withLeadingSlash.endsWith('/') ? withLeadingSlash : `${withLeadingSlash}/`
}

function siteBase() {
  if (process.env.DOCS_BASE) return normalizeBase(process.env.DOCS_BASE)
  const repository = process.env.GITHUB_REPOSITORY?.split('/')[1]
  return repository ? `/${repository}/` : '/'
}

function buildEnvironment(version, versions, base, archive) {
  return {
    ...process.env,
    DOCS_BASE: base,
    DOCS_ROOT_BASE: siteBase(),
    DOCS_VERSION: version,
    DOCS_IS_ARCHIVE: archive ? 'true' : 'false',
    DOCS_VERSION_LINKS: JSON.stringify(versions.map(({ version: label, path: versionPath, current }) => ({
      version: label,
      path: versionPath,
      current
    })))
  }
}

async function extractDocumentation(tag, destination) {
  await mkdir(destination, { recursive: true })

  const archive = spawn('git', ['archive', '--format=tar', tag, '--', 'docs'], {
    cwd: repositoryRoot,
    stdio: ['ignore', 'pipe', 'inherit']
  })
  const extract = spawn('tar', ['-x', '-C', destination], {
    stdio: ['pipe', 'inherit', 'inherit']
  })
  archive.stdout.pipe(extract.stdin)

  const exitCode = (child, name) => new Promise((resolve, reject) => {
    child.once('error', reject)
    child.once('close', (code) => code === 0 ? resolve() : reject(new Error(`${name} exited with status ${code}`)))
  })

  await Promise.all([exitCode(archive, 'git archive'), exitCode(extract, 'tar')])
}

async function prepareHistoricalConfig(sourceDirectory) {
  const configFilenames = ['config.mts', 'config.ts', 'config.mjs', 'config.js']
  for (const configFilename of configFilenames) {
    const filename = path.join(sourceDirectory, 'docs/.vitepress', configFilename)
    try {
      const configSource = await readFile(filename, 'utf8')
      await writeFile(filename, allowHistoricalDeadLinks(configSource))
      return
    } catch (error) {
      if (error.code !== 'ENOENT') throw error
    }
  }
  throw new Error('historical documentation has no VitePress config')
}

function rootLink(versionPath) {
  const root = siteBase()
  return versionPath === '/' ? root : `${root.slice(0, -1)}${versionPath}`
}

function archiveNotice(version, latestVersion, english) {
  const archiveText = english ? 'Archived documentation' : '历史文档'
  const latestText = english ? `View latest ${latestVersion}` : `查看最新版 ${latestVersion}`
  return `<aside class="anas-version-notice" role="note"><span>${archiveText} <strong>${version}</strong></span><a href="${rootLink('/')}">${latestText}</a></aside><style>.anas-version-notice{position:fixed;right:18px;bottom:18px;z-index:40;display:flex;gap:12px;align-items:center;padding:10px 14px;border:1px solid var(--vp-c-warning-2);border-radius:10px;background:var(--vp-c-warning-soft);color:var(--vp-c-text-1);box-shadow:var(--vp-shadow-3);font-size:13px}.anas-version-notice a{color:var(--vp-c-brand-1);font-weight:600}@media(max-width:640px){.anas-version-notice{left:12px;right:12px;bottom:12px;justify-content:space-between}}</style>`
}

async function markArchivedPages(directory, version, latestVersion, relativeDirectory = '') {
  const entries = await readdir(path.join(directory, relativeDirectory), { withFileTypes: true })
  for (const entry of entries) {
    const relativePath = path.join(relativeDirectory, entry.name)
    if (entry.isDirectory()) {
      await markArchivedPages(directory, version, latestVersion, relativePath)
      continue
    }
    if (!entry.isFile() || !entry.name.endsWith('.html')) continue

    const filename = path.join(directory, relativePath)
    const html = await readFile(filename, 'utf8')
    const english = relativePath === 'en.html' || relativePath.startsWith(`en${path.sep}`)
    const marked = html.replace('</body>', `${archiveNotice(version, latestVersion, english)}</body>`)
    await writeFile(filename, marked)
  }
}

try {
  const versions = selectDocumentationVersions(readStableTags())
  const currentVersion = versions[0]?.version ?? 'development'

  run(vitepressBinary, ['build', 'docs', '--outDir', stagingDirectory], {
    env: buildEnvironment(currentVersion, versions, siteBase(), false)
  })

  for (const version of versions.slice(1)) {
    const sourceDirectory = await mkdtemp(path.join(tmpdir(), 'anas-docs-'))
    temporaryDirectories.push(sourceDirectory)
    await extractDocumentation(version.tag, sourceDirectory)
    await prepareHistoricalConfig(sourceDirectory)
    await symlink(path.join(repositoryRoot, 'node_modules'), path.join(sourceDirectory, 'node_modules'), 'dir')

    const archiveOutput = path.join(stagingDirectory, 'versions', `${version.major}.x`)
    const archiveBase = rootLink(version.path)
    run(vitepressBinary, ['build', 'docs', '--outDir', archiveOutput], {
      cwd: sourceDirectory,
      env: buildEnvironment(version.version, versions, archiveBase, true)
    })
    await markArchivedPages(archiveOutput, version.version, currentVersion)
  }

  await writeFile(path.join(stagingDirectory, 'versions.json'), `${JSON.stringify({
    current: currentVersion,
    versions: versions.map(({ version, path: versionPath, current }) => ({ version, path: versionPath, current }))
  }, null, 2)}\n`)

  await rm(outputDirectory, { recursive: true, force: true })
  await rename(stagingDirectory, outputDirectory)
  console.log(`built documentation for ${currentVersion}${versions.length > 1 ? ` and ${versions.length - 1} archived major version(s)` : ''}`)
} catch (error) {
  await rm(stagingDirectory, { recursive: true, force: true })
  throw error
} finally {
  await Promise.all(temporaryDirectories.map((directory) => rm(directory, { recursive: true, force: true })))
}
