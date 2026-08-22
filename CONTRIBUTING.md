# Contributing to ANAS

Run the tests relevant to your change and keep implementation, contracts, examples, and documentation consistent.

Documentation changes must follow the [documentation standard](docs/developer/documentation-standard.md). In particular:

- choose the correct audience-oriented directory before creating a page;
- update the Chinese and English mirrors for user-facing documentation;
- verify commands and configuration against the current implementation;
- distinguish current behavior from proposals and historical records;
- treat everything under `docs/` as public;
- run `npm run docs:build` before submitting;
- run `npm run docs:check-requirements` when changing a requirement matrix or an implementation plan.

Module and Runner changes should also update any affected guide, reference contract, configuration inventory, and Module catalog in the same pull request.
