// ZRemote — Windows 4K120 远程桌面主机端
// 捕获: DXGI Desktop Duplication (ffmpeg ddagrab)  编码: NVENC/QSV/AMF/x264
// 传输: WebSocket + H.264 Annex-B  控制: SendInput 注入鼠标键盘
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/coder/websocket"
	"github.com/skip2/go-qrcode"
)

//go:embed web/index.html
var webFS embed.FS

var version = "1.0.0"

// ─────────────────────────── Windows API ───────────────────────────

var (
	modUser32          = syscall.NewLazyDLL("user32.dll")
	procSendInput      = modUser32.NewProc("SendInput")
	procEnumDisplayMon = modUser32.NewProc("EnumDisplayMonitors")
	procSetDpiAware    = modUser32.NewProc("SetProcessDpiAwarenessContext")
	procSetConsoleCP   = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleOutputCP")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseMoveF       = 0x0001
	mouseLeftDown    = 0x0002
	mouseRightDown   = 0x0008
	mouseRightUp     = 0x0010
	mouseMiddleDown  = 0x0020
	mouseMiddleUp    = 0x0040
	mouseWheelF      = 0x0800
	mouseVirtualDesk = 0x4000
	mouseAbsolute    = 0x8000

	keyExtended = 0x0001
	keyUp       = 0x0002
	keyUnicode  = 0x0004
	keyScanCode = 0x0008
)

type mouseIN struct {
	Type                     uint32
	_                        uint32
	Dx, Dy                   int32
	MouseData, DwFlags, Time uint32
	_                        uint32
	Extra                    uintptr
}

type keybdIN struct {
	Type          uint32
	_             uint32
	WVk, WScan    uint16
	DwFlags, Time uint32
	_             uint32
	Extra         uintptr
}

func sendMouse(dx, dy int32, data, flags uint32) {
	in := []mouseIN{{Type: inputMouse, Dx: dx, Dy: dy, MouseData: data, DwFlags: flags}}
	r, _, _ := procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
	if dbgInput {
		var p pointT
		procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
		fmt.Printf("[dbg] sendMouse(%d,%d,flags=%#x) ret=%d cursor=(%d,%d)\n", dx, dy, flags, r, p.X, p.Y)
	}
}

type pointT struct{ X, Y int32 }

var procGetCursorPos = modUser32.NewProc("GetCursorPos")
var dbgInput = os.Getenv("ZREMOTE_DBG") != ""

func sendKey(vk, scan uint16, flags uint32) {
	in := []keybdIN{{Type: inputKeyboard, WVk: vk, WScan: scan, DwFlags: flags}}
	procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
}

type rect struct{ Left, Top, Right, Bottom int32 }

func (r rect) w() int32 { return r.Right - r.Left }
func (r rect) h() int32 { return r.Bottom - r.Top }

var monMu sync.Mutex
var monitorRects []rect

func enumMonitors() []rect {
	monMu.Lock()
	defer monMu.Unlock()
	if monitorRects != nil {
		return monitorRects
	}
	cb := syscall.NewCallback(func(hm, hdc, lprc, lp uintptr) uintptr {
		monitorRects = append(monitorRects, *(*rect)(unsafe.Pointer(lprc)))
		return 1
	})
	procEnumDisplayMon.Call(0, 0, cb, 0)
	return monitorRects
}

// ─────────────────────────── 配置 ───────────────────────────

type cfgT struct {
	Bitrate int     `json:"bitrate_mbps"`
	Fps     int     `json:"fps"`
	ScaleW  int     `json:"scale_w"`
	ScaleH  int     `json:"scale_h"`
	Monitor int     `json:"monitor"`
	Encoder string  `json:"encoder"`
	GopSec  float64 `json:"gop_sec"`
	Token   string  `json:"token"`
}

