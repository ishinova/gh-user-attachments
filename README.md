# gh-user-attachments

[日本語版 README](README.ja.md)

`gh-user-attachments` is a GitHub CLI extension that uploads multiple local
files to GitHub `user-attachments` and prints the resulting
`https://github.com/user-attachments/assets/<uuid>` URLs.

- Each run handles 2 to 10 files. On success, stdout carries one URL per line
  in input order.
- It does not do single-file upload, Markdown generation, Issue / Pull Request /
  comment editing, or digest / media metadata output. Placing the URLs into
  content is the caller's responsibility.

Uploads use an internal GitHub Web UI endpoint. It is not a public REST or
GraphQL API, so GitHub-side changes may break it. Unexpected response status,
JSON framing, host, redirect, media type, or URL is treated as a fail-closed
error with no fallback to other storage. See [`SECURITY.md`](SECURITY.md) for
validation details and the guarantee boundary.

## Installation

### GitHub CLI Extension

Install with an explicitly approved release tag or the latest release of this repository:

```bash
gh auth status --hostname github.com
gh extension install ishinova/gh-user-attachments
gh user-attachments --version
```

To pin to a specific release tag:

```bash
gh extension install ishinova/gh-user-attachments --pin <APPROVED_TAG>
```

Releases target native macOS arm64. To try from source, run `go run .` in this
directory.

### Agent Skill Installation

To install the agent skill for AI coding assistants using `skills`:

```bash
npx skills add ishinova/gh-user-attachments/skills/gh-user-attachments
```

## Preparing the web session

Repository metadata is read through `gh` authentication. The internal upload
endpoint additionally requires a `user_session` cookie of the same GitHub user.

```bash
gh user-attachments auth login
gh user-attachments auth status
```

### auth login

Opens Chrome with a one-shot tool-owned Chrome profile. After you complete the
GitHub sign-in, the tool extracts `user_session`, verifies that it belongs to
the same user as `gh`, and stores it under the OS user config directory
(`os.UserConfigDir()`) inside the tool state as a mode `0600` regular file with
an atomic publish. The Chrome profile is deleted after the browser closes; the
only reusable credential copy is the stored session file. Sign-in aborts after
10 minutes without completion.

- The personal Chrome profile and browser cookie stores are never read.
- The Chrome executable is auto-detected. Set `GH_USER_ATTACHMENTS_CHROME` to
  use a different executable path.
- `auth login` and `auth logout` are mutually exclusive. A run fails while
  another login / logout is in progress.

### auth status

Only confirms that a usable session exists and matches the `gh` user. It never
prints the session source or value.

### auth logout

Deletes the stored session plus tool-owned temporary files and Chrome profile
residue. The remaining `auth.lock` in the state directory is a reusable lock
file, not residue.

If the stored state is invalid (symlink, non-regular file, unexpected
permission), the tool fails without auto-repair. Run `auth logout` and then
`auth login` to re-acquire. See [`SECURITY.md`](SECURITY.md) for the storage
and deletion guarantees.

### Headless environments

Where a browser cannot open, set the `user_session` value in the
`GH_USER_ATTACHMENTS_SESSION` environment variable. While set, it overrides the
stored session, and `auth logout` only reports that the override stays
effective, without printing the value.

This value is a session secret that acts on the whole GitHub account, not a
scoped token. Never put it in command arguments, logs, source, Issues, or Pull
Requests.

## Uploading

```bash
gh user-attachments upload \
  --repo OWNER/REPO \
  --file /absolute/path/to/before.png \
  --file /absolute/path/to/report.pdf \
  --file /absolute/path/to/debug.log
```

- `--repo` is required.
- `--file` is given 2 to 10 times. Specifications that point to the same file
  are rejected, both for identical path strings and when hardlinks or
  case-insensitive paths reach the same file.

Remote upload starts only after local validation of all files succeeds. The
preflight checks metadata and format without keeping payloads; right before
upload each file is re-validated and read one at a time. The batch has no fixed
deadline and stops on user interrupt. On success, only URLs that passed
validation and finalize appear on stdout, in input order.

## Supported formats and size limits

The source of truth is GitHub's
[Attaching files](https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/attaching-files)
documentation. The current implementation accepts the following extensions from
that document as verified on 2026-07-23.

- Images / video: `.png`, `.gif`, `.jpg`, `.jpeg`, `.svg`, `.mp4`, `.mov`, `.webm`
- Documents: `.pdf`, `.docx`, `.pptx`, `.xlsx`, `.xls`, `.xlsm`, `.odt`,
  `.fodt`, `.ods`, `.fods`, `.odp`, `.fodp`, `.odg`, `.fodg`, `.odf`,
  `.rtf`, `.doc`
- Text / data: `.txt`, `.md`, `.copilotmd`, `.csv`, `.tsv`, `.log`,
  `.json`, `.jsonc`
- Development files: `.c`, `.cs`, `.cpp`, `.css`, `.drawio`, `.dmp`, `.html`,
  `.htm`, `.java`, `.js`, `.ipynb`, `.patch`, `.php`, `.cpuprofile`,
  `.pdb`, `.py`, `.sh`, `.sql`, `.ts`, `.tsx`, `.xml`, `.yaml`, `.yml`
- Archives: `.zip`, `.gz`, `.tgz`
- Mail / log: `.debug`, `.msg`, `.eml`
- Additional images: `.bmp`, `.tif`, `.tiff`
- Audio: `.mp3`, `.wav`

The GitHub document distinguishes contexts: the first images / video group is
supported everywhere, while the additional formats are supported in repository
Issue comments, Pull Request comments, Discussion comments, and organization
Discussions. This CLI only returns URLs without choosing a destination, so the
caller places URLs of the additional formats into those supported contexts.

Local size validation follows the document's categories: 10 MiB for images and
GIF, 100 MiB for video, 25 MiB otherwise. On the GitHub side the free-plan
video limit is 10 MB; uploading larger videos requires the plan, organization
membership, and outside-collaborator conditions listed in the document. The CLI
cannot determine those account conditions locally, so it accepts up to 100 MiB
and defers the final decision to GitHub's upload policy. There is no separate
whole-batch size limit.

PNG, JPEG, and GIF are decoded to confirm the content matches the extension.
MP4 and MOV are checked for an ISO base-media `ftyp` box, WebM for the EBML
signature. Other formats are validated by documented extension and size.
Symlinks, non-regular files, and unsupported extensions are rejected.

## Exit status

- exit `0`: every file finished upload and finalize, and stdout printed all
  URLs in input order
- exit `2`: option or local file validation failed; no remote mutation started
- exit `3`: failure during session preparation, repository metadata reading, or
  before the first remote mutation
- exit `4`: at least one file completed, remote state changed, or a failure
  occurred whose remote effect cannot be ruled out (for example a transport cut
  after sending a policy request)

With exit `4`, stdout carries only URLs finalized before the failure, in input
order. Unfinished URLs are never printed. Even with empty stdout, incomplete
remote state may exist. Diagnostics go to stderr. Avoid blind retries of the
whole batch.

## Development

Install the Go, actionlint, and pinact versions pinned by the project and run
the repository's complete local gate.

```bash
mise install
mise run check
```

To refresh dependency license notices after updating Go modules and running
`go mod tidy`:

```bash
mise run licenses:update
```

Build a release candidate for native macOS arm64 with the version under
approval stated explicitly.

```bash
mise run release:build -- v1.2.3
```

Vulnerability reporting and the security guarantee boundary are documented in
[`SECURITY.md`](SECURITY.md).
