package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// runTerminalMode 在当前终端里交互收发文件（HTTP/发现服务照常在后台运行）。
func runTerminalMode(s *Server) {
	t := &termUI{
		srv:     s,
		in:      bufio.NewScanner(os.Stdin),
		noticed: map[string]bool{},
		tracked: map[string]bool{},
	}
	t.in.Buffer(make([]byte, 64*1024), 1<<20)

	line := strings.Repeat("─", 50)
	t.printf("%s\n LanShare 终端模式 · 本机「%s」\n", line, s.cfg.Name)
	t.printf(" 接收目录: %s\n", s.cfg.DownloadDir)
	t.printf(" 输入 help 查看命令，q 退出\n%s\n\n", line)

	go t.watchIncoming()
	t.run()
}

// termUI 拥有唯一的 stdin 读取者（run 循环）；其他 goroutine 只打印。
type termUI struct {
	srv *Server
	in  *bufio.Scanner
	mu  sync.Mutex // 序列化输出，避免进度条与提示交叉

	noticed map[string]bool // 已提示过的待收请求
	tracked map[string]bool // 已启动进度跟踪的传输
}

func (t *termUI) printf(format string, a ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fmt.Printf(format, a...)
}

// readLine 是唯一的 stdin 读取入口。
func (t *termUI) readLine(prompt string) (string, bool) {
	t.mu.Lock()
	fmt.Printf("%s ", prompt)
	t.mu.Unlock()
	if !t.in.Scan() {
		return "", false
	}
	return strings.TrimSpace(t.in.Text()), true
}

func (t *termUI) run() {
	for {
		line, ok := t.readLine("lanshare>")
		if !ok {
			return
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		cmd := strings.ToLower(fields[0])
		args := fields[1:]
		var err error
		switch cmd {
		case "list", "ls", "l":
			t.cmdList()
		case "send", "s":
			err = t.cmdSend(args)
		case "add":
			err = t.cmdAdd(args)
		case "y", "yes":
			t.cmdDecide(true)
		case "n", "no":
			t.cmdDecide(false)
		case "hist", "h":
			t.cmdHist()
		case "info", "i":
			t.cmdInfo()
		case "help", "?":
			t.help()
		case "q", "quit", "exit":
			return
		default:
			t.printf("未知命令 %q，输入 help 查看帮助\n", cmd)
		}
		if err != nil {
			t.printf("✗ %v\n", err)
		}
	}
}

func (t *termUI) help() {
	t.printf(`命令列表:
  list          附近设备列表
  send / s      发送文件（交互向导，或 send <序号|名称|IP> <路径...>）
  add <ip:端口> 手动连接设备（搜索不到时使用）
  y / n         接收 / 拒绝待收文件
  hist          传输历史
  info          本机信息
  q             退出
`)
}

func (t *termUI) onlinePeers() []Peer {
	var out []Peer
	for _, p := range t.srv.peers.List() {
		if !p.Self && p.Online {
			out = append(out, p)
		}
	}
	return out
}

func (t *termUI) cmdList() {
	peers := t.onlinePeers()
	if len(peers) == 0 {
		t.printf("附近设备:（暂未发现，等待中…可 add <ip:端口> 手动连接）\n")
		return
	}
	t.printf("附近设备:\n")
	for i, p := range peers {
		t.printf("  %d. %s  %s:%d  (%s)\n", i+1, p.Name, p.IP, p.Port, p.OS)
	}
}

// matchPeer 把用户输入（序号/名称/IP[:端口]）匹配到在线设备。
func (t *termUI) matchPeer(input string, peers []Peer) (Peer, error) {
	if n, err := strconv.Atoi(input); err == nil && n >= 1 && n <= len(peers) {
		return peers[n-1], nil
	}
	lower := strings.ToLower(input)
	host, _, err := splitHostPort(input)
	for _, p := range peers {
		if strings.ToLower(p.Name) == lower || p.ID == input || (err == nil && host == p.IP) {
			return p, nil
		}
	}
	return Peer{}, fmt.Errorf("没有匹配的设备 %q（list 查看序号）", input)
}

func splitHostPort(s string) (string, string, error) {
	if i := strings.LastIndexByte(s, ':'); i > 0 {
		return s[:i], s[i+1:], nil
	}
	return "", "", fmt.Errorf("no port")
}

func (t *termUI) cmdSend(args []string) error {
	peers := t.onlinePeers()
	if len(peers) == 0 {
		return fmt.Errorf("暂未发现在线设备，可先 add <ip:端口> 手动连接")
	}
	var peer Peer
	var paths []string
	if len(args) >= 2 { // send <目标> <路径...>
		p, err := t.matchPeer(args[0], peers)
		if err != nil {
			return err
		}
		peer, paths = p, args[1:]
	} else { // 交互向导
		t.printf("选择目标设备:\n")
		for i, p := range peers {
			t.printf("  %d. %s  %s:%d\n", i+1, p.Name, p.IP, p.Port)
		}
		sel, ok := t.readLine("设备序号（回车取消）:")
		if !ok || sel == "" {
			return fmt.Errorf("已取消")
		}
		p, err := t.matchPeer(sel, peers)
		if err != nil {
			return err
		}
		peer = p
		line, ok := t.readLine("文件/文件夹路径（空格分隔，回车取消）:")
		if !ok || line == "" {
			return fmt.Errorf("已取消")
		}
		paths = strings.Fields(line)
	}
	files, total, err := collectSendFiles(paths)
	if err != nil {
		return err
	}
	return t.sendTo(peer, files, total)
}

func (t *termUI) cmdAdd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: add <ip[:端口]>")
	}
	p, err := t.srv.probeAddress(args[0])
	if err != nil {
		return err
	}
	if p.ID == t.srv.cfg.ID {
		return fmt.Errorf("这是本机")
	}
	p.Manual = true
	t.srv.peers.Upsert(p)
	t.printf("✓ 已连接 %s（%s:%d）\n", p.Name, p.IP, p.Port)
	return nil
}

