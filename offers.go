package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 传输单（offer）状态机:
// pending -> accepted -> receiving -> done
//
//	      |-> rejected / expired
//		 receiving|sending -> failed
const (
	StatusPending   = "pending"
	StatusAccepted  = "accepted"
	StatusReceiving = "receiving"
	StatusSending   = "sending"
	StatusDone      = "done"
	StatusRejected  = "rejected"
	StatusFailed    = "failed"
	StatusExpired   = "expired"
)

type PeerSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	OS   string `json:"os"`
}

type OfferFile struct {
	Name string `json:"name"` // 相对路径（支持文件夹结构）
	Size int64  `json:"size"`
}

type Offer struct {
	ID       string      `json:"id"`
	Dir      string      `json:"dir"` // in: 接收 / out: 发送
	Peer     PeerSummary `json:"peer"`
	Files    []OfferFile `json:"files"`
	Total    int64       `json:"total"`
	Received int64       `json:"received"`
	Status   string      `json:"status"`
	Error    string      `json:"error,omitempty"`
	Created  int64       `json:"created"`
	Updated  int64       `json:"updated"`
}

type OfferStore struct {
	mu   sync.Mutex
	byID map[string]*Offer
}

func NewOfferStore() *OfferStore {
	return &OfferStore{byID: map[string]*Offer{}}
}

func (s *OfferStore) CreateWithID(id, dir string, peer PeerSummary, files []OfferFile, total int64, status string) *Offer {
	o := &Offer{
		ID: id, Dir: dir, Peer: peer, Files: files, Total: total,
		Status: status, Created: time.Now().Unix(), Updated: time.Now().Unix(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[o.ID] = o
	return o
}

func (s *OfferStore) Create(dir string, peer PeerSummary, files []OfferFile, total int64, status string) *Offer {
	return s.CreateWithID(randomID(), dir, peer, files, total, status)
}

func (s *OfferStore) Snapshot(id string) (Offer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.byID[id]
	if !ok {
		return Offer{}, false
	}
	return *o, true
}

func (s *OfferStore) SetStatus(id, status, errMsg string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.byID[id]
	if !ok {
		return false
	}
	o.Status = status
	o.Error = errMsg
	o.Updated = time.Now().Unix()
	return true
}

func (s *OfferStore) AddReceived(id string, n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o, ok := s.byID[id]; ok {
		o.Received += n
		o.Updated = time.Now().Unix()
	}
}

// ExpirePending 将超过 maxAge 仍无人处理的请求标记为过期。
func (s *OfferStore) ExpirePending(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := time.Now().Add(-maxAge).Unix()
	for _, o := range s.byID {
		if o.Status == StatusPending && o.Created < cut {
			o.Status = StatusExpired
			o.Updated = time.Now().Unix()
		}
	}
}

// PruneFinished 清理早已结束的传输单。
func (s *OfferStore) PruneFinished(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := time.Now().Add(-maxAge).Unix()
	for id, o := range s.byID {
		switch o.Status {
		case StatusPending, StatusAccepted, StatusReceiving, StatusSending:
			continue
		}
		if o.Updated < cut {
			delete(s.byID, id)
		}
	}
}

func (s *OfferStore) List() []Offer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Offer, 0, len(s.byID))
	for _, o := range s.byID {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created != out[j].Created {
			return out[i].Created > out[j].Created
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ---------- 传输历史 ----------

type HistoryEntry struct {
	Dir    string   `json:"dir"`
	Peer   string   `json:"peer"`
	Files  []string `json:"files"`
	Total  int64    `json:"total"`
	Status string   `json:"status"`
	Time   int64    `json:"time"`
}

type History struct {
	mu      sync.Mutex
	entries []HistoryEntry
}

func NewHistory() *History { return &History{} }

func (h *History) Add(e HistoryEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e.Time = time.Now().Unix()
	h.entries = append([]HistoryEntry{e}, h.entries...)
	if len(h.entries) > 200 {
		h.entries = h.entries[:200]
	}
}

func (h *History) List() []HistoryEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]HistoryEntry, len(h.entries))
	copy(out, h.entries)
	return out
}

// ---------- 文件名安全处理 ----------

// sanitizeRelPath 清洗对方声称的相对路径，杜绝目录穿越与非法字符。
func sanitizeRelPath(p string) (string, error) {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.ReplaceAll(p, "\x00", "")
	var out []string
	for _, seg := range strings.Split(p, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." {
			return "", errors.New("路径不安全")
		}
		seg = strings.Map(func(r rune) rune {
			switch {
			case r == ':' || r == '"' || r == '<' || r == '>' || r == '|' || r == '?' || r == '*' || r < 32:
				return '_'
			}
			return r
		}, seg)
		seg = strings.TrimRight(seg, ". ")
		if seg == "" {
			seg = "_"
		}
		if windowsReserved(seg) {
			seg = "_" + seg
		}
		out = append(out, seg)
	}
	if len(out) == 0 {
		return "", errors.New("文件名为空")
	}
	return filepath.Join(out...), nil
}

func windowsReserved(name string) bool {
	base := strings.ToUpper(name)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
}

// uniquePath 遇到重名文件时生成 "name (1).ext"。
func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	dir, file := filepath.Split(path)
	ext := filepath.Ext(file)
	base := strings.TrimSuffix(file, ext)
	for i := 1; ; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}

func fileSummary(files []OfferFile) string {
	if len(files) == 1 {
		return filepath.Base(files[0].Name)
	}
	return fmt.Sprintf("%d 个文件", len(files))
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
