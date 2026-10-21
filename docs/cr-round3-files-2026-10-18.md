# 第三轮发布级 CR：远程文件

日期：2026-10-18。范围：当前工作区 RC-7～RC-10 的修复及关联取消、锁、清理代码和测试。业务代码只读，未操作实际服务、SSH 或 tmux。本轮结论基于源码与 Go 标准库调用链核验，没有执行完整测试套件。

## 关闭矩阵

| 问题 | 状态 | 证据 |
|---|---|---|
| RC-7 Chown 静默失败 | 关闭，静态验证 | `remote_files.go:899–932` 先判断是否需变更 owner/group，需要时检查 Chown 错误并在 rename 前返回；未见针对拒绝 Chown 的新增隔离测试 |
| RC-8 canonical 锁自冲突 | 原反例关闭 | `LockHandle` 按 owner 排除自身锁，新增 `TestPathCoordinatorLockHandleSelfConflict` 覆盖原路径；同时有 `TestPathCoordinatorDeadlockDetection` 检查锁环，未复报原问题 |
| RC-9 HTTP body 超时不解阻 | 未关闭 | 新 HTTP watcher 与真正超时的 service ctx 不同；详见 F3-1 |
| RC-10 清理没有生产入口 | 接线缺口关闭，但新增并发安全问题 | `connection.go:450–467` 在 control connection 建立后触发有预算清理；活动上传保护的发布顺序不安全，见 F3-2 |

## F3-1 — P2：内部操作超时不会触发 HTTP 读取 deadline，上传仍可一直挂起

定位：`hub/internal/transport/http/files.go:283–293`、`hub/internal/host/remote_files.go:346–355`、`:792–813`。

HTTP 层创建 `writeCtx := context.WithCancel(r.Context())`，watcher 仅在它取消时设置网络 read deadline。远程文件服务再从 writeCtx 创建自己的有限 opContext。子 context 的 deadline 到期不会取消父 writeCtx，所以发生正常的 30 秒操作超时时，HTTP watcher 根本不会执行 `SetReadDeadline`。

service 新增的另一个 watcher 会调用 body.Close，但这不足以打断 Go HTTP 请求正文的阻塞读取。已核对本机 `/usr/local/go/src/net/http/transfer.go:840` 和 `:983`：`body.Read` 在整个底层读取期间持有 `b.mu`，`body.Close` 也要先取得同一个锁。`http.MaxBytesReader` 的 Close 最终仍调用该 body.Close。客户端保持 TCP 连接而停止发送声明长度内剩余正文时，Read 等网络，Close 等 Read，内部 op deadline 仅关闭 SFTP transport，路径锁及 handler 继续滞留。

建议把同一个预算传到 HTTP 网络读取边界，或者由服务向调用方提供实际触发超时的取消机制；先设置连接 read deadline 再 Close。修复时不要只给一个自定义“Close 会唤醒 Read”的 fake body 测试，它不同于 net/http 实现。应使用 httptest 的真实 TCP 连接，发送完整请求头及部分正文后暂停，在小于一秒的测试预算后验证请求/锁释放。

本轮未找到上述真实网络停顿回归测试。该项是上轮问题未完全修复，不是新猜测。

## F3-2 — P2：清理记录先发布、活动保护后发布，可删除正在上传的暂存文件

定位：`hub/internal/host/remote_files.go:774–787`、`:1218–1243`；生产触发：`hub/internal/host/connection.go:450–467`。

createStagingFile 的顺序为：创建文件并收紧权限 → AddPendingCleanup 把记录入库 → 添加本地 staging map → trackActiveStaging。RunPendingCleanups 则先从数据库取得记录，建立 adapter，再快照 activeStaging，随后移除不在快照中的文件。

因此存在明确交错：

1. 写操作完成 AddPendingCleanup 后、调用 trackActiveStaging 前被调度挂起。
2. 控制连接重建触发的后台清理读取该记录，快照中没有此活动路径。
3. 清理 Remove 暂存文件；上传恢复后继续写已打开句柄，最终提交因源文件消失失败。

已有 TestRunPendingCleanupsSkipsActiveStaging 先手工 Track 再启动清理，不能覆盖真实代码的“记录已可见、活动标记还未发布”窗口。接线后此错误从仅测试可调用变为真实并发路径。

建议在发布数据库记录之前登记活动路径，并确保错误退出撤销活动登记；清理与标记的协议需保证所有可见记录要么已标记活动、要么确实可恢复清理。增加同步 barrier，暂停写入于记录发布后，让真实后台清理运行，验证暂存文件不会消失。既有 activeSet 快照策略在这项发布顺序修正后才有成立基础。

## 测试与 nolint 评价

- 对锁自身冲突及二线程环路新增定向测试是有效改进；原自锁问题不再重复列为未解决。
- 清理新增测试只覆盖预先存在的活动标记，不覆盖发布窗口，也不证明通过生产 control reconnect 自动触发的生命周期。
- 文件读句柄及临时 spool 的收尾 Close/Remove 忽略错误通常合理；其内容正确性已有前置读取/写入检查，注释明确收尾用途。
- `remote_files.go:775` 的 `AddPendingCleanup` 使用 `nolint:errcheck // tracking` 不足以论证忽略安全：若数据库写失败仍继续上传，后续断线将没有恢复记录。至少应定义失败策略并提供可观测错误；不应把 tracking 当作可随意失败的非关键操作。此为评审建议，本轮不另计高优先级 finding。
- `connection.go:464` 后台清理结果完全忽略、HTTP read deadline 失败完全忽略，也缺少运维可观测性。生产边界的忽略应有能力假设或降级处理，不能仅靠 lint 注释证明。

结论：RC-7、RC-8 原问题可关闭；RC-9 尚未关闭，RC-10 已接线但并发安全需要修正。没有真实 OpenSSH 或端到端断网验收证据，不能将静态关闭矩阵解释为这些兼容性测试已通过。
