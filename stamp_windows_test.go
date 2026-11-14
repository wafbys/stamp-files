//go:build windows

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var testTarget = time.Date(2026, 11, 11, 11, 11, 11, 0, time.Local)

func TestRunStampEndToEnd(t *testing.T) {
	root := t.TempDir()
	data := []byte("hello")
	p := writeTestFile(t, root, "a.txt", data)
	db := writeTestDB(t, dbLine("a.txt", data)+"\n")

	var out, errOut bytes.Buffer
	code := run([]string{"-n", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("dry-run exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "[将打戳 CM]") {
		t.Errorf("dry-run 输出缺少「将打戳 CM」：%s", out.String())
	}
	assertTimesNotTarget(t, p)

	out.Reset()
	errOut.Reset()
	code = run([]string{"--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("stamp exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "[打戳 CM]") {
		t.Errorf("输出缺少「打戳 CM」：%s", out.String())
	}
	assertTimes(t, p, testTarget)

	// 打戳后如果再次读取文件，Windows 会按访问策略更新 atime；
	// 工具只保证打戳当时三个时间都等于目标值，不负责冻结之后的访问。
	out.Reset()
	errOut.Reset()
	code = run([]string{root, "--db", db}, &out, &errOut) // 选项放在目录后面
	if code != 0 {
		t.Fatalf("second run exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "[已一致]") {
		t.Errorf("第二次运行应显示「已一致」：%s", out.String())
	}
	assertTimes(t, p, testTarget)
}

func TestStampMask(t *testing.T) {
	target := testTarget
	off := target.Add(-time.Second)
	mk := func(creation, write, access time.Time) fileTimes {
		return fileTimes{creation: creation, write: write, access: access}
	}
	cases := []struct {
		name  string
		times fileTimes
		want  string
	}{
		{"创建和修改都一致", mk(target, target, target), "--"},
		{"创建和修改都不一致", mk(off, off, target), "CM"},
		{"仅创建不一致", mk(off, target, target), "C-"},
		{"仅修改不一致", mk(target, off, target), "-M"},
		{"只有访问不一致", mk(target, target, off), "--"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.times.stampMask(target); got != c.want {
				t.Errorf("stampMask = %q，期望 %q", got, c.want)
			}
		})
	}
}

func TestRunIgnoresAccessTime(t *testing.T) {
	root := t.TempDir()
	data := []byte("ignore atime")
	p := writeTestFile(t, root, "a.txt", data)
	setCreationWrite(t, p, testTarget)
	db := writeTestDB(t, dbLine("a.txt", data)+"\n")

	var out, errOut bytes.Buffer
	code := run([]string{"--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "[已一致]") {
		t.Errorf("创建和修改一致时应显示「已一致」：%s", out.String())
	}
	got := readTimes(t, p)
	if !got.creation.Equal(testTarget) || !got.write.Equal(testTarget) {
		t.Errorf("创建/修改时间不应再写：%s", formatTimes(got))
	}
	if got.access.Equal(testTarget) {
		t.Errorf("访问时间不应被写入：%s", formatTimes(got))
	}
}

func TestRunTimeFlag(t *testing.T) {
	root := t.TempDir()
	data := []byte("custom time")
	p := writeTestFile(t, root, "a.txt", data)
	db := writeTestDB(t, dbLine("a.txt", data)+"\n")
	custom := time.Date(2027, 1, 2, 3, 4, 5, 0, time.Local)

	var out, errOut bytes.Buffer
	code := run([]string{"--db", db, "--time", custom.Format(timeLayout), root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	assertTimes(t, p, custom)
}

func TestRunFilenameMatch(t *testing.T) {
	root := t.TempDir()
	data := []byte("match by name")
	p := writeTestFile(t, root, "a.txt", data)
	db := writeTestDB(t, dbLine("other/sub/a.txt", data)+"\n")

	var out, errOut bytes.Buffer
	code := run([]string{"--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "[打戳 ") {
		t.Errorf("应按文件名命中：%s", out.String())
	}
	assertTimes(t, p, testTarget)
}

func TestRunHashesMultiChunkFile(t *testing.T) {
	root := t.TempDir()
	data := bytes.Repeat([]byte("0123456789abcdef"), 200000) // 3.2 MB，跨多个 1 MB 读取块
	p := writeTestFile(t, root, "big.bin", data)
	db := writeTestDB(t, dbLine("big.bin", data)+"\n")

	var out, errOut bytes.Buffer
	code := run([]string{"-n", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "[将打戳 CM]") {
		t.Errorf("并发哈希结果与数据库不一致：%s", out.String())
	}
	assertTimesNotTarget(t, p)
}

func TestOpenAndHashProgress(t *testing.T) {
	root := t.TempDir()
	data := bytes.Repeat([]byte("x"), 2<<20)
	p := writeTestFile(t, root, "big.bin", data)

	var buf bytes.Buffer
	prog := newProgressPrinter(&buf, true)
	prog.minSize = 0
	prog.interval = 0

	of, err := openAndHash(p, prog)
	if err != nil {
		t.Fatal(err)
	}
	defer of.Close()
	s1, s256 := hashPair(data)
	if of.sha1 != s1 || of.sha256 != s256 {
		t.Error("带进度时哈希结果不一致")
	}
	out := buf.String()
	if !strings.Contains(out, "[读取]") || !strings.Contains(out, "big.bin") {
		t.Errorf("应输出读取进度：%q", out)
	}
}

func TestRunWildcardFiltersFiles(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"a.flac":          []byte("a"),
		"B.FLAC":          []byte("b"),
		"sub/c.flac":      []byte("c"),
		"sub/deep/d.flac": []byte("d"),
		"e.mp3":           []byte("e"),
		"sub/f.jpg":       []byte("f"),
	}
	paths := make(map[string]string, len(files))
	var dbContent strings.Builder
	for rel, data := range files {
		paths[rel] = writeTestFile(t, root, rel, data)
		dbContent.WriteString(dbLine(rel, data) + "\n")
	}
	db := writeTestDB(t, dbContent.String())

	var out, errOut bytes.Buffer
	code := run([]string{"--db", db, filepath.Join(root, "*.flac")}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "匹配：*.flac") {
		t.Errorf("输出缺少匹配行：%s", out.String())
	}
	if !strings.Contains(out.String(), "共 4 个文件") {
		t.Errorf("只应统计命中的 4 个文件：%s", out.String())
	}
	for _, rel := range []string{"a.flac", "B.FLAC", "sub/c.flac", "sub/deep/d.flac"} {
		assertTimes(t, paths[rel], testTarget)
	}
	for _, rel := range []string{"e.mp3", "sub/f.jpg"} {
		assertTimesNotTarget(t, paths[rel])
	}
}

func TestRunWildcardQuestionMark(t *testing.T) {
	root := t.TempDir()
	ab := writeTestFile(t, root, "ab.flac", []byte("ab"))
	c := writeTestFile(t, root, "c.flac", []byte("c"))
	db := writeTestDB(t, dbLine("ab.flac", []byte("ab"))+"\n"+dbLine("c.flac", []byte("c"))+"\n")

	var out, errOut bytes.Buffer
	code := run([]string{"--db", db, filepath.Join(root, "?.flac")}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "共 1 个文件") {
		t.Errorf("? 应只匹配单字符文件名：%s", out.String())
	}
	assertTimes(t, c, testTarget)
	assertTimesNotTarget(t, ab)
}

func TestRunWildcardSubdirectoryRoot(t *testing.T) {
	root := t.TempDir()
	top := writeTestFile(t, root, "a.flac", []byte("a"))
	sub := writeTestFile(t, root, "sub/b.flac", []byte("b"))
	deep := writeTestFile(t, root, "sub/deep/c.flac", []byte("c"))
	db := writeTestDB(t, dbLine("a.flac", []byte("a"))+"\n"+
		dbLine("b.flac", []byte("b"))+"\n"+
		dbLine("c.flac", []byte("c"))+"\n")

	var out, errOut bytes.Buffer
	code := run([]string{"--db", db, filepath.Join(root, "sub", "*.flac")}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "共 2 个文件") {
		t.Errorf("目录部分应作为扫描根：%s", out.String())
	}
	assertTimes(t, sub, testTarget)
	assertTimes(t, deep, testTarget)
	assertTimesNotTarget(t, top)
}

func TestRunAddWildcardFilters(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.txt", []byte("a"))
	writeTestFile(t, root, "b.jpg", []byte("b"))
	writeTestFile(t, root, "sub/c.txt", []byte("c"))
	db := filepath.Join(t.TempDir(), dbFileName)

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "--db", db, filepath.Join(root, "*.txt")}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	got := readDBNames(t, db)
	want := []string{"a.txt", "c.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("--add 只应写入匹配文件，实际 %v，期望 %v", got, want)
	}
}

func TestRunWildcardNoMatch(t *testing.T) {
	root := t.TempDir()
	p := writeTestFile(t, root, "a.txt", []byte("a"))
	db := writeTestDB(t, dbLine("a.txt", []byte("a"))+"\n")

	var out, errOut bytes.Buffer
	code := run([]string{"--db", db, filepath.Join(root, "*.jpg")}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "共 0 个文件") {
		t.Errorf("没有匹配时应统计 0 个文件：%s", out.String())
	}
	if strings.Contains(out.String(), "[未找到]") {
		t.Errorf("被通配符排除的文件不应报未找到：%s", out.String())
	}
	assertTimesNotTarget(t, p)
}

func TestRunWildcardInDirectoryRejected(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{`D:\2024*\*.jpg`}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit = %d，期望 2", code)
	}
	if !strings.Contains(errOut.String(), "通配符只支持文件名最后一段") {
		t.Errorf("错误信息不对：%s", errOut.String())
	}
}

func TestRunSHA1OnlyMatch(t *testing.T) {
	root := t.TempDir()
	data := []byte("sha1 only")
	p := writeTestFile(t, root, "a.txt", data)
	s1, _ := hashPair(data)
	db := writeTestDB(t, fmt.Sprintf("a.txt|%s|\n", strings.ToUpper(s1)))

	var out, errOut bytes.Buffer
	code := run([]string{"--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "SHA1 匹配") {
		t.Errorf("应报告 SHA1 匹配：%s", out.String())
	}
	assertTimes(t, p, testTarget)
}

func TestRunHashMismatch(t *testing.T) {
	root := t.TempDir()
	data := []byte("hello")
	p := writeTestFile(t, root, "a.txt", data)
	before := readTimes(t, p)
	db := writeTestDB(t, dbLine("a.txt", []byte("different"))+"\n")

	var out, errOut bytes.Buffer
	code := run([]string{"--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "[跳过]") {
		t.Errorf("应跳过不匹配文件：%s", out.String())
	}
	assertSameTimes(t, p, before)
}

func TestRunUnicodePath(t *testing.T) {
	root := t.TempDir()
	data := []byte("unicode")
	p := writeTestFile(t, root, "软件/VMware.exe", data)
	db := writeTestDB(t, dbLine("软件/VMware.exe", data)+"\n")

	var out, errOut bytes.Buffer
	code := run([]string{"--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "[打戳 ") {
		t.Errorf("中文路径应打戳：%s", out.String())
	}
	assertTimes(t, p, testTarget)
}

func TestRunLongPath(t *testing.T) {
	root := t.TempDir()
	rel := ""
	for i := 0; i < 14; i++ {
		rel = filepath.Join(rel, strings.Repeat("d", 20))
	}
	rel = filepath.Join(rel, "long.txt")
	data := []byte("long path")
	p := writeTestFile(t, root, rel, data)
	if len(p) < 260 {
		t.Fatalf("测试路径不够长：%d", len(p))
	}
	db := writeTestDB(t, dbLine(rel, data)+"\n")

	var out, errOut bytes.Buffer
	code := run([]string{"--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "[打戳 ") {
		t.Errorf("长路径应打戳：%s", out.String())
	}
	assertTimes(t, p, testTarget)
}

func TestRunReadOnlyFile(t *testing.T) {
	root := t.TempDir()
	data := []byte("readonly")
	p := writeTestFile(t, root, "a.txt", data)
	if err := os.Chmod(p, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	db := writeTestDB(t, dbLine("a.txt", data)+"\n")

	var out, errOut bytes.Buffer
	code := run([]string{"--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "[打戳 ") {
		t.Errorf("只读文件应能打戳：%s", out.String())
	}
	assertTimes(t, p, testTarget)
}

func TestRunHideMissing(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.txt", []byte("a"))
	other := []byte("other")
	db := writeTestDB(t, dbLine("other.txt", other)+"\n")

	var out, errOut bytes.Buffer
	code := run([]string{"--db", db, "--hide-missing", root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if strings.Contains(out.String(), "[未找到]") {
		t.Errorf("--hide-missing 不应打印未找到明细：%s", out.String())
	}
	if !strings.Contains(out.String(), "未找到 1") {
		t.Errorf("汇总应计数未找到：%s", out.String())
	}
}

func TestRunNoDirectoryUsesCwd(t *testing.T) {
	root := t.TempDir()
	data := []byte("cwd")
	p := writeTestFile(t, root, "a.txt", data)
	db := writeTestDB(t, dbLine("a.txt", data)+"\n")

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	var out, errOut bytes.Buffer
	code := run([]string{"--db", db}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "未指定目录，使用当前目录") {
		t.Errorf("应提示默认当前目录：%s", out.String())
	}
	assertTimes(t, p, testTarget)
}

func TestRunCaseSensitiveFlags(t *testing.T) {
	root := t.TempDir()
	cases := [][]string{
		{"-T", root},
		{"--Time", testTarget.Format(timeLayout), root},
		{"-v"},
		{"-d", "x.txt", root},
	}
	for _, args := range cases {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 2 {
			t.Errorf("args %v：exit = %d，期望 2；stderr=%s", args, code, errOut.String())
		}
	}
}

func TestRunExtraPositional(t *testing.T) {
	root := t.TempDir()
	var out, errOut bytes.Buffer
	code := run([]string{root, "extra"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit = %d，期望 2", code)
	}
	if !strings.Contains(errOut.String(), "只能指定一个目录") {
		t.Errorf("错误信息不对：%s", errOut.String())
	}
}

func TestRunMissingAndEmptyDB(t *testing.T) {
	root := t.TempDir()
	var out, errOut bytes.Buffer

	missing := filepath.Join(t.TempDir(), "missing.txt")
	code := run([]string{"--db", missing, root}, &out, &errOut)
	if code != 2 {
		t.Fatalf("missing db exit = %d，期望 2", code)
	}
	if !strings.Contains(errOut.String(), "数据库不存在") {
		t.Errorf("错误信息不对：%s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "stamp-files --add") {
		t.Errorf("错误信息缺少建库提示：%s", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	empty := writeTestDB(t, "# 只有注释\n")
	code = run([]string{"--db", empty, root}, &out, &errOut)
	if code != 2 {
		t.Fatalf("empty db exit = %d，期望 2", code)
	}
	if !strings.Contains(errOut.String(), "没有有效记录") {
		t.Errorf("错误信息不对：%s", errOut.String())
	}
}

func TestRunHelpAndVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-h"}, &out, &errOut); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	if !strings.Contains(out.String(), "用法") {
		t.Errorf("帮助缺少用法：%s", out.String())
	}

	out.Reset()
	errOut.Reset()
	if code := run([]string{"-V"}, &out, &errOut); code != 0 {
		t.Fatalf("version exit = %d", code)
	}
	if !strings.Contains(out.String(), "stamp-files 0.8.0") {
		t.Errorf("版本输出不对：%s", out.String())
	}

	out.Reset()
	errOut.Reset()
	if code := run([]string{"-v"}, &out, &errOut); code != 2 {
		t.Fatalf("小写 -v 应报错，实际 exit = %d", code)
	}
}

func TestRunBadTime(t *testing.T) {
	root := t.TempDir()
	var out, errOut bytes.Buffer
	code := run([]string{"--time", "2026/11/11", root}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit = %d，期望 2", code)
	}
	if !strings.Contains(errOut.String(), "时间格式错误") {
		t.Errorf("错误信息不对：%s", errOut.String())
	}
}

func TestRunAddCreatesDB(t *testing.T) {
	root := t.TempDir()
	dataA := []byte("aaa")
	dataB := []byte("bbb")
	pA := writeTestFile(t, root, "a.txt", dataA)
	pB := writeTestFile(t, root, "sub/b.txt", dataB)
	beforeA := readTimes(t, pA)
	beforeB := readTimes(t, pB)
	db := filepath.Join(t.TempDir(), dbFileName)

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "已写入 2 条记录") {
		t.Errorf("输出缺少写入提示：%s", out.String())
	}
	if !strings.Contains(out.String(), "用时：") {
		t.Errorf("输出缺少用时：%s", out.String())
	}

	var warn bytes.Buffer
	loaded, err := loadDatabase(db, &warn)
	if err != nil {
		t.Fatalf("读取生成的数据库失败：%v", err)
	}
	if loaded.count != 2 {
		t.Errorf("数据库记录数 = %d，期望 2", loaded.count)
	}
	if len(loaded.byName["a.txt"]) != 1 || len(loaded.byName["b.txt"]) != 1 {
		t.Errorf("文件名记录不对：%+v", loaded.byName)
	}
	raw, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.Split(strings.TrimSuffix(line, "\r"), "|")
		if len(parts) != 4 {
			t.Errorf("第 %d 行应为四字段（添加时间|文件名|SHA1|SHA256）：%q", i+1, line)
			continue
		}
		if _, err := time.ParseInLocation(timeLayout, parts[0], time.Local); err != nil {
			t.Errorf("第 %d 行添加时间无效：%q", i+1, parts[0])
		}
	}
	s1, s256 := hashPair(dataA)
	if rec := loaded.byName["a.txt"][0]; !rec.matches(s1, s256) {
		t.Error("a.txt 的哈希不匹配")
	}

	// add 模式只写数据库，不写时间戳。
	assertSameTimes(t, pA, beforeA)
	assertSameTimes(t, pB, beforeB)

	out.Reset()
	errOut.Reset()
	code = run([]string{"--add", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("second add exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "已存在 2") {
		t.Errorf("第二轮应全部已存在：%s", out.String())
	}
	if !strings.Contains(out.String(), "没有新增记录") {
		t.Errorf("第二轮不应新增：%s", out.String())
	}
	loaded, err = loadDatabase(db, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.count != 2 {
		t.Errorf("第二轮后记录数 = %d，期望 2", loaded.count)
	}
}

func TestRunAddDryRun(t *testing.T) {
	root := t.TempDir()
	data := []byte("dry add")
	p := writeTestFile(t, root, "a.txt", data)
	before := readTimes(t, p)
	db := filepath.Join(t.TempDir(), dbFileName)

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "-n", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "将新增 1") {
		t.Errorf("输出缺少将新增：%s", out.String())
	}
	if !strings.Contains(out.String(), "预览：将写入 1 条记录") {
		t.Errorf("输出缺少预览写入提示：%s", out.String())
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Errorf("dry-run 不应创建数据库，stat err = %v", err)
	}
	assertSameTimes(t, p, before)
}

func TestRunAddAppendsNoTrailingNewline(t *testing.T) {
	root := t.TempDir()
	dataA := []byte("aaa")
	dataB := []byte("bbb")
	writeTestFile(t, root, "a.txt", dataA)
	writeTestFile(t, root, "b.txt", dataB)
	db := filepath.Join(t.TempDir(), dbFileName)
	if err := os.WriteFile(db, []byte(dbLine("a.txt", dataA)), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "已存在 1") || !strings.Contains(out.String(), "已写入 1 条记录") {
		t.Errorf("输出不对：%s", out.String())
	}
	var warn bytes.Buffer
	loaded, err := loadDatabase(db, &warn)
	if err != nil {
		t.Fatalf("读取数据库失败：%v", err)
	}
	if loaded.count != 2 {
		t.Errorf("记录数 = %d，期望 2", loaded.count)
	}
	if len(loaded.byName["a.txt"]) != 1 || len(loaded.byName["b.txt"]) != 1 {
		t.Errorf("文件名记录不对：%+v", loaded.byName)
	}
}

func TestRunAddSkipsDBFile(t *testing.T) {
	root := t.TempDir()
	data := []byte("db skip")
	writeTestFile(t, root, "a.txt", data)
	db := filepath.Join(root, dbFileName)
	if err := os.WriteFile(db, []byte(dbLine("a.txt", data)+"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "已存在 1") {
		t.Errorf("输出不对：%s", out.String())
	}
	var warn bytes.Buffer
	loaded, err := loadDatabase(db, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.byName[dbFileName]) != 0 {
		t.Errorf("数据库文件不应把自己加入：%+v", loaded.byName[dbFileName])
	}
}

func TestRunAddSameNameDifferentContent(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a/x.txt", []byte("one"))
	writeTestFile(t, root, "b/x.txt", []byte("two"))
	db := filepath.Join(t.TempDir(), dbFileName)

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "已写入 2 条记录") {
		t.Errorf("输出不对：%s", out.String())
	}
	var warn bytes.Buffer
	loaded, err := loadDatabase(db, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.byName["x.txt"]) != 2 {
		t.Errorf("同名不同内容应保留 2 条：%d", len(loaded.byName["x.txt"]))
	}
}

func TestRunAddSameNameSameContent(t *testing.T) {
	root := t.TempDir()
	data := []byte("same")
	writeTestFile(t, root, "a/x.txt", data)
	writeTestFile(t, root, "b/x.txt", data)
	db := filepath.Join(t.TempDir(), dbFileName)

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "已写入 1 条记录") || !strings.Contains(out.String(), "重复 1") {
		t.Errorf("输出不对：%s", out.String())
	}
	var warn bytes.Buffer
	loaded, err := loadDatabase(db, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.count != 1 {
		t.Errorf("同名同内容应去重为 1 条：%d", loaded.count)
	}
}

func TestRunAddNoFiles(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(t.TempDir(), dbFileName)

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "没有新增记录") {
		t.Errorf("输出不对：%s", out.String())
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Errorf("没有新增时不应创建数据库，stat err = %v", err)
	}
}

func TestRunAddMissingParent(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.txt", []byte("x"))
	db := filepath.Join(t.TempDir(), "no-such-dir", dbFileName)

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "--db", db, root}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit = %d，期望 2", code)
	}
	if !strings.Contains(errOut.String(), "写入数据库失败") {
		t.Errorf("错误信息不对：%s", errOut.String())
	}
}

func TestRunAddReadOnlyFile(t *testing.T) {
	root := t.TempDir()
	data := []byte("readonly add")
	p := writeTestFile(t, root, "a.txt", data)
	if err := os.Chmod(p, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	before := readTimes(t, p)
	db := filepath.Join(t.TempDir(), dbFileName)

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "已写入 1 条记录") {
		t.Errorf("输出不对：%s", out.String())
	}
	assertSameTimes(t, p, before)
}

func TestRunAddUnicode(t *testing.T) {
	root := t.TempDir()
	data := []byte("unicode add")
	writeTestFile(t, root, "软件/VMware.exe", data)
	db := filepath.Join(t.TempDir(), dbFileName)

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	var warn bytes.Buffer
	loaded, err := loadDatabase(db, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.byName["vmware.exe"]) != 1 {
		t.Errorf("中文路径的文件名记录不对：%+v", loaded.byName)
	}
}

func TestRunAddSortsDatabase(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "alpha.txt", []byte("alpha"))
	writeTestFile(t, root, "mid.txt", []byte("mid"))
	db := writeTestDB(t, "# 注释\r\n\r\n"+
		dbLine("zeta.txt", []byte("z"))+"\r\n"+
		dbLine("Banana.txt", []byte("b"))+"\r\n")

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	got := readDBNames(t, db)
	want := []string{"alpha.txt", "Banana.txt", "mid.txt", "zeta.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("文件名顺序 = %v，期望 %v", got, want)
	}
	raw, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "# 注释\r\n\r\n") {
		t.Errorf("注释和空行应保留在开头：%q", string(raw))
	}
}

func TestRunAddSortsSameNameByAddedTime(t *testing.T) {
	root := t.TempDir()
	data := []byte("same")
	db := writeTestDB(t, dbLineAt("2026-11-14 22:10:00", "same.txt", data)+"\r\n"+
		dbLineAt("2026-11-14 22:08:23", "same.txt", data)+"\r\n"+
		dbLineAt("2026-11-14 22:09:00", "same.txt", data)+"\r\n")

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}

	raw, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.Split(strings.TrimSuffix(line, "\r"), "|")
		if len(parts) != 4 {
			t.Fatalf("行格式不对：%q", line)
		}
		got = append(got, parts[0])
	}
	want := []string{"2026-11-14 22:08:23", "2026-11-14 22:09:00", "2026-11-14 22:10:00"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("同名记录应按添加时间排序，实际 %v，期望 %v", got, want)
	}
}

func TestRunAddSortsExistingDBWithoutAdditions(t *testing.T) {
	root := t.TempDir()
	db := writeTestDB(t, dbLine("zeta.txt", []byte("z"))+"\r\n"+
		dbLine("alpha.txt", []byte("a"))+"\r\n")

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	got := readDBNames(t, db)
	want := []string{"alpha.txt", "zeta.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("文件名顺序 = %v，期望 %v", got, want)
	}
}

func TestRunAddDryRunDoesNotSortDB(t *testing.T) {
	root := t.TempDir()
	original := dbLine("zeta.txt", []byte("z")) + "\r\n" +
		dbLine("alpha.txt", []byte("a")) + "\r\n"
	db := writeTestDB(t, original)

	var out, errOut bytes.Buffer
	code := run([]string{"--add", "-n", "--db", db, root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	raw, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != original {
		t.Errorf("dry-run 不应改写数据库：%q", string(raw))
	}
}

func writeTestFile(t *testing.T, root, rel string, data []byte) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeTestDB(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), dbFileName)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const testAdded = "2026-11-14 22:08:23"

func dbLine(name string, data []byte) string {
	return dbLineAt(testAdded, name, data)
}

func dbLineAt(added, name string, data []byte) string {
	s1, s256 := hashPair(data)
	return fmt.Sprintf("%s|%s|%s|%s", added, filepath.ToSlash(name), strings.ToUpper(s1), strings.ToUpper(s256))
}

func readDBNames(t *testing.T, p string) []string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) == 4 {
			if parts[0] == "" {
				names = append(names, parts[1])
				continue
			}
			if _, err := time.ParseInLocation(timeLayout, strings.TrimSpace(parts[0]), time.Local); err == nil {
				names = append(names, parts[1])
				continue
			}
		}
		if len(parts) > 0 {
			names = append(names, parts[0])
		}
	}
	return names
}

func setCreationWrite(t *testing.T, p string, target time.Time) {
	t.Helper()
	ptr, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(
		ptr,
		syscall.FILE_WRITE_ATTRIBUTES,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(h)

	ft := syscall.NsecToFiletime(target.UnixNano())
	if err := syscall.SetFileTime(h, &ft, nil, &ft); err != nil {
		t.Fatal(err)
	}
}

func readTimes(t *testing.T, p string) fileTimes {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := fileTimesFromInfo(info)
	if !ok {
		t.Fatal("无法读取 Win32 文件时间")
	}
	return got
}

func assertTimes(t *testing.T, p string, want time.Time) {
	t.Helper()
	got := readTimes(t, p)
	if !got.equal(want) {
		t.Fatalf("时间不对：%s，期望 %s", formatTimes(got), want.Format(timeLayout))
	}
}

func assertTimesNotTarget(t *testing.T, p string) {
	t.Helper()
	got := readTimes(t, p)
	if got.equal(testTarget) {
		t.Fatalf("dry-run 不应改时间：%s", formatTimes(got))
	}
}

func assertSameTimes(t *testing.T, p string, want fileTimes) {
	t.Helper()
	got := readTimes(t, p)
	if !got.creation.Equal(want.creation) || !got.access.Equal(want.access) || !got.write.Equal(want.write) {
		t.Fatalf("时间不应变化：%s，原值 %s", formatTimes(got), formatTimes(want))
	}
}

func formatTimes(t fileTimes) string {
	return fmt.Sprintf("创建=%s 访问=%s 修改=%s",
		t.creation.Format(timeLayout), t.access.Format(timeLayout), t.write.Format(timeLayout))
}