// sanitizeCfg: 所有进入 ffmpeg 参数的值一律钳制为安全范围内的整数。
// ffmpeg 以「字面量程序名 + 白名单校验后的参数数组」调用 (见 launchFFmpeg), 无 shell 参与。
func sanitizeCfg(c *cfgT) {
	c.Bitrate = clampInt(c.Bitrate, 5, 300)
	c.Fps = clampInt(c.Fps, 10, 240)
	if c.ScaleW != 0 {
		c.ScaleW = clampInt(c.ScaleW, 320, 7680) &^ 1
		c.ScaleH = clampInt(c.ScaleH, 240, 4320) &^ 1
	}
	c.Monitor = clampInt(c.Monitor, 0, 8)
	switch c.Encoder {
	case "nvenc", "qsv", "amf", "x264":
	default:
		c.Encoder = "auto"
	}
	if c.GopSec <= 0 || c.GopSec > 4 {
		c.GopSec = 1
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

var (
	argPort     = flag.Int("port", 48080, "HTTP 端口 (HTTPS 为 port+363)")
	argConf     = flag.String("data", "", "配置/证书目录 (默认: exe 同目录)")
	argNewToken = flag.Bool("newtoken", false, "重新生成访问令牌")
	argVersion  = flag.Bool("v", false, "显示版本")
)

var (
	exeDir    string
	token     string
	persisted cfgT
)

// setupFFmpeg: 把 exe 所在目录加入 PATH, 之后一律以字面量 "ffmpeg" 启动,
// 程序名不含任何用户输入, 参数见 launchFFmpeg 的白名单校验。
func setupFFmpeg() {
	os.Setenv("PATH", exeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		log.Fatal("未找到 ffmpeg — 请把 ffmpeg.exe 放在 zremote.exe 同目录或加入 PATH")
	}
}

func main() {
	flag.Parse()
	if *argVersion {
		fmt.Println("zremote", version)
		return
	}
	exe, _ := os.Executable()
	exeDir = filepath.Dir(exe)
	if *argConf != "" {
		exeDir = *argConf
	}
	procSetConsoleCP.Call(65001)
	procSetDpiAware.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2

	confPath := filepath.Join(exeDir, "zremote.json")
	persisted = cfgT{Bitrate: 60, Fps: 120, Encoder: "auto", GopSec: 1.0}
	if b, err := os.ReadFile(confPath); err == nil {
		json.Unmarshal(b, &persisted)
	}
	if *argNewToken || persisted.Token == "" {
		persisted.Token = randToken()
	}
	sanitizeCfg(&persisted)
	os.WriteFile(confPath, mustJSON(persisted), 0600)
	token = persisted.Token

	setupFFmpeg()

	hub := newHub(persisted)
	mux := http.NewServeMux()
	mux.HandleFunc("/", serveIndex)
	mux.HandleFunc("/ws", hub.serveWS)
	mux.HandleFunc("/manifest.webmanifest", serveManifest)
	mux.HandleFunc("/sw.js", serveSW)
	mux.HandleFunc("/icon-192.png", serveIcon(192))
	mux.HandleFunc("/icon-512.png", serveIcon(512))

	httpLn, err := net.Listen("tcp", fmt.Sprintf(":%d", *argPort))
	if err != nil {
		log.Fatalf("HTTP 端口 %d 被占用: %v", *argPort, err)
	}
	httpsLn, lerr := net.Listen("tcp", fmt.Sprintf(":%d", *argPort+363))
	if lerr != nil {
		httpsLn = nil
		log.Printf("HTTPS 端口不可用: %v", lerr)
	}

	printBanner(httpsLn)

	httpSrv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { log.Fatal(httpSrv.Serve(httpLn)) }()
	if httpsLn != nil {
		cert, key := ensureCert()
		httpsSrv := &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() { log.Fatal(httpsSrv.ServeTLS(httpsLn, cert, key)) }()
	}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		fmt.Println("\n再见")
		hub.shutdown()
		os.Exit(0)
	}()

	select {}
}

func printBanner(httpsLn net.Listener) {
	host, _ := os.Hostname()
	fmt.Printf(`
┌───────────────────────────────────────────────────┐
│  ZRemote 远控主机 v%s                          │
└───────────────────────────────────────────────────┘
  主机名 : %s
  令牌   : %s
  编码   : %s  码率 %dMbps  帧率 %d  %s
  监视器 : %s
───────────────────────────────────────────────────
`, version, host, token,
		persisted.Encoder, persisted.Bitrate, persisted.Fps,
		scaleStr(persisted.ScaleW, persisted.ScaleH), monDesc())

	for _, ip := range lanIPs() {
		fmt.Printf("  局域网  : http://%s:%d/?k=%s\n", ip, *argPort, token)
	}
	if httpsLn != nil {
		ips := lanIPs()
		if len(ips) > 0 {
			u := fmt.Sprintf("https://%s:%d/?k=%s", ips[0], *argPort+363, token)
			fmt.Printf("  HTTPS   : %s   ← 手机推荐(证书警告时选\"继续访问\")\n", u)
			if q, err := qrcode.New(u, qrcode.Medium); err == nil {
				fmt.Println(q.ToSmallString(false))
			}
		}
	}
	fmt.Println("  等待连接中... (Ctrl+C 退出)")
}

func scaleStr(w, h int) string {
	if w == 0 {
		return "原生分辨率"
	}
	return fmt.Sprintf("%dx%d", w, h)
}

func monDesc() string {
	ms := enumMonitors()
	if len(ms) == 0 {
		return "#0 (枚举失败)"
	}
	m := ms[0]
	if len(ms) > 1 {
		return fmt.Sprintf("#0 %dx%d (+%d 台, -monitor 切换)", m.w(), m.h(), len(ms)-1)
	}
	return fmt.Sprintf("#0 %dx%d", m.w(), m.h())
}

func randToken() string {
	b := make([]byte, 8)
	rand.Read(b)
	const alpha = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	s := make([]byte, 8)
	for i := range s {
		s[i] = alpha[int(b[i])%len(alpha)]
	}
	return string(s)
}

func mustJSON(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }

func lanIPs() []string {
	var out []string
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				out = append(out, ipn.IP.String())
			}
		}
	}
	return out
}

// ─────────────────────────── 证书 ───────────────────────────

func ensureCert() (string, string) {
	certP := filepath.Join(exeDir, "cert.pem")
	keyP := filepath.Join(exeDir, "key.pem")
	if cb, err := os.ReadFile(certP); err == nil {
		if kb, kerr := os.ReadFile(keyP); kerr == nil && certCoversIPs(cb) {
			_ = kb
			return certP, keyP
		}
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "ZRemote"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	tpl.DNSNames = []string{"localhost"}
	tpl.IPAddresses = []net.IP{net.IPv4(127, 0, 0, 1)}
	for _, ip := range lanIPs() {
		if v := net.ParseIP(ip); v != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, v)
		}
	}
	der, _ := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &key.PublicKey, key)
	kb, _ := x509.MarshalECPrivateKey(key)
	cf, _ := os.Create(certP)
	pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	cf.Close()
	kf, _ := os.Create(keyP)
	pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	kf.Close()
	fmt.Println("[i] 已生成自签名证书 cert.pem/key.pem")
	return certP, keyP
}

