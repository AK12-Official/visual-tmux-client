# 第五轮 CR：上传 watcher 与清理生命周期

日期：2026-10-21。对象：当前工作区第四轮 R4-3 的修复及相邻上传清理实现。只读业务代码，未操作生产服务、真实 SSH 或 tmux。

结论：**R4-3 可关闭；本轮范围内无新增高置信 P1/P2 findings。** 以下测试证据限制不等于发现新的产品缺陷。

## 关闭矩阵

| 检查项 | 状态 | 证据 |
|---|---|---|
| watcher 在 handler 返回后操作 ResponseWriter | 关闭 | `hub/internal/transport/http/files.go:291–319` 新增 watcherDone；stop() 停止并同步等待 watcher 退出；成功响应前显式 stop，错误出口通过 defer stop 覆盖 |
| 正常结束 cancel 被误判成超时 | 关闭 | defer stop 先于 defer cancelWrite 执行；取消分支在操作 writer 前二次检查 stopWatcher；即使取消正在执行，stop 也等待它完成，handler 不会先返回 |
| 慢上传正文无法按预算中断 | 保持已修 | HTTP 与 service 共用已有 deadline；超时 watcher 先设置 read deadline 再关闭 body；实际 TCP 停顿测试通过 |
| 活动 staging 发布竞态 | 保持已修，静态复核 | `remote_files.go:793` 起先登记 active，再写 cleanup record；登记失败中止并撤销标记；正常结束只在不再继续上传时撤销 active |

## 实际验证

运行：

```text
go test -race ./internal/transport/http -run '^TestFilesWrite(SlowBodyTimeout|WatcherNeverUsesWriterAfterReturn|KeepAliveSequentialRequests|BodyCancellation)$' -count=1
ok github.com/AK12-Official/visual-tmux-client/hub/internal/transport/http 1.255s
```

四项定向测试通过，竞态检测未报告问题。SlowBodyTimeout 使用真实 TCP 连接和未完成 Content-Length 正文，证明原 net/http 阻塞点被打断。WatcherNeverUsesWriterAfterReturn 对 100 次正常保存检查无 deadline 回调；配合同步退出实现，支持 R4-3 关闭结论。

## 测试证据限制与可选改进

- `TestFilesWriteKeepAliveSequentialRequests` 开启 keep-alive，但未断言连接实际复用，并且响应正文未读至 EOF 即 Close。Go HTTP transport 可能重新拨号，因此它证明十次请求成功，不能单独证明同一个 TCP 连接上的连续请求成功。建议 drain 响应并以 httptrace.GotConn.Reused 或服务端连接计数断言复用。这是测试覆盖建议，不计为产品 finding。
- watcher spy 用短等待检查正常返回后的回调，没有直接用 barrier 验证“超时回调正在执行时 handler 必须等待退出”。现有 watcherDone 同步代码提供确定的静态依据；可增加受控阻塞 SetReadDeadline 的测试，进一步防止未来回归。
- HTTP 测试的文件服务为 mock，不代表 SSH/SFTP 端到端验收。本轮未运行清理集成测试、真实 OpenSSH 兼容测试或全部项目测试。

## 相邻代码与 nolint 评价

本轮未观察到 staging 的 active 登记、持久化发布和收尾解除顺序回退。cleanup 登记错误仍会中止正文传输，未重新引入静默忽略关键状态的问题。writer 的 deadline/Close 错误忽略仍有平台能力假设，但本项目真实 net/http 路由已经通过 TCP 停顿测试；未据此推定存在新的发布阻断问题。

本报告的通过范围是上述定向修复与调用链，不等同于整个项目发布验收完成。
