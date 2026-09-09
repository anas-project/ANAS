# Repository instructions

## Scope and execution

- Execute clear implementation requests directly. Resolve routine, reversible
  implementation details using repository conventions. Ask only when missing
  information materially changes scope, product behavior, or external effects;
  complete independent authorized work first.
- For review or proposal requests, deliver findings and recommendations without
  applying implementation changes unless requested.
- Preserve unrelated working-tree changes. When the user explicitly requests
  "git 提交", commit this task's changes and merge its working branch into master.
  If the branch includes unrelated work, explain the scope conflict first.
- Prefer existing repository abstractions. Explain any new dependency.

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
- End final responses with a concise "下一步"; if no action is needed, say so.