// certCoversIPs: 证书 SAN 需覆盖当前所有局域网 IP, 否则重新生成
func certCoversIPs(cb []byte) bool {
	block, _ := pem.Decode(cb)
	if block == nil {
		return false
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil || time.Now().After(c.NotAfter) {
		return false
	}
	for _, ip := range lanIPs() {
		found := false
		for _, cip := range c.IPAddresses {
			if cip.String() == ip {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ─────────────────────────── 页面 / 鉴权 ───────────────────────────

func serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("k") != token {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(403)
		io.WriteString(w, `<!doctype html><meta charset=utf-8><body style="font-family:system-ui;background:#111;color:#eee;display:flex;align-items:center;justify-content:center;height:100vh"><div style="text-align:center"><h2>⛔ 令牌无效</h2><p>请使用控制台打印的完整链接（含 ?k=令牌）</p></div></body>`)
		return
	}
	b, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}

// ─────────────────────────── 流中枢 ───────────────────────────

type hubT struct {
	mu       sync.Mutex
	clients  map[*clientT]struct{}
	cfg      cfgT
	lastAU   time.Time
	sps, pps []byte
	stats    struct {
		encFps, encKbps float64
		frames          int
	}

	procMu       sync.Mutex
	cmd          *exec.Cmd
	encoderInUse string
	encIdx       int
	closed       bool
}

type clientT struct {
	conn *websocket.Conn
	send chan []byte
	hub  *hubT
}

// ctrlMsg: 客户端 → 服务端 控制消息 (字段为枚举/数值; 数值经钳制后才进入 ffmpeg 参数)
// 注意: 每个字段必须独立 json tag — 重名 tag 会被 encoding/json 静默丢弃
type ctrlMsg struct {
	T       string   `json:"t"`
	X       float64  `json:"x,omitempty"`
	Y       float64  `json:"y,omitempty"`
	Dx      float64  `json:"dx,omitempty"`
	Dy      float64  `json:"dy,omitempty"` // mr 相对移动 / wh 滚轮共用
	B       int      `json:"b,omitempty"`
	C       string   `json:"c,omitempty"`
	S       string   `json:"s,omitempty"`
	Keys    []string `json:"keys,omitempty"`
	Bitrate int      `json:"bitrate,omitempty"`
	Fps     int      `json:"fps,omitempty"`
	Scale   string   `json:"scale,omitempty"`
}

var encOrder = []string{"nvenc", "qsv", "amf", "x264"}

func newHub(c cfgT) *hubT {
	h := &hubT{clients: map[*clientT]struct{}{}, cfg: c}
	go h.statsLoop()
	return h
}

func (h *hubT) shutdown() {
	h.procMu.Lock()
	h.closed = true
	h.procMu.Unlock()
	h.mu.Lock()
	cl := make([]*clientT, 0, len(h.clients))
	for c := range h.clients {
		cl = append(cl, c)
	}
	h.mu.Unlock()
	for _, c := range cl {
		c.conn.Close(websocket.StatusNormalClosure, "bye")
	}
	h.stopStream()
}

func (h *hubT) serveWS(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("k") != token {
		http.Error(w, "forbidden", 403)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"}, // token 即凭证
	})
	if err != nil {
		return
	}
	cl := &clientT{conn: c, send: make(chan []byte, 96), hub: h}

	h.mu.Lock()
	first := len(h.clients) == 0
	idle := time.Since(h.lastAU) > 2*time.Second
	h.clients[cl] = struct{}{}
	n := len(h.clients)
	sps, pps := h.sps, h.pps
	h.mu.Unlock()

	fmt.Printf("[+] 客户端接入 (%s) 在线 %d\n", r.RemoteAddr, n)

	if first || idle {
		h.startStream() // 首客户端或画面静止时重启以立即输出关键帧
	} else if len(sps) > 0 && len(pps) > 0 {
		select { // 已在推流: 补发参数集, 等下一个关键帧 (≤GOP 1s)
		case cl.send <- annexB(sps, pps):
		default:
		}
	}

	go cl.writeLoop()
	cl.readLoop()
	h.remove(cl)
}

func annexB(nals ...[]byte) []byte {
	var b []byte
	for _, n := range nals {
		b = append(b, 0, 0, 0, 1)
		b = append(b, n...)
	}
	return b
}

func (h *hubT) remove(cl *clientT) {
	h.mu.Lock()
	delete(h.clients, cl)
	n := len(h.clients)
	h.mu.Unlock()
	cl.conn.Close(websocket.StatusNormalClosure, "")
	fmt.Printf("[-] 客户端断开 在线 %d\n", n)
	if n == 0 {
		go func() {
			time.Sleep(3 * time.Second)
			h.mu.Lock()
			empty := len(h.clients) == 0
			h.mu.Unlock()
			if empty {
				h.stopStream()
			}
		}()
	}
}

func (cl *clientT) writeLoop() {
	for msg := range cl.send {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := cl.conn.Write(ctx, websocket.MessageBinary, msg)
		cancel()
		if err != nil {
			return
		}
	}
}

func (cl *clientT) readLoop() {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		_, data, err := cl.conn.Read(ctx)
		cancel()
		if err != nil {
			return
		}
		if len(data) == 0 || data[0] != '{' {
			continue
		}
		var m ctrlMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		cl.handle(&m)
	}
}

func (cl *clientT) handle(m *ctrlMsg) {
	h := cl.hub
	switch m.T {
	case "mm": // 绝对移动 (0..1)
		if dbgInput {
			fmt.Printf("[dbg] mm x=%.4f y=%.4f\n", m.X, m.Y)
		}
		ms := enumMonitors()
		h.mu.Lock()
		idx := clampInt(h.cfg.Monitor, 0, len(ms)-1)
		h.mu.Unlock()
		if idx < 0 || idx >= len(ms) {
			sendMouse(int32(m.X*65535), int32(m.Y*65535), 0, mouseMoveF|mouseAbsolute)
			return
		}
		mon := ms[idx]
		if mon.Left == 0 && mon.Top == 0 { // 主屏: 普通绝对坐标兼容性最好
			sendMouse(int32(m.X*65535), int32(m.Y*65535), 0, mouseMoveF|mouseAbsolute)
		} else { // 副屏: 虚拟桌面坐标
			vd := unionRects(ms)
			ax := (float64(mon.Left) + m.X*float64(mon.w()) - float64(vd.Left)) / float64(vd.w()) * 65535
			ay := (float64(mon.Top) + m.Y*float64(mon.h()) - float64(vd.Top)) / float64(vd.h()) * 65535
			sendMouse(int32(ax), int32(ay), 0, mouseMoveF|mouseAbsolute|mouseVirtualDesk)
		}
	case "mr": // 相对移动
		sendMouse(int32(m.Dx), int32(m.Dy), 0, mouseMoveF)
	case "md", "mu":
		var f uint32
		switch m.B {
		case 0:
			f = mouseLeftDown
		case 1:
			f = mouseMiddleDown
		case 2:
			f = mouseRightDown
		default:
			return
		}
		if m.T == "mu" {
			f <<= 1 // down 标志 ×2 即对应 up 标志
		}
		sendMouse(0, 0, 0, f)
	case "wh": // 滚轮
		if d := clampI32(int32(m.Dy), -20000, 20000); d != 0 {
			sendMouse(0, 0, uint32(d), mouseWheelF)
		}
	case "kd", "ku":
		sc, ext, ok := scancodeOf(m.C)
		if !ok {
			return
		}
		f := uint32(keyScanCode)
		if ext {
			f |= keyExtended
		}
		if m.T == "ku" {
			f |= keyUp
		}
		sendKey(0, sc, f)
	case "txt": // Unicode 文本 (支持中文输入法)
		for _, u := range utf16.Encode([]rune(m.S)) {
			sendKey(0, u, keyUnicode)
			sendKey(0, u, keyUnicode|keyUp)
		}
	case "combo":
		var ins []keybdIN
		for _, n := range m.Keys {
			if sc, ext, ok := scancodeOf(n); ok {
				ins = append(ins, keybdIN{Type: inputKeyboard, WScan: sc,
					DwFlags: keyScanCode | b2u(ext)})
			}
		}
		for i := len(m.Keys) - 1; i >= 0; i-- {
			if sc, ext, ok := scancodeOf(m.Keys[i]); ok {
				ins = append(ins, keybdIN{Type: inputKeyboard, WScan: sc,
					DwFlags: keyScanCode | keyUp | b2u(ext)})
			}
		}
		if len(ins) > 0 {
			procSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(ins[0]))
		}
	case "cfg":
		h.mu.Lock()
		if m.Bitrate > 0 {
			h.cfg.Bitrate = m.Bitrate
		}
		if m.Fps > 0 {
			h.cfg.Fps = m.Fps
		}
		switch m.Scale {
		case "native":
			h.cfg.ScaleW, h.cfg.ScaleH = 0, 0
		case "":
		default:
			if p := strings.SplitN(m.Scale, "x", 2); len(p) == 2 {
				w, e1 := strconv.Atoi(p[0])
				hh, e2 := strconv.Atoi(p[1])
				if e1 == nil && e2 == nil {
					h.cfg.ScaleW, h.cfg.ScaleH = w, hh
				}
			}
		}
		sanitizeCfg(&h.cfg)
		persisted = h.cfg
		os.WriteFile(filepath.Join(exeDir, "zremote.json"), mustJSON(persisted), 0600)
		h.mu.Unlock()
		h.stopStream()
		if len(h.clients) > 0 {
			h.startStream()
		}
	}
}

func b2u(b bool) uint32 {
	if b {
		return keyExtended
	}
	return 0
}

func clampI32(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func unionRects(ms []rect) rect {
	if len(ms) == 0 {
		return rect{}
	}
	u := ms[0]
	for _, m := range ms[1:] {
		if m.Left < u.Left {
			u.Left = m.Left
		}
		if m.Top < u.Top {
			u.Top = m.Top
		}
		if m.Right > u.Right {
			u.Right = m.Right
		}
		if m.Bottom > u.Bottom {
			u.Bottom = m.Bottom
		}
	}
	return u
}

// ─────────────────────────── ffmpeg 流管理 ───────────────────────────

// ffArgRe: ffmpeg 参数白名单 —— 参数仅允许字母/数字与少量标点。
// 实际取值全部来自固定枚举字符串与经 sanitizeCfg 钳制的整数, 不含任何自由文本。
var ffArgRe = regexp.MustCompile(`^[A-Za-z0-9_\-:=.,%/ ]*$`)

// launchFFmpeg: 校验参数数组后, 以字面量程序名 "ffmpeg" 直接启动 (无 shell)。
// 这等价于 subprocess.run([...], shell=False) 的参数列表形式。
func launchFFmpeg(args []string) *exec.Cmd {
	for _, a := range args {
		if !ffArgRe.MatchString(a) {
			log.Fatalf("内部错误: ffmpeg 参数未通过白名单校验: %q", a)
		}
	}
	return exec.Command("ffmpeg", args...)
}

func (h *hubT) encoderList() []string {
	h.mu.Lock()
	c := h.cfg.Encoder
	h.mu.Unlock()
	if c != "" && c != "auto" {
		return []string{c}
	}
	return encOrder
}

// buildArgs: ffmpeg 参数全部由固定枚举 + 钳制后的整数经 strconv 生成。
// 缩放链: ddagrab 输出 d3d11/bgra, 须 hwdownload+format=bgra 下传后再软件缩放转 nv12
// (scale_cuda 不接受 d3d11 帧); 无缩放时 nvenc 保持零拷贝纯硬路径。
func (h *hubT) buildArgs(enc string) []string {
	h.mu.Lock()
	c := h.cfg
	h.mu.Unlock()
	ms := enumMonitors()
	idx := clampInt(c.Monitor, 0, len(ms)-1)
	gop := int(float64(c.Fps)*c.GopSec) + 1
	if gop < 30 {
		gop = 30
	}
	dd := "ddagrab=output_idx=" + strconv.Itoa(idx) + ":framerate=" + strconv.Itoa(c.Fps)
	br := strconv.Itoa(c.Bitrate) + "M"
	buf := strconv.Itoa(maxInt(c.Bitrate/3, 4)) + "M"
	swVF := "hwdownload,format=bgra"
	if c.ScaleW > 0 {
		swVF += ",scale=" + strconv.Itoa(c.ScaleW) + ":" + strconv.Itoa(c.ScaleH)
	}
	swVF += ",format=nv12"
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "warning", "-stats",
		"-f", "lavfi", "-i", dd}
	switch enc {
	case "nvenc":
		if c.ScaleW > 0 {
			args = append(args, "-vf", swVF)
		}
		args = append(args, "-c:v", "h264_nvenc", "-preset", "p4", "-tune", "ull",
			"-rc", "cbr", "-b:v", br, "-maxrate", br, "-bufsize", buf,
			"-g", strconv.Itoa(gop), "-bf", "0", "-delay", "0")
	case "qsv":
		args = append(args, "-vf", swVF, "-c:v", "h264_qsv", "-preset", "veryfast",
			"-b:v", br, "-maxrate", br, "-bufsize", buf,
			"-g", strconv.Itoa(gop), "-bf", "0", "-async_depth", "1")
	case "amf":
		args = append(args, "-vf", swVF, "-c:v", "h264_amf", "-quality", "speed", "-rc", "cbr",
			"-b:v", br, "-maxrate", br, "-g", strconv.Itoa(gop))
	default: // x264
		args = append(args, "-vf", swVF, "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
			"-b:v", br, "-maxrate", br, "-bufsize", buf,
			"-g", strconv.Itoa(gop), "-bf", "0", "-x264-params", "scenecut=0")
	}
	return append(args, "-an", "-f", "h264", "pipe:1")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var ffStatsRe = regexp.MustCompile(`frame=\s*(\d+)\s+fps=\s*([\d.]+).*?bitrate=\s*([\d.]+)kbits/s`)

// startStream 启动 ffmpeg（带编码器回退）
func (h *hubT) startStream() {
	h.procMu.Lock()
	defer h.procMu.Unlock()
	if h.cmd != nil || h.closed {
		return
	}
	list := h.encoderList()
	for i := 0; i < len(list); i++ {
		enc := list[(h.encIdx+i)%len(list)]
		args := h.buildArgs(enc)
		cmd := launchFFmpeg(args)
		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()
		if err := cmd.Start(); err != nil {
			fmt.Printf("[!] %s 启动失败: %v\n", enc, err)
			continue
		}
		h.cmd = cmd
		h.encoderInUse = enc
		go h.readStdout(stdout)
		go h.readStderr(stderr)
		go h.watchdog(cmd, enc, time.Now())
		h.mu.Lock()
		cfgSnapshot := h.cfg
		h.mu.Unlock()
		fmt.Printf("[▶] 推流启动 编码器=%s 监视器#%d %dMbps %dfps%s\n", enc,
			cfgSnapshot.Monitor, cfgSnapshot.Bitrate, cfgSnapshot.Fps,
			scaleSuffix(cfgSnapshot.ScaleW, cfgSnapshot.ScaleH))
		return
	}
	fmt.Println("[!] 所有编码器均不可用")
}

func scaleSuffix(w, h int) string {
	if w == 0 {
		return ""
	}
	return fmt.Sprintf(" %dx%d", w, h)
}

func (h *hubT) stopStream() {
	h.procMu.Lock()
	cmd := h.cmd
	h.cmd = nil
	h.procMu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		fmt.Println("[■] 推流停止")
	}
}

// watchdog: 进程意外退出 → 若仍有客户端则重启; 启动 5 秒内失败则切换下一编码器。
// stopStream 主动停止时已把 h.cmd 置空/替换 → 此处仍持有旧 cmd 视为正常退出, 不重启。
func (h *hubT) watchdog(cmd *exec.Cmd, enc string, start time.Time) {
	err := cmd.Wait()
	h.procMu.Lock()
	mine := h.cmd == cmd
	if mine {
		h.cmd = nil
	}
	if mine && err != nil {
		if time.Since(start) < 5*time.Second {
			h.encIdx++ // 启动即失败 → 下一编码器
		} else {
			h.encIdx = 0 // 稳定运行后死亡属偶发 → 回首选编码器
		}
	}
	h.procMu.Unlock()
	if !mine || h.closed || err == nil {
		return
	}
	fmt.Printf("[!] ffmpeg(%s) 退出: %v\n", enc, err)
	if time.Since(start) < 5*time.Second {
		fmt.Println("[i] 切换到下一个编码器重试")
	}
	h.mu.Lock()
	n := len(h.clients)
	h.mu.Unlock()
	if n > 0 {
		time.Sleep(500 * time.Millisecond)
		h.startStream()
	}
}

func (h *hubT) readStderr(r io.Reader) {
	buf := make([]byte, 8192)
	var line []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			line = append(line, buf[:n]...)
			for {
				i, j := bytes.IndexByte(line, '\n'), bytes.IndexByte(line, '\r')
				k := i
				if k < 0 || (j >= 0 && j < k) {
					k = j
				}
				if k < 0 {
					break
				}
				s := strings.TrimSpace(string(line[:k]))
				line = line[k+1:]
				if s == "" || strings.HasPrefix(s, "frame=") {
					continue
				}
				fmt.Printf("    [ffmpeg] %s\n", s)
			}
			if m := ffStatsRe.FindSubmatch(line); m != nil {
				f, _ := strconv.ParseFloat(string(m[2]), 64)
				kb, _ := strconv.ParseFloat(string(m[3]), 64)
				h.mu.Lock()
				h.stats.encFps, h.stats.encKbps = f, kb/1000
				h.mu.Unlock()
			}
		}
		if err != nil {
			return
		}
	}
}