func (t *termUI) cmdDecide(accept bool) {
	var target *Offer
	for _, o := range t.srv.offers.List() {
		if o.Dir != "in" || o.Status != StatusPending {
			continue
		}
		c := o
		if target == nil || c.Created < target.Created {
			target = &c
		}
	}
	if target == nil {
		t.printf("当前没有待接收的请求\n")
		return
	}
	status := StatusRejected
	if accept {
		status = StatusAccepted
	}
	t.srv.offers.SetStatus(target.ID, status, "")
	if accept {
		t.printf("✓ 已接受「%s」的传输（%s），等待对方开始发送…\n", target.Peer.Name, humanSize(target.Total))
		if !t.tracked[target.ID] {
			t.tracked[target.ID] = true
			go t.trackReceive(target.ID)
		}
	} else {
		t.printf("已拒绝「%s」的传输请求\n", target.Peer.Name)
	}
}

func (t *termUI) cmdHist() {
	list := t.srv.hist.List()
	if len(list) == 0 {
		t.printf("暂无传输记录\n")
		return
	}
	t.printf("传输历史（最近 %d 条）:\n", len(list))
	for _, h := range list {
		arrow := "→"
		if h.Dir == "in" {
			arrow = "←"
		}
		name := filepath.Base(h.Files[0])
		if len(h.Files) > 1 {
			name = fmt.Sprintf("%s 等 %d 个文件", name, len(h.Files))
		}
		t.printf("  %s %s %s(%s) %s\n", arrow, name, h.Peer, humanSize(h.Total), statusText(h.Status))
	}
}

func (t *termUI) cmdInfo() {
	t.printf("设备名称: %s\n", t.srv.cfg.Name)
	t.printf("设备 ID:  %s\n", t.srv.cfg.ID)
	t.printf("服务端口: %d\n", t.srv.cfg.Port)
	t.printf("接收目录: %s\n", t.srv.cfg.DownloadDir)
	t.printf("自动接收: %v\n", t.srv.cfg.AutoAccept)
	if addrs := lanIPs(); len(addrs) > 0 {
		t.printf("本机地址: %s\n", strings.Join(addrs, ", "))
	}
}

func statusText(s string) string {
	switch s {
	case StatusDone:
		return "✓ 完成"
	case StatusFailed:
		return "✗ 失败"
	case StatusRejected:
		return "已拒绝"
	default:
		return s
	}
}

// ---------- 发送 ----------

func (t *termUI) sendTo(peer Peer, files []sendFile, total int64) error {
	offerFiles := make([]OfferFile, len(files))
	rels := make([]string, len(files))
	for i, f := range files {
		offerFiles[i] = OfferFile{Name: filepath.ToSlash(f.rel), Size: f.size}
		rels[i] = f.rel
	}
	payload, _ := json.Marshal(map[string]any{
		"from":  PeerSummary{ID: t.srv.cfg.ID, Name: t.srv.cfg.Name, OS: goosArch()},
		"files": offerFiles,
		"total": total,
	})
	t.printf("正在连接 %s（%s:%d）…\n", peer.Name, peer.IP, peer.Port)
	var resp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := t.srv.postJSON(peer.BaseURL()+"/api/offer", payload, &resp, 15*time.Second); err != nil {
		return fmt.Errorf("无法连接对方: %v", err)
	}
	status := resp.Status
	if status == StatusPending {
		t.printf("已发送请求（%s，%s），等待 %s 确认…\n", fileSummary(offerFiles), humanSize(total), peer.Name)
		status = t.srv.waitOfferDecision(peer, resp.ID, 130*time.Second)
	}
	switch status {
	case StatusAccepted:
	case StatusRejected:
		return fmt.Errorf("对方拒绝了本次传输")
	case StatusExpired, "":
		return fmt.Errorf("对方长时间未确认，已超时")
	default:
		return fmt.Errorf("对方状态异常: %s", status)
	}
	return t.uploadTo(peer, resp.ID, files, rels, total)
}

