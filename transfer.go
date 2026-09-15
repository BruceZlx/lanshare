package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---------- 接收端 ----------

// handleOffer 处理对方发来的“传输请求”（只有文件元数据，文件随后才推过来）。
func (s *Server) handleOffer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From  PeerSummary `json:"from"`
		Files []OfferFile `json:"files"`
		Total int64       `json:"total"`
	}
	if !readJSON(w, r, &req, 8<<20) {
		return
	}
	if req.From.ID == "" || req.From.Name == "" || len(req.Files) == 0 || req.Total < 0 {
		writeErr(w, http.StatusBadRequest, "无效的传输请求")
		return
	}
	status := StatusPending
	if s.cfg.AutoAccept {
		status = StatusAccepted
	}
	off := s.offers.Create("in", req.From, req.Files, req.Total, status)
	if status == StatusPending {
		go notifyUser("LanShare 收到传输请求",
			fmt.Sprintf("%s 想发送 %s（%s）", req.From.Name, fileSummary(req.Files), humanSize(req.Total)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": off.ID, "status": off.Status})
}

func (s *Server) handleOffers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.offers.List())
}

func (s *Server) handleOfferStatus(w http.ResponseWriter, r *http.Request) {
	off, ok := s.offers.Snapshot(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "传输不存在")
		return
	}
	writeJSON(w, http.StatusOK, off)
}

func (s *Server) handleOfferAccept(w http.ResponseWriter, r *http.Request) {
	s.decideOffer(w, r, StatusAccepted)
}

func (s *Server) handleOfferReject(w http.ResponseWriter, r *http.Request) {
	s.decideOffer(w, r, StatusRejected)
}

func (s *Server) decideOffer(w http.ResponseWriter, r *http.Request, target string) {
	id := r.PathValue("id")
	off, ok := s.offers.Snapshot(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "传输不存在")
		return
	}
	if off.Status != StatusPending {
		writeErr(w, http.StatusConflict, "该请求当前状态为 "+off.Status)
		return
	}
	s.offers.SetStatus(id, target, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": target})
}

// handleReceive 对方把文件（multipart 流）推过来，边收边落盘并汇报进度。
func (s *Server) handleReceive(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("offer")
	off, ok := s.offers.Snapshot(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "传输不存在或已过期")
		return
	}
	if off.Status != StatusAccepted {
		writeErr(w, http.StatusConflict, "对方尚未接受该传输（当前: "+off.Status+"）")
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		s.offers.SetStatus(id, StatusFailed, err.Error())
		writeErr(w, http.StatusBadRequest, "需要 multipart/form-data: "+err.Error())
		return
	}
	s.offers.SetStatus(id, StatusReceiving, "")

	var saved []string
	buf := make([]byte, 512*1024)
	fail := func(code int, msg string) {
		s.offers.SetStatus(id, StatusFailed, msg)
		s.hist.Add(HistoryEntry{Dir: "in", Peer: off.Peer.Name, Files: names(off.Files), Total: off.Received, Status: StatusFailed})
		writeErr(w, code, msg)
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			fail(http.StatusBadRequest, "读取数据失败: "+err.Error())
			return
		}
		name := partFilename(part)
		if name == "" {
			io.Copy(io.Discard, part)
			part.Close()
			continue
		}
		rel, err := sanitizeRelPath(name)
		if err != nil {
			part.Close()
			fail(http.StatusBadRequest, err.Error())
			return
		}
		dest := uniquePath(filepath.Join(s.cfg.DownloadDir, rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			part.Close()
			fail(http.StatusInternalServerError, "创建目录失败: "+err.Error())
			return
		}
		f, err := os.Create(dest)
		if err != nil {
			part.Close()
			fail(http.StatusInternalServerError, "写入文件失败: "+err.Error())
			return
		}
		_, copyErr := io.CopyBuffer(f, &countingReader{r: part, id: id, store: s.offers}, buf)
		closeErr := f.Close()
		part.Close()
		if copyErr != nil || closeErr != nil {
			os.Remove(dest)
			msg := "接收中断"
			if copyErr != nil {
				msg = "接收中断: " + copyErr.Error()
			}
			fail(http.StatusBadGateway, msg)
			return
		}
		saved = append(saved, dest)
	}
	if len(saved) == 0 {
		fail(http.StatusBadRequest, "未收到任何文件")
		return
	}
	s.offers.SetStatus(id, StatusDone, "")
	s.hist.Add(HistoryEntry{Dir: "in", Peer: off.Peer.Name, Files: saved, Total: off.Total, Status: StatusDone})
	go notifyUser("LanShare 已接收",
		fmt.Sprintf("来自 %s 的 %s 已保存", off.Peer.Name, fileSummary(off.Files)))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": saved})
}

type countingReader struct {
	r     io.Reader
	id    string
	store *OfferStore
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.store.AddReceived(c.id, int64(n))
	}
	return n, err
}

