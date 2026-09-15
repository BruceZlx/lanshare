package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// 自有 UDP 发现协议：不依赖 mDNS，在任何允许子网广播的网络里都能工作。
//
//	discover（每 5s 广播）: 探测周围的 LanShare 设备（自带本机信息）
//	announce（每 15s 广播）: 让后启动的设备快速补齐列表
//	应答（单播）: 收到 discover、或发现新设备时，回以自身信息 + 已知设备列表（gossip）
//	保活（单播）: 对每个已知设备的 UDP 地址定期单播 announce，
//	             使绑定在随机端口（如同机多开）的实例也能持续感知大家。
type discoveryPacket struct {
	Cmd   string `json:"cmd"` // discover | announce
	ID    string `json:"id"`
	Name  string `json:"name"`
	OS    string `json:"os"`
	Port  int    `json:"port"` // 对方的 TCP 服务端口
	Ver   string `json:"ver"`
	Peers []Peer `json:"peers,omitempty"` // gossip：应答方已知的设备
}

func (s *Server) startUDPDiscovery(ctx context.Context) {
	// UDP 固定使用默认端口收发广播；被占（如同机多开）时退化为随机端口，
	// 此后依靠单播应答/保活与 gossip 仍可互相发现。
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: defaultPort})
	if err != nil {
		conn, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
		if err != nil {
			log.Printf("[discovery] UDP 广播不可用: %v（mDNS 与手动添加仍可用）", err)
			return
		}
		log.Printf("[discovery] UDP 端口 %d 被占用，改用随机端口（单播应答模式）", defaultPort)
	}
	defer conn.Close()

	var udpMu sync.Mutex
	peerUDP := map[string]*net.UDPAddr{} // 设备最近一次发包来源，用于单播保活

	self := func() discoveryPacket {
		return discoveryPacket{
			Cmd: "announce", ID: s.cfg.ID, Name: s.cfg.Name,
			OS: goosArch(), Port: s.cfg.Port, Ver: s.version,
		}
	}
	withPeers := func(p discoveryPacket) discoveryPacket {
		p.Peers = s.peers.List()
		return p
	}
	rememberUDP := func(id string, addr *net.UDPAddr) {
		udpMu.Lock()
		peerUDP[id] = addr
		udpMu.Unlock()
	}
	known := func(id string) bool {
		p, ok := s.peers.Get(id)
		return ok && p.IsOnline()
	}

	replyTo := func(pktID string, dst *net.UDPAddr) {
		go func() {
			// 轻微抖动，避免大局域网内的应答风暴
			time.Sleep(time.Duration(int64(pktID[0]) % 120 * int64(time.Millisecond)))
			data, _ := json.Marshal(withPeers(self()))
			_, _ = conn.WriteToUDP(data, dst)
		}()
	}

	applyPeers := func(list []Peer) {
		for _, p := range list {
			if p.ID == "" || p.ID == s.cfg.ID || p.Name == "" ||
				p.Port <= 0 || p.Port > 65535 || net.ParseIP(p.IP) == nil {
				continue
			}
			s.peers.Upsert(Peer{ID: p.ID, Name: p.Name, IP: p.IP, Port: p.Port, OS: p.OS})
		}
	}

	handle := func(data []byte, src *net.UDPAddr) {
		if !isLanAddr(src.IP) {
			return
		}
		var pkt discoveryPacket
		if err := json.Unmarshal(data, &pkt); err != nil {
			return
		}
		if pkt.ID == "" || pkt.ID == s.cfg.ID {
			return
		}
		// 端口仅在记录对端时要求；discover 探测包（如 CLI probe）可以没有端口
		canRecord := pkt.Name != "" && pkt.Port > 0 && pkt.Port <= 65535
		if canRecord {
			// 同机广播会有环回副本：已记录真实内网 IP 时，不用环回地址覆盖
			if src.IP.IsLoopback() {
				if old, ok := s.peers.Get(pkt.ID); ok && !net.ParseIP(old.IP).IsLoopback() {
					rememberUDP(pkt.ID, src)
					if pkt.Cmd == "discover" {
						replyTo(pkt.ID, src)
					}
					return
				}
			}
			rememberUDP(pkt.ID, src)
			s.peers.Upsert(Peer{ID: pkt.ID, Name: pkt.Name, IP: src.IP.String(), Port: pkt.Port, OS: pkt.OS})
		}
		isNew := !known(pkt.ID)
		applyPeers(pkt.Peers)
		if pkt.Cmd == "discover" || (isNew && pkt.Name != "") {
			replyTo(pkt.ID, src)
		}
	}
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, src, err := conn.ReadFromUDP(buf)
			if err != nil {
				return // socket closed
			}
			data := make([]byte, n)
			copy(data, buf[:n])
			handle(data, src)
		}
	}()

	broadcastAddrs := broadcastTargets(0)

	sendBroadcast := func(pkt discoveryPacket) {
		data, _ := json.Marshal(pkt)
		for _, addr := range broadcastAddrs {
			_, _ = conn.WriteToUDP(data, addr)
		}
	}

	pkt := self()
	pkt.Cmd = "discover"
	sendBroadcast(pkt) // 启动立刻探测一轮

	discT := time.NewTicker(5 * time.Second)
	annT := time.NewTicker(15 * time.Second)
	defer discT.Stop()
	defer annT.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-discT.C:
			sendBroadcast(pkt)
			// 向已知设备单播保活（照顾绑定随机端口的实例）
			data, _ := json.Marshal(self())
			udpMu.Lock()
			targets := make([]*net.UDPAddr, 0, len(peerUDP))
			for id, addr := range peerUDP {
				if p, ok := s.peers.Get(id); ok && p.IsOnline() {
					targets = append(targets, addr)
				} else {
					delete(peerUDP, id)
				}
			}
			udpMu.Unlock()
			for _, addr := range targets {
				_, _ = conn.WriteToUDP(data, addr)
			}
		case <-annT.C:
			sendBroadcast(self())
		}
	}
}

