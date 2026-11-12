//go:build windows

package main

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type openedFile struct {
	f      *os.File
	md5    string
	sha1   string
	sha256 string
	info   os.FileInfo
}

func openAndHash(filePath string) (*openedFile, error) {
	p, err := syscall.UTF16PtrFromString(win32Path(filePath))
	if err != nil {
		return nil, fmt.Errorf("路径编码失败：%w", err)
	}
	h, err := syscall.CreateFile(
		p,
		syscall.GENERIC_READ|syscall.FILE_WRITE_ATTRIBUTES,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("打开失败：%w", err)
	}
	f := os.NewFile(uintptr(h), filePath)
	if f == nil {
		_ = syscall.CloseHandle(h)
		return nil, fmt.Errorf("打开失败：无效句柄")
	}

	of := &openedFile{f: f}
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
		}
	}()

	// 微软推荐：打开后立刻把 atime 设为 0xFFFFFFFF，
	// 阻止后续读取（计算哈希）时更新访问时间。
	freeze := syscall.Filetime{LowDateTime: 0xFFFFFFFF, HighDateTime: 0xFFFFFFFF}
	if err := syscall.SetFileTime(syscall.Handle(f.Fd()), nil, &freeze, nil); err != nil {
		return nil, fmt.Errorf("冻结访问时间失败：%w", err)
	}

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("读取文件信息失败：%w", err)
	}

	md5Hash := md5.New()
	sha1Hash := sha1.New()
	sha256Hash := sha256.New()
	buf := make([]byte, 1<<20)
	if _, err := io.CopyBuffer(io.MultiWriter(md5Hash, sha1Hash, sha256Hash), f, buf); err != nil {
		return nil, fmt.Errorf("读取文件失败：%w", err)
	}

	of.md5 = hex.EncodeToString(md5Hash.Sum(nil))
	of.sha1 = hex.EncodeToString(sha1Hash.Sum(nil))
	of.sha256 = hex.EncodeToString(sha256Hash.Sum(nil))
	of.info = info
	ok = true
	return of, nil
}

func (o *openedFile) Close() error {
	return o.f.Close()
}

func (o *openedFile) setTimes(t time.Time) error {
	ft := syscall.NsecToFiletime(t.UnixNano())
	if err := syscall.SetFileTime(syscall.Handle(o.f.Fd()), &ft, &ft, &ft); err != nil {
		return fmt.Errorf("设置时间戳失败：%w", err)
	}
	return nil
}

func (o *openedFile) verifyTimes(t time.Time) error {
	info, err := o.f.Stat()
	if err != nil {
		return fmt.Errorf("回读时间戳失败：%w", err)
	}
	got, ok := fileTimesFromInfo(info)
	if !ok {
		return fmt.Errorf("回读时间戳失败：系统未返回 Win32 文件时间")
	}
	if !got.equal(t) {
		return fmt.Errorf("时间戳回读不一致：创建=%s 访问=%s 修改=%s，期望=%s",
			got.creation.Format(timeLayout),
			got.access.Format(timeLayout),
			got.write.Format(timeLayout),
			t.Format(timeLayout))
	}
	return nil
}

type fileTimes struct {
	creation time.Time
	access   time.Time
	write    time.Time
}

func (t fileTimes) equal(x time.Time) bool {
	return t.creation.Equal(x) && t.access.Equal(x) && t.write.Equal(x)
}

// stampMask 返回三位掩码，顺序固定为创建、修改、访问：
// 与目标时间不一致（需要写入）的位显示 C/M/L，已经一致的位显示 -。
func (t fileTimes) stampMask(target time.Time) string {
	b := [3]byte{'-', '-', '-'}
	if !t.creation.Equal(target) {
		b[0] = 'C'
	}
	if !t.write.Equal(target) {
		b[1] = 'M'
	}
	if !t.access.Equal(target) {
		b[2] = 'L'
	}
	return string(b[:])
}

func fileTimesFromInfo(info os.FileInfo) (fileTimes, bool) {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return fileTimes{}, false
	}
	return fileTimes{
		creation: filetimeToTime(data.CreationTime),
		access:   filetimeToTime(data.LastAccessTime),
		write:    filetimeToTime(data.LastWriteTime),
	}, true
}

func filetimeToTime(ft syscall.Filetime) time.Time {
	ticks := int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)
	const (
		ticksPerSecond        = int64(10_000_000)
		unixEpochOffsetSecond = int64(11644473600)
	)
	return time.Unix(ticks/ticksPerSecond-unixEpochOffsetSecond, (ticks%ticksPerSecond)*100)
}

// win32Path 给超长绝对路径加 \\?\ 前缀；syscall.CreateFile 不像 os 包那样自动处理长路径。
func win32Path(p string) string {
	if len(p) < 248 || strings.HasPrefix(p, `\\?\`) {
		return p
	}
	if strings.HasPrefix(p, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(p, `\\`)
	}
	if filepath.IsAbs(p) {
		return `\\?\` + p
	}
	return p
}
