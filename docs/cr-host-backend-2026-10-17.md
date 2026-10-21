# 两轮 OpenSpec 后端独立代码审查

审查范围：`deb831d` 至审查时工作区（包含未提交的 `unify-host-management` 实现），重点为 host registry/store、凭据、信任、连接池、runtime，以及 HTTP/WebSocket 生命周期。按照当前 `openspec/specs/host-management/spec.md` 的统一主机管理要求评估，不沿用第一轮已被取代的“本地主机不可删除”约束。审查完成于 2026-10-18，文件名沿用本轮报告命名。

结论：发现 **1 项 P1、3 项 P2**。其中改名撤销租约、指纹探测超时失效已用临时本地夹具复现；其余为高置信静态控制流分析。此次未修改业务代码，未连接用户真实 SSH 主机或读取其凭据，未执行临时 tmux 命令。

## HB-1 · P1：SSH 主机改名后的控制请求会撤销仍有效的连接池

位置：[connection.go:332](../hub/internal/host/connection.go#L332)，同类错误还在 [connection.go:401](../hub/internal/host/connection.go#L401)。

`Lease` 按 `ConnectionRevision` 获取连接池，`dialTransport` 和 `dialDedicated` 也使用连接版本，但 `getControl` 和 `invalidateControl` 仍以 `target.Revision` 获取池。仅修改名称后，记录版本增长而连接版本保持不变；下一次查询会话或测试连接时，`getControl` 将现存池当作旧配置撤销并关闭其中的终端连接。随后 `dialTransport` 又按连接版本重建池，导致来回替换，也破坏控制连接的正常复用和生命周期追踪。

触发步骤：连接 SSH 主机并打开终端 → 仅修改主机名称 → 查询会话或测试连接。影响是无连接配置变化却断开现有终端、撤销旧租约；这是正常用户操作路径，违反规格的 “Rename does not disconnect”。

**证据：实测复现。** 临时测试 `TestCRRenameRevokesExistingLease` 建立 disposable SSH 连接及 dedicated transport，保存纯改名，再通过新租约调用 `GetControl`。调用前旧租约有效，调用后 `old.CheckValid()` 返回 `host_revision_conflict`。

建议：所有池选择统一使用连接版本；失效处理应定位已经捕获的池，避免失效操作意外创建或替换池。回归测试应覆盖已连接终端、纯改名、会话查询和能力检测，并断言旧租约有效、终端不断开、控制连接可复用。

## HB-2 · P2：指纹及能力探测的超时没有约束实际 SSH I/O

位置：[trust.go:160](../hub/internal/host/trust.go#L160)，[capabilities.go:209](../hub/internal/host/capabilities.go#L209)、[capabilities.go:213](../hub/internal/host/capabilities.go#L213)、[capabilities.go:225](../hub/internal/host/capabilities.go#L225)。

`ProbeEndpoint` 只给 TCP 建连设置超时。建连完成后直接调用 `ssh.NewClientConn`，没有设置 socket deadline，也没有在 context 取消后关闭 socket。`ssh.ClientConfig.Timeout` 不为直接调用的 `NewClientConn` 安装握手超时；本地依赖源码也确认该字段用于 TCP 建连。

能力检测虽然创建了 10 秒 `probeCtx`，但取得控制连接之后，同步执行 `NewSession`、`CombinedOutput("tmux -V")` 和 `RequestSubsystem`，这些调用不消费该 context。远端只要继续响应保活而不完成命令或子系统请求，能力检查仍可能持续挂起。

触发步骤：探测一个接受 TCP 但不发送 SSH banner 的端口；或测试一个 SSH 已连接、但 tmux 命令/子系统请求不返回的主机。影响是“探测指纹/测试连接”一直等待，取消页面请求也无法可靠释放后端工作。

**证据：指纹探测已实测；能力检测为高置信静态分析。** `TestCRProbeIgnoresCancellation` 同时给 context 和 dialTimeout 设置 20ms，连接本地静默 TCP 夹具，等待 150ms 仍未返回；关闭夹具连接后函数才返回。没有把 150ms 观察夸大为实测无限等待。

建议：指纹握手使用 deadline 和 context 取消关闭；能力检查使用有明确生命周期的探测 transport，并把取消落实到 session/transport。回归测试应覆盖不发 banner、不确认 channel、不结束命令、SFTP 不应答等阶段，同时确保探测取消不关闭用户正在使用的终端。本项仅覆盖探测，文件操作的取消问题由文件后端审查单独记录。

## HB-3 · P2：复用 `local` ID 为 SSH 注册会进入空 manager 租约路径

位置：[connection.go:187](../hub/internal/host/connection.go#L187)；相关入口 [router.go:557](../hub/internal/transport/http/router.go#L557)、[router.go:599](../hub/internal/transport/http/router.go#L599)。

统一管理允许删除初始本地主机，创建接口也允许显式指定 `connectionType: "ssh"` 和 `id: "local"`，registry/store 没有禁止该组合。runtime 已按记录的连接类型分派，但 `ConnectionManager.Lease` 仍把 ID 为 `local` 的记录硬编码为内置本地记录，返回没有 `manager` 的租约。后续远程 `GetControl` 会调用空 manager，产生 panic；指纹探测和信任确认还会因同一 ID 被硬编码拒绝。

触发步骤：通过认证 API 删除初始本地记录 → 注册 ID 为 `local` 的 SSH 主机 → 请求该主机的会话或能力。该组合是 API 接受的合法记录，浏览器随机 ID 创建流程通常不会触发。HTTP panic 通常由 Go HTTP 服务恢复，但该请求失败，该注册不可正常使用。

**证据：高置信静态控制流分析，未运行专门复现。** 创建及保存路径接受组合，resolver 按 SSH 分派，Lease 按 ID 提前返回，`ClientLease.GetControl` 无 nil manager 防护，构成完整可达路径。

建议：以注册记录的 `ConnectionType` 决定行为，移除 ID 驱动的权限和连接分派；补充删除后以同一 ID 注册不同连接类型的 API/runtime 测试。单纯加入 nil 检查只能避免 panic，不能满足统一管理规格。

## HB-4 · P2：删除主机可能漏掉正在创建、尚未注册的终端附件

位置：[handler.go:185](../hub/internal/transport/ws/handler.go#L185)、[handler.go:210](../hub/internal/transport/ws/handler.go#L210)，[manager.go:61](../hub/internal/terminal/manager.go#L61)。

WebSocket 在兑换 ticket 后执行 `NewProcess`，然后才注册 attachment。删除主机时 `CloseHost` 仅扫描已经注册的附件，没有记录被撤销的连接世代；`Register` 也只检查全局 shutdown 状态。

可达交错：本地主机终端 ticket 兑换成功 → `NewProcess` 尚未返回 → 另一个请求删除主机并完成 `CloseHost`（此时附件不在集合）→ `NewProcess` 返回 → `Register` 成功并启动附件。结果是已经删除的本地主机仍可留下新的可交互终端，ticket 的一次性撤销不能覆盖已经兑换的工作。

**证据：高置信静态并发窗口，未进行实际时序复现。** 本地 runtime 没有可撤销租约，manager 不存储主机版本或撤销状态，当前路径没有能挡住这一交错的二次检查。这里只报告本地路径，不推定所有远程连接都以相同方式存活。

建议：附件注册和主机版本失效应共享原子化的生命周期协议，按 host ID 与连接版本拒绝旧世代；不能仅在注册前增加一次无锁查询，否则仍留有检查与注册间隙。回归测试可用阻塞的 fake ProcessFactory，在 ticket 兑换后暂停创建、执行删除、再放行，断言旧附件被拒绝且 process 被 detach；同 ID 重建也不应恢复旧附件有效性。

## 验证记录与覆盖限制

临时文件位于 `/tmp/vtc-cr-host/`，通过 Go overlay 注入测试，没有给仓库添加测试或修改实现。执行命令：

```sh
go test -overlay=/tmp/vtc-cr-host/overlay.json ./internal/host -run '^TestCR' -count=1 -v
```

输出：

```text
TestCRRenameRevokesExistingLease:
  REPRODUCED: metadata-only rename then GetControl revoked prior lease: host_revision_conflict
TestCRProbeIgnoresCancellation:
  REPRODUCED: 20ms timeout/cancel ignored after TCP connection for >150ms
PASS
```

这里的 PASS 表示两个测试成功观察到待修复缺陷，**不是功能回归验收通过**。使用现有 SSH 测试夹具与包内 ParentGuard；未自行操作 tmux。

审查阅读了 registry/store 的保存、删除、版本水位与默认设置路径，凭据 AEAD、主机公钥确认和 pinning，连接池/租约及失效，runtime 的本地和远程分派，以及 HTTP 管理和 WS ticket/attachment 生命周期。积极方面包括凭据响应投影隔离、凭据认证加密、持久化版本水位、连接版本与元数据版本分离的设计；但后者尚未在所有调用点贯彻。

本报告不构成完整安全审计：没有全面审阅 SFTP 路径和文件原子保存，没有审阅前端所有交互，没有访问真实 OpenSSH 或用户配置，没有执行全库测试、性能/压力测试或跨平台迁移验证。HB-3、HB-4 及能力检测挂起需增加定向回归测试；普通测试通过不能排除上述生命周期问题。
