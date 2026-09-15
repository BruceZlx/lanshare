package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/grandcat/zeroconf"
)

const (
	mdnsService = "_lanshare._tcp"
	mdnsDomain  = "local."
)

// startAnnounce 通过 mDNS 对局域网广播本机服务。
func (s *Server) startAnnounce() error {
	txt := []string{
		"id=" + s.cfg.ID,
		"name=" + s.cfg.Name,
		"os=" + goosArch(),
		"ver=" + s.version,
	}
	instance := fmt.Sprintf("%s-%s", s.cfg.Name, shortID(s.cfg.ID))
	server, err := zeroconf.Register(instance, mdnsService, mdnsDomain, s.cfg.Port, txt, nil)
	if err != nil {
		return err
	}
	s.mdnsMu.Lock()
	if s.mdns != nil {
		s.mdns.Shutdown()
	}
	s.mdns = server
	s.mdnsMu.Unlock()
	return nil
}

func (s *Server) stopAnnounce() {
	s.mdnsMu.Lock()
	defer s.mdnsMu.Unlock()
	if s.mdns != nil {
		s.mdns.Shutdown()
		s.mdns = nil
	}
}

// startDiscovery 持续监听局域网内其它 LanShare 设备的广播。
func (s *Server) startDiscovery(ctx context.Context) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		log.Printf("[discovery] 创建 resolver 失败: %v", err)
		return
	}
	entries := make(chan *zeroconf.ServiceEntry, 64)
	if err := resolver.Browse(ctx, mdnsService, mdnsDomain, entries); err != nil {
		log.Printf("[discovery] mDNS 浏览失败（可改用页面里的 IP 手动添加）: %v", err)
		return
	}
	for entry := range entries {
		s.handleEntry(entry)
	}
}

func (s *Server) handleEntry(e *zeroconf.ServiceEntry) {
	if e == nil {
		return
	}
	tv := map[string]string{}
	for _, t := range e.Text {
		if i := strings.IndexByte(t, '='); i > 0 {
			tv[t[:i]] = t[i+1:]
		}
	}
	id := tv["id"]
	if id == "" || id == s.cfg.ID {
		return
	}
	ip := pickIPv4(e.AddrIPv4)
	if ip == "" {
		return
	}
	name := tv["name"]
	if name == "" {
		name = strings.TrimSuffix(e.Instance, "-"+shortID(id))
	}
	port := e.Port
	if port <= 0 || port > 65535 {
		port = defaultPort
	}
	s.peers.Upsert(Peer{ID: id, Name: name, IP: ip, Port: port, OS: tv["os"]})
}

// pickIPv4 优先选择局域网私网地址。
func pickIPv4(addrs []net.IP) string {
	var fallback string
	for _, a := range addrs {
		s := a.String()
		switch {
		case a.IsPrivate():
			return s
		case a.IsLoopback():
			if fallback == "" {
				fallback = s
			}
		}
	}
	if fallback != "" {
		return fallback
	}
	if len(addrs) > 0 {
		return addrs[0].String()
	}
	return ""
}
