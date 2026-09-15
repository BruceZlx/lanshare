package main

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	peerOnlineWindow = 25 * time.Second
)

// Peer 描述局域网中的一台设备。
type Peer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	OS       string `json:"os"`
	Manual   bool   `json:"manual"` // 通过 IP 手动添加（mDNS 被禁用的网络里可用）
	Self     bool   `json:"self"`
	Online   bool   `json:"online"`
	LastSeen int64  `json:"last_seen"`
}

func (p Peer) BaseURL() string { return fmt.Sprintf("http://%s:%d", p.IP, p.Port) }

func (p Peer) IsOnline() bool {
	return p.Self || time.Now().Unix()-p.LastSeen <= int64(peerOnlineWindow.Seconds())
}

// withOnline 计算并填充 Online 字段（JSON 输出用）。
func (p Peer) withOnline() Peer {
	p.Online = p.IsOnline()
	return p
}

// PeerStore 维护已发现的设备（含本机）。
type PeerStore struct {
	mu   sync.Mutex
	byID map[string]*Peer
	self Peer
}

func NewPeerStore(self Peer) *PeerStore {
	self.Self = true
	self.LastSeen = time.Now().Unix()
	return &PeerStore{byID: map[string]*Peer{}, self: self}
}

func (s *PeerStore) Self() Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.self
}

func (s *PeerStore) UpdateSelf(name string, port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name != "" {
		s.self.Name = name
	}
	if port > 0 {
		s.self.Port = port
	}
}

// Upsert 记录一次设备现身（mDNS 广播或手动添加探测成功）。
func (s *PeerStore) Upsert(p Peer) {
	if p.ID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.ID == s.self.ID {
		return
	}
	if old, ok := s.byID[p.ID]; ok && old.Manual {
		p.Manual = true
	}
	p.LastSeen = time.Now().Unix()
	cp := p
	s.byID[p.ID] = &cp
}

// RemoveStale 清理长时间未现身的自动发现设备；手动添加的保留。
func (s *PeerStore) RemoveStale(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := time.Now().Add(-maxAge).Unix()
	for id, p := range s.byID {
		if !p.Manual && p.LastSeen < cut {
			delete(s.byID, id)
		}
	}
}

func (s *PeerStore) Get(id string) (Peer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == s.self.ID {
		return s.self, true
	}
	p, ok := s.byID[id]
	if !ok {
		return Peer{}, false
	}
	return p.withOnline(), true
}

func (s *PeerStore) ManualPeers() []Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Peer
	for _, p := range s.byID {
		if p.Manual {
			out = append(out, *p)
		}
	}
	return out
}

// List 返回本机 + 所有设备，本机永远第一。
func (s *PeerStore) List() []Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Peer{s.self}
	for _, p := range s.byID {
		out = append(out, *p)
	}
	rest := out[1:]
	sort.Slice(rest, func(i, j int) bool {
		a, b := rest[i], rest[j]
		if a.Manual != b.Manual {
			return a.Manual
		}
		if a.IsOnline() != b.IsOnline() {
			return a.IsOnline()
		}
		return a.Name < b.Name
	})
	for i := range out {
		out[i] = out[i].withOnline()
	}
	return out
}
