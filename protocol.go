package main

import (
	"encoding/json"
	"io"
	"sync"
)

// プロトコルのバージョン。NNDD-RE 本体と互換が無くなる変更をしたら上げる。
const protocolVersion = 1

// 標準出力へ 1 行 1 JSON のイベントを出す (標準エラーはデバッグログ用)。
//
//	{"event":"hello","protocol":1,"version":"..."}
//	{"event":"status","state":"starting|needs_login|running|stopped"}
//	{"event":"auth_url","url":"https://login.tailscale.com/a/..."}
//	{"event":"ready","dnsName":"nndd-re.tailnet.ts.net","ips":["100.x.y.z"],"scheme":"http","port":80}
//	{"event":"error","message":"..."}
//	{"event":"logged_out"}
type event struct {
	Event    string   `json:"event"`
	Protocol int      `json:"protocol,omitempty"`
	Version  string   `json:"version,omitempty"`
	State    string   `json:"state,omitempty"`
	URL      string   `json:"url,omitempty"`
	DNSName  string   `json:"dnsName,omitempty"`
	IPs      []string `json:"ips,omitempty"`
	Scheme   string   `json:"scheme,omitempty"`
	Port     int      `json:"port,omitempty"`
	Message  string   `json:"message,omitempty"`
}

type emitter struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func newEmitter(w io.Writer) *emitter { return &emitter{enc: json.NewEncoder(w)} }

func (e *emitter) emit(ev event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.enc.Encode(ev)
}

// 標準入力。1 行目は起動パラメータ (秘密情報を引数・環境変数に載せないため)、以降はコマンド。
//
//	{"secret":"<共有シークレット>","authKey":"<任意。使い捨ての Auth key>"}
//	{"cmd":"stop"}      サーバーを止めて終了する
//	{"cmd":"logout"}    tailnet からログアウトして終了する (端末を削除)
//
// 標準入力が閉じられた (本体が終了した) 場合も停止する。
type startParams struct {
	Secret  string `json:"secret"`
	AuthKey string `json:"authKey"`
}

type command struct {
	Cmd string `json:"cmd"`
}
