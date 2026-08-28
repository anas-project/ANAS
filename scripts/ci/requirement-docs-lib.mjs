// Locates the requirement and plan documents the documentation gates scan.
//
// Development artefacts live outside the VitePress site: dev-docs/ at the
// repository root for ANAS itself and cross-Module topics, and
// modules/<name>/dev-docs/ for a topic that loses its meaning once that Module
// is removed. Both obey the same pairing rule -- <scope>/requirements/<topic>.md
// is delivered by <scope>/plans/<topic>.md -- so the gates treat them uniformly
// instead of privileging the repository-level directory.
//
// A topic must exist in exactly one scope. Two copies of the same requirement
// document read fine in isolation and drift silently, so finding one under both
// dev-docs/ and a Module is an error rather than a merge.
//
// A plan whose milestones are all done moves to <scope>/plans/archived/. Its
// requirement matrix stays put -- an implemented requirement is a regression
// baseline, not history -- so the pairing still has to find the plan, it is just
// no longer an active one.

import { existsSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

export const repositoryScopeLabel = 'dev-docs'

export function requirementScopes(root = '.') {
  const scopes = [scope(join(root, 'dev-docs'), repositoryScopeLabel)]
  const modulesDir = join(root, 'modules')
  if (!existsSync(modulesDir)) return scopes.filter(hasRequirements)

  for (const name of readdirSync(modulesDir).sort()) {
    scopes.push(scope(join(modulesDir, name, 'dev-docs'), `modules/${name}`))
  }
  return scopes.filter(hasRequirements)
}

export const archivedPlansDirName = 'archived'

function scope(base, label) {
  const plansDir = join(base, 'plans')
  return { label, requirementsDir: join(base, 'requirements'), plansDir, archivedPlansDir: join(plansDir, archivedPlansDirName) }
}

function hasRequirements(candidate) {
  return existsSync(candidate.requirementsDir)
}

export function collectRequirementDocuments(scopes) {
  const documents = []
  const errors = []
  const owner = new Map()

  for (const { label, requirementsDir, plansDir, archivedPlansDir } of scopes) {
    for (const name of readdirSync(requirementsDir).sort()) {
      if (!name.endsWith('.md') || name === 'index.md') continue

      const topic = name.slice(0, -'.md'.length)
      const requirementPath = join(requirementsDir, name)
      const claimed = owner.get(topic)
      if (claimed) {
        errors.push(
          `${requirementPath}: 主题 ${topic} 已经由 ${claimed} 承载；` +
          '一个主题只能有一份需求文档，迁移时删除旧位置而不是保留两份'
        )
        continue
      }

      owner.set(topic, requirementPath)

      const planPath = join(plansDir, name)
      const archivedPlanPath = join(archivedPlansDir, name)
      const active = existsSync(planPath)
      const archived = existsSync(archivedPlanPath)
      if (active && archived) {
        errors.push(
          `${archivedPlanPath}: ${topic} 同时有活跃计划 ${planPath}；` +
          '归档是移动而不是复制，两份会各自漂移'
        )
        continue
      }

      documents.push({
        topic,
        scope: label,
        requirementPath,
        planPath: archived ? archivedPlanPath : planPath,
        archived
      })
    }
  }

  return { documents, errors }
}
