package main

import (
	"bufio"
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

//go:embed web
var webFS embed.FS

func main() {
	log.SetFlags(log.Ltime)

	args := os.Args[1:]
	mode := "" // web | term
	if len(args) > 0 {
		switch args[0] {
		case "send":
			if err := cliSend(args[1:]); err != nil {
				log.Fatalf("发送失败: %v", err)
			}
			return
		case "web":
			mode, args = "web", args[1:]
		case "term", "cli":
			mode, args = "term", args[1:]
		}
	}

	fs := flag.NewFlagSet("lanshare", flag.ExitOnError)
	port := fs.Int("port", 0, "HTTP 监听端口（默认读配置，首次为 8612）")
	name := fs.String("name", "", "本机显示名称")
	dir := fs.String("dir", "", "接收文件的保存目录")
	noBrowser := fs.Bool("no-browser", false, "启动后不自动打开浏览器")
	showVersion := fs.Bool("version", false, "显示版本号")
	_ = fs.Parse(args)

	if *showVersion {
		fmt.Println("lanshare", version)
		return
	}
	if mode == "" {
		mode = askMode()
	}

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}
	if *port > 0 {
		cfg.Port = *port
	}
	if *name != "" {
		cfg.Name = *name
	}
	if *dir != "" {
		cfg.DownloadDir = *dir
	}
	if err := os.MkdirAll(cfg.DownloadDir, 0o755); err != nil {
		log.Printf("warn: 创建接收目录失败: %v", err)
	}

	srv := newServer(cfg, version)

	ln, realPort, err := listenOn(cfg.Port)
	if err != nil {
		log.Fatalf("监听端口失败: %v", err)
	}
	if realPort != cfg.Port {
		log.Printf("warn: 端口 %d 被占用，改用 %d", cfg.Port, realPort)
	}
	cfg.Port = realPort
	_ = cfg.Save()
	srv.peers.UpdateSelf(cfg.Name, realPort)

	if err := srv.startAnnounce(); err != nil {
		log.Printf("warn: mDNS 广播失败（仍可通过 IP 手动添加设备）: %v", err)
	}
	ctx, stopCtx := context.WithCancel(context.Background())
	defer stopCtx()
	go srv.startDiscovery(ctx)
	go srv.startUDPDiscovery(ctx)
	go srv.maintainLoop()

	url := fmt.Sprintf("http://127.0.0.1:%d", realPort)
	log.Printf("LanShare %s 已启动，设备名称「%s」", version, cfg.Name)
	if mode == "term" {
		log.Printf("接收目录: %s", cfg.DownloadDir)
	} else {
		log.Printf("请在浏览器打开: %s", localURLs(cfg))
		log.Printf("接收目录: %s", cfg.DownloadDir)
	}

	httpSrv := &http.Server{Handler: srv.handler()}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务异常: %v", err)
		}
	}()

	if mode == "term" {
		sigTerm := make(chan os.Signal, 1)
		signal.Notify(sigTerm, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigTerm
			fmt.Println("\n再见！")
			os.Exit(0)
		}()
		runTerminalMode(srv)
		fmt.Println("已退出")
		return
	}

	if !*noBrowser {
		openBrowser(url)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("正在退出…")
	ctx2, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx2)
	stopCtx()
	srv.stop()
}

// askMode 在交互式终端里询问使用 Web 还是终端模式；非终端环境默认 Web。
func askMode() string {
	st, err := os.Stdin.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return "web"
	}
	fmt.Println("LanShare · 局域网互传")
	fmt.Println()
	fmt.Println("  [1] Web 页面 —— 浏览器拖拽传输（回车默认）")
	fmt.Println("  [2] 终端模式 —— 在当前终端直接收发文件")
	fmt.Print("\n请选择 [1/2]: ")
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "2", "2.", "term", "terminal", "t":
		return "term"
	default:
		return "web"
	}
}

func listenOn(preferred int) (net.Listener, int, error) {
	if preferred <= 0 || preferred > 65535 {
		preferred = defaultPort
	}
	for p := preferred; p <= preferred+50 && p <= 65535; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
		if err == nil {
			return ln, p, nil
		}
	}
	return nil, 0, fmt.Errorf("端口 %d-%d 均被占用", preferred, preferred+50)
}

func lanIPs() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.IsPrivate() {
				out = append(out, ip.String())
			}
		}
	}
	sort.Strings(out)
	return out
}

func localURLs(cfg *Config) string {
	ips := lanIPs()
	if len(ips) == 0 {
		return fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	}
	parts := make([]string, 0, len(ips))
	for _, ip := range ips {
		parts = append(parts, fmt.Sprintf("http://%s:%d", ip, cfg.Port))
	}
	return strings.Join(parts, " 或 ")
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("无法自动打开浏览器，请手动访问 %s", url)
	}
}
