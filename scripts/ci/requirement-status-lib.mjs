// Derives per-document completion from a requirement matrix and the plan that
// delivers it, and renders it into the index's status column.
//
// Every input already exists: the matrix names all the IDs, and the paired plan's
// milestone table says which milestone owns each ID and what state that milestone
// is in. A status column typed by a human drifts the moment a milestone lands and
// the index is not touched in the same commit; a derived one cannot.

import { parseRequirementMatrix, parseMilestoneAssignments } from './requirement-coverage-lib.mjs'

// A milestone's state is the leading word of its status cell. Everything after the
// separator is prose for the reader -- "实施中；只读 list/detail 已完成" says both
// that the milestone is in flight and which part of it landed, and only the first
// half has to be machine-readable.
export const milestoneStates = ['已完成', '实施中', '未开始', '阻塞']

export function milestoneState(cell) {
  const head = (cell ?? '').split(/[；;]/)[0].trim()
  return milestoneStates.includes(head) ? head : null
}

// Retired requirements stay in the matrix so their numbers are never reissued, but
// they are not work anyone still owes, so they leave the denominator.
export function documentStatus(requirementMarkdown, planMarkdown) {
  const matrix = parseRequirementMatrix(requirementMarkdown)
  if (matrix.requirements.length === 0) return { kind: 'no-matrix', label: '无矩阵（未采用 ID）' }

  const live = matrix.requirements.filter((requirement) => !requirement.retired)
  if (planMarkdown === null || planMarkdown === undefined) {
    return { kind: 'no-plan', label: `${live.length} 项需求，无配套计划` }
  }

  const known = new Set(matrix.requirements.map((requirement) => requirement.number))
  const stateByNumber = new Map()
  const unknown = []
  for (const assignment of parseMilestoneAssignments(planMarkdown, known)) {
    const state = milestoneState(assignment.status)
    if (state === null) unknown.push(assignment.milestone)
    for (const entry of assignment.entries) stateByNumber.set(entry.number, state)
  }
  if (unknown.length > 0) return { kind: 'bad-state', unknown }

  let done = 0
  let unassigned = 0
  for (const requirement of live) {
    const state = stateByNumber.get(requirement.number)
    if (state === undefined) unassigned += 1
    else if (state === '已完成') done += 1
  }
  if (unassigned > 0) return { kind: 'unassigned', unassigned }
  return { kind: 'ok', label: `${done}/${live.length} 已完成`, done, total: live.length }
}

const linkedRow = /^\|\s*\[[^\]]*\]\(([A-Za-z0-9._-]+\.md)\)\s*\|/

// Rewrites the third cell of every table row whose link target has a computed
// figure. Rows without one are left untouched rather than blanked, so an index
// entry can never be silently emptied by a parse miss.
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

// The generator fills a row's status but never creates the row: the document title
// and its scope description are editorial, and inventing them from a filename would
// produce a worse index than the one a person writes. That split leaves one gap the
// generator has to close itself -- a new document whose row nobody added is simply
// absent, and an index that silently omits a document is worse than a stale one.
export function missingRows(markdown, names) {
  const listed = new Set()
  for (const line of markdown.split('\n')) {
    const match = linkedRow.exec(line.trim())
    if (match !== null) listed.add(match[1])
  }
  return [...names].filter((name) => !listed.has(name))
}
