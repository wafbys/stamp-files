# stamp-files

按数据库记录强制设置文件时间戳的 Windows 命令行工具。

## 作用

1. `--add` 模式扫描目录，计算每个普通文件的 MD5、SHA1、SHA256，把记录追加到数据库。
2. 默认模式递归扫描目录，按文件名查数据库。
3. MD5、SHA1、SHA256 **任一命中**，就把文件的创建、修改、访问时间都设为 `-t/--time` 指定的时间。
4. 哈希不匹配或数据库中没有记录的文件不动。

默认时间：`2026-11-11 11:11:11`。

## 环境

- Windows 10/11 x64。
- 文件系统建议 NTFS；FAT/exFAT 的时间精度较低，回读校验可能失败。
- 支持超过 `MAX_PATH`（260 字符）的长路径。
- 构建需要 Go 1.27+。

## 用法

```powershell
stamp-files [选项] [目录]
```

目录是唯一的位置参数，默认 `.`（当前目录），始终递归子目录。
时间和数据库只用选项指定，不做位置参数。

| 短 | 长 | 说明 |
|---|---|---|
| `-t` | `--time <时间>` | 所有命中文件统一使用的时间，格式 `yyyy-MM-dd HH:mm:ss` |
| | `--db <文件>` | 数据库路径，默认 exe 同目录 `stamp-db.txt` |
| | `--add` | 扫描目录并把文件哈希追加到数据库，不写时间戳 |
| `-n` | `--dry-run` | 只预览，不写时间戳、不写数据库 |
| `-a` | `--hash-all` | 打戳模式下对所有文件计算哈希（默认只算有候选的） |
| | `--hide-missing` | 打戳模式下不打印「未找到」明细 |
| | `--no-color` | 禁用颜色，也支持 `NO_COLOR` 环境变量 |
| `-h` | `--help` | 显示帮助 |
| `-V` | `--version` | 显示版本 |

选项区分大小写：`-t` 是时间，`-T` 不存在；`--time` 有效，`--Time` 报未知选项；`-V` 是版本，`-v` 不存在。
Windows 文件路径本身不区分大小写，数据库里的文件名匹配也做了小写归一。

### 示例

```powershell
stamp-files
stamp-files D:\Archive
stamp-files -n D:\Archive
stamp-files D:\Archive -n
stamp-files D:\Archive --time "2026-11-11 11:11:11"
stamp-files --db D:\db.txt D:\Archive
stamp-files --db=D:\db.txt -n D:\Archive
stamp-files --add D:\Archive
stamp-files --add -n D:\Archive
stamp-files -- -以横线开头的目录
```

选项可以放在目录前后，二者不互斥。`--` 用于以横线开头的目录名。

## 建立数据库

```powershell
stamp-files --add D:\Archive
stamp-files --add -n D:\Archive      # 只预览，不创建/不写入
```

`--add` 的行为：

- 递归扫描目录下的所有普通文件，计算 MD5、SHA1、SHA256。
- 数据库不存在时自动创建；存在时追加新记录。
- 去重键是「文件名 + 三个哈希」：完全相同的内容不会重复写入；同一轮扫描中的后续副本标记为「重复」。
- 同名但内容不同的文件会写成多条记录，匹配时按哈希区分。
- 文件名存的是 basename，不存相对路径。
- 数据库文件本身如果位于扫描目录内，会被跳过。
- 不修改任何文件的时间戳。
- `--add` 不使用 `-t/--time`、`-a/--hash-all`、`--hide-missing`；`-n/--dry-run` 有效。

典型流程：

```powershell
# 1. 信任这个目录，把文件加入数据库
stamp-files --add D:\Archive

# 2. 按数据库打戳
stamp-files D:\Archive
```

默认打戳模式要求数据库已存在且至少有一条有效记录；否则退出码 2。

## 数据库格式

UTF-8 文本，每行一条：

```
文件名|MD5|SHA1|SHA256
```

示例：

```
VMware.exe|A1B2C3D4E5F6...|A1B2C3D4E5F6...|A1B2C3D4E5F6...
old.exe|A1B2C3D4E5F6...||
```

规则：

