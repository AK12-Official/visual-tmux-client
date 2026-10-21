# 发布前独立 CR：远程文件（第二轮）

审查日期：2026-10-18；对象：当前工作区修复版本。审查业务代码只读，未连接真实 SSH、未操作生产服务、未执行 tmux 命令。本轮实际运行一项从当前源码提取的纯 Go 锁协调器实验，没有运行端到端或整个测试套件。

## 上轮问题关闭矩阵

| 上轮项 | 结论 | 当前证据 |
|---|---|---|
| RF-1 SFTP 阻塞不会取消 | 主要远端请求路径修复；上传正文读取仍有缺口 | `remote_files.go:195` 新增有限 opContext；`connection.go:508` 后新增 ctx watcher 主动关闭 dedicated transport，覆盖 SFTP 初始化和同步请求。见 RRF-3 的输入端阻塞 |
| RF-2 私有正文写入宽权限暂存文件 | 原问题关闭（静态验证） | `remote_files.go:588` 在复制正文前对句柄 Chmod(0600)，失败即停止；尚无新增专门回归测试证明真实服务端行为 |
| RF-3 旧权限及静默保留失败 | 部分关闭 | `remote_files.go:676` 使用提交前元数据，Chmod 错误已检查；Chown 错误仍忽略，见 RRF-1 |
| RF-4 目录别名绕过互斥 | 常规别名写入互斥已补；新增嵌套锁问题 | Write/Create/Delete/Rename 均追加 canonical 锁，但取得方式可能与本操作已持有的词法锁冲突，见 RRF-2 |
| RF-5 文件类型错误 | 关闭（静态验证） | `sftp_adapter.go:418` 已改为 `switch perm & 0170000` 精确匹配文件类型，普通文件不再被标成 symlink |

关闭表示原故障代码路径已改变，不表示已经通过真实 OpenSSH 兼容验收。

## RRF-1 — P2：owner/group 保留失败仍静默成功

定位：`hub/internal/host/remote_files.go:689`。

当前代码仍 `_ = adapter.Client().Chown(stagePath, curUid, curGid)`。当 SSH 账号可以替换共享目录中的文件，但没有权限恢复目标 owner/group，服务器拒绝 Chown 后仍执行 PosixRename，并向用户报告保存成功。内容虽然保存，文件归属或所属组却改变，后续共享权限可能失效。此处仅指出未修复的 Chown 部分；当前权限重读及 Chmod 错误检查已修，不重复报错。

规格 `openspec/specs/file-manager/spec.md:701` 要求保留失败被报告。“where permitted” 可以允许无法保留，但不能把失败静默丢弃。建议明确结果：要么提交前拒绝并保留原文件，要么以可识别的降级结果告知用户，不应简单忽略。测试应让隔离 SFTP fixture 对 chown 类型 SETSTAT 返回 permission denied，而 chmod/rename 正常，断言不会无提示成功。

## RRF-2 — P2：追加规范路径锁会与本操作已有锁自冲突

定位：`hub/internal/host/remote_files.go:1108`、`:1141`；同类结构位于 `:464` / `:511`、`:860` / `:909` 和 Rename 的 `:981` / `:1057`。

`pathCoordinator` 不识别锁的所有者。新实现先持有词法路径锁，再取得 canonical 路径锁，后者与前者存在父子关系时，会等待自己释放锁，直到操作预算耗尽。

确定例子：远端 `/home/u/self -> /home/u`，调用 Delete(`/home/u/self/self`, false)。词法路径 `/home/u/self/self` 先加锁；父目录解析后目标为 `/home/u/self`，此目标是原路径的祖先；第二次加锁与自己的 active 项冲突。该删除本应删除 self 符号链接，而不是等待超时。路径不含 `..`、不涉及禁用目录，也不需要并发外部进程。

本轮实际实验：原样提取当前 `pathCoordinator` 至 `/tmp/vtc-release-lock.go`，依次取得上述两个锁，使用 50ms deadline，输出：

