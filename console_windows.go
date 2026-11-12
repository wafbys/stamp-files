//go:build windows

package main

import (
	"os"
	"syscall"
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleOutputCP = kernel32.NewProc("GetConsoleOutputCP")
	procSetConsoleOutputCP = kernel32.NewProc("SetConsoleOutputCP")
)

// enableUTF8Console 让 Go 写出的 UTF-8 字节在 Windows 控制台按 UTF-8 解释；
// 仅在 stdout 是控制台时生效，退出时恢复原代码页。
func enableUTF8Console() func() {
	info, err := os.Stdout.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return func() {}
	}
	old, _, _ := procGetConsoleOutputCP.Call()
	if old == 0 || old == 65001 {
		return func() {}
	}
	procSetConsoleOutputCP.Call(65001)
	return func() {
		procSetConsoleOutputCP.Call(old)
	}
}