- 空行跳过；以 `#` 开头的行视为注释。
- 字段数必须为 4。
- MD5 为 32 位十六进制，SHA1 为 40 位，SHA256 为 64 位；大小写不敏感。MD5 仅用于文件匹配，不作安全校验。
- 三个哈希可以只填一个或两个；全为空的行会被忽略。
- 文件名不区分大小写；如果字段里带路径，只取最后一段作为文件名。
- 同一文件名可以有多条记录；取第一条哈希命中的，命中多条时在输出中提示行号。
- 打戳模式只读数据库；`--add` 模式会追加记录。
- 旧格式 `路径|SHA1|SHA256|时间戳` 已不兼容，需要改成 `文件名|MD5|SHA1|SHA256`。

## 匹配规则

```
文件 → 文件名（basename，不区分大小写）
  ├─ 有同名记录：逐条比对 MD5 / SHA1 / SHA256
  │                任一命中 → 打戳
  │                全不命中 → 跳过
  └─ 没有同名记录 → 未找到
```

不再按相对路径匹配。文件名相同但内容不同的文件靠哈希区分，不会误打戳。
未命中候选的文件默认不计算哈希；需要全量计算时用 `-a/--hash-all`。

## 时间戳

- 三个时间一起设置：CreationTime、LastWriteTime、LastAccessTime。
- 时间按本地时区解析，写入 NTFS 的 UTC 时间。
- 打开文件后先把 atime 冻结为 `0xFFFFFFFF`，再读取计算哈希，最后统一设置三个时间；避免工具自己的读取覆盖访问时间。
- 设置后立即回读校验，不一致按错误处理。
- 打戳后如果再次读取文件，Windows 可能按访问时间策略更新 atime；这是系统行为，不是工具没写。
  本项目只保证打戳当时三个时间正确，不追求 atime 永久不变，也不检测、不恢复后续变化。
  CreationTime 和 LastWriteTime 不会因为读取而改变。

## 输出与退出码

- 正常结果走 stdout，警告/错误走 stderr。
- 打戳状态：`[打戳]`、`[将打戳]`、`[已一致]`、`[跳过]`、`[未找到]`、`[错误]`。
- 加库状态：`[新增]`、`[将新增]`、`[已存在]`、`[重复]`、`[错误]`。
- 末尾汇总：总数、打戳/新增、已一致/已存在、重复、不匹配、未找到、错误。
- 颜色自动检测终端，支持 `NO_COLOR` 和 `--no-color`。
- 退出码：
  - `0`：正常完成（包括全部未找到、没有新增）。
  - `1`：有文件级错误（打开、读取、写时间、扫描失败）。
  - `2`：参数、目录或数据库错误。

## 构建与安装

```powershell
cd C:\Users\YF\Src\stamp-files
go build -trimpath -ldflags "-s -w" -o C:\Users\YF\Bin\stamp-files.exe .
```

`C:\Users\YF\Bin` 已在用户 PATH 中，任意目录可直接运行 `stamp-files`。

首次使用可以先跑 `stamp-files --add D:\Archive` 生成 `C:\Users\YF\Bin\stamp-db.txt`；也可以手工按上面的格式创建。

## 测试

```powershell
go vet ./...
go test ./...
```

测试覆盖：

- 参数解析、默认当前目录、选项放在目录前后、大小写敏感。
- 数据库 BOM/CRLF、注释、坏行、单哈希、重复文件名、旧格式拒绝。
- 文件名匹配、任一哈希命中、哈希不匹配跳过。
- `--add` 创建数据库、追加、去重、同名不同内容、同名同内容、跳过数据库自身、dry-run、无文件、父目录不存在、只读文件、中文文件名。
- dry-run 不改时间、重复运行幂等。
- 中文路径、只读文件、超过 260 字符的长路径。
- 三个时间精确写入并回读。
- 帮助、版本、退出码。

## 设计参考

- [clig.dev](https://clig.dev/)：位置参数只放主操作数，可选配置用 flag；`-n/--dry-run`、`-h/--help` 等标准名。
- [POSIX Utility Syntax Guidelines](https://pubs.opengroup.org/onlinepubs/9699919799/basedefs/V1_chap12.html)：选项、操作数、`--` 终止符。
- [GNU Coding Standards](https://www.gnu.org/prep/standards/html_node/Command_002dLine-Interfaces.html)：长选项与短选项对应、`--help`/`--version`。
