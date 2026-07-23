# Security

脆弱性は、この repository の private GitHub Security Advisory で報告してください。
GitHub session cookie、token、private repository data、アップロード対象の非公開 file
を Issue に含めないでください。

support 対象は最新 release だけです。native attachment upload は GitHub Web UI の
非公開 contract と web session を使うため、予期しない endpoint、policy、host、
redirect、media type、final URL は hard failure として扱います。

web session は明示的な `auth login` だけで取得し、mode `0600` の tool 専用 file に
保存します。CLI は個人用 browser の cookie store を読まず、cookie を出力せず、
`github.com` 以外へ送りません。
