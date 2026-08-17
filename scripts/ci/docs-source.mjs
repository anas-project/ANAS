import { cp, mkdtemp, readFile, rm, symlink } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    encoding: 'utf8',
    stdio: 'inherit',
    ...options
  })

  if (result.error) throw result.error
  if (result.status !== 0) throw new Error(`${command} exited with status ${result.status}`)
}

function shouldCopyDocs(docsRoot, source) {
  const relative = path.relative(docsRoot, source)
  if (relative === '') return true
  const segments = relative.split(path.sep)
  if (segments[0] !== '.vitepress') return true
  return !segments.some((segment) => segment === 'dist' || segment.startsWith('versioned-dist-'))
}

function extractCoreDocumentation(repositoryRoot, sourceDirectory, tag) {
  const archive = spawnSync('git', ['archive', '--format=tar', tag, '--', 'docs'], {
    cwd: repositoryRoot,
    encoding: null,
    maxBuffer: 128 * 1024 * 1024
  })
  if (archive.error) throw archive.error
  if (archive.status !== 0) throw new Error(`git archive ${tag} exited with status ${archive.status}`)

  const extract = spawnSync('tar', ['-x', '-C', sourceDirectory], {
    input: archive.stdout,
    stdio: ['pipe', 'inherit', 'inherit']
  })
  if (extract.error) throw extract.error
  if (extract.status !== 0) throw new Error(`tar exited with status ${extract.status}`)
}

export async function prepareDocumentationSource(repositoryRoot, options = {}) {
  const sourceDirectory = await mkdtemp(path.join(tmpdir(), 'anas-current-docs-'))
  const sourceDocs = path.join(repositoryRoot, 'docs')
  const destinationDocs = path.join(sourceDirectory, 'docs')

  try {
    if (options.coreTag) {
      extractCoreDocumentation(repositoryRoot, sourceDirectory, options.coreTag)
      await rm(path.join(destinationDocs, '.vitepress'), { recursive: true, force: true })
      await cp(path.join(sourceDocs, '.vitepress'), path.join(destinationDocs, '.vitepress'), {
        recursive: true,
        filter: (source) => shouldCopyDocs(sourceDocs, source)
      })
    } else {
      await cp(sourceDocs, destinationDocs, {
        recursive: true,
        filter: (source) => shouldCopyDocs(sourceDocs, source)
      })
    }
    await symlink(path.join(repositoryRoot, 'node_modules'), path.join(sourceDirectory, 'node_modules'), 'dir')
    run('go', [
      'run',
      './cmd/materialize-module-docs',
      '--root', repositoryRoot,
      '--docs-root', destinationDocs,
      ...(options.releaseModules ? ['--release-mode'] : [])
    ], {
      cwd: repositoryRoot,
      env: {
        ...process.env,
        GOCACHE: path.join(tmpdir(), 'anas-docs-go-build-cache')
      }
    })

    const moduleIndex = JSON.parse(await readFile(path.join(destinationDocs, '.vitepress/generated/module-docs.json'), 'utf8'))
    return { sourceDirectory, moduleIndex }
  } catch (error) {
    await rm(sourceDirectory, { recursive: true, force: true })
    throw error
  }
}
