package main

import (
	"fmt"
	"io"
	"os"
	"time"
)

const (
	progressMinSize  = 128 << 20
	progressInterval = 100 * time.Millisecond
)

// progressPrinter 在终端上显示单行读取进度；非终端或小文件自动跳过。
type progressPrinter struct {
	out      io.Writer
	enabled  bool
	minSize  int64
	interval time.Duration
	name     string
	total    int64
	done     int64
	started  time.Time
	lastAt   time.Time
	shown    bool
}

func newProgressPrinter(out io.Writer, enabled bool) *progressPrinter {
	return &progressPrinter{
		out:      out,
		enabled:  enabled,
		minSize:  progressMinSize,
		interval: progressInterval,
	}
}

// begin 决定是否显示进度；返回 true 时调用方需要在结束时调用 end。
func (p *progressPrinter) begin(name string, total int64) bool {
	if p == nil || !p.enabled || total < p.minSize {
		return false
	}
	p.name = shortenName(name, 24)
	p.total = total
	p.done = 0
	p.started = time.Now()
	p.lastAt = time.Time{}
	p.shown = true
	p.render()
	return true
}

func (p *progressPrinter) add(n int64) {
	if p == nil || !p.shown || n <= 0 {
		return
	}
	p.done += n
	if p.done >= p.total || p.interval <= 0 || time.Since(p.lastAt) >= p.interval {
		p.render()
	}
}

func (p *progressPrinter) end() {
	if p == nil || !p.shown {
		return
	}
	fmt.Fprint(p.out, "\r\x1b[K")
	p.shown = false
	p.name = ""
}

type progressReader struct {
	r io.Reader
	p *progressPrinter
}

func (r *progressReader) Read(b []byte) (int, error) {
	n, err := r.r.Read(b)
	r.p.add(int64(n))
	return n, err
}

func (p *progressPrinter) render() {
	if p.total <= 0 {
		return
	}
	pct := p.done * 100 / p.total
	if pct > 100 {
		pct = 100
	}
	fmt.Fprintf(p.out, "\r[读取] %s  %s / %s  %d%%  已用 %.1f 秒\x1b[K",
		p.name, formatBytes(p.done), formatBytes(p.total), pct, time.Since(p.started).Seconds())
	p.lastAt = time.Now()
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 3 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%.0f 毫秒", float64(d)/float64(time.Millisecond))
	case d < time.Minute:
		return fmt.Sprintf("%.1f 秒", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%d 分 %.1f 秒", int(d/time.Minute), (d % time.Minute).Seconds())
	default:
		return fmt.Sprintf("%d 小时 %d 分", int(d/time.Hour), int((d%time.Hour)/time.Minute))
	}
}

// shortenName 按 rune 截断过长的文件名，避免进度行折行。
func shortenName(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// isTerminal 判断输出是否连到字符设备；重定向到文件或管道时为 false。
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
