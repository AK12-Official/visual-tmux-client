# 第七轮 CR：文件后端有限回归

日期：2026-10-21。范围：HTTP watcher/预算、活动暂存发布、预期连接身份，以及本轮前端 scope 改动的接口边界。只读业务代码；未接触真实 SSH、生产服务或 tmux。

**结论：限定后端范围内无新增高置信 P1/P2 findings，已有关键保护未见回退。**

| 保护 | 本轮判断 |
|---|---|
| watcher 生命周期 | 保持修复。`http/files.go:289` 起通过 stopWatcher/watcherDone 同步停止，成功响应前及错误返回路径均保证退出；stop 先于 cancelWrite |
| 慢正文预算 | 保持修复。HTTP WithTimeout 传入文件服务，已有 deadline 不被另建更长预算替代；真实 TCP 停顿回归通过 |
| 活动 staging 发布 | 保持修复。先 trackActiveStaging，再 AddPendingCleanup；登记失败停止操作并撤销保护、尝试收尾 |
| 连接身份拒绝 | 保持修复。统一 checkExpectedConnRev 比较捕获 runtime 的 ConnectionRevision；query/header/body 不一致、非法数字及旧 revision 拒绝逻辑保留 |
| 前端路径变更 scope 边界 | getChanges 使用 host:connectionRevision 键，并非改名显示名或一般配置 revision；与后端连接 revision 语义一致 |

## 本轮实际验证

初次执行因沙箱禁止 httptest 临时本机监听失败，按权限流程重新运行，最终通过：

```text
go test -race ./internal/transport/http -run '^Test(FilesWrite(SlowBodyTimeout|WatcherNeverUsesWriterAfterReturn|KeepAliveSequentialRequests|ExpectedConnectionRevisionConflict)|FilesMutateExpectedConnectionRevisionConflict|ExpectedConnectionRevisionInputValidation)$' -count=1
ok github.com/AK12-Official/visual-tmux-client/hub/internal/transport/http 1.259s
```

六个定向入口及子场景覆盖慢正文、watcher、真实连接复用、写入/其他文件 mutation 的旧连接拒绝及参数校验。无 race 报告。沙箱错误不计为产品失败。

## scope 对照与覆盖边界

当前 `getChanges` 已按连接 scope 分区；但 `pending.ts` 的 API 仍以可选 hostId 字符串作为分区，Overlay 的 saving.begin/end、saving.idle、renaming/deleting 调用仍传裸 hostId（例如 `FileManagerOverlay.vue:842`、`:951`、`:1429`、`:1556`、`:1558`）。因此不能把本轮实现表述为“所有 pending 状态均已按 connection revision 隔离”。该状态可能造成跨代保守等待；本有限后端审查未确认产生新的错误目标写入，不单独计 finding，已交前端 reviewer 核对交互语义。

HTTP 测试使用 mock FileService；真实 TCP 验证只覆盖 HTTP，不代表真实 OpenSSH/SFTP。staging 本轮为静态回归复核，没有重跑其 SSH fixture 套件，也未运行全仓测试。无 findings 仅适用于本报告范围。
