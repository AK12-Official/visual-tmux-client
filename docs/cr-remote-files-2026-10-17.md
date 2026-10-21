# 独立 CR：远程文件与 SSH 会话适配

审查日期：2026-10-17。范围：`deb831d` 至当前工作区的 `multi-host-support`、`unify-host-management`，重点 `remote_files.go`、`remote_paths.go`、`sftp_adapter.go`、`remote_sessions.go`、`remote_terminal.go` 及对应测试、连接生命周期调用链。只读业务代码，没有连接用户主机、操作生产服务或执行 tmux 命令。

结论：常规读写及冲突处理结构完整，但仍存在远程操作取消、暂存权限、元数据保留和路径别名并发问题。以下问题均来自可定位的实现证据；除类型解析外，本次没有运行集成复现，建议通过隔离 fixture 补充回归验证后修复。

## RF-1 — P1：SFTP 阻塞请求不会随取消或操作预算结束

- 定位：`hub/internal/host/remote_files.go:316`、`:335`、`:367`；`hub/internal/host/sftp_adapter.go:94`；`hub/internal/host/connection.go:319`、`:464`。
- 触发：SSH 握手成功后，SFTP 服务接受连接但不回复初始化、STAT、READ、WRITE 等请求，或网络黑洞让连接长期未关闭。
- 证据：`dialTransport` 在返回前清除网络 deadline，握手 cancellation watcher 随函数退出结束；`dialDedicated` 未绑定请求取消。`sftp.NewClient` 及 `pkg/sftp` 同步文件请求不接受 ctx；文件服务只用 `defer Close()`，必须等阻塞调用返回后才关闭。`OperationTimeout` 仅定义和设默认值，没有实际使用。
- 影响：关闭浏览器/取消 HTTP 请求也无法释放连接；写请求还占有路径锁。多次挂起可耗尽该主机默认 16 个连接名额，之后新操作无法取得连接，需关闭连接管理器或使连接失效恢复。
- 修复建议：给短时文件操作建立有限预算，并用取消回调主动关闭该操作专用 SSH 连接；长时终端生命周期独立处理，不能统一套 30 秒截止时间。
- 测试建议：fixture 完成 SSH 握手及 SFTP 初始化后扣留 STAT/READ 回复；取消 ctx，断言操作在有限时间返回、连接名额与路径锁释放，且其他主机不受影响。另覆盖初始化阶段停顿。
- 现有覆盖限制：`TestSFTPAdapterEffectiveCancellation` 只测预取消 ctx 的 `ReadDirBounded`。该方法确有关闭自身 channel 的逻辑，但不覆盖文件服务前置握手、RealPath/Stat 或 Read/Write。

## RF-2 — P1：私有文件内容先写入可被其他用户读取的暂存文件

- 定位：`hub/internal/host/remote_files.go:555`、`:596`、`:649`。
- 触发：在其他账号可遍历的远端目录保存一个 `0600` 私有文件，SFTP 默认创建权限允许其他用户读取（例如常见 022 umask）。
- 证据：暂存文件通过 `OpenFile(O_CREATE|O_EXCL|O_WRONLY)` 创建，没有指定私有权限，随后立即传入内容，仅上传完成后才 Chmod 为原文件权限。已核对当前依赖 `pkg/sftp@v1.13.11/client.go:668`：OPEN 不发送权限属性；仓库 fixture 使用的该库服务端默认创建权限为 `0644`（`server.go:507`）。
- 影响：上传期间敏感内容以 `.vtc-write-*` 文件暴露给可读取该目录的其他账号；连接中断留下暂存文件时暴露延续。文件名随机和前端隐藏不是权限隔离。
- 修复建议：在任何正文写入前把暂存句柄设为 `0600`，并检查失败；最好在创建时直接指定限制权限。完整上传、校验后再设置最终元数据并提交。
- 测试建议：隔离 SFTP fixture 使用可遍历目录、0644 默认创建权限，暂停 body 读取；断言暂存文件始终为 0600，并在权限设置失败时不写入任何正文。

## RF-3 — P2：保存恢复过时权限，并静默忽略权限/属主保留失败

