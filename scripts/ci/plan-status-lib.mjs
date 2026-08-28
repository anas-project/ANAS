// Reads a plan's declared status and renders it into the plan index.
//
// The status of a plan used to be written in seven places in seven shapes -- a
// frontmatter field in some documents, a quote block under the title in others,
// and a hand-typed column in the index -- with no two of them obliged to agree.
// The frontmatter field is now the only machine-readable source, and this
// derives the index column from it so the two cannot drift apart.

// Deliberately four values, not a free-text field. "partial" and "implementing"
// are different answers to "should I expect movement": partial means some
// milestones landed and the rest are not currently being worked, implementing
// means they are.
export const planStates = ['proposed', 'implementing', 'partial', 'done']

const stateLabels = {
  proposed: '提案',
  implementing: '实施中',
  partial: '部分实施',
  done: '已完成'
}

export function planStatus(markdown) {
  if (!markdown.startsWith('---\n')) return { kind: 'no-frontmatter' }
  const end = markdown.indexOf('\n---', 4)
  if (end < 0) return { kind: 'no-frontmatter' }

  const frontmatter = markdown.slice(4, end)
  const match = /^status:[ \t]*(\S+)[ \t]*$/m.exec(frontmatter)
  if (match === null) return { kind: 'no-status' }
  if (!planStates.includes(match[1])) return { kind: 'bad-state', status: match[1] }
  return { kind: 'ok', status: match[1], label: stateLabels[match[1]] }
}

const linkedRow = /^\|\s*\[[^\]]*\]\((?:archived\/)?([A-Za-z0-9._-]+\.md)\)\s*\|/

// Rewrites the third cell of every row whose link target has a computed label,
// and leaves the others exactly as they are: a parse miss must not blank an
// entry, because an empty status reads as "unknown" rather than "not generated".
export function renderIndex(markdown, rows) {
  return markdown
    .split('\n')
    .map((line) => {
      const match = linkedRow.exec(line.trim())
      if (match === null || !rows.has(match[1])) return line
      const cells = line.trim().replace(/^\|/, '').replace(/\|$/, '').split('|')
      if (cells.length < 3) return line
      cells[2] = ` ${rows.get(match[1])} `
      return `|${cells.join('|')}|`
    })
    .join('\n')
}

// The generator fills a status but never creates a row: the title and scope are
// editorial. A plan with no row would otherwise vanish from the index silently,
// which is the failure a generated column is supposed to prevent.
export function missingRows(markdown, names) {
  const listed = new Set()
  for (const line of markdown.split('\n')) {
    const match = linkedRow.exec(line.trim())
    if (match !== null) listed.add(match[1])
  }
  return [...names].filter((name) => !listed.has(name))
}

// A row pointing at a plan that no longer exists is the other half of the same
// problem: archiving or deleting a plan must not leave a dead entry behind.
export function staleRows(markdown, names) {
  const known = new Set(names)
  const stale = []
  for (const line of markdown.split('\n')) {
    const match = linkedRow.exec(line.trim())
    if (match !== null && !known.has(match[1])) stale.push(match[1])
  }
  return stale
}
