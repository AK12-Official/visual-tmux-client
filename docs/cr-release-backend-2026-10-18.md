# 修复后第二轮发布级后端 CR

审查对象为 HEAD `93718a1051055553480d40b17b1c8203ef13e35f` 加本次未提交工作区实现，2026-10-18。审查只修改本报告，隔离实验文件放在 `/tmp/vtc-release-cr/`。未连接用户服务、真实 SSH 主机或读取用户凭据。

**结论：当前后端不宜发布。** 发现 1 项 P1、3 项 P2，其中旧问题中 2 项 P2 尚未完整解决。以下 4 项均有本轮定向隔离实验支持；不是直接沿用上轮结论。

## 旧问题关闭情况

| 旧编号 | 当前状态 | 当前实现及验证 |
| --- | --- | --- |
| HB-1：改名后连接池错误撤销 | 关闭，针对原触发路径 | `getControl` 使用 ConnectionRevision；`invalidateControl` 只查找匹配池，不再创建池。`TestReleaseRenamePreservesLease` 验证 dedicated 连接存在时，纯改名并 GetControl 后旧 lease 仍有效。 |
| HB-2：探测超时没有约束 I/O | 部分修复，未关闭 | 指纹探测增加 deadline/cancel，20ms 静默 TCP 测试通过。但能力检查的 channel 建立仍不受取消约束，见 RB-2。 |
| HB-3：local ID 作为 SSH 时空 manager | 关闭，针对原触发路径 | Lease 优先查询真实记录；探测/确认接口按 ConnectionType 判断。`TestReleaseLocalIDSSH` 删除初始本地记录后创建同 ID SSH 注册，GetControl 成功，无 panic。 |
| HB-4：删除主机漏掉未注册附件 | 部分修复，未关闭 | Manager epoch 检查补住 NewProcess 期间的删除，但 epoch 捕获晚于 ticket 兑换，仍有前置窗口，见 RB-3。 |

关闭仅针对列出的原问题，不代表整个子系统没有其他风险。

## RB-1 · P1：专用连接全程绑定请求 context，使远程终端建立后立即断开

