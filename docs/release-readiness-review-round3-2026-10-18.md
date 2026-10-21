# 第三轮 CR 与发布准入评估

日期：2026-10-18。对象：HEAD `93718a1051055553480d40b17b1c8203ef13e35f` 加 code agent 再次修复后的当前工作区。结论：**质量门禁已全部通过，但仍有 1 项 P1、4 项 P2，暂不建议合入主线发布。**

三位独立审查者分别复核前端、后端生命周期和远程文件；主审执行全量检查、核对证据及文档，并额外运行真实 TCP 慢正文的标准库夹具。本轮只新增报告，没有修改业务代码、替换运行程序或访问用户 SSH 主机。审查判断针对当前快照，不把上轮问题直接沿用为本轮发现。

## 当前发现

### R3-1 / P1：等待保存的删除未绑定连接版本，可能删除新服务器文件

位置：[FileManagerOverlay.vue:1242](../hub/web/src/components/files/FileManagerOverlay.vue#L1242)、[删除请求:1298](../hub/web/src/components/files/FileManagerOverlay.vue#L1298)、[files/api.ts:420](../hub/web/src/files/api.ts#L420)、[HTTP 删除接口:378](../hub/internal/transport/http/files.go#L378)。

主机 A 的文件正在保存，用户确认删除后等待 saving.idle。等待期间 A 的地址/凭据发生连接配置变更，connectionRevision 从 1 变为 2，hostId 保持不变。原保存结束后，删除仍向 A 发送，且没有携带或验证确认时的连接版本；后端按 ID 解析到新 runtime，因此可能删除新服务器上未经用户确认的同路径文件。

**本轮 DOM 实证**：真实 FileManagerOverlay 配合可控保存 Promise，等待期间更新 availableHosts 和 hostRevision，再放行保存，实际捕获到 `POST /api/hosts/remote-1/files/delete`，正文仅有 path/recursive，没有 expected connection revision。仅 mock 网络，未实际删除远端文件。请求执行到新 runtime 的后半条链路为源码核对。

修复要求：用户发起/确认所有有副作用操作时冻结 hostId + connectionRevision，等待后仍验证同一身份，API 和服务端也验证该版本。已补的 write 版本参数不能保护 delete。补充“同一 ID 重配置期间等待删除”的端到端回归；检查 rename/create/批量会话操作是否共享同样目标约束。

### R3-2 / P2：Reconcile 重载后不更新连接身份，仍无法保存

位置：[FileManagerOverlay.vue:879](../hub/web/src/components/files/FileManagerOverlay.vue#L879)，关键 [更新:896](../hub/web/src/components/files/FileManagerOverlay.vue#L896)。

打开文件时记录了 connectionRevision，保存守卫也开始使用它；但 reconcile 只更新普通 revision，保留旧 connectionRevision。用户按提示确认从新连接重载后，文件仍被视为过期，后续编辑无法保存。跨主机草稿还使用当前选中主机的 props.hostRevision，未使用草稿归属目标的版本。

**DOM 实证**：1/1 版本打开 → 更新为 2/2 → Reconcile → tab 得到 revision=2、connectionRevision=1；再次编辑时 Save 仍禁用。应按实际读取目标捕获并更新成对身份，读取期间再次变更时拒绝陈旧结果；增加重载后再次保存的测试。

### R3-3 / P2：远程终端建立过程仍无法可靠取消

位置：[remote_terminal.go:72](../hub/internal/host/remote_terminal.go#L72)、[PTY:90](../hub/internal/host/remote_terminal.go#L90)、[exec:105](../hub/internal/host/remote_terminal.go#L105)，关联 [connection.go:606](../hub/internal/host/connection.go#L606)。

正常建立的终端已经不会随 HTTP handler 返回断开。但 terminal 用途被完全排除在取消 watcher 外，认证后同步的 NewSession、RequestPty、Start 没有建立总预算。服务端不应答时，即使请求 context 取消，建立工作仍阻塞并占用连接额度。

**隔离实证**：本地 SSH 夹具认证成功、不确认 channel，40ms context 到期后等待150ms仍不返回，关闭 manager 后才解除。PTY/exec 路径为静态核对。应在建立阶段可取消、有预算，成功时同步移交给 attachment 生命周期；同时验证正常交互、每个建立阶段故障、取消与移交并发。

### R3-4 / P2：HTTP 慢上传没有消费真正的操作 deadline

位置：[HTTP writeCtx:283](../hub/internal/transport/http/files.go#L283)、[RemoteFileService.opContext](../hub/internal/host/remote_files.go#L346)、[正文传输:798](../hub/internal/host/remote_files.go#L798)。这是上一轮 RC-9 尚未关闭的部分。

HTTP watcher 监听父 writeCtx，仅 WithCancel；service 的30秒操作超时发生在新建的子 ctx。子 ctx 超时不会取消父 ctx，故 HTTP watcher 不设置 read deadline。service 虽调用 body.Close，但标准库 HTTP body.Read/Close 共用互斥锁，Close 不能打断正在持锁等待网络的 Read。客户端发完部分正文但不断开时，路径锁和 handler 仍可能滞留。

**主审运行了真实 TCP/httptest 标准库夹具** `/tmp/vtc-r3-body-repro.go`：按当前父子 context 接线设置40ms内部预算，过期后再等150ms，正文 Read/Close 仍阻塞；关闭客户端 socket 才结束。此实验验证标准库行为及接线效果，没有声称运行了完整远程文件 handler。已有 io.Pipe 自定义正文的取消测试不能替代真实 HTTP 正文测试。

修复应让实际 operation budget 作用于网络读取边界，先设 read deadline 再 Close；通过真实 TCP 部分上传测试证明超时后请求及路径锁均释放。

### R3-5 / P2：后台清理可看到未登记为活跃的上传记录

位置：[createStagingFile:774](../hub/internal/host/remote_files.go#L774)、[活动登记:787](../hub/internal/host/remote_files.go#L787)、[清理快照:1233](../hub/internal/host/remote_files.go#L1233)。

修复已在控制连接建立后接入后台清理，但 staging 先通过 AddPendingCleanup 入库，随后才 trackActiveStaging。若写操作在两步间被暂停，清理能读到记录、却在 activeSet 快照中找不到它，于是删除正在使用的暂存路径。上传恢复后最终提交因暂存文件消失而失败。

**静态并发交错确认，未运行该窗口的集成复现。** 已有测试先手工 Track 再清理，不能验证真实发布顺序。应在发布恢复记录前建立活动保护，并对失败路径撤销标记；增加 barrier 控制此交错的回归。还需给 AddPendingCleanup 失败定义明确策略，不能仅用 `nolint:errcheck // tracking` 将恢复记录视为无关紧要。

## 上轮问题关闭矩阵

| 上轮项 | 本轮判断 | 证据 |
| --- | --- | --- |
| RC-1 正常远程终端立即关闭 | 原路径关闭 | 真实 HTTP/WS handler 返回后双向 PTY echo 成功；建立故障仍有 R3-3 |
| RC-2 旧草稿切主机后绕过保存校验 | 原路径关闭 | 全主机版本映射 + tab.connectionRevision + write 预期版本到后端的链路已补，DOM/API 测试通过；其他修改接口仍有 R3-1 |
| RC-3 discovery 失败丢草稿 | 关闭 | 初始化失败与使用中失败分开，使用中错误保留工作区；真实草稿经过503、Retry后仍在 |
| RC-4 能力探测建channel不取消 | 原路径关闭 | 独立有预算 probe transport，40ms故障夹具能够退出 |
| RC-5 epoch 捕获太晚 | 原路径关闭 | resolve/redeem前捕获，删除交错后 ActiveCount=0 |
| RC-6 错误key先迁移 | 原路径关闭 | validator在迁移提交前校验，错误32字节key下 schema仍为1 |
| RC-7 Chown静默失败 | 原路径关闭，静态验证 | 必要时检查Chown并在替换前报告失败；还需专门服务端拒绝测试 |
| RC-8 规范锁自冲突 | 原反例关闭 | LockHandle区分owner，新增自冲突及锁环测试通过 |
| RC-9 慢正文超时 | 未关闭 | 见 R3-4 |
| RC-10 清理无生产入口 | 原接线缺口关闭，但安全性未完成 | 已接control重建后的有预算清理，新增活动登记窗口见 R3-5 |
| Q1 lint / 格式 | 关闭 | 现配置未放宽；lint 0 issues，diff check通过 |
| Q2 恢复文档 | 上轮具体矛盾已修 | README明确缺key拒绝启动；SECURITY明确当前强制持久化；例子限定首次初始化生成key |
| Q3 依赖清单 | 关闭 | go mod tidy -diff退出0、无差异 |

## 本轮发布检查

| 检查 | 结果 |
| --- | --- |
| 固定 v2.13.2 lint及配置校验 | **通过，0 issues**；未降低现有规则阈值 |
| git diff --check | 通过 |
| go mod tidy -diff | 通过，无差异，未修改文件 |
| Go 全量 `-count=1` | 12包，722个测试及子测试通过，无用例跳过；web包无Go测试文件 |
| host/auth/files/terminal/HTTP/WS race | 6包，449个测试及子测试通过 |
| go vet ./... | 通过 |
| npm test | 18个测试文件通过，无失败/跳过 |
| npm run build | Vue/TypeScript检查及Vite构建通过 |
| CGO=0 四目标构建 | Linux amd64/arm64、macOS amd64/arm64全部通过 |
| OpenSpec 全库严格校验 | 8 passed, 0 failed：7份主规格及1个活动变更 |

本轮使用 Go1.27.1、Node24.12.0和现有前端依赖安装；没有将本机结果说成使用CI Go1.26.3及干净npm ci的远端检查结果，也没有在四个目标平台执行运行测试。没有真实OpenSSH/浏览器视觉/长期负载验收。正常套件全绿不覆盖所有可控异步交错。

## 架构与文档评价

本轮不只是抑制lint：保存、schema、连接和capability逻辑已拆成职责更清楚的函数，阈值保持不变，常量、错误处理、格式与依赖同步也已完成。大部分测试夹具收尾Close/Remove的定向errcheck豁免合理；生产恢复记录和后台清理全丢错则仍需明确可靠性与可观测性契约，不能仅靠注释证明安全。

当前主要架构缺口集中在三条边界：连接身份需要覆盖所有有副作用操作；终端建立阶段与已建立attachment之间需要明确所有权移交；上传取消与暂存恢复需要在HTTP输入、SFTP输出、持久记录和活动标记之间采用一致协议。R3-1、R3-3、R3-4/5分别给出了具体反例。

README、SECURITY和示例配置的上轮恢复矛盾已经对齐；源代码里Registry仍有“lazy/local-only不写入”的过时类型注释，可作为后续非阻断清理。标名“Real OpenSSH”的测试依然是Go SSH/SFTP夹具，不能将其作为真实服务端兼容性验收证据。

建议修复上述5项并将本轮复现转为仓库回归，再对最终提交复跑门禁；其中P1删除身份问题应优先处理。可以保留已通过的质量检查结论，但当前仍不具备发布通过结论。

## 独立报告与证据

- [后端独立复审](cr-round3-backend-2026-10-18.md)：8个定向实验，7项修复/历史保护验证，1项终端建立取消缺陷复现。
- [前端独立复审](cr-round3-web-2026-10-18.md)：2项DOM缺陷复现；副本47项通过含2项错误行为断言，不等于修复通过。
- [文件独立复审](cr-round3-files-2026-10-18.md)：取消、锁、活动清理及nolint静态核对。

临时日志：`/tmp/vtc-r3-go.json`、`/tmp/vtc-r3-race.json`、`/tmp/vtc-r3-lint.log`、`/tmp/vtc-r3-web.log`、`/tmp/vtc-r3-web-build.log`、`/tmp/vtc-r3-spec.log`、`/tmp/vtc-r3-mod.log`、`/tmp/vtc-r3-vet.log`、`/tmp/vtc-r3-whitespace.log`。临时实验位置见各独立报告。未合入、创建tag或发布。
