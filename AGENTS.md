# Repository instructions

- Create and deliver all research reports under `docs/research/` as Markdown (`.md`).
- Do not create DOCX, PDF, or other report formats unless the user explicitly requests them.

## Workflow agreements

- Before implementing a change, present the proposed approach and obtain the user's confirmation. If the approach, requirements, or trade-offs are uncertain, ask the user instead of making a material assumption.
- When the user explicitly asks for a "git 提交", commit the requested changes and then merge the working branch into `master`. Before finishing, review the current conversation for discussed or agreed implementation items that remain incomplete, and clearly remind the user about them.

## Code reuse and dependencies

- Prefer reusing existing code and abstractions in the repository. When no suitable implementation exists, prefer a mature, actively maintained library over a custom implementation; explain the choice when introducing a new dependency.

## Documentation synchronization

- When changing functionality in `module` or `anas`, update the corresponding documentation sections in the same change so that the documentation remains consistent with the implementation.
