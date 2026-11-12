package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/pflag"
)

const (
	timeLayout        = "2006-01-02 15:04:05"
	defaultTimeString = "2026-11-11 11:11:11"
	dbFileName        = "stamp-db.txt"
)

var version = "0.2.0"

const usageText = `用法：
  stamp-files [选项] [目录]

目录是唯一的位置参数，默认 .（当前目录），始终递归子目录。
时间和数据库只用选项指定，不做位置参数。

数据库格式：文件名|MD5|SHA1|SHA256
MD5、SHA1、SHA256 任一命中即可，文件名不区分大小写。

选项（区分大小写）：
  -t, --time <时间>        所有命中文件统一使用的时间
                          格式 yyyy-MM-dd HH:mm:ss，默认 2026-11-11 11:11:11
      --db <文件>          数据库路径，默认 <exe 同目录>\stamp-db.txt
      --add                扫描目录并追加到数据库（不存在则创建；不写时间戳）
  -n, --dry-run            只预览，不写时间戳/数据库
  -a, --hash-all           对所有文件计算哈希（默认只算有候选的）
      --hide-missing       不打印「未找到」明细
      --no-color           禁用颜色（也支持 NO_COLOR 环境变量）
  -h, --help               显示帮助
  -V, --version            显示版本

示例：
  stamp-files
  stamp-files D:\Archive
  stamp-files -n D:\Archive
  stamp-files D:\Archive --time "2026-11-11 11:11:11"
  stamp-files --db D:\db.txt D:\Archive
  stamp-files --add D:\Archive
  stamp-files --add -n D:\Archive
  stamp-files -- -以横线开头的目录

退出码：0 正常；1 有文件级错误；2 参数/目录/数据库错误。
`

type record struct {
	nameKey string
	md5     string
	sha1    string
	sha256  string
	line    int
}

type database struct {
	byName   map[string][]*record
	count    int
	warnings int
}

func newDatabase() *database {
	return &database{
		byName: make(map[string][]*record),
	}
}

type options struct {
	dir         string
	dirDefault  bool
	stamp       time.Time
	dbPath      string
	add         bool
	dryRun      bool
	hashAll     bool
	hideMissing bool
	noColor     bool
	help        bool
	version     bool
}

type status int

const (
	statusStamped status = iota
	statusWouldStamp
	statusAlready
	statusMismatch
	statusNotFound
	statusError
)

type fileResult struct {
	rel    string
	status status
	detail string
	err    error
}

type summary struct {
	total    int
	stamped  int
	already  int
	mismatch int
	notFound int
	skipped  int
	errors   int
}

type addSummary struct {
	total      int
	added      int
	existing   int
	duplicates int
	skipped    int
	errors     int
}

func main() {
	restore := enableUTF8Console()
	code := run(os.Args[1:], os.Stdout, os.Stderr)
	restore()
	os.Exit(code)
}

