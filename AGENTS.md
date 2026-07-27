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

The golden files under `internal/userattachments/testdata/cli/` record what a caller observes: accepted argv, exit codes, stdout, stderr, environment names read, which local files are admitted, and the entry-point wiring. `mise run check` fails when behavior and golden disagree. Regenerate with `go test ./internal/userattachments -update` and review the diff; never regenerate to silence a failure.

They are evidence of **what** changed, not the detector of **whether** something changed. Nothing keys an obligation on them. A golden covers the observations its sections happen to drive, and the set of observations a caller could make is unbounded, so a complete detector is not reachable by adding sections; treating one as complete is what lets a real change ship while every file stays still. A section that fails to record something weakens the evidence and is worth fixing when noticed. It does not bypass a trigger.

Every golden section obeys three rules, which keep the evidence worth reading.

- Produced by calling the implementation, never by transcribing a map, a constant, or hand-written text.
- Total over its input domain, or labelled `(representative)` in its heading.
- Reachable only through a registered section; an unregistered file under the directory is an orphan and fails the suite.

Two obligations, each with its own trigger. A pull request meets the ones it triggers and states in its body which triggers fired, or that none did and why.

- Skill contract, triggered by a diff in any non-test `.go` file or under `skills/gh-user-attachments/`: update `skills/gh-user-attachments/` in same pull request so `SKILL.md` and `agents/openai.yaml` match the surface, or state that the regenerated goldens did not move and the Skill already matches. The trigger is the whole Go source because a change to what a caller observes can originate anywhere in it, and no narrower rule stays true as the code moves. Over-triggering costs one regeneration and one sentence; under-triggering ships a deployed Skill that disagrees with the released CLI. Keep deployed Skill unchanged until the release is published and verified.
- Release, triggered by a change to what a released binary does, to which artifacts `.mise/tasks/release/build` publishes, or to `skills/gh-user-attachments/`: never leave one pending. The Skill directory is a trigger on its own because the deployed copy is synchronized only from a published release; without it a Skill-only fix merges and never reaches the deployed copy. A change observable only in builds the release pipeline did not produce is not a trigger, because the released binary behaves identically. After merge run release procedure unprompted: build release candidate, run the E2E gate below against it, present version and evidence for explicit human approval. Pick next version by semver from Conventional Commit subjects since previous tag (`!` or `BREAKING CHANGE` = major bump). Create and push tag only with that per-release approval; tag workflow builds and publishes release. Verify published assets and `gh user-attachments --version`, then sync deployed Skill copy from `skills/`.

E2E is a release gate, not a pull-request gate. What it protects is the released binary's upload path, and every release that changes behavior is already approved by a human, so running it per pull request repeats the same protection at higher cost. Against the release candidate: confirm `auth status` valid, `upload` real PNG and MP4, verify canonical `user-attachments` URLs plus GitHub inline rendering (inline image and `<video` in rendered HTML). Record target, digests, URLs as release evidence. If a release ever fails this gate because of a change outside the upload and auth paths, record the file and reinstate a pull-request trigger scoped to the paths that actually failed.

## GitHub mutation boundary

- Use Japanese Conventional Commit subjects and pull-request titles without scope, example `chore: Go Modules依存関係を更新`.
- No merge, no auto-merge enable, no workflow dispatch, no repository settings change, no label add/remove. Create releases or tags only through Release contract with explicit per-release human approval.

