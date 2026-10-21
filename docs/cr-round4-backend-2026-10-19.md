# 第四轮发布级 CR：终端建立与请求身份

审查对象：HEAD `93718a1051055553480d40b17b1c8203ef13e35f` 加第四轮未提交修复，2026-10-19。仅新增报告；隔离实验放在 `/tmp/vtc-round4-cr/`，没有修改业务或仓库测试代码，没有访问真实用户服务或 SSH。

**结论：第三轮 R3-3 已修复；本轮发现 1 项 P2，未发现新的 P1。** 新接入的连接版本约束保护了会话删除，但尚未覆盖会话改名和创建的浏览器/API 链路。

## 关闭与保护情况

| 项目 | 当前状态 | 本轮依据 |
| --- | --- | --- |
| R3-3：终端建立阶段取消无效 | 关闭 | NewProcess 使用 30 秒 setupCtx；channel/PTY/exec 期间取消关闭专用 transport；成功前检查取消并锁内标记 handover，等待建立 watcher 退出后移交。三阶段 50ms 超时测试通过。 |
| 历史 RC-1：handler 返回后终端关闭 | 保持关闭 | 真实 HTTP/WS handler 返回存活测试及 handover 后主动取消 setup context 的回显测试通过，定向 race 检查通过。 |
| 历史 RC-5：兑换后捕获 epoch | 保持关闭 | handler 仍在 resolve/兑换之前捕获 epoch；兑换时 CloseHost 的交错测试拒绝附件。 |
| 历史 RC-6：错误 key 先迁移后失败 | 保持关闭 | `TestRegistryWrongKeyPreventsV1Migration` 本轮通过。 |
| 会话 DELETE expected connection revision | 合法版本路径生效 | 新测试的匹配版本放行、旧版本返回 409，通过本轮重跑。输入解析仍有补充观察，见下文。 |
| 文件写入/创建/改名/删除版本冲突 | 定向冲突测试通过 | `TestFilesWriteExpectedConnectionRevisionConflict`、`TestFilesMutateExpectedConnectionRevisionConflict` 通过；完整文件事务由其他 CR 覆盖。 |

30 秒默认预算根据代码确认，本轮没有等待完整 30 秒；运行验证使用更短调用者 deadline，以覆盖相同取消路径。不把有限定向 race 测试等同于所有交错都已验证。

## R4B-1 · P2：会话创建和改名未携带/校验连接版本，仍可操作同 ID 的新目标

主要位置：[router.go:168](../hub/internal/transport/http/router.go#L168)，关联 [router.go:146](../hub/internal/transport/http/router.go#L146)、[api.ts:113](../hub/web/src/api.ts#L113)、[App.vue:649](../hub/web/src/App.vue#L649)。

新版本约束只添加到 `killSession`。`createSession` 和 `renameSession` 仍按 host ID resolve 当前 runtime 后直接执行，既不接收浏览器原先看到的 connectionRevision，也不校验请求中显式提供的同名 header/query。前端 onCreate/onRename 与 API 签名也只传 host ID。前端独立 CR 已交叉确认该链路，本报告统一计数。

**触发：** 浏览器 A 仍显示某主机旧连接配置的会话列表；浏览器 B 修改该注册的连接配置，或删除后复用同 ID；A 在刷新前点击创建或改名。服务器按该 ID 获取新 runtime，旧列表中的同名会话可能在新目标上被改名，或在新目标创建用户原想在旧目标创建的会话。现有 DELETE 版本检查说明该身份变化已被产品视为需要拒绝的条件，但同样的保护尚未覆盖另外两种副作用。

**本轮实证：** 临时 `TestRound4StaleRenameAccepted` 使用真实 HTTP router，注入 ConnectionRevision=3 的当前 runtime 和含 `shared-session` 的 mock session service。向 PATCH 同时发送 `expected_connection_revision=1` 和 `X-Expected-Connection-Revision: 1`。响应为 200，service 内会话确实改名为 `renamed`，并非仅响应状态异常。该实验验证 API 漏校验；浏览器跨窗口完整流程没有另行跑 DOM 测试。创建路径由前后端源码确认，没有单独做创建反例。

建议：创建和改名与删除共用连接版本输入/校验协议，前端在用户开始动作时捕获目标 ID 与 connectionRevision，服务器在副作用前拒绝不匹配版本。异步成功回调也应核对版本，避免旧请求结果更新新目标的会话状态。兼容性允许旧客户端不传该字段时，应明确区分该策略与当前浏览器必须发送版本的行为；不要把有副作用接口的覆盖范围默认为“删除已修即全部已修”。

回归测试应覆盖同 ID 改连接、删除后重建、metadata-only rename 不误拒绝，以及过期 POST/PATCH 不调用 session service。

## 补充观察：显式非法版本值被静默当作没有约束

位置：[router.go:200](../hub/internal/transport/http/router.go#L200)，文件写入 query helper 有同类行为。

`ParseUint` 失败时 DELETE 继续执行。query 优先于 header；若 query 非法，即使 header 中有一个有效但过期的版本，也不会再检查 header。`TestRound4InvalidRevisionDeletes` 发送 `expected_connection_revision=invalid` 加 header 版本 1，当前 runtime 版本 3，结果为 204 且会话已删除。

此项不另列 P1/P2：目前浏览器发送合法数字，没有证据表明正常 UI 会产生该输入。它是已验证的 API 输入处理缺口，建议在统一版本校验时一起修正：显式提供但无法解析的版本返回 400；对同时提供的不同来源制定明确一致规则，避免无效高优先级输入使有效约束失效。未将此问题描述为绕过中央认证。

## 验证记录

定向重跑现有测试：

```sh
go test ./internal/host ./internal/transport/http ./internal/transport/ws -run 'TestRemoteTerminal(SetupCancellation|HandoverImmunity)|TestSessionKillExpectedConnectionRevisionConflict|TestFiles(WriteExpectedConnectionRevisionConflict|MutateExpectedConnectionRevisionConflict)|TestWSDeletedHostUsesNewEpoch|TestRegistryWrongKeyPreventsV1Migration' -count=1 -v

go test -race ./internal/host -run 'TestRemoteTerminal(SurvivesServeHTTPReturn|SetupCancellation|HandoverImmunity)' -count=1 -v

go test -overlay=/tmp/vtc-round4-cr/overlay.json ./internal/transport/http -run '^TestRound4' -count=1 -v
```

前两条通过；第三条的两个测试均以“观察到上述缺陷”为断言，PASS 表示复现成功，**不是两项行为符合验收预期**。HTTP 复现仅操作 mock service，终端测试使用现有本地 SSH 夹具和 ParentGuard；按工具授权在 sandbox 外运行需要本地隔离 socket 的测试，未执行 ad hoc tmux 操作。

覆盖限制：未做真实 OpenSSH、跨浏览器完整生命周期、全库压力或跨平台验证；不重复主审的全库测试与 lint 结论。没有在本轮有限审查中确认新的 terminal context/epoch 回归或凭据迁移 P1/P2，但 R4B-1 表明请求身份约束仍需覆盖所有受影响的副作用接口。
