// Every design, requirement and plan document must say up front whether it
// describes what runs today, what is proposed, or a historical baseline.
//
// Without it a proposal reads exactly like a shipped mechanism -- app-catalog
// looked like the implemented capability designs beside it while none of its
// contracts existed in the tree. The declaration is cheap; noticing its absence
// months later is not.
//
// Two spellings are accepted because the repository uses both: frontmatter for
// requirements and newer plans, an inline line for architecture documents.

const frontmatterStatus = /^---\r?\n([\s\S]*?)\r?\n---/
// Leading emphasis is common: several drafts open with `> **状态：…`.
const inlineStatus = /^\s*>?\s*\**\s*(?:状态|Status)\s*[：:]\s*(\S.*)$/m

// How far into the document the inline form may appear. Far enough to sit under
// a title and an admonition, close enough that it is still "up front".
const inlineSearchLines = 14

export function parseDocumentStatus(markdown) {
  const frontmatter = frontmatterStatus.exec(markdown)
  if (frontmatter) {
    const match = /^status:\s*(\S.*)$/m.exec(frontmatter[1])
    if (match) return { source: 'frontmatter', value: match[1].trim() }
  }

  const head = markdown.split('\n').slice(0, inlineSearchLines).join('\n')
  const inline = inlineStatus.exec(head)
  if (inline) return { source: 'inline', value: inline[1].trim() }

  return { source: null, value: null }
}

export function checkDocumentStatus(documents) {
  const errors = []
  for (const { path, markdown } of documents) {
    const { source, value } = parseDocumentStatus(markdown)
    if (!source) {
      errors.push(`${path}: 开头未声明状态（frontmatter 的 status:，或前 ${inlineSearchLines} 行内的「状态：」/「Status:」）`)
      continue
    }
    if (value.length < 2) errors.push(`${path}: 状态声明为空`)
  }
  return { errors, checked: documents.length }
}