func (h *hubT) statsLoop() {
	for range time.Tick(2 * time.Second) {
		h.mu.Lock()
		n := len(h.clients)
		st := h.stats
		cfg := h.cfg
		h.mu.Unlock()
		if n == 0 {
			continue
		}
		h.procMu.Lock()
		enc := h.encoderInUse
		h.procMu.Unlock()
		b, _ := json.Marshal(map[string]any{
			"type": "stats", "encFps": st.encFps, "encKbps": st.encKbps,
			"clients": n, "encoder": enc, "cfg": cfg,
		})
		h.mu.Lock()
		for c := range h.clients {
			select {
			case c.send <- b:
			default:
			}
		}
		h.mu.Unlock()
	}
}

// ─────────────────────────── H.264 Annex-B → 帧 ───────────────────────────

// auSplit: 按 NAL 起始码拆流, 聚合为访问单元 (AU)
type auSplit struct {
	buf      []byte
	cur      [][]byte
	hasSlice bool
	hasKey   bool
}

func findStartCode(b []byte, from int) int {
	for i := from; i+2 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			return i
		}
	}
	return -1
}

func (s *auSplit) feed(data []byte, out func(au []byte, key bool)) {
	s.buf = append(s.buf, data...)
	for {
		sc := findStartCode(s.buf, 0)
		if sc < 0 {
			if len(s.buf) > 3 {
				s.buf = s.buf[len(s.buf)-3:]
			}
			return
		}
		if sc > 0 {
			s.buf = s.buf[sc:]
		}
		hdr := 3
		if len(s.buf) > 3 && s.buf[0] == 0 && s.buf[1] == 0 && s.buf[2] == 0 && s.buf[3] == 1 {
			hdr = 4
		}
		if hdr >= len(s.buf) {
			return
		}
		next := findStartCode(s.buf, hdr)
		if next < 0 {
			if len(s.buf) > 4<<20 { // 异常保护
				s.buf = nil
				s.cur = nil
				s.hasSlice = false
				s.hasKey = false
			}
			return
		}
		scBytes := append([]byte(nil), s.buf[:hdr]...)
		nal := append([]byte(nil), s.buf[hdr:next]...)
		s.buf = s.buf[next:]

		if len(nal) == 0 {
			continue
		}
		t := nal[0] & 0x1F
		switch t {
		case 9: // AUD — 帧边界
			s.flush(out)
			s.cur = append(s.cur, scBytes, nal)
		case 1, 5: // slice
			if s.hasSlice {
				s.flush(out)
			}
			s.cur = append(s.cur, scBytes, nal)
			s.hasSlice = true
			if t == 5 {
				s.hasKey = true
			}
		case 7, 8, 6: // SPS/PPS/SEI 归属下一帧
			if s.hasSlice {
				s.flush(out)
			}
			s.cur = append(s.cur, scBytes, nal)
		default:
			s.cur = append(s.cur, scBytes, nal)
		}
	}
}

