# Repository instructions

## Scope and execution

- Execute clear implementation requests directly. Resolve routine, reversible
  implementation details using repository conventions. Ask when missing
  information materially changes scope, product behavior, or external effects,
  or when the design confirmation rule below applies. Complete independent
  authorized work first.
- For review or proposal requests, deliver findings and recommendations without
  applying implementation changes unless requested.
- Preserve unrelated working-tree changes. When the user explicitly requests
  "git 提交", commit this task's changes and merge its working branch into master.
  If the branch includes unrelated work, explain the scope conflict first.
- Prefer the simplest design that meets current requirements, correctness, and
  safety needs. Reuse existing code and keep added components, layers,
  configuration, and operational steps to a minimum. Do not build general
  frameworks for speculative future needs or sacrifice readability merely to
  reduce line count. Explain any new dependency.
- When a simple approach cannot meet a concrete requirement and the proposed
  design substantially increases the effort to understand or maintain it, obtain
  user confirmation before finalizing or implementing that part. Explain why the
  simple approach is insufficient, what complexity is added, the benefits and
  costs, and the recommended choice. Examples include a new standalone service,
  keeping data in multiple places synchronized, or multi-stage execution with
  failure recovery. Continue independent work while awaiting confirmation. Do
  not ask again for an explicitly confirmed design whose scope has not changed.

## Requirements and documentation

- Deliver research reports under docs/research/ as Markdown unless another
  format is explicitly requested. Put repository reviews and dated assessments
  under dev-docs/reviews/ using YYYY-MM-DD-topic.md filenames.
- Read dev-docs/requirements/index.md and dev-docs/plans/index.md before opening
  documents under them. Follow the relevant topic plan for implementation;
  acceptance criteria belong to the paired requirement matrix with stable IDs.
- Keep milestone checklists and blockers current. After changing requirements,
  milestone status, or document membership, regenerate the affected indexes with
  npm run docs:requirement-status and npm run docs:plan-status.
  Validate with docs:check-requirements, docs:check-requirement-status,
  and docs:check-plan-status as applicable.
- Changes to modules/, cmd/, internal/, or web/ that affect behavior must update
  the relevant documentation in the same change. Follow the documentation
  standard for source generation and bilingual pages; edit generator inputs,
  not generated mirrors.

## Verification and review

- Run relevant tests and required repository gates. Once they pass, repeat or
  broaden validation only when changes, failures, or unresolved risks justify it.
- Verify old review findings against current call paths. Distinguish observed
  defects, design debt, and unverified hypotheses. Report checks that failed or
  were not run; do not infer real-host acceptance from unit tests.
- Keep research and proposed architecture separate from implemented behavior.

## Responses

- Default to concise Chinese answers, leading with the result. Include material
  findings, verification limits, and outstanding agreed work.
- Use familiar, precise names in replies, proposals, documentation, and new
  naming. Common technical terms such as idempotency (幂等) need no explanation.
  Explain uncommon domain terms, coined names, and unfamiliar abbreviations in
  plain language at first use; add an example when useful. Avoid dressing up
  simple concepts with complex names. Preserve exact existing code and interface
  names, explaining them when needed.
- End final responses with a concise "下一步"; if no action is needed, say so.
