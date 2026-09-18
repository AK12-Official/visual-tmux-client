## Why

v0.3.1 到 a863232 之间的代码评审在文件管理功能上发现了 10 个问题，其中 3 个被判为发布阻断级。三个阻断项有一个共同形状：**一份声明与其实际保证不符**。

- 保存的并发控制声称「以开始时的文件为准」，但强制覆盖这条路径跳过了最后一道检查，于是传输窗口内的改名会被回滚成一个「旧名字下重新长出来的文件」；前端又只按 tab id 归并结果，于是把这个写入记在新名字上，报告为「已保存」。
- 二进制判定声称「二进制文件不会作为可编辑文本打开」，但只看前 8192 字节；开头是文本、后面含 NUL 或非法 UTF-8 的文件会被当作文本解码，用户保存时把 U+FFFD 写回，原始字节永久丢失。
- `roots` 被描述为访问边界，但设计文档已经明确记录了「检查用字符串路径、执行也用它」这一 TOCTOU 残余风险——即产品文档的承诺比设计文档的承认更强。

其余七项是同一类不一致的较轻版本：列举的成本不受 `max_dir_entries` 约束（只约束响应）、读取的长度与校验的长度可能不一致、目录树不跟随指向目录的软链、FIFO 能无限挂住请求、毫秒级 mtime 无法区分同毫秒内的两次修改、pane 工作目录被 `TrimSpace` 抹掉真实字符、切换 tab 会销毁编辑器的 undo 历史（与源码注释「每个打开文件一个实例」相矛盾）。

## What Changes

**写入绑定到它开始时的那个文件**

- 强制覆盖不再跳过最后一道检查：提交前始终确认目标仍是本次写入开始时的那一个（`os.SameFile`），file 被改名/删除则拒绝；开始时名字是空闲的，则要求它现在仍然空闲。
- 观察到的修改时间改为双精度：毫秒（JSON 可精确表示）与纳秒（以不透明十进制字符串传输，客户端原样回带）。比较优先使用纳秒，同毫秒内的两次修改因此是两次修改。
- 前端 `settleSave` 按「写入时命名的路径」而非仅按 tab id 归并应答：路径已改变则**不**标记为已保存，而是提示用户「文件已改名，请再存一次」。

**读取的判定与边界要说真话**

- 文本/二进制判定扫描**整个文件**：读到第一个决定性字节即停（NUL 或非法 UTF-8），因此多数二进制文件的开销与原来相当；只有通篇像文本的文件才会被读到底。
- 只读普通文件；`O_NONBLOCK` 打开后按描述符判定类型，命名管道不再可能挂住请求 goroutine。
- 响应体严格等于已校验并已通告的长度（`io.NewSectionReader`），文件在传输中增长既不会越限也不会与 `X-File-Size` 不一致。
- 列举按 `max_dir_entries` 分批读取并在每批之间检查 ctx；成本由列举上限决定而不是目录大小决定。代价是：被截断的结果不再是字典序最靠前的那些条目，这一点写进了 spec。
- 目录树中的软链按目标描述（指向目录的软链 `is_dir: true`），但只在边界允许时跟随；指向 root 外的软链不会泄漏目标的 size/mtime。

**把 `roots` 的说法改成它实际做的事**

- `openspec/specs/file-manager/spec.md`、两个 README 明确写出：`roots` 约束调用方能**指定什么**，不构成对同机其他进程的隔离；残余窗口需要全程 fd 相对访问才能关闭，本 hub 尚未实现。
- 选择记录而不是立刻实现，理由与探针数据写在 `design.md` 的决策 1 里。

**其余**

- pane 工作目录只去掉 tmux 追加的那一个换行，不再 `TrimSpace`。
- 文件管理器为每个打开的文件各保留一个编辑器实例（`v-show` 切换可见性，按 tab id 而非路径作 key），undo 历史不再随切换销毁。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `file-manager`：访问边界的声明范围（`roots` 是调用方边界而非隔离保证）；目录列举的读取成本与软链描述；读取的整文件判定、仅普通文件、响应体长度；并发写入的提交前身份检查与修改时间精度；会话工作目录的原样返回；编辑器状态保留与「保存在途改名」的归并规则。

## Impact

- `hub/internal/files/service.go`：`Write` 增加 `origin` 身份检查与提交前重检；`Read` 改为只读普通文件并做整文件判定；`List` 改为分批读取 + 显式排序 + 软链解析；`looksBinary`/`binarySample` 换成流式 `textScan`。
- `hub/internal/files/model.go`：`ReadResult` 增加 `MtimeNanos`；新增 `WriteResult` 与 `ExpectedMtime`（`Write` 的签名随之变化）。
- `hub/internal/transport/http/files.go`、`dto.go`：新增 `X-File-Mtime-Nanos` 响应头与 `expected_mtime_nanos` 查询参数，写响应新增 `mtime_nanos` 字符串字段；读路由改用 `io.NewSectionReader`。
- `hub/internal/tmux/panes.go`：`PaneWorkingDirectory` 只去一个行尾换行。
- 前端 `hub/web/src/files/api.ts` 新增 `Stamp`（`{millis, nanos}`），`writeFile`/`readFile`/`FileContents` 随之变化；`tabs.ts` 的 `OpenFile.mtime` 换成 `stamp`，新增 `settleSave`/`editable`/`presentation`；`FileManagerOverlay.vue` 为每个打开文件保留编辑器实例；`CodeEditor.vue` 新增 `active` prop 并在重新可见时重新测量。
- 线协议是**增量**的：旧客户端不发 `expected_mtime_nanos` 时按毫秒比较；未收到 `X-File-Mtime-Nanos` 时前端退回毫秒比较。
