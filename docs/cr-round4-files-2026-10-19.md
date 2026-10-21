# 第四轮发布级 CR：远程文件

日期：2026-10-19。审查当前工作区第三轮修复，业务代码只读，未连接真实 SSH 或操作生产服务、tmux。

## 关闭矩阵

| 项目 | 结论 | 证据 |
|---|---|---|
| R3-4 慢 HTTP body 的预算不联通 | 原问题关闭 | `http/files.go:293` 在 HTTP 边界创建 WithTimeout，并把同一 ctx 传入 Write；远程服务 opContext 遇已有 deadline 返回原 ctx。因此到期确实触发网络 read deadline |
| R3-5 活动暂存标记发布过晚 | 原问题关闭 | `remote_files.go:793–819` 先登记本地及全局 active staging，再 AddPendingCleanup；登记失败会撤销 active 标记、清理文件并返回错误，不再忽略 tracking 错误 |

实际运行：`go test ./internal/transport/http -run '^TestFilesWriteSlowBodyTimeout$' -count=1`，通过，输出耗时 0.106s。这条测试使用真实 httptest TCP 连接，发送部分 Content-Length 正文后停住，证明 HTTP deadline 能唤醒实际 net/http body 读取。FileService 使用 mock，故不是 SSH/SFTP 端到端测试。

`TestStagingPublicationOrderProtectsAgainstConcurrentCleanup` 在持久化记录发布之后同步调用清理，检查暂存仍存在及最终内容正确，覆盖原错误交错；`TestAddPendingCleanupFailureAbortsAndUntracks` 覆盖登记失败。两者本轮只审阅，没有重新运行，不将它们写为本轮执行通过。

## F4-1 — P2：取消 watcher 可能在正常响应结束后设置过期读取期限

定位：`hub/internal/transport/http/files.go:293–305`。

当前正常请求退出时，defer 先 close(stopWatcher)，再 cancelWrite。若 watcher 此前未得到调度，此时 stopWatcher 和 writeCtx.Done 两个分支都可读，Go select 可以选择取消分支，继续执行 SetReadDeadline(time.Now())。handler 没有等待 watcher 退出。

这意味着正常保存也可能在 handler 返回之后操作已经交还 net/http 管理的 ResponseController；若该 keep-alive 连接已进入下一请求读取，迟到的过期 deadline 会让后继请求读取超时。不是对慢上传修复的重复报告，而是新增异步副作用缺乏生命周期同步的问题。成立所需交错为：正常 handler 完成 → 两个信号均关闭 → net/http 开始下一次读取 → watcher 执行过期 deadline。

证据等级：源码确定存在该可执行交错；本轮没有用真实网络复现该调度，因此不声称观察到线上失败或发生概率。现有慢 body 测试设置 `Connection: close`，不覆盖同连接下一请求。

建议：handler 返回前确定 watcher 已停止；将成功结束与超时取消的原因区分，不能在本函数正常 defer cancel 后误当作业务超时。增加正常快速保存后同一 TCP 连接连续发起请求的测试，包含延迟发送下一请求正文，并验证没有迟到 deadline。不要仅把 SetReadDeadline 错误继续忽略。

## 事务、清理和 nolint 复核

- active 标记先于数据库记录发布，使清理获得可见记录时该文件已经受保护；快照可能保守跳过刚完成文件，但不会产生原“可见记录尚未受保护”的误删窗口。
- AddPendingCleanup 的错误现在会中止正文传输；已移除原先关键 tracking 错误静默丢弃的问题。失败收尾 Close/Remove 的 nolint 只处理已报错路径，范围合理；网络失败时这些动作仍不保证完成，这是远程失败的真实限制。
- 原有元数据读取、Chown 检查和 owner-aware canonical 锁未观察到本轮引入的高置信事务回归。不将此前已修问题再次计入。
- 生产清理仍在控制连接建立后触发。本轮没有实际执行连接重建→清理的集成测试；不能把手工调用 cleanup 的单测解释为生产调度已验收。

结论：两个指定阻塞问题的原实现缺陷均已修复；保留一项 HTTP watcher 生命周期 P2（静态并发证据，未复现网络表现）。本轮没有真实 OpenSSH 兼容性或完整端到端断网测试结果。
