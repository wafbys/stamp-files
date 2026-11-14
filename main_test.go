package main

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defaultDB := filepath.Join(t.TempDir(), dbFileName)

	cases := []struct {
		name        string
		args        []string
		wantErr     bool
		wantDir     string
		wantPattern string
		wantTime    string
		wantDB      string
		wantDry     bool
		wantAdd     bool
		wantHashAll bool
		wantHide    bool
		wantNoColor bool
		wantNoProg  bool
		wantHelp    bool
		wantVersion bool
	}{
		{name: "no args", wantDir: cwd, wantTime: defaultTimeString, wantDB: defaultDB},
		{name: "dir only", args: []string{`D:\Archive`}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB},
		{name: "pattern only", args: []string{"*.flac"}, wantDir: cwd, wantPattern: "*.flac", wantTime: defaultTimeString, wantDB: defaultDB},
		{name: "pattern with dir", args: []string{`D:\Archive\*.jpg`}, wantDir: `D:\Archive`, wantPattern: "*.jpg", wantTime: defaultTimeString, wantDB: defaultDB},
		{name: "pattern drive root", args: []string{`D:\*.jpg`}, wantDir: `D:\`, wantPattern: "*.jpg", wantTime: defaultTimeString, wantDB: defaultDB},
		{name: "pattern question", args: []string{"a?.txt"}, wantDir: cwd, wantPattern: "a?.txt", wantTime: defaultTimeString, wantDB: defaultDB},
		{name: "pattern with add", args: []string{"--add", `D:\Archive\*.jpg`}, wantDir: `D:\Archive`, wantPattern: "*.jpg", wantTime: defaultTimeString, wantDB: defaultDB, wantAdd: true},
		{name: "brackets literal", args: []string{`D:\Archive\[abc].txt`}, wantDir: `D:\Archive\[abc].txt`, wantTime: defaultTimeString, wantDB: defaultDB},
		{name: "dir then dry-run", args: []string{`D:\Archive`, "-n"}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB, wantDry: true},
		{name: "dry-run then dir", args: []string{"-n", `D:\Archive`}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB, wantDry: true},
		{name: "time flag", args: []string{"-t", "2027-01-02 03:04:05", `D:\Archive`}, wantDir: `D:\Archive`, wantTime: "2027-01-02 03:04:05", wantDB: defaultDB},
		{name: "db flag", args: []string{"--db", "x.txt", `D:\Archive`}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: mustAbs(t, "x.txt")},
		{name: "add flag", args: []string{"--add", `D:\Archive`}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB, wantAdd: true},
		{name: "add dry-run", args: []string{"--add", "-n", `D:\Archive`}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB, wantAdd: true, wantDry: true},
		{name: "all bool flags", args: []string{"--hide-missing", "--no-color", "-a", `D:\Archive`}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB, wantHashAll: true, wantHide: true, wantNoColor: true},
		{name: "no progress", args: []string{"--no-progress", "-n", `D:\Archive`}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB, wantDry: true, wantNoProg: true},
		{name: "flags around dir", args: []string{"-a", `D:\Archive`, "-n"}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB, wantDry: true, wantHashAll: true},
		{name: "dash dash", args: []string{"--", "-dir"}, wantDir: filepath.Join(cwd, "-dir"), wantTime: defaultTimeString, wantDB: defaultDB},
		{name: "help", args: []string{"-h"}, wantHelp: true},
		{name: "help after dir", args: []string{`D:\Archive`, "--help"}, wantHelp: true},
		{name: "version", args: []string{"-V"}, wantVersion: true},
		{name: "extra positional", args: []string{`D:\A`, `D:\B`}, wantErr: true},
		{name: "wildcard in dir part", args: []string{`D:\2024*\*.jpg`}, wantErr: true},
		{name: "pattern missing name", args: []string{`D:\Archive\*.jpg\`}, wantErr: true},
		{name: "drive relative pattern", args: []string{`C:*.jpg`}, wantErr: true},
		{name: "unknown short upper T", args: []string{"-T", `D:\Archive`}, wantErr: true},
		{name: "unknown long Time", args: []string{"--Time", "2027-01-02 03:04:05", `D:\Archive`}, wantErr: true},
		{name: "unknown lowercase v", args: []string{"-v"}, wantErr: true},
		{name: "unknown -p", args: []string{"-p", `D:\Archive`}, wantErr: true},
		{name: "unknown -d", args: []string{"-d", "x.txt", `D:\Archive`}, wantErr: true},
		{name: "empty dir", args: []string{""}, wantErr: true},
		{name: "empty time", args: []string{"--time", ""}, wantErr: true},
		{name: "empty db", args: []string{"--db", ""}, wantErr: true},
		{name: "bad time", args: []string{"--time", "2026/11/11", `D:\Archive`}, wantErr: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts, err := parseArgs(c.args, defaultDB)
			if c.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际成功：%+v", opts)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseArgs 失败：%v", err)
			}
			if c.wantHelp {
				if !opts.help {
					t.Fatal("期望 help")
				}
				return
			}
			if c.wantVersion {
				if !opts.version {
					t.Fatal("期望 version")
				}
				return
			}
			if opts.dir != filepath.Clean(c.wantDir) {
				t.Errorf("dir = %q，期望 %q", opts.dir, filepath.Clean(c.wantDir))
			}
			if opts.pattern != c.wantPattern {
				t.Errorf("pattern = %q，期望 %q", opts.pattern, c.wantPattern)
			}
			if got := opts.stamp.Format(timeLayout); got != c.wantTime {
				t.Errorf("time = %q，期望 %q", got, c.wantTime)
			}
			if opts.dbPath != filepath.Clean(c.wantDB) {
				t.Errorf("db = %q，期望 %q", opts.dbPath, filepath.Clean(c.wantDB))
			}
			if opts.dryRun != c.wantDry {
				t.Errorf("dryRun = %v，期望 %v", opts.dryRun, c.wantDry)
			}
			if opts.add != c.wantAdd {
				t.Errorf("add = %v，期望 %v", opts.add, c.wantAdd)
			}
			if opts.hashAll != c.wantHashAll {
				t.Errorf("hashAll = %v，期望 %v", opts.hashAll, c.wantHashAll)
			}
			if opts.hideMissing != c.wantHide {
				t.Errorf("hideMissing = %v，期望 %v", opts.hideMissing, c.wantHide)
			}
			if opts.noColor != c.wantNoColor {
				t.Errorf("noColor = %v，期望 %v", opts.noColor, c.wantNoColor)
			}
			if opts.noProgress != c.wantNoProg {
				t.Errorf("noProgress = %v，期望 %v", opts.noProgress, c.wantNoProg)
			}
		})
	}
}

func TestLoadDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, dbFileName)
	sha1v := strings.Repeat("B", 40)
	sha256v := strings.Repeat("C", 64)

	content := "\uFEFF# 注释\r\n" +
		"2026-11-14 22:08:23|" + `软件\VMware.exe|` + sha1v + "|" + sha256v + "\r\n" +
		"2026-11-14 22:09:00|only-sha1.exe|" + strings.Repeat("E", 40) + "|\r\n" +
		"only-sha256.exe||" + strings.Repeat("F", 64) + "\r\n" +
		"legacy-md5.exe|" + strings.Repeat("A", 32) + "|" + sha1v + "|" + sha256v + "\r\n" +
		"2026-11-14 22:09:30|bad-sha1.exe|XYZ|\r\n" +
		"2026-11-14 22:09:40|bad-sha256.exe||XYZ\r\n" +
		"empty.exe||\r\n" +
		"too|few\r\n" +
		"2026-11-14 22:10:00|" + `软件/VMware.exe|` + strings.Repeat("1", 40) + "|" + strings.Repeat("2", 64) + "\r\n"

	if err := os.WriteFile(dbPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var warn bytes.Buffer
	db, err := loadDatabase(dbPath, &warn)
	if err != nil {
		t.Fatalf("loadDatabase 失败：%v", err)
	}
	if db.count != 5 {
		t.Errorf("count = %d，期望 5", db.count)
	}
	if db.warnings != 4 {
		t.Errorf("warnings = %d，期望 4", db.warnings)
	}
	if len(db.byName["vmware.exe"]) != 2 {
		t.Errorf("文件名记录数 = %d，期望 2", len(db.byName["vmware.exe"]))
	}
	if db.byName["vmware.exe"][0].line != 2 || db.byName["vmware.exe"][1].line != 10 {
		t.Errorf("行号不对：%d, %d", db.byName["vmware.exe"][0].line, db.byName["vmware.exe"][1].line)
	}
	if got := db.byName["vmware.exe"][0].added.Format(timeLayout); got != "2026-11-14 22:08:23" {
		t.Errorf("第一条添加时间 = %q", got)
	}
	if got := db.byName["vmware.exe"][1].added.Format(timeLayout); got != "2026-11-14 22:10:00" {
		t.Errorf("第二条添加时间 = %q", got)
	}
	if db.byName["only-sha1.exe"][0].sha256 != "" {
		t.Error("only-sha1 的 SHA256 应为空")
	}
	if !db.byName["only-sha256.exe"][0].added.IsZero() {
		t.Error("旧三字段格式的添加时间应为零值")
	}
	if db.byName["only-sha256.exe"][0].sha1 != "" {
		t.Error("only-sha256 的 SHA1 应为空")
	}
	legacy := db.byName["legacy-md5.exe"]
	if len(legacy) != 1 || legacy[0].sha1 != sha1v || legacy[0].sha256 != sha256v {
		t.Error("旧四字段格式应读取 SHA1/SHA256 并忽略 MD5 列")
	}
	if !legacy[0].added.IsZero() {
		t.Error("旧四字段格式的添加时间应为零值")
	}
	for _, line := range []string{"第 6 行", "第 7 行", "第 8 行", "第 9 行"} {
		if !strings.Contains(warn.String(), line) {
			t.Errorf("警告缺少 %s：%s", line, warn.String())
		}
	}
	if strings.Contains(warn.String(), "第 5 行") {
		t.Errorf("旧四字段格式不应告警：%s", warn.String())
	}
}

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		`软件\VMware.exe`: "vmware.exe",
		"软件/VMware.exe": "vmware.exe",
		"./a/b":         "b",
		"/x/y.EXE":      "y.exe",
		"a.txt":         "a.txt",
		"":              "",
		".":             "",
	}
	for in, want := range cases {
		if got := normalizeName(in); got != want {
			t.Errorf("normalizeName(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestMatchFileName(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"*.flac", "a.flac", true},
		{"*.flac", "A.FLAC", true},
		{"*.flac", "a.mp3", false},
		{"A*.TXT", "abc.txt", true},
		{"a?.txt", "ab.txt", true},
		{"a?.txt", "abc.txt", false},
		{"a?.txt", "a.txt", false},
		{"?", "中", true},
		{"??", "中文", true},
		{"*", "", true},
		{"*", "任意.txt", true},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "aXbY", false},
		{"[abc].txt", "[abc].txt", true},
		{"[abc].txt", "a.txt", false},
		{"*.*", "README", false},
	}
	for _, c := range cases {
		if got := matchFileName(c.pattern, c.name); got != c.want {
			t.Errorf("matchFileName(%q, %q) = %v，期望 %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestRecordMatches(t *testing.T) {
	sha1v := strings.Repeat("B", 40)
	sha256v := strings.Repeat("C", 64)
	r := &record{sha1: sha1v, sha256: sha256v}

	if !r.matches(strings.ToLower(sha1v), strings.Repeat("0", 64)) {
		t.Error("SHA1 应命中")
	}
	if !r.matches(strings.Repeat("0", 40), strings.ToLower(sha256v)) {
		t.Error("SHA256 应命中")
	}
	if !r.matches(strings.ToLower(sha1v), strings.ToLower(sha256v)) {
		t.Error("双哈希应命中")
	}
	if r.matches(strings.Repeat("0", 40), strings.Repeat("0", 64)) {
		t.Error("不应命中")
	}
	if (&record{}).matches("", "") {
		t.Error("空记录不应命中空哈希")
	}
	if got := matchDetail(r, strings.ToLower(sha1v), strings.Repeat("0", 64)); got != "SHA1 匹配" {
		t.Errorf("matchDetail = %q", got)
	}
	if got := matchDetail(r, strings.Repeat("0", 40), strings.ToLower(sha256v)); got != "SHA256 匹配" {
		t.Errorf("matchDetail = %q", got)
	}
	if got := matchDetail(r, strings.ToLower(sha1v), strings.ToLower(sha256v)); got != "SHA1+SHA256 匹配" {
		t.Errorf("matchDetail = %q", got)
	}
}

func TestUsageMentionsCaseSensitivityAndFormat(t *testing.T) {
	if !strings.Contains(usageText, "区分大小写") {
		t.Error("帮助文本应写明选项区分大小写")
	}
	if !strings.Contains(usageText, "添加时间|文件名|SHA1|SHA256") {
		t.Error("帮助文本应写明数据库格式")
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func hashPair(data []byte) (string, string) {
	return sha1Sum(data), sha256Sum(data)
}

func sha1Sum(data []byte) string {
	h := sha1.New()
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func sha256Sum(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}