func run(args []string, stdout, stderr io.Writer) int {
	defaultDB, err := defaultDatabasePath()
	if err != nil {
		fmt.Fprintf(stderr, "错误：无法定位程序路径：%v\n", err)
		return 2
	}

	opts, err := parseArgs(args, defaultDB)
	if err != nil {
		fmt.Fprintf(stderr, "错误：%v\n", err)
		fmt.Fprintln(stderr, "运行 stamp-files --help 查看用法。")
		return 2
	}
	if opts.help {
		fmt.Fprint(stdout, usageText)
		return 0
	}
	if opts.version {
		fmt.Fprintf(stdout, "stamp-files %s (%s %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return 0
	}

	info, err := os.Stat(opts.dir)
	if err != nil {
		fmt.Fprintf(stderr, "错误：目录不可用：%s：%v\n", opts.dir, err)
		return 2
	}
	if !info.IsDir() {
		fmt.Fprintf(stderr, "错误：不是目录：%s\n", opts.dir)
		return 2
	}

	if opts.add {
		return runAdd(opts, stdout, stderr)
	}

	if _, statErr := os.Stat(opts.dbPath); statErr != nil {
		if os.IsNotExist(statErr) {
			fmt.Fprintf(stderr, "错误：数据库不存在：%s\n", opts.dbPath)
			fmt.Fprintln(stderr, "首次使用请先建立数据库，例如：")
			fmt.Fprintf(stderr, "  stamp-files --add \"%s\"\n", opts.dir)
			fmt.Fprintln(stderr, "也可以加 --db 指定已有数据库。")
			return 2
		}
		fmt.Fprintf(stderr, "错误：无法访问数据库 %s：%v\n", opts.dbPath, statErr)
		return 2
	}

	db, err := loadDatabase(opts.dbPath, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "错误：%v\n", err)
		return 2
	}
	if db.count == 0 {
		fmt.Fprintf(stderr, "错误：数据库没有有效记录：%s\n", opts.dbPath)
		return 2
	}

	if opts.dirDefault {
		fmt.Fprintf(stdout, "未指定目录，使用当前目录：%s\n", opts.dir)
	}
	fmt.Fprintf(stdout, "目录：%s（递归）\n", opts.dir)
	fmt.Fprintf(stdout, "时间：%s\n", opts.stamp.Format(timeLayout))
	fmt.Fprintf(stdout, "数据库：%s（%d 条记录）\n", opts.dbPath, db.count)
	if opts.dryRun {
		fmt.Fprintln(stdout, "模式：预览（不写入时间戳）")
	}
	fmt.Fprintln(stdout)

	rep := newReporter(stdout, stderr, opts.noColor)
	sum := scan(opts, db, rep)
	printSummary(stdout, sum, opts.dryRun)

	if sum.errors > 0 {
		return 1
	}
	return 0
}

func defaultDatabasePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), dbFileName), nil
}

func parseArgs(args []string, defaultDB string) (*options, error) {
	fs := pflag.NewFlagSet("stamp-files", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.SortFlags = false

	var (
		timeArg     string
		dbArg       string
		add         bool
		dryRun      bool
		hashAll     bool
		hideMissing bool
		noColor     bool
		help        bool
		showVersion bool
	)
	fs.StringVarP(&timeArg, "time", "t", defaultTimeString, "兜底时间")
	fs.StringVar(&dbArg, "db", defaultDB, "数据库文件")
	fs.BoolVar(&add, "add", false, "扫描并加入数据库")
	fs.BoolVarP(&dryRun, "dry-run", "n", false, "只预览")
	fs.BoolVarP(&hashAll, "hash-all", "a", false, "对所有文件计算哈希")
	fs.BoolVar(&hideMissing, "hide-missing", false, "不打印未找到明细")
	fs.BoolVar(&noColor, "no-color", false, "禁用颜色")
	fs.BoolVarP(&help, "help", "h", false, "显示帮助")
	fs.BoolVarP(&showVersion, "version", "V", false, "显示版本")

	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("选项错误：%w", err)
	}
	if help {
		return &options{help: true}, nil
	}
	if showVersion {
		return &options{version: true}, nil
	}

	positional := fs.Args()
	if len(positional) > 1 {
		return nil, fmt.Errorf("只能指定一个目录；时间和数据库请用 --time 和 --db")
	}

	dir := "."
	dirDefault := true
	if len(positional) == 1 {
		if positional[0] == "" {
			return nil, fmt.Errorf("目录不能为空")
		}
		dir = positional[0]
		dirDefault = false
	}
	if timeArg == "" {
		return nil, fmt.Errorf("时间不能为空")
	}
	if dbArg == "" {
		return nil, fmt.Errorf("数据库路径不能为空")
	}

	stamp, err := time.ParseInLocation(timeLayout, timeArg, time.Local)
	if err != nil {
		return nil, fmt.Errorf("时间格式错误：%q，应为 yyyy-MM-dd HH:mm:ss", timeArg)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("无法解析目录 %q：%w", dir, err)
	}
	if resolved, err := filepath.EvalSymlinks(absDir); err == nil {
		absDir = resolved
	}
	absDB, err := filepath.Abs(dbArg)
	if err != nil {
		return nil, fmt.Errorf("无法解析数据库路径 %q：%w", dbArg, err)
	}

	return &options{
		dir:         absDir,
		dirDefault:  dirDefault,
		stamp:       stamp,
		dbPath:      absDB,
		add:         add,
		dryRun:      dryRun,
		hashAll:     hashAll,
		hideMissing: hideMissing,
		noColor:     noColor,
	}, nil
}

