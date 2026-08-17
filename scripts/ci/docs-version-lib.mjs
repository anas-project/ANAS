const stableCoreTagPattern = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/

export function parseStableCoreTag(tag) {
  const match = stableCoreTagPattern.exec(tag.trim())
  if (!match) return null

  return {
    tag: match[0],
    version: match[0],
    major: Number(match[1]),
    minor: Number(match[2]),
    patch: Number(match[3])
  }
}

export function compareVersionsDescending(left, right) {
  return right.major - left.major || right.minor - left.minor || right.patch - left.patch
}

export function selectDocumentationVersions(tags) {
  const stableVersions = tags
    .map(parseStableCoreTag)
    .filter((version) => version !== null)
    .sort(compareVersionsDescending)

  const selected = []
  const seenMajors = new Set()

  for (const version of stableVersions) {
    if (seenMajors.has(version.major)) continue
    seenMajors.add(version.major)
    selected.push(version)
  }

  return selected.map((version, index) => ({
    ...version,
    current: index === 0,
    path: index === 0 ? '/' : `/versions/${version.major}.x/`
  }))
}

export function allowHistoricalDeadLinks(configSource) {
  if (/\bignoreDeadLinks\s*:/.test(configSource)) return configSource

  const configStart = 'defineConfig({'
  const index = configSource.indexOf(configStart)
  if (index === -1) {
    throw new Error('historical VitePress config does not use defineConfig({')
  }

  return `${configSource.slice(0, index)}${configStart}\n  ignoreDeadLinks: true,${configSource.slice(index + configStart.length)}`
}