func (s *auSplit) flush(out func(au []byte, key bool)) {
	if len(s.cur) == 0 || !s.hasSlice {
		s.cur = s.cur[:0]
		return
	}
	var au []byte
	for _, p := range s.cur {
		au = append(au, p...)
	}
	out(au, s.hasKey)
	s.cur = s.cur[:0]
	s.hasSlice = false
	s.hasKey = false
}

func (h *hubT) readStdout(r io.Reader) {
	sp := &auSplit{}
	buf := make([]byte, 1<<16)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			sp.feed(chunk, func(au []byte, key bool) {
				h.mu.Lock()
				h.lastAU = time.Now()
				h.stats.frames++
				if key {
					h.cacheParamSets(au)
				}
				for c := range h.clients {
					select {
					case c.send <- au:
					default: // 慢客户端丢帧保延迟
						select {
						case <-c.send:
						default:
						}
						select {
						case c.send <- au:
						default:
						}
					}
				}
				h.mu.Unlock()
			})
		}
		if err != nil {
			return
		}
	}
}

// cacheParamSets: 从关键帧提取 SPS/PPS 缓存 (供新客户端初始化解码器), 须在 h.mu 内调用
func (h *hubT) cacheParamSets(au []byte) {
	i := 0
	for i < len(au) {
		sc := findStartCode(au, i)
		if sc < 0 {
			return
		}
		hdr := 3
		if sc+3 < len(au) && au[sc] == 0 && au[sc+1] == 0 && au[sc+2] == 0 && au[sc+3] == 1 {
			hdr = 4
		}
		start := sc + hdr
		if start >= len(au) {
			return
		}
		next := findStartCode(au, start)
		if next < 0 {
			next = len(au)
		}
		if next > start {
			nal := au[start:next]
			switch nal[0] & 0x1F {
			case 7:
				h.sps = append(h.sps[:0], nal...)
			case 8:
				h.pps = append(h.pps[:0], nal...)
			}
		}
		i = next
	}
}

