package main

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strings"
)

// notifyUser 尽力弹出系统通知，失败时静默（不打断传输）。
func notifyUser(title, msg string) {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display notification %q with title %q sound name "Glass"`, msg, title)
		if err := exec.Command("osascript", "-e", script).Run(); err != nil {
			log.Printf("[notify] %s: %s", title, msg)
		}
	case "windows":
		// 用 .NET NotifyIcon 的气泡通知，无需额外依赖；失败则忽略。
		ps := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$n = New-Object System.Windows.Forms.NotifyIcon
$n.Icon = [System.Drawing.SystemIcons]::Information
$n.Visible = $true
$n.ShowBalloonTip(6000, %q, %q, [System.Windows.Forms.ToolTipIcon]::Info)
Start-Sleep -Seconds 7
$n.Dispose()
`, title, msg)
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", strings.TrimSpace(ps))
		if err := cmd.Start(); err != nil {
			log.Printf("[notify] %s: %s", title, msg)
		} else {
			go func() { _ = cmd.Wait() }()
		}
	default:
		log.Printf("[notify] %s: %s", title, msg)
	}
}
