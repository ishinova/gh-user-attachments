# gh-user-attachments

[English README](README.md)

`gh-user-attachments` は、複数のローカルファイルを GitHub の `user-attachments` へアップロードし、生成された `https://github.com/user-attachments/assets/<uuid>` URL を出力する GitHub CLI エクステンションです。

- 1 回の実行で 2〜10 個のファイルを処理します。成功時、標準出力に入力順で 1 行につき 1 つの URL を出力します。
- 単一ファイルのアップロード、Markdown 生成、Issue / Pull Request / コメントの編集、ダイジェスト / メディアメタデータの出力は行いません。URL をコンテンツへ配置するのは呼び出し側の責任です。

アップロードには GitHub 内部の Web UI エンドポイントを使用します。公開 REST や GraphQL API ではないため、GitHub 側の変更によって動作しなくなる可能性があります。予期しないレスポンスステータス、JSON フレーミング、ホスト、リダイレクト、メディアタイプ、または URL はフェイルクローズドエラーとして処理され、他のストレージへのフォールバックは行われません。検証の詳細および保証範囲については [`SECURITY.md`](SECURITY.md) を参照してください。

## インストール

### GitHub CLI エクステンション

最新リリース、または承認されたリリースリリースタグを指定してインストールします。

```bash
gh auth status --hostname github.com
gh extension install ishinova/gh-user-attachments
gh user-attachments --version
```

特定のリリースタグに固定してインストールする場合：

```bash
gh extension install ishinova/gh-user-attachments --pin <APPROVED_TAG>
```

リリースは macOS arm64、Linux amd64、Linux arm64 を対象としています。ソースから試す場合は、このディレクトリで `go run .` を実行してください。

Linux で `auth login` を使用する場合、サインインは表示されるブラウザウィンドウで行われるため、Google Chrome または Chromium のインストールと利用可能なディスプレイが必要です。WSL では WSLg がディスプレイを提供しますが、`systemd=true` で起動しているディストリビューションでは `DISPLAY` が設定されないことがあります。その場合はサインイン前に `DISPLAY=:0` を export してください。

### Agent スキルのインストール

AI コーディングアシスタント向けの Agent スキルを `skills` CLI を使用してインストールする場合：

```bash
npx skills add ishinova/gh-user-attachments/skills/gh-user-attachments
```

## Web セッションの準備

リポジトリのメタデータは `gh` 認証経由で読み込まれます。内部のアップロードエンドポイントには、同じ GitHub ユーザーの `user_session` クッキーが追加で必要です。

```bash
gh user-attachments auth login
gh user-attachments auth status
```

### auth login

ツール所有の使い捨て Chrome プロファイルを使用して Chrome を起動します。GitHub へのサインイン完了後、ツールは `user_session` を抽出し、`gh` と同じユーザーに属していることを検証した上で、OS のユーザー設定ディレクトリ (`os.UserConfigDir()`) 内のツール状態配下にパーミッション `0600` の通常ファイルとしてアトミックに保存します。Chrome プロファイルはブラウザの終了後に削除されます。再利用可能な資格情報の複製は保存されたセッションファイルのみです。サインインが完了しないまま 10 分経過すると処理は中断されます。

- 個人の Chrome プロファイルやブラウザのクッキーストアが読み込まれることはありません。
- Chrome の実行ファイルは自動検出されます。macOS では Google Chrome のバンドル、Linux では `PATH` 上の `google-chrome-stable`、`google-chrome`、`chromium`、`chromium-browser` が対象です。サインインのウィンドウを表示する必要があるため、ヘッドレス専用のビルドが選択されることはありません。別の実行ファイルパスを使用する場合は `GH_USER_ATTACHMENTS_CHROME` を設定してください。
- `auth login` と `auth logout` は排他的です。他のログイン / ログアウトが進行中の場合、実行は失敗します。

### auth status

利用可能なセッションが存在し、`gh` ユーザーと一致することのみを確認します。セッションの取得元や値を出力することはありません。

### auth logout

保存されたセッションに加え、ツール所有の一時ファイルや Chrome プロファイルの残骸を削除します。状態ディレクトリに残る `auth.lock` は再利用可能なロックファイルであり、残骸ではありません。

保存された状態が無効な場合（シンボリックリンク、通常ファイル以外、予期しないパーミッション）、ツールは自動修復を行わずに失敗します。再取得するには `auth logout` を実行してから `auth login` を実行してください。ストレージおよび削除の保証については [`SECURITY.md`](SECURITY.md) を参照してください。

### ヘッド環境 (Headless environments)

ブラウザが開けない環境では、`GH_USER_ATTACHMENTS_SESSION` 環境変数に `user_session` の値を設定してください。設定されている間は保存されたセッションをオーバーライドし、`auth logout` は値を出力せずにオーバーライドが有効なまま推進されていることのみを報告します。

この値は scoped token ではなく GitHub アカウント全体に作用するセッションシークレットです。コマンド引数、ログ、ソース、Issue、Pull Request に絶対に含めないでください。

## アップロード

```bash
gh user-attachments upload \
  --repo OWNER/REPO \
  --file /absolute/path/to/before.png \
  --file /absolute/path/to/report.pdf \
  --file /absolute/path/to/debug.log
```