func (t *termUI) uploadTo(peer Peer, offerID string, files []sendFile, rels []string, total int64) error {
	pr, pw := io.Pipe()
	var sent int64
	lastT, lastB := time.Now(), int64(0)
	speed := int64(0)
	cw := &countWriter{w: pw, fn: func(n int64) {
		sent = n
		now := time.Now()
		if d := now.Sub(lastT).Seconds(); d >= 0.35 {
			if inst := int64(float64(n-lastB) / d); inst > 0 {
				speed = inst
			}
			lastT, lastB = now, n
			t.progress("发送", sent, total, speed)
		}
	}}
	mw := multipart.NewWriter(cw)
	go func() {
		var err error
		defer func() { pw.CloseWithError(err) }()
		for _, f := range files {
			var fw io.Writer
			fw, err = mw.CreateFormFile("files", filepath.ToSlash(f.rel))
			if err != nil {
				return
			}
			var src *os.File
			src, err = os.Open(f.path)
			if err != nil {
				return
			}
			_, err = io.Copy(fw, src)
			src.Close()
			if err != nil {
				return
			}
		}
		err = mw.Close()
	}()

	req, err := http.NewRequest(http.MethodPost, peer.BaseURL()+"/api/receive?offer="+offerID, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := t.srv.client.Do(req)
	t.printf("\n")
	if err != nil {
		t.srv.hist.Add(HistoryEntry{Dir: "out", Peer: peer.Name, Files: rels, Total: total, Status: StatusFailed})
		return fmt.Errorf("传输失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		t.srv.hist.Add(HistoryEntry{Dir: "out", Peer: peer.Name, Files: rels, Total: total, Status: StatusFailed})
		return fmt.Errorf("对方接收失败: %s", msg)
	}
	t.srv.hist.Add(HistoryEntry{Dir: "out", Peer: peer.Name, Files: rels, Total: total, Status: StatusDone})
	t.printf("✓ 发送完成：%s（%s）→ %s\n", rels[0], humanSize(total), peer.Name)
	return nil
}

type countWriter struct {
	w  io.Writer
	n  int64
	fn func(int64)
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if c.fn != nil {
		c.fn(c.n)
	}
	return n, err
}

// ---------- 接收 ----------

// watchIncoming 只负责打印提醒与启动进度跟踪，绝不读 stdin。
func (t *termUI) watchIncoming() {
	for {
		time.Sleep(400 * time.Millisecond)
		for _, o := range t.srv.offers.List() {
			switch {
			case o.Dir == "in" && o.Status == StatusPending && !t.noticed[o.ID]:
				t.noticed[o.ID] = true
				t.printf("\n📥 收到来自「%s」的传输请求：%s（%s）\n   输入 y 接收 / n 拒绝\n\n", o.Peer.Name, fileSummary(o.Files), humanSize(o.Total))
			case o.Dir == "in" && (o.Status == StatusAccepted || o.Status == StatusReceiving) && !t.tracked[o.ID]:
				t.tracked[o.ID] = true
				go t.trackReceive(o.ID)
			}
		}
	}
}

func (t *termUI) trackReceive(id string) {
	lastT, lastB := time.Now(), int64(0)
	for {
		o, ok := t.srv.offers.Snapshot(id)
		if !ok {
			return
		}
		if o.Status == StatusReceiving {
			now := time.Now()
			var speed int64
			if d := now.Sub(lastT).Seconds(); d >= 0.4 {
				if inst := int64(float64(o.Received-lastB) / d); inst > 0 {
					speed = inst
				}
				lastT, lastB = now, o.Received
			}
			t.progress("接收", o.Received, o.Total, speed)
		}
		switch o.Status {
		case StatusDone:
			t.printf("\n✓ 已接收「%s」发来的 %s（%s），保存在 %s\n", o.Peer.Name, fileSummary(o.Files), humanSize(o.Total), t.srv.cfg.DownloadDir)
			return
		case StatusFailed:
			t.printf("\n✗ 接收失败: %s\n", o.Error)
			return
		case StatusRejected, StatusExpired:
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// ---------- 进度条 ----------

func (t *termUI) progress(label string, sent, total int64, speed int64) {
	if total <= 0 {
		total = 1
	}
	if sent > total {
		sent = total
	}
	const width = 24
	pct := float64(sent) * 100 / float64(total)
	filled := int(pct / 100 * width)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	sp := ""
	if speed > 0 {
		sp = " " + humanSize(speed) + "/s"
	}
	t.printf("\r%s [%s] %5.1f%% %s/%s%s   ", label, bar, pct, humanSize(sent), humanSize(total), sp)
}
