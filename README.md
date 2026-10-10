# nndd-re-tailscale

[NNDD-RE](https://github.com/NNDD-Rebuild/NNDD-RE) の内蔵HTTPサーバーを、**RE 専用の独立した Tailscale 端末 (tsnet ノード)** として tailnet に公開するサイドカーです。
同じPCで動く Tailscale 本体 (tailscaled) とは無関係に動きます (root・TUN 不要)。ライセンスは MPL-2.0。

NNDD-RE の「設定 → 外部ツール」から取得・更新・削除・ログインを行います。単体で使うことは想定していません。

## 仕組み

```
tailnet の端末 ──(WireGuard)──> nndd-re-tailscale ──> 127.0.0.1:12345 (NNDD-RE)
```

- 転送先は **loopback のみ** です。任意のホストへの転送には使えません。
- 接続元を `WhoIs` で確認し、`Tailscale-User-Login` / `Tailscale-Node-Name` を付けて転送します。確認できない接続は 403 です。
- 転送先は `127.0.0.1` / `::1` の IP リテラルのみ受け付けます (ホスト名は解決先が変わりうるため不可)。
- クライアントが付けた `Tailscale-*` ヘッダーは破棄します。
- NNDD-RE 本体が起動ごとに作る共有シークレットを `X-Nndd-Sidecar-Secret` で付けます。本体はこれが一致するときだけサイドカー経由と見なします。

## 制御プロトコル (v1)

NNDD-RE が子プロセスとして起動し、標準入出力の JSON Lines で制御します。

起動: `nndd-re-tailscale --state-dir <dir> [--hostname nndd-re] [--upstream http://127.0.0.1:12345] [--https] [--ephemeral]`

標準入力 1 行目 (秘密情報を引数・環境変数に載せないため):

```json
{"secret":"<16文字以上の共有シークレット>","authKey":"<任意。使い捨ての Auth key>"}
```

以降のコマンド: `{"cmd":"stop"}` / `{"cmd":"logout"}` (tailnet から端末を削除して終了)。標準入力が閉じられたら停止します。

標準出力のイベント:

| event | 内容 |
|---|---|
| `hello` | `protocol` (互換性のあるバージョン)、`version` |
| `status` | `state`: `starting` / `needs_login` / `needs_approval` (tailnet 管理者の端末承認待ち) / `stopped` |
| `auth_url` | `url`: ブラウザで開いて承認するログイン用 URL |
| `ready` | `dnsName`、`ips`、`scheme` (`http` / `https`)、`port`。MagicDNS が無効、または `--https` で HTTPS 証明書が無効なときは `ready` ではなく `error` |
| `error` | `message` |
| `logged_out` | ログアウト完了 |

## ビルド

```
go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=dev" .
```

`v*` のタグを push すると (リリースは常に prerelease。本体はバージョンとハッシュをピン留めして取得するので、「最新」を自動で追いません)、4 ターゲット (linux/amd64、windows/amd64、darwin/amd64、darwin/arm64) をビルドして
`SHA256SUMS` 付きで GitHub Release (prerelease) に添付します。NNDD-RE 本体はリリースのバージョンとハッシュを埋め込んでピン留めします。

## ライセンス

MPL-2.0。Tailscale (BSD-3-Clause) などの依存ライセンスは [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) を参照してください。