- 定位：`hub/internal/host/remote_files.go:502`、`:609`、`:649`。
- 触发 A：上传过程中管理员把目标从 0644 改为 0600；chmod 不改变文件内容/mtime，现有冲突检查仍通过。
- 触发 B：服务器拒绝暂存文件 Chmod/Chown，但允许 rename。
- 证据：权限、UID、GID 从上传前的 `targetFi` 捕获；上传后已经读取 `curTargetFi`，却未用它刷新元数据。随后 Chmod/Chown 两个错误都被 `_ =` 丢弃。
- 影响：A 保存撤销管理员刚收紧的权限；B 返回保存成功但文件权限/属主已变化，可能造成执行权限丢失或权限放宽。规格 `openspec/specs/file-manager/spec.md:701` 明确要求保存当前权限并报告保留失败。
- 修复建议：从提交前检查获得的元数据保留当前权限；区分可接受的属主限制与必须报告的失败，禁止静默成功。
- 测试建议：利用阻塞 reader 在上传中 chmod；验证最终权限为更新后的权限。注入 SETSTAT 拒绝，验证错误与原文件保留策略。
- 现有覆盖：`remote_files_test.go:888` 只验证权限从头至尾不变的 0755 文件。

## RF-4 — P2：符号链接目录别名绕过 Hub 自身写入互斥

- 定位：`hub/internal/host/remote_files.go:444`、`:464`、`:485`、`:644`。
- 触发：同一主机 `/alias` 为 `/real` 的目录符号链接，两窗口同时编辑 `/alias/a.txt` 和 `/real/a.txt`。
- 证据：协调器在解析 canonicalParent 之前按词法路径 `cleaned` 加锁，`pathsConflict` 只比较字符串或父子前缀；两条路径不冲突。后续二者得到同一 canonical target，均可能先完成相同 digest 的检查，再先后 PosixRename。
- 影响：两个保存均返回成功，后者覆盖前者，遗漏本应由 Hub 内部序列化保证的冲突。这并非规格已免责声明中的任意外部进程竞态，而是本进程通过静态目录别名发出的两次写入。
- 修复建议：协调器使用解析后的规范目标及规范父路径，并保持 rename/delete/write 一致的锁语义，处理解析后取得锁的重校验。
- 测试建议：设置稳定目录别名；两份 service 使用同一主机，在 beforePosixRename hook 处同步两次提交。修复后应只允许一方提交，另一方返回冲突；同样覆盖 alias 写入与 real 删除。

## RF-5 — P2：SFTP 类型掩码错误，普通文件都触发额外串行 STAT

- 定位：`hub/internal/host/sftp_adapter.go:418`、`:422`；消费点 `hub/internal/host/remote_files.go:263`。
- 证据：判断使用 `perm & 0120000 != 0` 而不是 `(perm & 0170000) == 0120000`。普通文件的 `0100000` 与该掩码相交，所以都被标为 symlink；socket 同时被标为 directory/symlink。
- 本次实际验证：将原样 `parseNameEntries` 和依赖常量/结构提取至 `/tmp/vtc-cr-sftp-parser.go`，只运行纯 Go 解析，没有网络或 tmux。输出：

```text
type=0100644 IsDir=false IsLink=true
type=040755 IsDir=true IsLink=false
type=0120777 IsDir=false IsLink=true
type=0140600 IsDir=true IsLink=true
```

- 影响：普通文件目录列表为每个条目追加一个串行远程 STAT。默认上限 1000 个条目时，额外延迟近似为 1000 次网络往返；100ms RTT 下约 100 秒，实际值取决于网络和服务端。socket 类型也会出现错误分类路径。
- 修复建议：先提取 POSIX 文件类型掩码，再按完整类型值匹配；为所有常见类型建立表驱动解析测试。
- 测试建议：普通文件、目录、符号链接、socket、FIFO、字符/块设备分别编码 NAME 响应；列表测试计数 STAT 请求，普通文件不应进入链接解析路径。

## 测试证据与审查边界

- 本次没有重跑整个 Go 集成测试，也未把历史全绿结果当作本次证据。已实际运行的只有以上独立解析复现。
- `sftp_fault_injection_test.go` 名称及注释中的 “Real OpenSSH” 实际使用 `testutil.NewDisposableSSHServer`（Go SSH/SFTP fixture），不代表通过真实 OpenSSH 服务端兼容测试。
- 同文件的粗粒度冲突测试从本地 os.Stat 取 UnixMilli，但 SFTP v3 返回秒级时间，且没有设置 expected Digest。出现 ErrConflict 可能来自毫秒精度不同；这条测试不能单独证明“相同 mtime、内容改变”的 digest 检测成立。应固定整秒时间并明确传入读到的原内容摘要。
- 已阅读远程命令 POSIX 编码、exact tmux target、专用终端连接和终端关闭调用链；没有验证真实 OpenSSH、真实终端交互、外部服务器并发替换或断网行为。未把这些覆盖限制当作已确认缺陷。
- 连接池 metadata revision 与 connection revision 混用的问题已交由另一名独立后端 reviewer 汇总，不在本报告重复计数。
