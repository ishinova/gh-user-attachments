# Repository operating contract

Repository owns `gh-user-attachments` GitHub CLI extension. Read `README.md`,
`SECURITY.md`, affected workflows before behavior change.

## Change and verification contract

- Keep release binaries reproducible. Pin GitHub Actions to immutable full commit SHAs, version in adjacent comment.
- Run `mise run check` before opening or updating pull request.
- On workflow change, inspect every remote `uses:` reference: inputs, permissions, runner compatibility, runtime migration requirements.
- Keep secrets, browser sessions, credentials, private repository data out of source, logs, issues, pull-request bodies.

## Dependency maintenance contract

Dependency maintenance = two independent scheduled lanes. No second writer for either lane.

GitHub Actions and Go modules are independent lanes. Each lane:

- Start from current `main`, freeze all candidates found at run start, produce zero or one pull request for complete batch.
- Continue existing open pull request for same lane, never duplicate. Identify lane by dependency scope and changed files, not fixed branch name. No update needed = no branch, commit, or pull request.
- Read official release notes, changelogs, migration guides, security advisories across complete current-to-target interval. Identify breaking changes and required migrations before editing.
- Include major updates only when migration and repository verification complete in same pull request. Never keep obsolete path as unverified fallback.
- Open ready-for-review pull request only after `mise run check` succeeds. Body lists current and target versions, immutable references where applicable, official sources, impact, migrations, commands and results, remaining risks.

GitHub Actions lane: update every use of same action to one validated full SHA and version comment. Go modules lane: evaluate direct modules, new major module paths, transitive changes, imports, generated consumers, licenses; run `go mod tidy`, regenerate `third_party_licenses` with `mise run licenses:update`, include resulting `go.sum` and license changes. `go-licenses` tool directive in `go.mod` belongs to this lane; `mise run check` verifies notices stay in sync with `go.mod`.

## Release contract

Pull request that changes CLI surface, JSON schema, or Skill contract ships its release, never leaves one pending.

- Update `skills/gh-user-attachments/` in same pull request: `SKILL.md` and `agents/openai.yaml` describe new contract. Pick next version by semver from Conventional Commit subjects since previous tag (`!` or `BREAKING CHANGE` = major bump). Keep deployed Skill unchanged until that release is published and verified.
- After such pull request merges, run release procedure unprompted: build release candidate, run real upload/render E2E below, present version and evidence for explicit human approval. Create and push tag only with that per-release approval; tag workflow builds and publishes release. Verify published assets and `gh user-attachments --version`, then sync deployed Skill copy from `skills/`.
- E2E runs on the affected pull request: confirm `auth status` valid, `upload` real PNG and MP4, verify canonical `user-attachments` URLs plus GitHub inline rendering (inline image and `<video` in rendered HTML). Record target, digests, URLs as evidence in the pull request.

## GitHub mutation boundary

- Use Japanese Conventional Commit subjects and pull-request titles without scope, example `chore: Go Modules依存関係を更新`.
- No merge, no auto-merge enable, no workflow dispatch, no repository settings change, no label add/remove. Create releases or tags only through Release contract with explicit per-release human approval.