// ─────────────────────────── PWA 独立 App ───────────────────────────

// serveManifest: 动态 manifest, start_url 内嵌当前令牌, 安装后直连
func serveManifest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("k") != token {
		http.Error(w, "forbidden", 403)
		return
	}
	q := "?k=" + token
	m := map[string]any{
		"name":            "ZRemote 远程桌面",
		"short_name":      "ZRemote",
		"description":     "4K/120fps 低延迟远程桌面",
		"start_url":       "/" + q,
		"scope":           "/",
		"display":         "standalone",
		"orientation":     "any",
		"background_color": "#0b0e14",
		"theme_color":     "#0b0e14",
		"icons": []map[string]any{
			{"src": "/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any maskable"},
			{"src": "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any maskable"},
		},
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Write(b)
}

const swJS = `// ZRemote Service Worker — 仅提供 PWA 安装资格与离线导航提示, 不缓存数据
const V = "zremote-sw-1";
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", e => e.waitUntil(self.clients.claim()));
self.addEventListener("fetch", e => {
  if (e.request.mode === "navigate") {
    e.respondWith(fetch(e.request).catch(() =>
      new Response("<!doctype html><meta charset=utf-8><body style=\"font-family:system-ui;background:#0b0e14;color:#eee;display:flex;align-items:center;justify-content:center;height:100vh\"><h2>主机不可达</h2></body>",
        {status: 503, headers: {"Content-Type": "text/html; charset=utf-8"}})));
  }
});
`

