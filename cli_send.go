package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

// cliSend 提供 `lanshare send -to <名称|IP[:端口]> 文件...` 的命令行发送能力。
func cliSend(args []string) error {
	fl := flag.NewFlagSet("send", flag.ExitOnError)
	to := fl.String("to", "", "目标设备：设备名称 或 IP[:端口]（端口默认 8612）")
	wait := fl.Int("wait", 90, "等待对方确认的最长秒数")
	fl.Usage = func() {
		fmt.Fprintf(fl.Output(), "用法: lanshare send -to <名称|IP[:端口]> 文件/文件夹...\n\n参数:\n")
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		return err
	}
	files := fl.Args()
	if *to == "" || len(files) == 0 {
		fl.Usage()
		return errors.New("缺少 -to 或文件参数")
	}

	type sendFile struct {
		path string
		rel  string
		size int64
	}
	var list []sendFile
	var total int64
	for _, p := range files {
		st, err := os.Stat(p)
		if err != nil {
			return err
		}
		if st.IsDir() {
			root := filepath.Dir(p)
			err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				list = append(list, sendFile{path: path, rel: rel, size: info.Size()})
				total += info.Size()
				return nil
			})
			if err != nil {
				return err
			}
		} else {
			list = append(list, sendFile{path: p, rel: st.Name(), size: st.Size()})
			total += st.Size()
		}
	}
	if len(list) == 0 {
		return errors.New("没有可发送的文件")
	}

	base, peerName, err := resolveTarget(*to, 30*time.Second)
	if err != nil {
		return err
	}
	fmt.Printf("目标设备: %s (%s)\n", peerName, base)

	client := &http.Client{Transport: &http.Transport{
		DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
	}}

	offerFiles := make([]OfferFile, len(list))
	for i, f := range list {
		offerFiles[i] = OfferFile{Name: filepath.ToSlash(f.rel), Size: f.size}
	}
	payload, _ := json.Marshal(map[string]any{
		"from":  PeerSummary{ID: "cli-" + randomID(), Name: defaultName(), OS: runtime.GOOS + "/" + runtime.GOARCH},
		"files": offerFiles,
		"total": total,
	})
	var resp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := doJSON(client, "POST", base+"/api/offer", payload, &resp); err != nil {
		return fmt.Errorf("对方无响应: %v", err)
	}
	if resp.Status == StatusPending {
		fmt.Printf("已发送请求（%s，共 %s），等待对方在 LanShare 页面上确认…\n", fileSummary(offerFiles), humanSize(total))
		deadline := time.Now().Add(time.Duration(*wait) * time.Second)
		for resp.Status == StatusPending && time.Now().Before(deadline) {
			time.Sleep(time.Second)
			var off Offer
			if err := doJSON(client, "GET", base+"/api/offer/"+resp.ID, nil, &off); err == nil && off.Status != "" {
				resp.Status = off.Status
			}
		}
	}
	switch resp.Status {
	case StatusAccepted, "":
		// 对方 auto-accept 时直接返回 accepted
	case StatusRejected:
		return errors.New("对方拒绝了本次传输")
	case StatusExpired:
		return errors.New("对方长时间未确认，已超时")
	default:
		if resp.Status != StatusAccepted {
			return fmt.Errorf("对方状态: %s", resp.Status)
		}
	}

	fmt.Printf("对方已接受，开始发送 %d 个文件（%s）…\n", len(list), humanSize(total))
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		var sendErr error
		defer func() {
			if sendErr != nil {
				pw.CloseWithError(sendErr)
				return
			}
			mw.Close()
			pw.Close()
		}()
		for _, f := range list {
			fw, err := mw.CreateFormFile("files", filepath.ToSlash(f.rel))
			if err != nil {
				sendErr = err
				return
			}
			src, err := os.Open(f.path)
			if err != nil {
				sendErr = err
				return
			}
			_, err = io.Copy(fw, src)
			src.Close()
			if err != nil {
				sendErr = err
				return
			}
		}
	}()

	req, err := http.NewRequest(http.MethodPost, base+"/api/receive?offer="+resp.ID, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	httpResp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("传输失败: %v", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4<<10))
		return fmt.Errorf("对方接收失败: %s", strings.TrimSpace(string(body)))
	}
	var done struct {
		Files []string `json:"files"`
	}
	_ = json.NewDecoder(httpResp.Body).Decode(&done)
	fmt.Println("发送成功:")
	for _, f := range done.Files {
		fmt.Println("  → " + f)
	}
	return nil
}

func doJSON(client *http.Client, method, url string, payload []byte, out any) error {
	var body io.Reader
	if payload != nil {
		body = strings.NewReader(string(payload))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		msg := strings.TrimSpace(string(b))
		if msg != "" {
			return errors.New(msg)
		}
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}

// resolveTarget 把 "设备名" 或 "IP[:端口]" 解析成 http://ip:port。
func resolveTarget(target string, browseTimeout time.Duration) (string, string, error) {
	target = strings.TrimSpace(target)
	if host, port, err := net.SplitHostPort(target); err == nil && net.ParseIP(host) != nil {
		return fmt.Sprintf("http://%s:%s", host, port), host, nil
	}
	if net.ParseIP(target) != nil {
		return fmt.Sprintf("http://%s:%d", target, defaultPort), target, nil
	}

	// 优先 UDP 广播探测（快，且不依赖 mDNS）
	lower := strings.ToLower(target)
	probeWait := min(browseTimeout, 6*time.Second)
	probeCtx, probeCancel := context.WithTimeout(context.Background(), probeWait)
	peers := udpProbePeers(probeCtx, probeWait)
	probeCancel()
	for _, p := range peers {
		if strings.ToLower(p.Name) == lower || p.ID == target {
			return fmt.Sprintf("http://%s:%d", p.IP, p.Port), p.Name, nil
		}
	}

	// 回退：mDNS 浏览
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return "", "", err
	}
	entries := make(chan *zeroconf.ServiceEntry, 32)
	ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
	defer cancel()
	if err := resolver.Browse(ctx, mdnsService, mdnsDomain, entries); err != nil {
		return "", "", err
	}
	for {
		select {
		case <-ctx.Done():
			return "", "", fmt.Errorf("在局域网内没有找到名为「%s」的设备（也可以直接用 IP:端口）", target)
		case e := <-entries:
			if e == nil {
				continue
			}
			tv := map[string]string{}
			for _, t := range e.Text {
				if i := strings.IndexByte(t, '='); i > 0 {
					tv[t[:i]] = t[i+1:]
				}
			}
			name := strings.ToLower(tv["name"])
			instance := strings.ToLower(e.Instance)
			if name != lower && !strings.HasPrefix(instance, lower+"-") && tv["id"] != target {
				continue
			}
			if len(e.AddrIPv4) == 0 || e.Port <= 0 {
				continue
			}
			ip := pickIPv4(e.AddrIPv4)
			if ip == "" {
				continue
			}
			display := tv["name"]
			if display == "" {
				display = e.Instance
			}
			return fmt.Sprintf("http://%s:%d", ip, e.Port), display, nil
		}
	}
}