func loadDatabase(dbPath string, warn io.Writer) (*database, error) {
	f, err := os.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("无法读取数据库 %s：%w", dbPath, err)
	}
	defer f.Close()

	db := newDatabase()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if lineNo == 1 {
			line = strings.TrimPrefix(line, "\uFEFF")
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "|")
		if len(parts) != 4 {
			db.warnf(warn, lineNo, "字段数应为 4，实际 %d，已忽略", len(parts))
			continue
		}

		nameKey := normalizeName(parts[0])
		if nameKey == "" {
			db.warnf(warn, lineNo, "文件名为空，已忽略")
			continue
		}

		md5v, ok := normalizeHash(parts[1], 32)
		if !ok {
			db.warnf(warn, lineNo, "MD5 格式无效（应为 32 位十六进制），已忽略")
			continue
		}
		sha1v, ok := normalizeHash(parts[2], 40)
		if !ok {
			db.warnf(warn, lineNo, "SHA1 格式无效（应为 40 位十六进制），已忽略")
			continue
		}
		sha256v, ok := normalizeHash(parts[3], 64)
		if !ok {
			db.warnf(warn, lineNo, "SHA256 格式无效（应为 64 位十六进制），已忽略")
			continue
		}
		if md5v == "" && sha1v == "" && sha256v == "" {
			db.warnf(warn, lineNo, "MD5、SHA1、SHA256 都为空，已忽略")
			continue
		}

		rec := &record{
			nameKey: nameKey,
			md5:     md5v,
			sha1:    sha1v,
			sha256:  sha256v,
			line:    lineNo,
		}
		db.byName[rec.nameKey] = append(db.byName[rec.nameKey], rec)
		db.count++
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取数据库 %s 失败：%w", dbPath, err)
	}
	return db, nil
}

func (d *database) warnf(w io.Writer, line int, format string, args ...any) {
	d.warnings++
	fmt.Fprintf(w, "[警告] 数据库第 %d 行：%s\n", line, fmt.Sprintf(format, args...))
}

func normalizeHash(s string, length int) (string, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return "", true
	}
	if len(s) != length {
		return "", false
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", false
	}
	return s, true
}

func normalizeName(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	if p == "" {
		return ""
	}
	p = path.Clean(p)
	p = strings.TrimPrefix(p, "/")
	p = path.Base(p)
	if p == "" || p == "." || p == "/" {
		return ""
	}
	return strings.ToLower(p)
}

func (r *record) matches(md5v, sha1v, sha256v string) bool {
	return (r.md5 != "" && strings.EqualFold(r.md5, md5v)) ||
		(r.sha1 != "" && strings.EqualFold(r.sha1, sha1v)) ||
		(r.sha256 != "" && strings.EqualFold(r.sha256, sha256v))
}

func matchDetail(r *record, md5v, sha1v, sha256v string) string {
	var parts []string
	if r.md5 != "" && strings.EqualFold(r.md5, md5v) {
		parts = append(parts, "MD5")
	}
	if r.sha1 != "" && strings.EqualFold(r.sha1, sha1v) {
		parts = append(parts, "SHA1")
	}
	if r.sha256 != "" && strings.EqualFold(r.sha256, sha256v) {
		parts = append(parts, "SHA256")
	}
	return strings.Join(parts, "+") + " 匹配"
}

