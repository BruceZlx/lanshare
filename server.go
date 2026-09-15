package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

// Server 是每台设备上运行的本地服务：Web UI + 设备发现 + 收发中转。
type Server struct {
	cfg     *Config
	version string

	peers  *PeerStore
	offers *OfferStore
	hist   *History

	client *http.Client // 设备间互访用的 HTTP 客户端（只连局域网地址）

	mdns   *zeroconf.Server
	mdnsMu sync.Mutex

	cfgMu sync.Mutex
	done  chan struct{}
}

func newServer(cfg *Config, version string) *Server {
	return &Server{
		cfg:     cfg,
		version: version,
		peers:   NewPeerStore(Peer{ID: cfg.ID, Name: cfg.Name, IP: "127.0.0.1", Port: cfg.Port, OS: goosArch()}),
		offers:  NewOfferStore(),
		hist:    NewHistory(),
		client: &http.Client{
			Transport: &http.Transport{
				DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				MaxIdleConns:        32,
				MaxIdleConnsPerHost: 8,
				IdleConnTimeout:     60 * time.Second,
			},
		},
		done: make(chan struct{}),
	}
}

func (s *Server) stop() {
	s.stopAnnounce()
	close(s.done)
}

func goosArch() string { return runtime.GOOS + "/" + runtime.GOARCH }

func isLanAddr(ip net.IP) bool {
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// guard 限制只有局域网/本机来源可以访问（防止被端口映射暴露到公网）。
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if !isLanAddr(net.ParseIP(host)) {
			http.Error(w, "LanShare 仅允许局域网内访问", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()

	static := http.FileServerFS(mustSub(webFS, "web"))
	mux.Handle("GET /", noStore(static))

	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("POST /api/settings", s.handleSettings)
	mux.HandleFunc("GET /api/peers", s.handlePeers)
	mux.HandleFunc("POST /api/peers/add", s.handlePeerAdd)

	mux.HandleFunc("POST /api/send/prepare", s.handleSendPrepare)
	mux.HandleFunc("POST /api/send/transfer", s.handleSendTransfer)

	mux.HandleFunc("POST /api/offer", s.handleOffer)            // 对方发来传输请求
	mux.HandleFunc("GET /api/offers", s.handleOffers)           // 本 UI 轮询
	mux.HandleFunc("GET /api/offer/{id}", s.handleOfferStatus)  // 发送方轮询状态
	mux.HandleFunc("POST /api/offer/{id}/accept", s.handleOfferAccept)
	mux.HandleFunc("POST /api/offer/{id}/reject", s.handleOfferReject)
	mux.HandleFunc("POST /api/receive", s.handleReceive)        // 对方推文件过来

	mux.HandleFunc("GET /api/history", s.handleHistory)

	return s.guard(mux)
}

func mustSub(f fs.FS, name string) fs.FS {
	sub, err := fs.Sub(f, name)
	if err != nil {
		panic(err)
	}
	return sub
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// ---------- JSON 工具 ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "无效的请求数据: "+err.Error())
		return false
	}
	return true
}

// ---------- 基础 API ----------

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           s.cfg.ID,
		"name":         s.cfg.Name,
		"os":           goosArch(),
		"version":      s.version,
		"port":         s.cfg.Port,
		"download_dir": s.cfg.DownloadDir,
		"auto_accept":  s.cfg.AutoAccept,
		"addrs":        lanIPs(),
	})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		DownloadDir string `json:"download_dir"`
		AutoAccept  *bool  `json:"auto_accept"`
	}
	if !readJSON(w, r, &req, 64<<10) {
		return
	}
	nameChanged := false
	s.cfgMu.Lock()
	if n := strings.TrimSpace(req.Name); n != "" {
		if len(n) > 64 {
			n = n[:64]
		}
		s.cfg.Name = n
		nameChanged = true
	}
	if d := strings.TrimSpace(req.DownloadDir); d != "" && d != s.cfg.DownloadDir {
		if err := osMkdirAll(d); err != nil {
			s.cfgMu.Unlock()
			writeErr(w, http.StatusBadRequest, "目录不可用: "+err.Error())
			return
		}
		s.cfg.DownloadDir = d
	}
	if req.AutoAccept != nil {
		s.cfg.AutoAccept = *req.AutoAccept
	}
	err := s.cfg.Save()
	name := s.cfg.Name
	port := s.cfg.Port
	dir := s.cfg.DownloadDir
	auto := s.cfg.AutoAccept
	s.cfgMu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, "保存配置失败: "+err.Error())
		return
	}
	s.peers.UpdateSelf(name, port)
	if nameChanged {
		go func() {
			if err := s.startAnnounce(); err != nil {
				log.Printf("[discovery] 重新广播失败: %v", err)
			}
		}()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "name": name, "download_dir": dir, "auto_accept": auto,
	})
}

func osMkdirAll(dir string) error {
	if !filepath.IsAbs(filepath.Clean(dir)) {
		return fmt.Errorf("请使用绝对路径")
	}
	return os.MkdirAll(dir, 0o755)
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.peers.List())
}

func (s *Server) handlePeerAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
	}
	if !readJSON(w, r, &req, 4<<10) {
		return
	}
	peer, err := s.probeAddress(strings.TrimSpace(req.Address))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if peer.ID == s.cfg.ID {
		writeErr(w, http.StatusBadRequest, "这是本机")
		return
	}
	peer.Manual = true
	s.peers.Upsert(peer)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "peer": peer})
}

// probeAddress 访问 ip:port 的 /api/info 确认对方是 LanShare 设备。
func (s *Server) probeAddress(address string) (Peer, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		host = address
		portStr = strconv.Itoa(defaultPort)
	}
	if net.ParseIP(host) == nil {
		return Peer{}, fmt.Errorf("请输入 IP 地址，当前: %s", host)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return Peer{}, fmt.Errorf("端口无效: %s", portStr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s:%d/api/info", host, port), nil)
	if err != nil {
		return Peer{}, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return Peer{}, fmt.Errorf("无法连接 %s:%d", host, port)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Peer{}, fmt.Errorf("对方返回 %d，可能不是 LanShare 服务", resp.StatusCode)
	}
	var info struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		OS   string `json:"os"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&info); err != nil {
		return Peer{}, fmt.Errorf("对方响应异常: %v", err)
	}
	if info.ID == "" {
		return Peer{}, fmt.Errorf("对方响应异常：缺少设备 ID")
	}
	return Peer{ID: info.ID, Name: info.Name, IP: host, Port: port, OS: info.OS}, nil
}

// maintainLoop 周期清理过期设备/传输单，并重探手动添加的设备。
func (s *Server) maintainLoop() {
	nextProbe := time.Now()
	for {
		select {
		case <-s.done:
			return
		case <-time.After(3 * time.Second):
		}
		s.peers.RemoveStale(30 * time.Second)
		s.offers.ExpirePending(120 * time.Second)
		s.offers.PruneFinished(30 * time.Minute)

		if time.Now().Before(nextProbe) {
			continue
		}
		nextProbe = time.Now().Add(15 * time.Second)
		for _, p := range s.peers.ManualPeers() {
			if p.IsOnline() {
				continue
			}
			go func(p Peer) {
				if np, err := s.probeAddress(fmt.Sprintf("%s:%d", p.IP, p.Port)); err == nil {
					np.Manual = true
					s.peers.Upsert(np)
				}
			}(p)
		}
	}
}