// partFilename 取出浏览器/CLI 写入的完整文件名（含相对路径）。
// 注意不能用 Part.FileName()，Go 会强制 filepath.Base 丢掉目录结构。
func partFilename(part *multipart.Part) string {
	cd := part.Header.Get("Content-Disposition")
	if cd == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(cd)
	if err != nil {
		return ""
	}
	return params["filename"]
}

func names(files []OfferFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Name
	}
	return out
}

// ---------- 发送端 ----------

// handleSendPrepare 浏览器先提交文件清单，本机向对方发起询问并等待确认。
func (s *Server) handleSendPrepare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target string      `json:"target"`
		Files  []OfferFile `json:"files"`
	}
	if !readJSON(w, r, &req, 8<<20) {
		return
	}
	peer, ok := s.peers.Get(req.Target)
	if !ok {
		writeErr(w, http.StatusNotFound, "目标设备不在线或不存在")
		return
	}
	if peer.Self {
		writeErr(w, http.StatusBadRequest, "不能发送给本机")
		return
	}
	var total int64
	for _, f := range req.Files {
		total += f.Size
	}
	payload, _ := json.Marshal(map[string]any{
		"from":  PeerSummary{ID: s.cfg.ID, Name: s.cfg.Name, OS: goosArch()},
		"files": req.Files,
		"total": total,
	})
	var resp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := s.postJSON(peer.BaseURL()+"/api/offer", payload, &resp, 15*time.Second); err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("无法连接 %s（%s:%d）: %v", peer.Name, peer.IP, peer.Port, err))
		return
	}
	if resp.ID == "" {
		writeErr(w, http.StatusBadGateway, "对方响应异常")
		return
	}

	// 对方开了“询问”模式：轮询直到对方点接受/拒绝或超时。
	status := resp.Status
	if status == StatusPending {
		status = s.waitOfferDecision(peer, resp.ID, 130*time.Second)
	}
	if status == StatusAccepted {
		s.offers.CreateWithID(resp.ID, "out", peerSummaryOf(peer), req.Files, total, StatusSending)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"offer": resp.ID, "status": status, "peer": peer.Name,
	})
}

func (s *Server) waitOfferDecision(peer Peer, id string, maxWait time.Duration) string {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		time.Sleep(1200 * time.Millisecond)
		var off Offer
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		err := s.getJSON(ctx, peer.BaseURL()+"/api/offer/"+id, &off)
		cancel()
		if err != nil {
			continue // 网络抖动，继续等
		}
		switch off.Status {
		case StatusPending:
		case "":
		default:
			return off.Status
		}
	}
	return StatusExpired
}

// handleSendTransfer 对方已接受，把浏览器上传的 multipart 流直接转发给对方。
func (s *Server) handleSendTransfer(w http.ResponseWriter, r *http.Request) {
	offerID := r.URL.Query().Get("offer")
	peerID := r.URL.Query().Get("target")
	off, ok := s.offers.Snapshot(offerID)
	if !ok || off.Dir != "out" {
		writeErr(w, http.StatusNotFound, "传输会话不存在，请重新发起")
		return
	}
	if off.Status != StatusSending {
		writeErr(w, http.StatusConflict, "该传输当前状态为 "+off.Status)
		return
	}
	peer, ok := s.peers.Get(peerID)
	if !ok {
		s.offers.SetStatus(offerID, StatusFailed, "目标设备已下线")
		writeErr(w, http.StatusBadGateway, "目标设备已下线")
		return
	}

	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		peer.BaseURL()+"/api/receive?offer="+offerID, r.Body)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.ContentLength = r.ContentLength
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))

	resp, err := s.client.Do(req)
	if err != nil {
		msg := "连接对方失败: " + err.Error()
		if ctx.Err() != nil {
			msg = "发送已取消"
		}
		s.offers.SetStatus(offerID, StatusFailed, msg)
		s.hist.Add(HistoryEntry{Dir: "out", Peer: peer.Name, Files: names(off.Files), Total: off.Total, Status: StatusFailed})
		writeErr(w, http.StatusBadGateway, msg)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		msg := string(body)
		if msg == "" {
			msg = "对方返回 " + resp.Status
		}
		s.offers.SetStatus(offerID, StatusFailed, msg)
		s.hist.Add(HistoryEntry{Dir: "out", Peer: peer.Name, Files: names(off.Files), Total: off.Total, Status: StatusFailed})
		writeErr(w, http.StatusBadGateway, msg)
		return
	}
	io.Copy(w, resp.Body)
	s.offers.SetStatus(offerID, StatusDone, "")
	s.hist.Add(HistoryEntry{Dir: "out", Peer: peer.Name, Files: names(off.Files), Total: off.Total, Status: StatusDone})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.hist.List())
}

// ---------- 设备间 HTTP 小工具 ----------

func (s *Server) postJSON(url string, payload []byte, out any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		if msg := strings.TrimSpace(string(body)); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return fmt.Errorf("对方返回 %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}

func (s *Server) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("对方返回 %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

func peerSummaryOf(p Peer) PeerSummary {
	return PeerSummary{ID: p.ID, Name: p.Name, OS: p.OS}
}
