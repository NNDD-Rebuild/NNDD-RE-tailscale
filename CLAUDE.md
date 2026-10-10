# nndd-re-tailscale 開発ルール

NNDD-RE 用の Tailscale サイドカー (Go + tsnet)。仕様は README、NNDD-RE 側の事情は
[NNDD-RE の dev-docs/tailscale-integration.md](https://github.com/NNDD-Rebuild/NNDD-RE/blob/main/dev-docs/tailscale-integration.md) を読む。

## ブランチ・コミット・PR
- ブランチは `<type>/<短い英語スラッグ>` (`feature/` `fix/` `docs/` `chore/`)。コミットは日本語で `feat:` / `fix:` などで始める。
- 作者・コミッターは `NNDD-Rebuild <nndd-re@nks-s.jp>`。Claude などの名義・`Co-Authored-By` は付けない。PR も日本語で、署名は入れない。
- PR は頼まれたときだけ作る。マージは人が行う。履歴の書き換え・force push は明示的に許可されたときだけ。

## 確認
```
gofmt -l .      # 出力が空であること
go vet ./...
go test ./...
```
CI はこれに加えて 4 ターゲット (linux/amd64、windows/amd64、darwin/amd64、darwin/arm64) のビルドを確認する。

## 変えるときの注意
- 制御プロトコル (`protocol.go`、README) を **非互換に変えたら `protocolVersion` を上げ**、NNDD-RE 側の `SIDECAR_PROTOCOL` も同時に直す。
- 転送先は loopback の IP リテラルだけ (`checkUpstream`)。クライアントが付けた `Tailscale-*` と共有シークレットのヘッダーは必ず捨てる (`scrub`)。
- Auth key・共有シークレットは標準入力で受け取る。引数・環境変数に載せない。

## リリース
- Actions の `release` を **Run workflow (タグ名を入力、例 `v0.1.1`)** で作る。画面から先にリリース (タグ) を作らない。
- 毎週月曜 日本時間 5:00 に `update-tailscale` が新しい tailscale.com を確認して PR を作る。マージとリリースは人が行う。
- リリースを作ったら、NNDD-RE 側で `node scripts/update-sidecar-pin.mjs <タグ>` (または週次の自動 PR) でピンを更新する。**NNDD-RE は、ピン留めしたバージョンと SHA256 のバイナリしか実行しない。**
