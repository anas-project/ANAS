# Developer Guide

Identify the layer before making a change:

- the runner owns the Go CLI, resolution, planning, state, and lifecycle;
- a module is an independently released service unit with Compose, `module.yml`, and optional hooks;
- a contract defines stable communication across modules;
- a resource is persistent state managed through provider operations.

Read [repository layout](repository-layout.md), [module development](module-development.md), [testing](testing.md), [container image releases](release.md), the [documentation standard](documentation-standard.md), and the [architecture overview](/en/architecture/). The detailed Chinese design documents remain normative while English translations are completed.