func processFile(opts *options, db *database, fullPath, rel string) fileResult {
	nameKey := normalizeName(rel)
	candidates := db.byName[nameKey]
	if len(candidates) == 0 && !opts.hashAll {
		return fileResult{rel: rel, status: statusNotFound}
	}

	of, err := openAndHash(fullPath)
	if err != nil {
		return fileResult{rel: rel, status: statusError, err: err}
	}
	defer of.Close()

	if len(candidates) == 0 {
		return fileResult{rel: rel, status: statusNotFound}
	}

	var matched []*record
	for _, rec := range candidates {
		if rec.matches(of.md5, of.sha1, of.sha256) {
			matched = append(matched, rec)
		}
	}
	if len(matched) == 0 {
		return fileResult{rel: rel, status: statusMismatch, detail: "哈希不匹配"}
	}

	rec := matched[0]
	detail := matchDetail(rec, of.md5, of.sha1, of.sha256)
	if len(matched) > 1 {
		detail += fmt.Sprintf("；%d 条记录命中，采用第 %d 行", len(matched), rec.line)
	}

	times, ok := fileTimesFromInfo(of.info)
	if !ok {
		return fileResult{rel: rel, status: statusError, err: fmt.Errorf("无法读取文件时间戳")}
	}
	if times.equal(opts.stamp) {
		return fileResult{rel: rel, status: statusAlready, detail: detail}
	}
	if opts.dryRun {
		return fileResult{rel: rel, status: statusWouldStamp, detail: detail}
	}
	if err := of.setTimes(opts.stamp); err != nil {
		return fileResult{rel: rel, status: statusError, err: err}
	}
	if err := of.verifyTimes(opts.stamp); err != nil {
		return fileResult{rel: rel, status: statusError, err: err}
	}
	return fileResult{rel: rel, status: statusStamped, detail: detail}
}