func serveSW(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	io.WriteString(w, swJS)
}

// 图标: 程序化绘制 (全出血蓝底 + 白色显示器播放图形), 生成一次缓存
var iconCache sync.Map // size int -> []byte

func serveIcon(size int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b []byte
		if v, ok := iconCache.Load(size); ok {
			b = v.([]byte)
		} else {
			b = drawIcon(size)
			iconCache.Store(size, b)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=86400")
		w.Write(b)
	}
}

func rrIn(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx := math.Min(math.Max(x, x0+r), x1-r)
	cy := math.Min(math.Max(y, y0+r), y1-r)
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

func inTri(px, py, ax, ay, bx, by, cx, cy float64) bool {
	d1 := (px-bx)*(ay-by) - (ax-bx)*(py-by)
	d2 := (px-cx)*(by-cy) - (bx-cx)*(py-cy)
	d3 := (px-ax)*(cy-ay) - (cx-ax)*(py-ay)
	hasNeg := d1 < 0 || d2 < 0 || d3 < 0
	hasPos := d1 > 0 || d2 > 0 || d3 > 0
	return !(hasNeg && hasPos)
}

func drawIcon(size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	white := color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
	blue := color.RGBA{0x4F, 0x8C, 0xFF, 0xFF}
	// 屏幕外框 (白色圆角环)
	sx0, sy0, sx1, sy1 := s*0.25, s*0.24, s*0.75, s*0.70
	rad, bw := s*0.05, s*0.055
	ix0, iy0, ix1, iy1 := sx0+bw, sy0+bw, sx1-bw, sy1-bw
	irad := rad * 0.55
	// 播放三角
	tx1, ty1, tx2, ty2, tx3, ty3 := s*0.43, s*0.335, s*0.43, s*0.605, s*0.585, s*0.47
	// 底座
	dx0, dy0, dx1, dy1, drad := s*0.34, s*0.755, s*0.66, s*0.815, s*0.03
	for y := 0; y < size; y++ {
		fy := float64(y) + 0.5
		for x := 0; x < size; x++ {
			fx := float64(x) + 0.5
			c := blue
			switch {
			case rrIn(fx, fy, dx0, dy0, dx1, dy1, drad):
				c = white
			case rrIn(fx, fy, sx0, sy0, sx1, sy1, rad) && !rrIn(fx, fy, ix0, iy0, ix1, iy1, irad):
				c = white
			case rrIn(fx, fy, ix0, iy0, ix1, iy1, irad) && inTri(fx, fy, tx1, ty1, tx2, ty2, tx3, ty3):
				c = white
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// ─────────────────────────── 键盘码表 (浏览器 code → 扫描码 set1) ───────────────────────────

type scT struct {
	code uint16
	ext  bool
}

var scancodes = map[string]scT{
	"Escape": {0x01, false}, "F1": {0x3B, false}, "F2": {0x3C, false}, "F3": {0x3D, false},
	"F4": {0x3E, false}, "F5": {0x3F, false}, "F6": {0x40, false}, "F7": {0x41, false},
	"F8": {0x42, false}, "F9": {0x43, false}, "F10": {0x44, false}, "F11": {0x57, false},
	"F12": {0x58, false},
	"Digit1": {0x02, false}, "Digit2": {0x03, false}, "Digit3": {0x04, false}, "Digit4": {0x05, false},
	"Digit5": {0x06, false}, "Digit6": {0x07, false}, "Digit7": {0x08, false}, "Digit8": {0x09, false},
	"Digit9": {0x0A, false}, "Digit0": {0x0B, false},
	"Minus": {0x0C, false}, "Equal": {0x0D, false}, "Backspace": {0x0E, false}, "Tab": {0x0F, false},
	"KeyQ": {0x10, false}, "KeyW": {0x11, false}, "KeyE": {0x12, false}, "KeyR": {0x13, false},
	"KeyT": {0x14, false}, "KeyY": {0x15, false}, "KeyU": {0x16, false}, "KeyI": {0x17, false},
	"KeyO": {0x18, false}, "KeyP": {0x19, false},
	"BracketLeft": {0x1A, false}, "BracketRight": {0x1B, false}, "Enter": {0x1C, false},
	"ControlLeft": {0x1D, false},
	"KeyA": {0x1E, false}, "KeyS": {0x1F, false}, "KeyD": {0x20, false}, "KeyF": {0x21, false},
	"KeyG": {0x22, false}, "KeyH": {0x23, false}, "KeyJ": {0x24, false}, "KeyK": {0x25, false},
	"KeyL": {0x26, false}, "Semicolon": {0x27, false}, "Quote": {0x28, false}, "Backquote": {0x29, false},
	"ShiftLeft": {0x2A, false}, "Backslash": {0x2B, false},
	"KeyZ": {0x2C, false}, "KeyX": {0x2D, false}, "KeyC": {0x2E, false}, "KeyV": {0x2F, false},
	"KeyB": {0x30, false}, "KeyN": {0x31, false}, "KeyM": {0x32, false},
	"Comma": {0x33, false}, "Period": {0x34, false}, "Slash": {0x35, false}, "ShiftRight": {0x36, false},
	"NumpadMultiply": {0x37, false}, "AltLeft": {0x38, false}, "Space": {0x39, false}, "CapsLock": {0x3A, false},
	"NumLock": {0x45, false}, "ScrollLock": {0x46, false},
	"NumpadDivide": {0x35, true}, "Numpad7": {0x47, false}, "Numpad8": {0x48, false}, "Numpad9": {0x49, false},
	"NumpadSubtract": {0x4A, false}, "Numpad4": {0x4B, false}, "Numpad5": {0x4C, false}, "Numpad6": {0x4D, false},
	"NumpadAdd": {0x4E, false}, "Numpad1": {0x4F, false}, "Numpad2": {0x50, false}, "Numpad3": {0x51, false},
	"Numpad0": {0x52, false}, "NumpadDecimal": {0x53, false},
	"NumpadEnter": {0x1C, true}, "ControlRight": {0x1D, true}, "AltRight": {0x38, true},
	"Home": {0x47, true}, "ArrowUp": {0x48, true}, "PageUp": {0x49, true},
	"ArrowLeft": {0x4B, true}, "ArrowRight": {0x4D, true},
	"End": {0x4F, true}, "ArrowDown": {0x50, true}, "PageDown": {0x51, true},
	"Insert": {0x52, true}, "Delete": {0x53, true},
	"MetaLeft": {0x5B, true}, "MetaRight": {0x5C, true}, "ContextMenu": {0x5D, true},
	"PrintScreen": {0x37, true}, "Pause": {0x46, true},
	// 语义别名 (客户端组合键/虚拟按键用)
	"Ctrl": {0x1D, false}, "Alt": {0x38, false}, "Shift": {0x2A, false}, "Win": {0x5B, true},
	"Esc": {0x01, false}, "Del": {0x53, true}, "Up": {0x48, true}, "Down": {0x50, true},
	"Left": {0x4B, true}, "Right": {0x4D, true},
}

func scancodeOf(code string) (uint16, bool, bool) {
	s, ok := scancodes[code]
	return s.code, s.ext, ok
}
