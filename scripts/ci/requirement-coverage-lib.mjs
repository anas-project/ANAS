// Consistency checks between a requirement matrix and the plan that delivers it.
//
// The matrix in <scope>/requirements/<topic>.md is the normative source: one row per
// requirement, each with a stable ID. The plan in <scope>/plans/<topic>.md assigns
// every ID to a milestone and records e2e evidence. Nothing keeps the two in step
// on its own -- a requirement added to the matrix but never assigned is simply
// never delivered, and it looks fine in review because both documents read well
// in isolation. These functions are what makes that failure loud.
//
// Tables are located by their header cells rather than by section number, so
// renumbering the documents does not break the check.

const idPattern = /`?([A-Z][A-Z0-9]*(?:-[A-Z0-9]+)*)-R-(\d{3})`?/

// A row is retired when its text says so; retired IDs stay in the matrix to keep
// the number from being handed to a different requirement later, but they are not
// expected to appear in any milestone.
const retiredMarkers = ['已废弃', 'deprecated', 'retired']

function tableRows(markdown, headerMatches) {
  const lines = markdown.split('\n')
  const rows = []
  let inTable = false

  for (const line of lines) {
    const trimmed = line.trim()
    if (!trimmed.startsWith('|')) {
      inTable = false
      continue
    }
    const cells = trimmed.slice(1, trimmed.endsWith('|') ? -1 : undefined).split('|').map((cell) => cell.trim())

    if (!inTable) {
      if (headerMatches(cells)) inTable = 'header'
      continue
    }
    if (inTable === 'header') {
      // the |---|---| separator
      inTable = cells.every((cell) => /^:?-{3,}:?$/.test(cell)) ? true : false
      continue
    }
    rows.push(cells)
  }

  return rows
}

export function parseRequirementMatrix(markdown) {
  const rows = tableRows(markdown, (cells) => cells.length >= 3 && cells[0] === 'ID')
  const requirements = []
  const prefixes = new Set()

  for (const cells of rows) {
    const match = idPattern.exec(cells[0])
    if (!match) continue
    prefixes.add(match[1])
    requirements.push({
      id: `${match[1]}-R-${match[2]}`,
      prefix: match[1],
      number: match[2],
      text: cells[1] ?? '',
      verification: (cells[2] ?? '').trim(),
      retired: retiredMarkers.some((marker) => cells.join(' ').includes(marker))
    })
  }

  return { prefixes: [...prefixes], requirements }
}

// Milestone cells list IDs in short form (R-042) and may use ranges written with
// either an em dash or a hyphen. A range is expanded against the IDs the matrix
// actually declares, so a range that spans a gap does not invent requirements.
export function expandIdCell(cell, knownNumbers) {
  const numbers = []
  const ranges = [...cell.matchAll(/R-(\d{3})\s*[—–-]\s*R-(\d{3})/g)]

  for (const [, start, end] of ranges) {
    const from = Number(start)
    const to = Number(end)
    if (to < from) {
      numbers.push({ number: start, invalidRange: `R-${start}—R-${end}` })
      continue
    }
    for (let value = from; value <= to; value += 1) {
      const padded = String(value).padStart(3, '0')
      if (knownNumbers.has(padded)) numbers.push({ number: padded })
    }
  }

  let remainder = cell
  for (const [full] of ranges) remainder = remainder.replace(full, ' ')
  for (const [, number] of remainder.matchAll(/R-(\d{3})/g)) numbers.push({ number })

  return numbers
}

export function parseMilestoneAssignments(markdown, knownNumbers) {
  const rows = tableRows(
    markdown,
    (cells) => cells.length >= 2 && cells[0].includes('里程碑') && cells[1].includes('需求 ID')
  )

  return rows.map((cells) => ({
    milestone: cells[0],
    entries: expandIdCell(cells[1] ?? '', knownNumbers),
    status: cells[2] ?? ''
  }))
}

export function parseE2eRecords(markdown) {
  const rows = tableRows(
    markdown,
    (cells) => cells.length >= 2 && cells[0].includes('需求 ID') && cells[1].includes('脚本')
  )

  return rows
    .map((cells) => /R-(\d{3})/.exec(cells[0]))
    .filter((match) => match !== null)
    .map((match) => match[1])
}

export function checkRequirementCoverage({ matrix, assignments, e2eRecords }) {
  const errors = []
  const { prefixes, requirements } = matrix

  if (requirements.length === 0) return { errors, checked: 0 }
  if (prefixes.length > 1) {
    errors.push(`需求矩阵混用了多个 ID 前缀：${prefixes.join('、')}`)
  }

  const seen = new Set()
  for (const requirement of requirements) {
    if (seen.has(requirement.number)) {
      errors.push(`需求矩阵重复定义 ${requirement.id}`)
    }
    seen.add(requirement.number)
  }

  const active = requirements.filter((requirement) => !requirement.retired)
  const byNumber = new Map(requirements.map((requirement) => [requirement.number, requirement]))

  const assignedCount = new Map()
  for (const { milestone, entries } of assignments) {
    for (const entry of entries) {
      if (entry.invalidRange) {
        errors.push(`${milestone} 的区间 ${entry.invalidRange} 起止颠倒`)
        continue
      }
      if (!byNumber.has(entry.number)) {
        errors.push(`${milestone} 引用了矩阵中不存在的 R-${entry.number}`)
        continue
      }
      assignedCount.set(entry.number, (assignedCount.get(entry.number) ?? 0) + 1)
    }
  }

  for (const requirement of active) {
    const count = assignedCount.get(requirement.number) ?? 0
    if (count === 0) errors.push(`${requirement.id} 没有归属任何里程碑，不会被任何阶段验收`)
    if (count > 1) errors.push(`${requirement.id} 归属了 ${count} 个里程碑，必须恰好一个`)
  }

  for (const requirement of requirements) {
    if (!requirement.retired) continue
    if ((assignedCount.get(requirement.number) ?? 0) > 0) {
      errors.push(`${requirement.id} 已废弃但仍归属于某个里程碑`)
    }
  }

  const recorded = new Set(e2eRecords)
  for (const requirement of active) {
    const needsE2e = requirement.verification.includes('e2e')
    if (needsE2e && !recorded.has(requirement.number)) {
      errors.push(`${requirement.id} 的验证方式是 e2e，但实现检查表的 e2e 记录里没有它`)
    }
    if (!needsE2e && recorded.has(requirement.number)) {
      errors.push(`${requirement.id} 出现在 e2e 记录里，但矩阵标注的验证方式是「${requirement.verification}」`)
    }
  }

  for (const number of recorded) {
    if (!byNumber.has(number)) errors.push(`e2e 记录引用了矩阵中不存在的 R-${number}`)
  }

  return { errors, checked: active.length, retired: requirements.length - active.length }
}
