package main

import (
	"bytes"
	"crypto/md5"
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
		wantTime    string
		wantDB      string
		wantDry     bool
		wantAdd     bool
		wantHashAll bool
		wantHide    bool
		wantNoColor bool
		wantHelp    bool
		wantVersion bool
	}{
		{name: "no args", wantDir: cwd, wantTime: defaultTimeString, wantDB: defaultDB},
		{name: "dir only", args: []string{`D:\Archive`}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB},
		{name: "dir then dry-run", args: []string{`D:\Archive`, "-n"}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB, wantDry: true},
		{name: "dry-run then dir", args: []string{"-n", `D:\Archive`}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB, wantDry: true},
		{name: "time flag", args: []string{"-t", "2027-01-02 03:04:05", `D:\Archive`}, wantDir: `D:\Archive`, wantTime: "2027-01-02 03:04:05", wantDB: defaultDB},
		{name: "db flag", args: []string{"--db", "x.txt", `D:\Archive`}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: mustAbs(t, "x.txt")},
		{name: "add flag", args: []string{"--add", `D:\Archive`}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB, wantAdd: true},
		{name: "add dry-run", args: []string{"--add", "-n", `D:\Archive`}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB, wantAdd: true, wantDry: true},
		{name: "all bool flags", args: []string{"--hide-missing", "--no-color", "-a", `D:\Archive`}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB, wantHashAll: true, wantHide: true, wantNoColor: true},
		{name: "flags around dir", args: []string{"-a", `D:\Archive`, "-n"}, wantDir: `D:\Archive`, wantTime: defaultTimeString, wantDB: defaultDB, wantDry: true, wantHashAll: true},
		{name: "dash dash", args: []string{"--", "-dir"}, wantDir: filepath.Join(cwd, "-dir"), wantTime: defaultTimeString, wantDB: defaultDB},
		{name: "help", args: []string{"-h"}, wantHelp: true},
		{name: "help after dir", args: []string{`D:\Archive`, "--help"}, wantHelp: true},
		{name: "version", args: []string{"-V"}, wantVersion: true},
		{name: "extra positional", args: []string{`D:\A`, `D:\B`}, wantErr: true},
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
		})
	}
}

func TestLoadDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, dbFileName)
	md5v := strings.Repeat("A", 32)
	sha1v := strings.Repeat("B", 40)
	sha256v := strings.Repeat("C", 64)

	content := "\uFEFF# 注释\r\n" +
		`软件\VMware.exe|` + md5v + "|" + sha1v + "|" + sha256v + "\r\n" +
		"only-md5.exe|" + strings.Repeat("D", 32) + "||\r\n" +
		"only-sha1.exe||" + strings.Repeat("E", 40) + "|\r\n" +
		"only-sha256.exe|||" + strings.Repeat("F", 64) + "\r\n" +
		"bad-md5.exe|XYZ||\r\n" +
		"bad-sha1.exe||XYZ|\r\n" +
		"bad-sha256.exe|||XYZ\r\n" +
		"empty.exe||||\r\n" +
		"too|few|fields\r\n" +
		"软件/VMware.exe|" + strings.Repeat("1", 32) + "|" + strings.Repeat("2", 40) + "|" + strings.Repeat("3", 64) + "\r\n" +
		"old-format.exe|" + strings.Repeat("A", 40) + "|" + strings.Repeat("B", 64) + "|2026-11-11 11:11:11\r\n"

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
	if db.warnings != 6 {
		t.Errorf("warnings = %d，期望 6", db.warnings)
	}
	if len(db.byName["vmware.exe"]) != 2 {
		t.Errorf("文件名记录数 = %d，期望 2", len(db.byName["vmware.exe"]))
	}
	if db.byName["vmware.exe"][0].line != 2 || db.byName["vmware.exe"][1].line != 11 {
		t.Errorf("行号不对：%d, %d", db.byName["vmware.exe"][0].line, db.byName["vmware.exe"][1].line)
	}
	if db.byName["only-md5.exe"][0].sha1 != "" || db.byName["only-md5.exe"][0].sha256 != "" {
		t.Error("only-md5 的 SHA1/SHA256 应为空")
	}
	if db.byName["only-sha1.exe"][0].md5 != "" || db.byName["only-sha1.exe"][0].sha256 != "" {
		t.Error("only-sha1 的 MD5/SHA256 应为空")
	}
	if db.byName["only-sha256.exe"][0].md5 != "" || db.byName["only-sha256.exe"][0].sha1 != "" {
		t.Error("only-sha256 的 MD5/SHA1 应为空")
	}
	for _, line := range []string{"第 6 行", "第 7 行", "第 8 行", "第 9 行", "第 10 行", "第 12 行"} {
		if !strings.Contains(warn.String(), line) {
			t.Errorf("警告缺少 %s：%s", line, warn.String())
		}
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

func TestRecordMatches(t *testing.T) {
	md5v := strings.Repeat("A", 32)
	sha1v := strings.Repeat("B", 40)
	sha256v := strings.Repeat("C", 64)
	r := &record{md5: md5v, sha1: sha1v, sha256: sha256v}

	if !r.matches(strings.ToLower(md5v), strings.Repeat("0", 40), strings.Repeat("0", 64)) {
		t.Error("MD5 应命中")
	}
	if !r.matches(strings.Repeat("0", 32), strings.ToLower(sha1v), strings.Repeat("0", 64)) {
		t.Error("SHA1 应命中")
	}
	if !r.matches(strings.Repeat("0", 32), strings.Repeat("0", 40), strings.ToLower(sha256v)) {
		t.Error("SHA256 应命中")
	}
	if !r.matches(strings.ToLower(md5v), strings.ToLower(sha1v), strings.ToLower(sha256v)) {
		t.Error("三哈希应命中")
	}
	if r.matches(strings.Repeat("0", 32), strings.Repeat("0", 40), strings.Repeat("0", 64)) {
		t.Error("不应命中")
	}
	if (&record{}).matches("", "", "") {
		t.Error("空记录不应命中空哈希")
	}
	if got := matchDetail(r, strings.ToLower(md5v), strings.Repeat("0", 40), strings.Repeat("0", 64)); got != "MD5 匹配" {
		t.Errorf("matchDetail = %q", got)
	}
	if got := matchDetail(r, strings.ToLower(md5v), strings.Repeat("0", 40), strings.ToLower(sha256v)); got != "MD5+SHA256 匹配" {
		t.Errorf("matchDetail = %q", got)
	}
	if got := matchDetail(r, strings.ToLower(md5v), strings.ToLower(sha1v), strings.ToLower(sha256v)); got != "MD5+SHA1+SHA256 匹配" {
		t.Errorf("matchDetail = %q", got)
	}
}

func TestUsageMentionsCaseSensitivityAndFormat(t *testing.T) {
	if !strings.Contains(usageText, "区分大小写") {
		t.Error("帮助文本应写明选项区分大小写")
	}
	if !strings.Contains(usageText, "文件名|MD5|SHA1|SHA256") {
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

func hashTriple(data []byte) (string, string, string) {
	return md5Sum(data), sha1Sum(data), sha256Sum(data)
}

func md5Sum(data []byte) string {
	h := md5.New()
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
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
