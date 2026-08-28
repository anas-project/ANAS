# Repository instructions

- Create and deliver all research reports under `docs/research/` as Markdown (`.md`).
- Do not create DOCX, PDF, or other report formats unless the user explicitly requests them.

## Workflow agreements

- Execute clear, explicit implementation instructions directly without asking for confirmation. Present the proposed approach and obtain the user's confirmation only when the approach, requirements, or trade-offs are uncertain or require a material user decision; ask instead of making a material assumption.
- When the user explicitly asks for a "git 提交", commit the requested changes and then merge the working branch into `master`. Before finishing, review the current conversation for discussed or agreed implementation items that remain incomplete, and clearly remind the user about them.
- At the end of every response, include a concise "下一步" section that tells the user what to do next. If no user action is required, explicitly state that no further action is needed.

## Response style

- Default to concise, direct responses. Lead with the outcome or answer.
- For ordinary questions, use no more than 3–6 sentences or 5 bullets unless additional detail is necessary to complete the task safely and correctly.
- Preserve required facts, decisions, material caveats, verification results, and next actions. Omit repeated context, generic reassurance, unnecessary introductions, and optional background first.
- Use headings, tables, and long explanations only when they materially improve comprehension or the user requests them.

- Implementation work is driven by `dev-docs/plans/<topic>.md`: it names the current milestone, the
  verification commands, and what is blocked. Acceptance criteria are not in the plan — they live in
  the paired `dev-docs/requirements/<topic>.md` requirement matrix and are addressed by stable IDs.
  Keep the plan's checklist current as you work; `npm run docs:check-requirements` gates consistency.
- Read `dev-docs/requirements/index.md` and `dev-docs/plans/index.md` before opening any document
  under them. Both have a generated status column, so one screen tells you which documents are live
  and which are finished; `dev-docs/plans/archived/` holds plans whose milestones are all done.
- The status column of `dev-docs/requirements/index.md` is generated, not written: after changing a
  requirement matrix, a milestone status, or the set of requirement/plan documents, run
  `npm run docs:requirement-status`. `npm run docs:check-requirement-status` gates it in CI.
  The plan index works the same way: `npm run docs:plan-status` regenerates it from each plan's
  frontmatter `status` (`proposed` / `implementing` / `partial` / `done`), gated by
  `npm run docs:check-plan-status`.

## Code reuse and dependencies

- Prefer reusing existing code and abstractions in the repository. When no suitable implementation exists, prefer a mature, actively maintained library over a custom implementation; explain the choice when introducing a new dependency.

## Documentation synchronization

- When changing functionality in `module` or `anas`, update the corresponding documentation sections in the same change so that the documentation remains consistent with the implementation.
