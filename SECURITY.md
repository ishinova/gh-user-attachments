# Security

## 脆弱性の報告

脆弱性報告はこの repository の private GitHub Security Advisory で。
GitHub session cookie、token、private repository data、アップロード対象非公開 file、Issue へ含めるな。

## Support 対象

support 対象最新 release だけ。

## 脅威モデルと信頼境界

tool 守る資産 3つ。

- `user_session` cookie。GitHub account 全体へ作用する session secret。
- アップロード対象 local file、その読み取り経路。
- stdout 出す URL 真正性。呼出側 stdout そのまま Issue や Pull Request 貼る、GitHub canonical asset URL 以外出すな。

信頼するもの: OS 返す `os.UserConfigDir()`、同一 UID 動く他 process、`gh` CLI とその認証、環境変数と command 引数。

信頼しないもの: network 応答。TLS 内側でも GitHub Web UI 非公開 contract 予告なく変わる、応答構造すべて検証、想定外 fail-closed でエラー。別 storage への fallback なし。

## 保証すること

session 取得と保存:

- session 明示的 `auth login` だけで取得。個人用 browser cookie store 読まない。login 用 Chrome profile acquisition ごと ephemeral directory、browser 終了後自分所有 profile だけ削除。
- 保存先 `os.UserConfigDir()` 配下 tool 専用 state directory（mode ちょうど `0700`）内 regular file（mode `0600`）。state 操作開いた `os.Root` 内 root-relative 操作限定、state / session / profile / lock child symlink 辿って tool 外 write / read / delete しない。
- publication 同一 directory 内一意 temporary file（`O_EXCL`）へ書込、`Sync` 後 atomic rename。新規 state / profile directory mode `0700` で作成、作成後実 directory と mode 検証。
- 既存 state symlink・非 directory・mode `0700` 以外、session file symlink・非 regular・mode `0600` 以外なら利用せず失敗。path ベース自動 `chmod` 修復なし。利用者 permission 直すか state 削除して再作成。

排他と cleanup:

- `auth login` / `auth logout` 同一 process 間 flock、session acquisition から validation・保存まで、cleanup 全体排他。lock file `auth.lock` symlink・非 regular・mode `0600` 以外なら critical section 入る前拒否、自動修復なし。`auth.lock` 自体再利用可能 file、残骸でなし。
- `auth logout` session、legacy temporary、orphan publication temporary（`.session-*`）、Chrome profile residue 削除可能な範囲すべて試行、失敗集約して返す。symlink 残骸 link 自身だけ削除。

secret 非露出:

- cookie 値、token、response body error・log・stdout へ含めない。
- session cookie `github.com` だけへ送る。cross-origin redirect 追従せず、S3 upload client cookie jar 持たず。

protocol fail-closed 検証:

- GitHub HTML page（auth 検証 / repository page）status、`text/html` media type、nil でない body、body size 上限、same-origin redirect 検証。
- upload policy / finalize response expected status、`application/json` media type、body size 上限、単一 JSON value、critical metadata（S3 host / userinfo / port / query / fragment、asset href policy との一致、required form keys、`/upload/assets/<id>` 固定 finalize path）検証。
- stdout 出す URL `https://github.com/user-attachments/assets/<uuid>` canonical 形式であること、upload 層と batch 層両方検証。

## 保証しないこと

- `os.UserConfigDir()` 至る ancestor path 全体 symlink 検査。production `UserConfigDir` 自体 trusted external precondition 扱い。
- 任意 absolute anchor に対する full-path confinement。
- 同一 UID 悪意 process への防御。`auth login` 中他 process が `UserConfigDir` / state pathname rename / replace しない前提。
- Chrome へ渡した absolute profile pathname confinement。

## GitHub 側の変更で停止する protocol 仮定

次の仮定 GitHub Web UI 非公開 contract 依存、変更あれば fallback せず fail-closed で停止。

- session 注入 `user_session` と `__Host-user_session_same_site` 両 cookie へ同値載せる仮定に依存。両 cookie 分離・別発行なら停止。
- 認証 identity と upload token GitHub HTML page `meta[name="user-login"]` と `"uploadToken"` の抽出に依存。
- upload policy / S3 POST / finalize endpoint 形状と response schema に依存。
