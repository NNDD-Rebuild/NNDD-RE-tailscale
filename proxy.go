package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// secretHeader は NNDD-RE 本体がサイドカー経由のリクエストだと確認するための共有シークレットのヘッダー。
// 値は起動ごとに本体が生成して stdin で渡す。クライアントから届いた同名ヘッダーは必ず捨てる。
const secretHeader = "X-Nndd-Sidecar-Secret"

// Identity は tailnet 上の接続元 (WhoIs の結果)。
type Identity struct {
	Login string // 例: alice@example.com
	Name  string // 表示名
	Node  string // 端末名
}

// WhoIsFunc は接続元アドレス (ip:port) から tailnet 上の接続元を引く。
type WhoIsFunc func(remoteAddr string) (*Identity, error)

// checkUpstream は転送先が loopback であることを確認する。
// このサイドカーは「同じPCの NNDD-RE 内蔵HTTPサーバー」専用で、任意のホストへの転送には使わせない。
func checkUpstream(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" {
		return nil, fmt.Errorf("upstream must be http: %q", raw)
	}
	// ホスト名 (localhost 等) は解決先が変わりうるので受け付けず、loopback の IP リテラルだけにする
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("upstream must be a loopback IP address (127.0.0.1 or ::1): %q", raw)
	}
	if u.Port() == "" {
		return nil, errors.New("upstream must include a port")
	}
	return u, nil
}

// newProxy は tailnet からのリクエストを upstream (127.0.0.1 の NNDD-RE) へ転送するハンドラを作る。
//   - クライアントが付けた Tailscale-* と共有シークレットのヘッダーは除去する (なりすまし防止)
//   - 接続元を WhoIs で確認し、Tailscale-User-* を付け直す。確認できなければ 403
//   - 共有シークレットを付ける (本体が「サイドカー経由」と判定する)
//   - Host はそのまま渡す (本体の Host 検証が MagicDNS 名を見る)
func newProxy(upstream *url.URL, secret, scheme string, whois WhoIsFunc) http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Proto", scheme)
			scrub(pr.Out.Header)
			id := pr.In.Context().Value(identityKey{}).(*Identity)
			// 表示名は非 ASCII を含みうる (HTTP ヘッダーでは文字化けする) ため付けない
			pr.Out.Header.Set("Tailscale-User-Login", id.Login)
			pr.Out.Header.Set("Tailscale-Node-Name", id.Node)
			pr.Out.Header.Set(secretHeader, secret)
		},
		// 動画のストリーミングを溜め込まず流す
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy error: %s %s: %v", r.Method, r.URL.Path, err)
			http.Error(w, "NNDD-RE に接続できません", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := whois(r.RemoteAddr)
		if err != nil || id == nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		rp.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), id)))
	})
}

// scrub はクライアントから届いた、本体が信頼しうるヘッダーを除去する。
func scrub(h http.Header) {
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "tailscale-") {
			h.Del(k)
		}
	}
	h.Del(secretHeader)
}