位置：[connection.go:517](../hub/internal/host/connection.go#L517)，关联 [handler.go:188](../hub/internal/transport/ws/handler.go#L188)、[handler.go:219](../hub/internal/transport/ws/handler.go#L219)。

新加入的 `dialDedicated` goroutine 在 `ctx.Done()` 时关闭 SSH transport。WebSocket handler 把 HTTP 请求的 `r.Context()` 传入 `RemoteProcessFactory.NewProcess`，后者原样传给 `OpenDedicated`。handler 完成 `att.Start()` 就返回；Go HTTP server 随之取消请求 context。现在这一正常返回路径会关闭远程终端的专用 SSH 连接，输出泵收到 EOF，随后 WebSocket 关闭。

**触发与影响：** 用户正常打开任一 SSH 终端，即使网络、凭据和 tmux 均正常，连接也会建立后立即断开。此次为修复文件取消而引入的跨用途生命周期回归，覆盖核心正常使用路径。

**实验证据：** `TestReleaseRemoteWSClosesOnReturn` 使用既有 `startMockPTYSSHServer`、实际 `RemoteProcessFactory`、实际 WebSocket handler、`ws.DefaultOptions()` 和 `httptest.Server`。handler 返回后客户端约 0.03 秒收到 `StatusNormalClosure`，没有关闭浏览器连接或主动取消测试 context。读循环跳过 ready 等正常帧，确认了实际关闭。

建议：区分建立请求的预算和已建立 attachment 的生命周期。让 terminal transport 由 attachment 关闭、主机失效和应用 shutdown 管理；文件操作则继续由 operation context 取消。不能简单删除所有取消逻辑，否则会重新引入文件挂起问题。应添加真正穿过 HTTP server 返回边界的远程终端测试，确认 ready 后仍能双向输入输出，再验证主动断开会回收 transport。

## RB-2 · P2：能力探测仍可在打开 SSH channel 时无限等待

位置：[capabilities.go:209](../hub/internal/host/capabilities.go#L209)，同类调用在后面的 SFTP `client.NewSession()`；另 [sftp_adapter.go:96](../hub/internal/host/sftp_adapter.go#L96) 的 `sftp.NewClient` 仍不接收 context。

修复为已创建的 tmux/SFTP session 加了取消 watcher，但 watcher 位于 `client.NewSession()` 返回之后。服务器接受 SSH 连接、却不确认 channel open 时，程序无法进入 watcher，10 秒 probe budget 或上层请求取消都无法让此调用退出。最后查询 SFTP 扩展的 `NewSFTPAdapter(probeCtx, client)` 同样没有把 context 作用到 `sftp.NewClient` 握手。

**触发与影响：** 一台 SSH 服务能够认证、但 session channel 请求无响应的主机使“测试连接”长期等待；这可能由故障或资源耗尽触发，不要求整个 TCP 连接已经断开。该问题是 HB-2 尚未修复的子路径。

**实验证据：** `TestReleaseCapabilitiesIgnoreOpenChannelCancellation` 使用本地 SSH 夹具，完成认证并保持 transport，但收到 channel open 后不应答。传入 40ms deadline，150ms 后 `CheckCapabilities` 仍未返回；调用 `mgr.Close()` 关闭 transport 后才结束。该实验验证取消无效，没有把 150ms 观察表述为实测无限时长。SFTP 握手路径为源码分析，未单独注入握手故障。

建议：用单独、有总预算的探测 transport 覆盖握手、channel open、exec、subsystem 和 SFTP 协议握手。取消应能够关闭拥有这些阻塞调用的 transport；不要为了中止探测关闭用户复用中的控制工作。补充每个阻塞阶段的定向故障测试，而不仅测试已开始命令后的 session.Close。

## RB-3 · P2：在兑换票据之后捕获 epoch，仍能让已删除主机注册新附件

位置：[handler.go:185](../hub/internal/transport/ws/handler.go#L185)，关联 `Manager.CurrentEpoch`、`Manager.Register`、`Manager.CloseHost`。

Manager 现在在 CloseHost 时递增 epoch，Register 对 epoch 做锁内检查，这是必要改进；但 handler 是先 resolve、兑换 ticket，然后才读取当前 epoch。若删除发生在 ticket 兑换完成后、epoch 读取前，旧 runtime 会取得删除后的新 epoch，Register 因此把旧附件当作有效的新世代放行。

**触发与影响：** 浏览器开始连接本地主机终端；另一个窗口恰在票据已兑换、附件未创建时删除主机。该终端仍可以注册并继续交互。即使以后复用同 ID，这个旧请求也不应借用新世代身份。

**实验证据：** `TestReleaseDeletedHostUsesNewEpoch` 使用真实 WebSocket handler、terminal.Manager 和既有 fake ProcessFactory，在 ticket redeemer 返回前调用 `CloseHost("local")`，模拟票据已被接受后另一个线程执行删除的交错。handler 返回后 `ActiveCount()` 仍为 1。实验没有操作真实 tmux；它定向验证 epoch 接线错误，不声称模拟了完整数据库删除事务。

建议：在任何可失效操作前捕获世代，并将其与已解析 runtime/连接版本绑定；注册时继续在 manager 锁内原子校验。需要覆盖 resolve、ticket 兑换、process 创建、注册各边界上的删除与同 ID 重建。仅在 NewProcess 前查询当前 epoch 不足以证明旧 runtime 有效。

## RB-4 · P2：合法长度的错误主密钥在迁移提交后才被拒绝

位置：[registry.go:66](../hub/internal/host/registry.go#L66)，关联后面的 `vault.Open` 及 `OpenStore` schema 迁移。

`LoadVault` 只校验密钥文件权限、长度并创建 AEAD，并不能证明该密钥能解开现有凭据。`initialize` 随后调用 `OpenStore`，后者提交 schema v1→v2 迁移，再遍历记录执行 `vault.Open`。因此使用另一个合法 32 字节密钥启动已有 v1 状态时，启动虽然失败，数据库却已经被迁移，违反“主密钥错误时不重写状态”的规格与代码注释承诺。

**实验证据：** `TestReleaseWrongKeyMigratesBeforeFailing` 用 key A 加密有效测试凭据，写入临时 v1 数据库，再将不同但合法长度的 key B 写为 master.key。`NewRegistry` 返回 `credential decryption failed`，重新读取数据库的 `PRAGMA user_version` 却为 2。未使用用户密钥或数据。

影响是排查恢复密钥之前已经产生状态变更；若管理员按失败意味着未升级处理并回退旧二进制，旧版本会面对不支持的新 schema，需要显式恢复备份。当前备份减轻恢复成本，但不满足失败不改状态约定，不能表述为数据已不可恢复。

建议：在同一状态所有权锁保护下，迁移提交前用候选主密钥验证旧数据的凭据，或把验证与迁移纳入可回滚流程。加入错误但长度合法的密钥、正确密钥、损坏密文三类 v1 升级测试，并断言失败后 schema 和内容未变化。

## 架构、迁移与信任复查

本轮关注到的主要架构问题是生命周期边界没有统一：HTTP 请求、SSH 建立过程、单次文件操作、长寿命 terminal attachment 使用同一种 ctx 接口，却具有不同取消语义；连接池世代和 terminal manager epoch 又是分开的标识。RB-1 和 RB-3 分别是这些边界错误落地的实例，适合通过明确所有权和捕获版本的接口来解决，而不是增加更多互不协调的布尔检查。

抽查了 schema v2 迁移、版本水位、默认主机原子清空、凭据持久化与主机 key pinning。当前迁移仍有事务和版本检查，信任回调仍在认证前比较 pinned key，探测仍不发送认证凭据；迁移验证顺序存在 RB-4；未确认凭据泄漏问题，这不等于这些模块完整通过安全审计。部署/回滚文档与强制持久化启动要求是否一致，交由主审统一报告，避免重复计数。

## 本轮实验记录

```sh
go test -overlay=/tmp/vtc-release-cr/overlay.json ./internal/host ./internal/transport/ws -run '^TestRelease' -count=1 -v
```

运行 7 个定向测试（最后一项以相同 overlay、`-run '^TestReleaseWrongKey'` 单独执行）：

| 测试 | 结果含义 |
| --- | --- |
| TestReleaseRenamePreservesLease | 原 HB-1 触发路径回归通过 |
| TestReleaseProbeBudget | 指纹探测超时子问题回归通过 |
| TestReleaseLocalIDSSH | 原 HB-3 触发路径回归通过 |
| TestReleaseRemoteWSClosesOnReturn | 成功复现 RB-1 缺陷 |
| TestReleaseCapabilitiesIgnoreOpenChannelCancellation | 成功复现 RB-2 缺陷 |
| TestReleaseDeletedHostUsesNewEpoch | 成功复现 RB-3 缺陷 |
| TestReleaseWrongKeyMigratesBeforeFailing | 成功复现 RB-4 缺陷 |

整个命令 PASS **包含以观察缺陷为断言的测试（共 4 个），因此不代表发布验收通过**。首次 sandbox 内运行因 ParentGuard 无法建立隔离服务器而拒绝执行；随后按工具授权机制在 sandbox 外使用相同隔离夹具执行成功，没有削弱守卫。初次 WS 诊断误用了空 Options，产生读超时，随后改为产品默认选项并确认缺陷；上表及结论均依据最终版本实验。

覆盖限制：本报告不重复全库功能/race/lint 结果，不审前端完整行为和全部 SFTP 文件语义，不访问真实 OpenSSH，不做跨平台或进程崩溃迁移实验。当前发现均基于本轮工作区；未来代码继续修改后需重新运行相关回归实验。
