# 第六轮发布 CR：文件安全保护回归复核

日期：2026-10-21。范围限于当前工作区文件侧关键保护及新前端树连接身份修复的接口边界。业务代码只读，未接触实际服务、真实 SSH 或 tmux。

**结论：本轮范围内没有新增高置信 P1/P2 findings；此前已关闭的文件问题未观察到回退。**

## 复核矩阵

| 保护 | 状态与证据 |
|---|---|
| watcher 退出同步 | 保持修复。`http/files.go:289` 起 stop 通过 watcherDone 等待退出，成功响应前调用，错误出口 defer 覆盖，stop 先于 cancelWrite；不会把未结束的 writer 副作用留到 handler 返回后 |
| HTTP 上传预算 | 保持修复。HTTP 创建有限 deadline，传入 Write；`remote_files.go:365` 的 opContext 对已有 deadline 保留原 ctx，因此网络 read deadline 与远端操作预算联通 |
| active staging 发布 | 保持修复。`remote_files.go:793` 起先标记 active，再发布数据库清理记录；登记失败撤销标记、停止传输并报错 |
| 元数据与锁 | 保持修复。提交前使用当前 metadata，Chmod/必要 Chown 错误检查仍在；LockHandle owner 排除自身锁、锁环拒绝及 canonical 追加锁调用链未见回退 |
| 预期连接身份 | 保持保护。write/create/rename/delete 均先解析一次 runtime，再调用统一 checkExpectedConnRev，随后执行对应文件服务；query/header/body 的冲突与无效值明确拒绝，旧 connection revision 返回 409 |
| 前端树身份边界 | 新代码在树操作捕获 tree.connectionRevision，并在当前连接不匹配及 prompt 后变化时阻止提交；文件 API 把捕获的 expected_connection_revision 传给后端。这里只核对接口边界，不替代前端 reviewer 的完整 UI 审查 |

## 本轮实际测试

执行：

```text
go test -race ./internal/transport/http -run '^Test(FilesWrite(SlowBodyTimeout|WatcherNeverUsesWriterAfterReturn|KeepAliveSequentialRequests|BodyCancellation|ExpectedConnectionRevisionConflict)|FilesMutateExpectedConnectionRevisionConflict|ExpectedConnectionRevisionInputValidation)$' -count=1
ok github.com/AK12-Official/visual-tmux-client/hub/internal/transport/http 1.263s
```

覆盖七个定向测试入口及其子场景，通过且无 race 报告。包含实际 TCP 慢正文、watcher 正常退出、keep-alive、取消、写入和其他文件变更的连接版本拒绝、统一版本参数校验。

第五轮提出的 keep-alive 测试证据限制已补齐：当前测试读完响应正文，并用 httptrace.GotConn.Reused 断言第二次及以后请求复用连接；本轮实际运行通过。因此不再把“可能每次重拨”列为该测试的残留限制。

## 证据边界

上述 HTTP 测试使用 mock 文件服务；实际网络覆盖 HTTP 层，不代表真实 SSH/SFTP 端到端验证。staging、metadata 和锁本轮做静态回归复核，没有重复执行其隔离 SSH fixture 套件。没有执行整个仓库测试，也没有真实 OpenSSH 或生产断网验收。本报告不把限定范围内无 findings 外推为整个项目不存在缺陷。