- `--repo` は必須です。
- `--file` は 2〜10 回指定します。同一ファイルを指す指定は拒否されます（同一パス文字列の場合、およびハードリンクや大文字小文字を区別しないパスで同一ファイルに到達する場合の両方）。

リモートへのアップロードは、すべてのファイルのローカル検証が成功した後にのみ開始されます。事前チェックでは、ペイロードを保持せずにメタデータと形式を検証します。アップロード直前に各ファイルが再検証され、1 つずつ読み込まれます。バッチ処理に固定の期限はなく、ユーザーの割り込みによって停止します。成功時、検証を通過して確定 (finalize) した URL のみが入力順で標準出力に表示されます。

## サポートされる形式とサイズ制限

正本となる仕様は GitHub の
[Attaching files](https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/attaching-files)
ドキュメントです。現在の実装は、2026-07-23 時点で検証された同ドキュメントの以下の拡張子を受け入れます。

- 画像 / 動画: `.png`, `.gif`, `.jpg`, `.jpeg`, `.svg`, `.mp4`, `.mov`, `.webm`
- ドキュメント: `.pdf`, `.docx`, `.pptx`, `.xlsx`, `.xls`, `.xlsm`, `.odt`,
  `.fodt`, `.ods`, `.fods`, `.odp`, `.fodp`, `.odg`, `.fodg`, `.odf`,
  `.rtf`, `.doc`
- テキスト / データ: `.txt`, `.md`, `.copilotmd`, `.csv`, `.tsv`, `.log`,
  `.json`, `.jsonc`
- 開発ファイル: `.c`, `.cs`, `.cpp`, `.css`, `.drawio`, `.dmp`, `.html`,
  `.htm`, `.java`, `.js`, `.ipynb`, `.patch`, `.php`, `.cpuprofile`,
  `.pdb`, `.py`, `.sh`, `.sql`, `.ts`, `.tsx`, `.xml`, `.yaml`, `.yml`
- アーカイブ: `.zip`, `.gz`, `.tgz`
- メール / ログ: `.debug`, `.msg`, `.eml`
- 追加画像: `.bmp`, `.tif`, `.tiff`
- 音声: `.mp3`, `.wav`

GitHub のドキュメントではコンテキストが区別されています。最初の「画像 / 動画」グループはあらゆる場所でサポートされますが、追加の形式はリポジトリの Issue コメント、Pull Request コメント、Discussion コメント、および Organization の Discussion でサポートされます。本 CLI は送信先を選択せずに URL のみを返すため、追加形式の URL をこれらのサポート対象コンテキストに配置するのは呼び出し側の責任です。

ローカルでのサイズ検証はドキュメントのカテゴリに従います。画像および GIF は 10 MiB、動画は 100 MiB、その他は 25 MiB です。GitHub 側では、Free プランの動画上限は 10 MB です。より大きい動画をアップロードするには、ドキュメントに記載されているプラン、Organization メンバーシップ、Outside collaborator の条件を満たす必要があります。CLI はこれらのアカウント条件をローカルで判定できないため、最大 100 MiB まで受け入れ、最終判断を GitHub のアップロードポリシーに委ねます。バッチ全体に対する個別のサイズ制限はありません。

PNG、JPEG、GIF はデコードされ、コンテンツが拡張子と一致することが確認されます。MP4 と MOV は ISO base-media の `ftyp` ボックス、WebM は EBML シグネチャがチェックされます。その他の形式は、ドキュメント化された拡張子とサイズによって検証されます。シンボリックリンク、通常ファイル以外、およびサポートされていない拡張子は拒否されます。

## 終了ステータス

- exit `0`: すべてのファイルのアップロードと確定が完了し、標準出力に入力順ですべての URL が出力された
- exit `2`: オプションまたはローカルファイルの検証が失敗した。リモートの変更は開始されていない
- exit `3`: セッション準備、リポジトリメタデータの読み込み、または最初のリモート変更の前に失敗が発生した
- exit `4`: 少なくとも 1 つのファイルが完了したか、リモート状態が変更されたか、またはリモートへの影響を排除できない失敗が発生した（例: ポリシーリクエスト送信後のトランスポート切断）

exit `4` の場合、標準出力には失敗前に確定した URL のみが入力順で出力されます。未完了の URL が出力されることはありません。標準出力が空であっても、不完全なリモート状態が存在する可能性があります。診断情報は標準エラー出力に送られます。バッチ全体に対するブラインドな再試行は避けてください。

## 開発

プロジェクトで固定されている Go、actionlint、pinact のバージョンをインストールし、リポジトリの完全なローカルゲートを実行します。

```bash
mise install
mise run check
```

Go モジュールを更新して `go mod tidy` を実行した後に依存関係のライセンス通知を更新するには:

```bash
mise run licenses:update
```

承認対象のバージョンを明示的に指定して、対応する全プラットフォーム向けのリリース候補をビルドします。

```bash
mise run release:build -- v1.2.3
```

脆弱性報告およびセキュリティ保証範囲は [`SECURITY.md`](SECURITY.md) に記載されています。