```text
canonical acquisition while own lexical child is locked: context deadline exceeded
```

此外分阶段取得多路径锁缺乏全局顺序，两个交叉 rename 也可能互相等待。建议将一次操作的规范锁集合原子取得，或引入明确的 owner/reentrancy 及锁升级协议；不能只跳过字符串完全相同的锁。补充上述单请求自冲突和交叉 rename 测试，确认仍保有 canonical 冲突保护。

## RRF-3 — P2：操作预算只关闭 SFTP，不能中止正在等待 HTTP 上传正文的保存

定位：`hub/internal/host/remote_files.go:627`（io.Copy）；调用来源 `hub/internal/transport/http/files.go:253`；HTTP server 配置 `hub/internal/app/app.go:149`。

Write 的有限 ctx 已绑定专用 SSH 连接，但 `io.Copy(stageFile, io.LimitReader(body, declaredSize+1))` 仍可能阻塞在输入端 `body.Read`。HTTP handler 直接传入请求 body；server 只有 ReadHeaderTimeout 和 IdleTimeout，没有正文读取期限。客户端发完请求头和部分正文后停住、保持 TCP 连接时，30 秒后 SSH 虽关闭，正在等待的 body.Read 不会因此返回，路径锁、handler 和暂存收尾逻辑仍无法释放。它与已修复的“远端不回复 SFTP 包”是不同阻塞端。

建议在 HTTP 边界将正文读取 deadline 与操作预算联动，并确保取消实际打断读取；不要仅在读取前轮询 ctx。测试使用真实 httptest 网络请求，声明合法 Content-Length 后只发送部分 body，验证预算结束会释放对应路径锁，随后另一保存可以开始。普通 `bytes.Reader` 测试无法覆盖该问题。

## RRF-4 — P2：暂存恢复清理只有实现和测试，没有生产调用入口

定位：`hub/internal/host/remote_files.go:767`（RunPendingCleanups）；`hub/internal/host/runtime.go` 的运行时构造；`remote_files.go:594`（记录待清理项）。

全仓搜索 `RunPendingCleanups` 仅找到方法定义及 `remote_files_test.go:1110` 的显式测试调用。启动、远端连接恢复、运行时构造均未调用。网络故障后正常 defer 无法移除 staging 时，虽然记录入库，后续恢复或重启并不会执行 promised later safe attempt。重复失败累积隐藏远端文件及 DB 记录，消耗磁盘，并长期保留编辑内容。

建议接入有预算的重连后或后台清理调度。接入前必须过滤仍在写入中的 staging：现有方法没有查询 `s.staging`，且不同请求创建不同 service，直接无条件加入请求路径可能删除另一个活动上传。回归应覆盖中断写入→重启/重连→自动清理，并验证活动 staging 不受影响；不能再仅手动调用 cleanup 方法作为端到端证据。

## 架构与验证评价

修复保持每次文件操作独立 SSH transport，取消不会直接影响另一操作；正文前收紧暂存权限、提交前重读元数据、精确 POSIX 类型解析都改善了原实现。需要继续处理的是锁协调器的原子协议和输入/输出两端的取消边界。

当前变更没有修改 `remote_files_test.go` 或 `sftp_adapter_test.go`，因此上轮 RF-1 至 RF-5 的新故障场景未获得对应新增回归保护。既有 `TestSFTPAdapterEffectiveCancellation` 仍只验证预取消 ReadDirBounded，不能证明 STALLED STAT/WRITE 或 HTTP body 的取消。`sftp_fault_injection_test.go` 的 “Real OpenSSH” 仍是 Go SSH/SFTP fixture；没有真实 OpenSSH 结果。本轮不把历史测试全绿换算成上述新增场景已通过。

建议在 RRF-1 至 RRF-4 被处理且增加相应隔离回归前，保留发布验收未完成状态。未发现证据不等于不存在其他缺陷；本轮没有评估真实网络延迟、远端 ACL/xattr 或外部进程 path replacement 竞态。
