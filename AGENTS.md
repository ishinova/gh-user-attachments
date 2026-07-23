# Repository operating contract

This repository owns the `gh-user-attachments` GitHub CLI extension. Read `README.md`,
`SECURITY.md`, and the affected workflows before changing behavior.

## Change and verification contract

- Preserve the native macOS arm64 release contract unless the task explicitly
  changes supported platforms.
- Keep release binaries reproducible and GitHub Actions pinned to immutable full
  commit SHAs with the corresponding version in an adjacent comment.
- Run `mise run check` before opening or updating a pull request.
- When a workflow changes, inspect every remote `uses:` reference and verify its
  inputs, permissions, runner compatibility, and runtime migration requirements.
- Keep secrets, browser sessions, credentials, and private repository data out of
  source, logs, issues, and pull-request bodies.

## Dependency maintenance contract

Dependency maintenance runs as two independent scheduled lanes. Do not add a
second writer for either lane.

Treat GitHub Actions and Go modules as independent maintenance lanes. For each
lane:

- Start from current `main`, freeze all candidates found at the beginning of the
  run, and produce zero or one pull request for the complete batch.
- Continue an existing open pull request for the same lane instead of creating a
  duplicate. Identify the lane from its dependency scope and changed files; do
  not require a fixed branch name. If no update is needed, create no branch,
  commit, or pull request.
- Read official release notes, changelogs, migration guides, and security
  advisories across the complete current-to-target interval. Identify breaking
  changes and required migrations before editing.
- Include major updates only when their migration and repository verification can
  be completed in the same pull request. Never preserve an obsolete path as an
  unverified fallback.
- Open a ready-for-review pull request only after `mise run check` succeeds. The
  body must list current and target versions, immutable references where
  applicable, official sources, impact, migrations, commands and results, and
  remaining risks.

For the GitHub Actions lane, update every use of the same action to one validated
full SHA and version comment. For the Go modules lane, evaluate direct modules,
new major module paths, transitive changes, imports, generated consumers, and
licenses; run `go mod tidy`, regenerate `third_party_licenses` with
`mise run licenses:update`, and include the resulting `go.sum` and license
changes. The `go-licenses` tool directive in `go.mod` belongs to this lane, and
`mise run check` verifies that the notices stay in sync with `go.mod`.

## Release contract

A pull request that changes the CLI surface, JSON schema, or Skill contract
ships its release instead of leaving one pending.

- Update `skills/gh-user-attachments/` in the same pull request: `SKILL.md` and
  `agents/openai.yaml` describe the new contract, and `extension.json` pins
  the intended next tag so the deployed Skill gates until that release
  exists. Choose the next version by semver from the Conventional Commit
  subjects since the previous tag (`!` or `BREAKING CHANGE` means a major
  bump).
- After such a pull request merges, run the release procedure without being
  asked: build the release candidate, run the real upload/render E2E below,
  and present the version and evidence for explicit human approval. Create
  and push the tag only with that per-release approval; the tag workflow
  builds and publishes the release. Verify the published assets and
  `gh user-attachments --version`, then sync the deployed Skill copy from
  `skills/`.
- E2E uses the designated sandbox issue in this repository (create one titled
  `E2E sandbox` when absent): confirm `auth status` is valid, `upload` a real
  PNG and MP4, and verify canonical `user-attachments` URLs plus GitHub
  inline rendering (inline image and `<video` in the rendered HTML). Record
  the target, digests, and URLs as evidence.

## GitHub mutation boundary

- Use Japanese Conventional Commit subjects and Japanese Conventional Commit
  pull-request titles, for example `chore(deps): Go Modules依存関係を更新`.
- Do not merge, enable auto-merge, dispatch workflows, change repository
  settings, or add/remove labels. Create releases or tags only through the
  Release contract with explicit per-release human approval.
- Do not post comments that invoke automated reviewers, including
  `@codex review`.
