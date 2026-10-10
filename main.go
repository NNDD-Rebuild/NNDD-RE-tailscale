// nndd-re-tailscale は NNDD-RE の内蔵HTTPサーバーを、RE 専用の独立した Tailscale 端末 (tsnet ノード) として
// tailnet に公開するサイドカー。同じPCで動く Tailscale 本体 (tailscaled) とは無関係に動く (root・TUN 不要)。
//
// NNDD-RE 本体が子プロセスとして起動し、標準入出力の JSON Lines で制御する (protocol.go 参照)。
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

// ビルド時に -ldflags "-X main.version=..." で埋め込む。
var version = "dev"

func main() {
	stateDir := flag.String("state-dir", "", "tsnet の状態ディレクトリ (必須。ノード鍵が入るため他ユーザーから読めない場所に置く)")
	hostname := flag.String("hostname", "nndd-re", "tailnet 上の端末名 (MagicDNS 名になる)")
	upstreamRaw := flag.String("upstream", "http://127.0.0.1:12345", "転送先 (同じPCの NNDD-RE 内蔵HTTPサーバー。loopback のみ)")
	useHTTPS := flag.Bool("https", false, "HTTPS (443) で公開する (管理画面で HTTPS 証明書を有効にしている必要がある)。既定は HTTP (80)。tailnet 内は WireGuard で暗号化される")
	ephemeral := flag.Bool("ephemeral", false, "一時的な端末として登録する (停止すると端末一覧から消える)")
	showVersion := flag.Bool("version", false, "バージョンを表示して終了")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	out := newEmitter(os.Stdout)
	fail := func(msg string) {
		out.emit(event{Event: "error", Message: msg})
		os.Exit(1)
	}
	if *stateDir == "" {
		fail("--state-dir is required")
	}
	upstream, err := checkUpstream(*upstreamRaw)
	if err != nil {
		fail(err.Error())
	}

	out.emit(event{Event: "hello", Protocol: protocolVersion, Version: version})

	// 標準入力の 1 行目: 起動パラメータ
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1<<20)
	if !in.Scan() {
		fail("missing start parameters on stdin")
	}
	var sp startParams
	if err := json.Unmarshal(in.Bytes(), &sp); err != nil {
		fail("invalid start parameters: " + err.Error())
	}
	if len(sp.Secret) < 16 {
		fail("secret must be at least 16 characters")
	}

	if err := os.MkdirAll(*stateDir, 0o700); err != nil {
		fail("cannot create state dir: " + err.Error())
	}

	srv := &tsnet.Server{
		Dir:       *stateDir,
		Hostname:  *hostname,
		AuthKey:   sp.AuthKey,
		Ephemeral: *ephemeral,
		// バックエンドの詳細ログは標準エラーへ (標準出力はプロトコル専用)
		UserLogf: func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) },
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// SIGINT / SIGTERM / 標準入力のコマンド / 標準入力の EOF (本体の終了) で止める
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	logout := make(chan struct{}, 1)
	go func() {
		defer cancel()
		for in.Scan() {
			var c command
			if json.Unmarshal(in.Bytes(), &c) != nil {
				continue
			}
			switch c.Cmd {
			case "stop":
				return
			case "logout":
				logout <- struct{}{}
				return
			}
		}
		// EOF: 親プロセスが終了した。孤児にならないよう止まる
	}()
	go func() {
		select {
		case <-sigs:
			cancel()
		case <-ctx.Done():
		}
	}()

	out.emit(event{Event: "status", State: "starting"})
	if err := srv.Start(); err != nil {
		fail("tsnet start failed: " + err.Error())
	}
	lc, err := srv.LocalClient()
	if err != nil {
		fail("local client: " + err.Error())
	}

	scheme, port := "http", 80
	var ln net.Listener
	if *useHTTPS {
		scheme, port = "https", 443
		ln, err = srv.ListenTLS("tcp", ":443")
	} else {
		ln, err = srv.Listen("tcp", ":80")
	}
	if err != nil {
		fail("listen failed: " + err.Error())
	}

	whois := func(remote string) (*Identity, error) {
		r, err := lc.WhoIs(ctx, remote)
		if err != nil {
			return nil, err
		}
		if r.Node == nil || r.UserProfile == nil {
			return nil, errors.New("peer has no identity (tagged devices are not supported)")
		}
		return &Identity{Login: r.UserProfile.LoginName, Name: r.UserProfile.DisplayName, Node: r.Node.ComputedName}, nil
	}
	httpSrv := &http.Server{
		Handler:           newProxy(upstream, sp.Secret, scheme, whois),
		ReadHeaderTimeout: 15 * time.Second,
		// 動画の長時間配信を切らないため、Read/Write のタイムアウトは設けない
	}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			out.emit(event{Event: "error", Message: "serve: " + err.Error()})
			cancel()
		}
	}()

	// ログイン状態の監視: 承認用 URL・接続状態を本体へ通知する
	go watchState(ctx, srv, out, scheme, port)

	<-ctx.Done()

	select {
	case <-logout:
		lctx, lcancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := lc.Logout(lctx); err != nil {
			out.emit(event{Event: "error", Message: "logout failed: " + err.Error()})
		} else {
			out.emit(event{Event: "logged_out"})
		}
		lcancel()
	default:
	}

	sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer scancel()
	_ = httpSrv.Shutdown(sctx)
	_ = srv.Close()
	out.emit(event{Event: "status", State: "stopped"})
}

func watchState(ctx context.Context, srv *tsnet.Server, out *emitter, scheme string, port int) {
	lc, err := srv.LocalClient()
	if err != nil {
		return
	}
	for ctx.Err() == nil {
		w, err := lc.WatchIPNBus(ctx, ipn.NotifyInitialState)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		for {
			n, err := w.Next()
			if err != nil {
				break
			}
			if n.BrowseToURL != nil && *n.BrowseToURL != "" {
				out.emit(event{Event: "auth_url", URL: *n.BrowseToURL})
			}
			if n.State != nil {
				switch *n.State {
				case ipn.NeedsLogin, ipn.NeedsMachineAuth:
					out.emit(event{Event: "status", State: "needs_login"})
				case ipn.Running:
					st, err := lc.StatusWithoutPeers(ctx)
					if err != nil || st.Self == nil {
						continue
					}
					ips := make([]string, 0, len(st.Self.TailscaleIPs))
					for _, ip := range st.Self.TailscaleIPs {
						ips = append(ips, ip.String())
					}
					out.emit(event{
						Event:   "ready",
						DNSName: strings.TrimSuffix(st.Self.DNSName, "."),
						IPs:     ips,
						Scheme:  scheme,
						Port:    port,
					})
				default:
					out.emit(event{Event: "status", State: "starting"})
				}
			}
		}
		w.Close()
	}
}
