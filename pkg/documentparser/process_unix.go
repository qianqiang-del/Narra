//go:build !windows

package documentparser

import "syscall"

// sysProcAttr 让子进程单独成组，这样取消时可以整组一起杀。
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree 先按进程组杀（负号即进程组），拿不到再退化为杀单个进程。
func killProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}
