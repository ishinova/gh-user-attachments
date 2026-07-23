---
name: gh-user-attachments
description: "GitHub公式対応形式の複数local fileをuser-attachmentsへ一括アップロードし、完了したURLを取得する。Triggers: upload GitHub attachments, gh-user-attachments, GitHubへ複数ファイルをアップロード, GitHubへ画像や動画や文書をアップロード, 添付URLを取得."
---

# gh-user-attachments

private GitHub CLI extension `gh user-attachments`を使い、ローカルfile 2〜10件を
GitHub `user-attachments`へ一括uploadして、検証済みの最終URLを入力順で取得する。
単発upload、Markdown生成、Issue・Pull Request・コメント編集は行わない。

## 実行する

1. 実行環境が native macOS arm64 であることを確認する。
2. この `SKILL.md` と同じ directory の `extension.json` を読み、
   `gh user-attachments --version` の stdout が `versionOutput` と完全一致することを
   確認する。不一致なら停止し、利用者が明示的に依頼した場合だけ
   `gh extension install <repository> --force --pin <tag>` を実行する。
3. `gh auth status --hostname github.com` と `gh user-attachments auth status` を実行する。
   後者はsessionと`gh`のuserが一致するかだけを検証し、sessionの取得元や値は
   出力しない。exit `0` でなければ `gh user-attachments auth login` を案内する。
4. 各fileを開いて意図した内容であることを確認する。credential、private
   customer data、secret、意図しない情報を含むfileは送信しない。
5. repositoryを`OWNER/REPO`で明示し、異なるfileを2〜10件uploadする。

```bash
gh user-attachments upload \
  --repo OWNER/REPO \
  --file /absolute/path/to/before.png \
  --file /absolute/path/to/report.pdf \
  --file /absolute/path/to/debug.log
```

## 結果を判定する

- exit `0`: stdoutの各行が、全fileについてuploadとfinalizeを完了したcanonical URL。
  行の順序は入力fileの順序と一致する。
- exit `2`: optionまたはlocal fileのvalidation failure。remote mutationはない。
- exit `3`: session準備、repository情報取得、または最初のremote mutation前のfailure。
- exit `4`: completed uploadまたは未完成remote stateが存在するbatch failure。

stdoutへ出るURLは`https://github.com/user-attachments/assets/<uuid>`形式で、
finalizeまで完了したものだけである。exit `4`ではcompleted URLだけが入力順で
stdoutへ出る場合があり、未完成URLは出ない。stderrを成功URLとして扱わない。
得たURLの本文・コメントへの配置は、その宛先を所有する別workflowが行う。

## 実装済み境界

- 対応拡張子はGitHub公式
  [Attaching files](https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/attaching-files)
  で2026-07-23に確認した一覧と一致する。
- 画像・動画: `.png`、`.gif`、`.jpg`、`.jpeg`、`.svg`、`.mp4`、`.mov`、`.webm`。
- 文書: `.pdf`、`.docx`、`.pptx`、`.xlsx`、`.xls`、`.xlsm`、`.odt`、
  `.fodt`、`.ods`、`.fods`、`.odp`、`.fodp`、`.odg`、`.fodg`、`.odf`、
  `.rtf`、`.doc`。
- text・data・code・archive・audio等: `.txt`、`.md`、`.copilotmd`、`.csv`、
  `.tsv`、`.log`、`.json`、`.jsonc`、`.c`、`.cs`、`.cpp`、`.css`、
  `.drawio`、`.dmp`、`.html`、`.htm`、`.java`、`.js`、`.ipynb`、`.patch`、
  `.php`、`.cpuprofile`、`.pdb`、`.py`、`.sh`、`.sql`、`.ts`、`.tsx`、
  `.xml`、`.yaml`、`.yml`、`.zip`、`.gz`、`.tgz`、`.debug`、`.msg`、
  `.eml`、`.bmp`、`.tif`、`.tiff`、`.mp3`、`.wav`。
- 画像・動画groupはすべてのcontext向けであり、その他の追加形式はGitHub文書が
  対応を明記するIssue・Pull Request・Discussion comment等へ呼出側が配置する。
- 1回に指定できるfileは2〜10件。画像・GIFは1件10 MiB、動画は100 MiB、
  その他は25 MiBまで。batch全体への独自上限はない。
- free planの動画上限は10 MB。10 MBを超える動画はpaid planとGitHub文書記載の
  membership条件に依存するため、localで100 MiBまで検証した後の最終可否は
  GitHubのupload policyに従う。
- 同じpathの重複指定を拒否し、全fileのlocal validation後にremote uploadを始める。
- 事前検証ではpayloadを保持せず、upload直前に対象fileを再検証して1件ずつ
  読み込む。batch全体に固定の処理期限はなく、利用者の割り込みで中止する。
- symlink、非 regular file、未対応拡張子、壊れた画像、拡張子と一致しない画像、
  `ftyp` box のない MP4/MOV、EBML signature のない WebM は拒否する。
- upload には、`gh` の repository read と、同じ GitHub user の明示的な web session
  が必要である。
- `GH_USER_ATTACHMENTS_SESSION` は scope のない account session secret である。command
  argument、log、source、Issue、Pull Request に置かない。
- upload endpoint は GitHub の公開 REST/GraphQL API ではない。予期しない response、
  host、redirect、media type、final URL では停止し、別 storage へ fallback しない。