func scan(opts *options, db *database, rep *reporter) summary {
	var sum summary
	_ = filepath.WalkDir(opts.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			sum.errors++
			rel := p
			if r, relErr := filepath.Rel(opts.dir, p); relErr == nil {
				rel = r
			}
			rep.errorf("%s：%v", rel, err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == opts.dir {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(opts.dir, p)
		if relErr != nil {
			sum.errors++
			rep.errorf("%s：无法计算相对路径：%v", p, relErr)
			return nil
		}
		if !d.Type().IsRegular() {
			sum.skipped++
			rep.skipped(rel, "非普通文件（符号链接等）")
			return nil
		}
		sum.total++
		res := processFile(opts, db, p, rel)
		switch res.status {
		case statusStamped:
			sum.stamped++
			rep.stamped(res.rel, res.detail)
		case statusWouldStamp:
			sum.stamped++
			rep.wouldStamp(res.rel, res.detail)
		case statusAlready:
			sum.already++
			rep.already(res.rel, res.detail)
		case statusMismatch:
			sum.mismatch++
			rep.mismatch(res.rel, res.detail)
		case statusNotFound:
			sum.notFound++
			if !opts.hideMissing {
				rep.notFound(res.rel)
			}
		case statusError:
			sum.errors++
			rep.errorf("%s：%v", res.rel, res.err)
		}
		return nil
	})
	return sum
}

func printSummary(w io.Writer, s summary, dryRun bool) {
	label := "打戳"
	if dryRun {
		label = "将打戳"
	}
	fmt.Fprintf(w, "\n共 %d 个文件：%s %d，已一致 %d，不匹配 %d，未找到 %d，错误 %d",
		s.total, label, s.stamped, s.already, s.mismatch, s.notFound, s.errors)
	if s.skipped > 0 {
		fmt.Fprintf(w, "，跳过非普通文件 %d", s.skipped)
	}
	fmt.Fprintln(w)
}

func runAdd(opts *options, stdout, stderr io.Writer) int {
	dbExists := false
	db := newDatabase()
	if _, statErr := os.Stat(opts.dbPath); statErr == nil {
		dbExists = true
		loaded, err := loadDatabase(opts.dbPath, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "错误：%v\n", err)
			return 2
		}
		db = loaded
	} else if !os.IsNotExist(statErr) {
		fmt.Fprintf(stderr, "错误：无法访问数据库 %s：%v\n", opts.dbPath, statErr)
		return 2
	}

	if opts.dirDefault {
		fmt.Fprintf(stdout, "未指定目录，使用当前目录：%s\n", opts.dir)
	}
	fmt.Fprintf(stdout, "目录：%s（递归）\n", opts.dir)
	if dbExists {
		fmt.Fprintf(stdout, "数据库：%s（%d 条记录）\n", opts.dbPath, db.count)
	} else {
		fmt.Fprintf(stdout, "数据库：%s（新建）\n", opts.dbPath)
	}
	if opts.dryRun {
		fmt.Fprintln(stdout, "模式：预览加入（不写数据库、不写时间戳）")
	} else {
		fmt.Fprintln(stdout, "模式：加入数据库（不写时间戳）")
	}
	fmt.Fprintln(stdout)

	seenDB := make(map[string]bool)
	for _, recs := range db.byName {
		for _, rec := range recs {
			seenDB[dedupKey(rec.nameKey, rec.md5, rec.sha1, rec.sha256)] = true
		}
	}
	seenScan := make(map[string]bool)

	rep := newReporter(stdout, stderr, opts.noColor)
	var sum addSummary
	var writeErr error
	app := &dbAppender{path: opts.dbPath}
	defer app.Close()

	_ = filepath.WalkDir(opts.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			sum.errors++
			rel := p
			if r, relErr := filepath.Rel(opts.dir, p); relErr == nil {
				rel = r
			}
			rep.errorf("%s：%v", rel, err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == opts.dir {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(opts.dir, p)
		if relErr != nil {
			sum.errors++
			rep.errorf("%s：无法计算相对路径：%v", p, relErr)
			return nil
		}
		if !d.Type().IsRegular() {
			sum.skipped++
			rep.skipped(rel, "非普通文件（符号链接等）")
			return nil
		}
		if strings.EqualFold(filepath.Clean(p), filepath.Clean(opts.dbPath)) {
			sum.skipped++
			rep.skipped(rel, "数据库文件本身")
			return nil
		}

		sum.total++
		of, err := openAndHash(p)
		if err != nil {
			sum.errors++
			rep.errorf("%s：%v", rel, err)
			return nil
		}
		name := filepath.Base(rel)
		key := dedupKey(normalizeName(name), of.md5, of.sha1, of.sha256)
		if seenDB[key] {
			sum.existing++
			rep.existing(rel)
			_ = of.Close()
			return nil
		}
		if seenScan[key] {
			sum.duplicates++
			rep.duplicate(rel)
			_ = of.Close()
			return nil
		}
		seenScan[key] = true

		line := fmt.Sprintf("%s|%s|%s|%s",
			name, strings.ToUpper(of.md5), strings.ToUpper(of.sha1), strings.ToUpper(of.sha256))
		if opts.dryRun {
			sum.added++
			rep.wouldAdd(rel)
		} else {
			if err := app.WriteLine(line); err != nil {
				writeErr = fmt.Errorf("写入数据库失败：%w", err)
				_ = of.Close()
				return writeErr
			}
			sum.added++
			rep.added(rel)
		}
		_ = of.Close()
		return nil
	})

	if err := app.Close(); err != nil {
		fmt.Fprintf(stderr, "错误：关闭数据库失败：%v\n", err)
		return 2
	}
	if writeErr != nil {
		fmt.Fprintf(stderr, "错误：%v\n", writeErr)
		return 2
	}

	printAddSummary(stdout, sum, opts.dryRun)
	if sum.added > 0 {
		if opts.dryRun {
			fmt.Fprintf(stdout, "预览：将写入 %d 条记录到 %s\n", sum.added, opts.dbPath)
		} else {
			fmt.Fprintf(stdout, "已写入 %d 条记录到 %s\n", sum.added, opts.dbPath)
		}
	} else {
		fmt.Fprintln(stdout, "没有新增记录")
	}
	return exitForErrors(sum.errors)
}

func exitForErrors(n int) int {
	if n > 0 {
		return 1
	}
	return 0
}

func printAddSummary(w io.Writer, s addSummary, dryRun bool) {
	label := "新增"
	if dryRun {
		label = "将新增"
	}
	fmt.Fprintf(w, "\n共 %d 个文件：%s %d，已存在 %d，重复 %d，错误 %d", s.total, label, s.added, s.existing, s.duplicates, s.errors)
	if s.skipped > 0 {
		fmt.Fprintf(w, "，跳过 %d", s.skipped)
	}
	fmt.Fprintln(w)
}

func dedupKey(nameKey, md5v, sha1v, sha256v string) string {
	return nameKey + "|" + strings.ToUpper(md5v) + "|" + strings.ToUpper(sha1v) + "|" + strings.ToUpper(sha256v)
}

type dbAppender struct {
	path string
	f    *os.File
}

func (a *dbAppender) WriteLine(line string) error {
	if a.f == nil {
		prefix, err := newlinePrefix(a.path)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		a.f = f
		if prefix != "" {
			if _, err := f.WriteString(prefix); err != nil {
				_ = f.Close()
				a.f = nil
				return err
			}
		}
	}
	_, err := a.f.WriteString(line + "\r\n")
	return err
}

func (a *dbAppender) Close() error {
	if a.f == nil {
		return nil
	}
	err := a.f.Close()
	a.f = nil
	return err
}

func newlinePrefix(dbPath string) (string, error) {
	info, err := os.Stat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if info.Size() == 0 {
		return "", nil
	}
	f, err := os.Open(dbPath)
	if err != nil {
		return "", err
	}
	last := make([]byte, 1)
	n, readErr := f.ReadAt(last, info.Size()-1)
	closeErr := f.Close()
	if readErr != nil && readErr != io.EOF {
		return "", readErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n != 1 {
		return "", fmt.Errorf("读取数据库末尾失败")
	}
	if last[0] == '\n' {
		return "", nil
	}
	return "\r\n", nil
}

type reporter struct {
	out      io.Writer
	errOut   io.Writer
	outColor bool
	errColor bool
}

func newReporter(out, errOut io.Writer, noColor bool) *reporter {
	return &reporter{
		out:      out,
		errOut:   errOut,
		outColor: useColor(out, noColor),
		errColor: useColor(errOut, noColor),
	}
}

func useColor(w io.Writer, noColor bool) bool {
	if noColor || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (r *reporter) paint(color bool, code, text string) string {
	if !color {
		return text
	}
	return code + text + "\x1b[0m"
}

func (r *reporter) stamped(rel, detail string) {
	fmt.Fprintf(r.out, "%s %s  %s\n", r.paint(r.outColor, "\x1b[32m", "[打戳]"), rel, detail)
}

func (r *reporter) wouldStamp(rel, detail string) {
	fmt.Fprintf(r.out, "%s %s  %s\n", r.paint(r.outColor, "\x1b[33m", "[将打戳]"), rel, detail)
}

func (r *reporter) already(rel, detail string) {
	fmt.Fprintf(r.out, "%s %s  %s\n", r.paint(r.outColor, "\x1b[36m", "[已一致]"), rel, detail)
}

func (r *reporter) added(rel string) {
	fmt.Fprintf(r.out, "%s %s\n", r.paint(r.outColor, "\x1b[32m", "[新增]"), rel)
}

func (r *reporter) wouldAdd(rel string) {
	fmt.Fprintf(r.out, "%s %s\n", r.paint(r.outColor, "\x1b[33m", "[将新增]"), rel)
}

func (r *reporter) existing(rel string) {
	fmt.Fprintf(r.out, "%s %s\n", r.paint(r.outColor, "\x1b[36m", "[已存在]"), rel)
}

func (r *reporter) duplicate(rel string) {
	fmt.Fprintf(r.out, "%s %s\n", r.paint(r.outColor, "\x1b[33m", "[重复]"), rel)
}

func (r *reporter) mismatch(rel, detail string) {
	fmt.Fprintf(r.out, "%s %s  %s\n", r.paint(r.outColor, "\x1b[33m", "[跳过]"), rel, detail)
}

func (r *reporter) notFound(rel string) {
	fmt.Fprintf(r.out, "%s %s\n", r.paint(r.outColor, "\x1b[90m", "[未找到]"), rel)
}

func (r *reporter) skipped(rel, detail string) {
	fmt.Fprintf(r.out, "%s %s  %s\n", r.paint(r.outColor, "\x1b[90m", "[跳过]"), rel, detail)
}

func (r *reporter) errorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(r.errOut, "%s %s\n", r.paint(r.errColor, "\x1b[31m", "[错误]"), msg)
}
