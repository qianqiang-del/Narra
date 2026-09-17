//go:build windows

package documentparser

import (
	"os/exec"
	"strconv"
	"syscall"
)

// sysProcAttr 在 Windows 上隐藏子进程窗口，避免解析时闪出黑框。
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}

// killProcessTree 用 taskkill /T 连子进程一起结束。
//
// 只杀父进程的话，docling / RapidOCR 派生的工作进程会留下来继续吃 CPU 和内存。
func killProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = sysProcAttr()
	return cmd.Run()
}
