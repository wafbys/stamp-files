package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "500 毫秒"},
		{1500 * time.Millisecond, "1.5 秒"},
		{90 * time.Second, "1 分 30.0 秒"},
		{2*time.Hour + 5*time.Minute, "2 小时 5 分"},
	}
	for _, c := range cases {
		if got := formatDuration(c.d); got != c.want {
			t.Errorf("formatDuration(%s) = %q，期望 %q", c.d, got, c.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{128 << 20, "128.0 MB"},
		{3 << 30, "3.0 GB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.n); got != c.want {
			t.Errorf("formatBytes(%d) = %q，期望 %q", c.n, got, c.want)
		}
	}
}

func TestProgressPrinter(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressPrinter(&buf, true)
	p.minSize = 0
	p.interval = 0

	if !p.begin("激情信箱.mkv", 1000) {
		t.Fatal("应显示进度")
	}
	p.add(250)
	p.add(250)
	p.end()

	out := buf.String()
	for _, want := range []string{"[读取]", "激情信箱.mkv", "50%", "已用"} {
		if !strings.Contains(out, want) {
			t.Errorf("进度输出缺少 %q：%q", want, out)
		}
	}
	if !strings.HasSuffix(out, "\r\x1b[K") {
		t.Errorf("结束时应清除进度行：%q", out)
	}
	if p.shown {
		t.Error("end 后不应再处于显示状态")
	}
}

func TestProgressPrinterSkipped(t *testing.T) {
	var buf bytes.Buffer
	disabled := newProgressPrinter(&buf, false)
	if disabled.begin("big.mkv", 1<<30) {
		t.Error("非终端不应显示进度")
	}
	small := newProgressPrinter(&buf, true)
	if small.begin("small.bin", 1<<20) {
		t.Error("小文件不应显示进度")
	}
	if buf.Len() != 0 {
		t.Errorf("跳过时不应写输出：%q", buf.String())
	}
}
