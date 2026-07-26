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

CLI surface is the golden files under `internal/userattachments/testdata/cli/`: commands, flags, exit codes, the `--version` line format, the result URL shape, and the accepted files. `mise run check` fails when behavior and golden disagree, so a pull request changes CLI surface exactly when its diff touches that directory. Regenerate with `go test ./internal/userattachments -update` and review the diff; never regenerate to silence a failure. Recording a surface that already exists is not a change to it, so the pull request that first adds these files triggers nothing.

Three obligations, each with its own trigger. A pull request meets the ones it triggers and states in its body which triggers fired, or that none did and why.

- Skill contract, triggered by a diff under `internal/userattachments/testdata/cli/`: update `skills/gh-user-attachments/` in same pull request so `SKILL.md` and `agents/openai.yaml` match the new surface. Keep deployed Skill unchanged until the release is published and verified.
- Release, triggered by a change to what a released binary does or to which artifacts `.mise/tasks/release/build` publishes: never leave one pending. A change observable only in builds the release pipeline did not produce is not a trigger, because the released binary behaves identically. After merge run release procedure unprompted: build release candidate, present version and evidence for explicit human approval. Pick next version by semver from Conventional Commit subjects since previous tag (`!` or `BREAKING CHANGE` = major bump). Create and push tag only with that per-release approval; tag workflow builds and publishes release. Verify published assets and `gh user-attachments --version`, then sync deployed Skill copy from `skills/`.
- E2E, triggered by a diff in the upload or auth path (`batch.go`, `file.go`, `native_*.go`, `auth*.go`): run it on the pull request. Confirm `auth status` valid, `upload` real PNG and MP4, verify canonical `user-attachments` URLs plus GitHub inline rendering (inline image and `<video` in rendered HTML). Record target, digests, URLs as evidence in the pull request.

## GitHub mutation boundary

- Use Japanese Conventional Commit subjects and pull-request titles without scope, example `chore: Go Modules依存関係を更新`.
- No merge, no auto-merge enable, no workflow dispatch, no repository settings change, no label add/remove. Create releases or tags only through Release contract with explicit per-release human approval.