// broadcastTargets 汇总所有可用的广播目标地址。
func broadcastTargets(selfUDPPort int) []*net.UDPAddr {
	addrs := map[string]*net.UDPAddr{
		fmt.Sprintf("255.255.255.255:%d", defaultPort): {IP: net.IPv4bcast, Port: defaultPort},
	}
	if selfUDPPort > 0 && selfUDPPort != defaultPort {
		addrs["self-port"] = &net.UDPAddr{IP: net.IPv4bcast, Port: selfUDPPort}
	}
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, ifc := range ifaces {
			if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&net.FlagBroadcast == 0 {
				continue
			}
			addrs2, err := ifc.Addrs()
			if err != nil {
				continue
			}
			for _, a := range addrs2 {
				ipnet, ok := a.(*net.IPNet)
				if !ok || ipnet.IP.To4() == nil {
					continue
				}
				bc := broadcastIP(ipnet)
				addrs[bc.String()] = &net.UDPAddr{IP: bc, Port: defaultPort}
			}
		}
	}
	out := make([]*net.UDPAddr, 0, len(addrs))
	for _, v := range addrs {
		out = append(out, v)
	}
	return out
}

// udpProbePeers 主动广播探测一轮并收集应答（CLI 名称解析使用）。
func udpProbePeers(ctx context.Context, wait time.Duration) []Peer {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		return nil
	}
	defer conn.Close()

	var mu sync.Mutex
	seen := map[string]Peer{}
	observe := func(p Peer) {
		if p.ID == "" || p.Port <= 0 || p.Port > 65535 || net.ParseIP(p.IP) == nil {
			return
		}
		mu.Lock()
		seen[p.ID] = p
		mu.Unlock()
	}
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, src, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if !isLanAddr(src.IP) {
				continue
			}
			var pkt discoveryPacket
			if json.Unmarshal(buf[:n], &pkt) != nil || pkt.ID == "" || pkt.Port <= 0 || pkt.Port > 65535 {
				continue
			}
			if pkt.Name != "" {
				observe(Peer{ID: pkt.ID, Name: pkt.Name, IP: src.IP.String(), Port: pkt.Port, OS: pkt.OS})
			}
			for _, p := range pkt.Peers {
				observe(p)
			}
		}
	}()

	targets := broadcastTargets(0)
	send := func() {
		data, _ := json.Marshal(discoveryPacket{
			Cmd: "discover", ID: "probe-" + randomID(),
			OS: goosArch(),
		})
		for _, a := range targets {
			_, _ = conn.WriteToUDP(data, a)
		}
	}
	send()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	deadline := time.After(wait + 500*time.Millisecond)
	for {
		select {
		case <-ctx.Done():
			// 外层取消（超时）时返回已收集到的结果
			mu.Lock()
			out := make([]Peer, 0, len(seen))
			for _, p := range seen {
				out = append(out, p)
			}
			mu.Unlock()
			return out
		case <-deadline:
			mu.Lock()
			defer mu.Unlock()
			out := make([]Peer, 0, len(seen))
			for _, p := range seen {
				out = append(out, p)
			}
			return out
		case <-t.C:
			send()
		}
	}
}

// broadcastIP 由 ip/mask 计算子网定向广播地址（如 192.168.0.255）。
func broadcastIP(ipnet *net.IPNet) net.IP {
	ip := ipnet.IP.To4()
	mask := ipnet.Mask
	bc := make(net.IP, 4)
	for i := range ip {
		bc[i] = ip[i] | ^mask[i]
	}
	return bc
}
