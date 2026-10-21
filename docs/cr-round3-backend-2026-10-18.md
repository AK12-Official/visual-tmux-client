# 第三轮发布级 CR：后端生命周期与迁移

审查对象：HEAD `93718a1051055553480d40b17b1c8203ef13e35f` 加第三轮修复后的工作区，2026-10-18。复核第二轮汇总的 RC-1、RC-4、RC-5、RC-6，并检查这些修复的邻接路径。仅新增本报告，实验通过 `/tmp/vtc-round3-cr/` 的 Go overlay 注入，未修改业务或仓库测试代码，未访问真实用户服务、SSH 或凭据。

**结论：四项原触发路径均已验证修复；当前仍有 1 项 P2 的终端建立阶段取消缺口。未发现新的 P1。** 不能因为已建立终端不再立即断开，就认为建立过程中所有阻塞阶段也已具备预算。

## 旧项关闭矩阵

| 旧编号 | 当前判断 | 代码及独立验证 |
| --- | --- | --- |
| RC-1：handler 返回后远程终端立即断开 | 原问题关闭 | `dialDedicated` 不再把终端整个生命周期绑定 request context。本轮实际 `httptest.Server`、WebSocket handler 和 SSH PTY 夹具验证：handler 返回后，客户端仍能写入 `round3-echo` 并收到远端回显。另有建立阶段问题见 R3B-1。 |
| RC-4：能力检测 channel/SFTP 建立不受取消约束 | 原问题关闭 | `CheckCapabilities` 改用独立 `OpenDedicated(probeCtx, "probe")`，context 取消关闭其 transport，能打断 channel open 和后续协议 I/O。原不确认 channel 的夹具现在在 40ms deadline 后退出，无需外部关闭 manager。 |
| RC-5：ticket 兑换之后捕获 epoch | 原问题关闭 | handler 在 resolve、ticket 兑换之前捕获 epoch。原测试在 redeemer 内调用 CloseHost，随后 Register 拒绝旧 epoch，ActiveCount 为 0。 |
| RC-6：错误但合法长度的 key 在迁移后才拒绝 | 原问题关闭 | Registry 将 credential validator 传入 Store，迁移事务提交前验证旧密文，失败回滚。原 schema v1 + key A 密文 + 合法长度 key B 夹具仍拒绝启动，重新读取 user_version 为 1。 |

额外复跑了纯改名后旧 lease 保持有效、指纹静默 TCP 的 20ms 预算，以及删除本地记录后以 `local` ID 注册 SSH 并建立控制连接，均符合预期。以上关闭仅对应原触发路径和本轮有限验证，不是对整个模块没有缺陷的保证。

## R3B-1 · P2：终端建立阶段的 channel/PTY/exec 没有取消和总预算

位置：[remote_terminal.go:72](../hub/internal/host/remote_terminal.go#L72)，同类阻塞点为 [remote_terminal.go:90](../hub/internal/host/remote_terminal.go#L90) 和 [remote_terminal.go:105](../hub/internal/host/remote_terminal.go#L105)；相关分支 [connection.go:606](../hub/internal/host/connection.go#L606)。

第三轮为避免已建立终端随 HTTP handler 返回而关闭，将 `purpose == "terminal"` 的专用 transport 完全排除在 context watcher 之外。SSH 握手完成后，`RemoteProcessFactory.NewProcess` 同步执行 `NewSession`、`RequestPty` 和 `Start`；这些调用没有接收 context，也没有独立 deadline。`defer` 清理只能在函数已经返回时执行，无法打断正在等待远端应答的调用。

**触发：** SSH 认证成功，但服务端不确认 session channel（同理可发生于 PTY 或 exec 请求）。即使调用者的 context 已取消，NewProcess 仍不返回。生产请求本身通常没有 deadline，也没有本函数内的建立总预算，因此远端故障时用户会一直停留在连接中，后端保留未完成工作；取消不可靠，多次尝试还会消耗该主机连接配额。

**实证：** `TestRound3TerminalSetupCancellation` 用本地 SSH 夹具完成认证但不应答 channel open，向 NewProcess 传入 40ms deadline。150ms 后仍未返回；测试显式 `mgr.Close()` 后才解除阻塞。这里实测的是“40ms 取消被忽略至少 150ms”，长期无上限等待是依据缺少后续中止路径的源码判断，不将有限观察夸大为无限时长实验。PTY 和 exec 子路径经过静态检查，没有分别注入故障。

建议：终端建立应有独立有限预算，覆盖 transport 建立、channel open、PTY、exec；建立过程中 context 取消应关闭该专用连接。建立成功后以同步的所有权移交停止建立阶段 watcher，再将连接交给 attachment 的断开、主机失效和 shutdown 生命周期管理。既不能重新绑定整个终端寿命到 HTTP request context，也不能让整个 terminal 用途豁免取消。回归测试需要同时证明：

- handler 返回后真实 PTY 仍可交互；
- channel open、PTY、exec 分别挂起时，建立预算或主动取消会释放连接；
- 取消与建立成功同时发生时，不泄漏 transport、不误关闭已经移交的 attachment。

## 当前架构与验证边界

独立探测 transport、提前捕获 epoch、提交迁移前校验凭据，均直接处理了上轮发现的所有权或顺序问题。迁移失败仍会生成备份这一行为没有被误报为 schema 迁移；本轮断言的是错误密钥下 schema 保持 v1。终端连接依赖 `purpose` 字符串选择 watcher，仍把“建立过程”与“建立后寿命”的区别压缩成一次用途判断，R3B-1 说明这个分支尚不足以表达两个阶段。

没有在本轮有限检查中确认新的信任绕过、凭据泄漏或迁移数据丢失问题。没有做完整安全审计、真实 OpenSSH 兼容实验、跨平台迁移或崩溃恢复实验；文件事务和前端交互由其他审查报告覆盖。

## 实验记录

```sh
go test -overlay=/tmp/vtc-round3-cr/overlay.json ./internal/host ./internal/transport/ws -run '^TestRound3' -count=1 -v
```

共 8 个定向测试，7 个验证符合预期的行为，1 个以观察 R3B-1 缺陷为断言。首次运行 host 的 7 个测试通过，WS overlay 的 helper 与代码新增测试同名导致编译冲突；只将临时 helper 改名为 `round3CloseOnRedeem` 后，单独运行 WS 包通过。这是临时夹具命名冲突，不能记作业务构建失败。

| 测试 | 本轮结果含义 |
| --- | --- |
| TestRound3RemoteWSClosesOnReturn | 名称沿用上轮诊断，断言已改为 handler 返回后真实双向回显成功 |
| TestRound3RenamePreservesLease | 改名不会撤销原租约 |
| TestRound3CapabilitiesIgnoreOpenChannelCancellation | 名称沿用上轮诊断，断言已改为 capability 取消有效 |
| TestRound3ProbeBudget | 指纹探测预算有效 |
| TestRound3LocalIDSSH | local ID 可合法用于重建的 SSH 注册 |
| TestRound3WrongKeyMigratesBeforeFailing | 名称沿用上轮诊断，断言已改为失败后 schema 仍为 1 |
| TestRound3DeletedHostUsesNewEpoch | 名称沿用上轮诊断，断言已改为删除交错后无活动附件 |
| TestRound3TerminalSetupCancellation | 成功复现 R3B-1，不代表该功能验收通过 |

按现有 ParentGuard 和本地临时 SSH/HTTP 夹具执行，未执行 ad hoc tmux 命令；sandbox 外运行通过工具授权进行。本报告不重复主审的全库测试、竞态或 lint 结果。
