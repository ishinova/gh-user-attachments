# gh-user-attachments

`gh-user-attachments` は、ローカルの複数 file を GitHub の
`user-attachments` へアップロードし、完了後の
`https://github.com/user-attachments/assets/<uuid>` URL を返す GitHub CLI
extension です。

1回の実行で2〜10ファイルを扱い、成功時のstdoutには入力順でURLを1行ずつ
出力します。単発upload、Markdown生成、Issue・Pull Request・コメント編集、
digestやmedia metadataの返却は行いません。

アップロードには GitHub Web UI の内部 endpoint を利用します。この endpoint は
公開 REST API または GraphQL API ではないため、GitHub 側の変更で動作しなくなる
可能性があります。予期しない response、host、redirect、media type、URL は
エラーとして扱い、別 storage へ fallback しません。

## インストール

この repository の承認済み release tag を明示してインストールします。

```bash
gh auth status --hostname github.com
gh extension install ishinova/gh-user-attachments --pin <APPROVED_TAG>
gh user-attachments --version
```

release 対象は native macOS arm64 です。source から確認する場合は、この
directory で `go run .` を使えます。

## Web session の準備

repository 情報の取得には `gh` の認証を使います。内部 upload endpoint には、
同じ GitHub user の `user_session` cookie が別途必要です。

```bash
gh user-attachments auth login
gh user-attachments auth status
```

`auth login` は tool 専用 profile の Google Chrome を開き、sign-in 完了後に
`user_session` を取得して tool の config directory に mode `0600` で保存します。
個人用 Chrome profile や browser cookie store は読みません。
`auth status` は利用可能な session と `gh` の user が一致することだけを確認し、
session の取得元や値は出力しません。

保存済み session は `gh user-attachments auth logout` で削除できます。
headless 環境では `GH_USER_ATTACHMENTS_SESSION` を利用します。この値は GitHub account
全体へ作用する session secret であり、scope 付き token ではありません。command
argument、log、source、Issue、Pull Request に含めないでください。

## アップロード

```bash
gh user-attachments upload \
  --repo OWNER/REPO \
  --file /absolute/path/to/before.png \
  --file /absolute/path/to/report.pdf \
  --file /absolute/path/to/debug.log
```

`--repo`は必須です。`--file`は2〜10回指定し、同じpathは重複指定できません。
全fileのlocal validationが成功してからremote uploadを開始します。事前検証では
metadataと形式を確認してpayloadを保持せず、upload直前に対象fileを再検証して
1件ずつ読み込みます。batch全体に固定の処理期限は設けず、利用者の割り込みで
中止します。成功時は、validationとfinalizeを通過したURLだけが入力順でstdoutに
出ます。配置先の本文やコメントは呼出側が扱います。

対応形式の正本は GitHub の
[Attaching files](https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/attaching-files)
です。現在の実装は、2026-07-23に確認した同文書の次の拡張子を受け付けます。

- 画像・動画: `.png`、`.gif`、`.jpg`、`.jpeg`、`.svg`、`.mp4`、`.mov`、`.webm`
- 文書: `.pdf`、`.docx`、`.pptx`、`.xlsx`、`.xls`、`.xlsm`、`.odt`、
  `.fodt`、`.ods`、`.fods`、`.odp`、`.fodp`、`.odg`、`.fodg`、`.odf`、
  `.rtf`、`.doc`
- text・data: `.txt`、`.md`、`.copilotmd`、`.csv`、`.tsv`、`.log`、
  `.json`、`.jsonc`
- 開発 file: `.c`、`.cs`、`.cpp`、`.css`、`.drawio`、`.dmp`、`.html`、
  `.htm`、`.java`、`.js`、`.ipynb`、`.patch`、`.php`、`.cpuprofile`、
  `.pdb`、`.py`、`.sh`、`.sql`、`.ts`、`.tsx`、`.xml`、`.yaml`、`.yml`
- archive: `.zip`、`.gz`、`.tgz`
- 通信・log: `.debug`、`.msg`、`.eml`
- 追加画像: `.bmp`、`.tif`、`.tiff`
- audio: `.mp3`、`.wav`

GitHub文書では、最初の画像・動画groupはすべてのcontextで対応し、それ以外の
追加形式はrepositoryのIssue comment、Pull Request comment、Discussion commentと
organization Discussionで対応すると区別されています。このCLIは配置先を変更せず
URLだけを返すため、追加形式のURLは呼出側がこれらの対応contextへ配置します。

local size validationはGitHub文書の区分に合わせ、画像・GIFは10 MiB、動画は
100 MiB、その他は25 MiBを上限にします。GitHub側ではfree planの動画上限が
10 MBです。paid planで10 MBを超える動画をuploadするには、文書に記載された
plan・organization membership・outside collaboratorの条件を満たす必要があります。
CLIはこれらのaccount条件をlocalでは確定できないため、100 MiBまでは受理し、
最終的な可否をGitHubのupload policyに委ねます。batch全体への独自のsize上限は
設けません。

PNG、JPEG、GIFは実データをdecodeして拡張子との一致を確認します。MP4とMOVは
ISO base-mediaの`ftyp` box、WebMはEBML signatureを確認します。その他の形式は
公式文書に記載された拡張子とsizeを検証します。symlink、非regular file、
未対応拡張子は拒否します。

## 終了状態

- exit `0`: 全fileのuploadとfinalizeが完了し、stdoutに全URLを入力順で出力した
- exit `2`: optionまたはlocal fileのvalidationに失敗し、remote mutationは開始していない
- exit `3`: session準備、repository情報取得、または最初のremote mutation前に失敗した
- exit `4`: 1件以上が完了したか、upload途中でremote stateが作成された後に失敗した

exit `4`のstdoutには、失敗前にfinalizeまで完了したURLだけが入力順で出ます。
未完成URLは出力しません。stdoutが空でも、完成していないremote stateが存在する
可能性があります。診断はstderrに出します。

## 開発

repository の完全な local gate は次のとおりです。

```bash
mise run check
```

release、dependency maintenance、workflow、security の運用契約は
[`AGENTS.md`](AGENTS.md) と [`SECURITY.md`](SECURITY.md) にあります。
