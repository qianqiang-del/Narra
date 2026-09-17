package documentparser

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"time"
)

// waitDelay 是进程被取消后，留给它自行退出的时间；超时后再强杀进程树。
const waitDelay = 3 * time.Second

// commandRunner 抽象外部命令执行，便于测试替换。
type commandRunner interface {
	Run(ctx context.Context, dir string, env []string, name string, args ...string) (stdout []byte, stderr []byte, err error)
}

// osRunner 是默认实现。
//
// 两个关键点：
//   - stdout/stderr 用 bytes.Buffer 收集。非 *os.File 的 writer 会由 os/exec 起 goroutine
//     并发拷贝，所以几百页 PDF 解析出几 MB markdown 也不会把管道缓冲写满造成死锁
//     （这正是"先 Wait() 再读"会踩的坑）。
//   - Cancel 走 killProcessTree：取消时连带子进程一起结束。脚本自身没有任何超时机制，
//     OCR 卡住时如果只杀父进程，会留下一堆孤儿 Python。
type osRunner struct{}

func (osRunner) Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.SysProcAttr = sysProcAttr()
	cmd.WaitDelay = waitDelay
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return killProcessTree(cmd.Process.Pid)
	}

	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
