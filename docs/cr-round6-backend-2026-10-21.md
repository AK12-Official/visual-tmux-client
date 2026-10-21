# 第六轮后端发布 CR

审查对象：HEAD `93718a1051055553480d40b17b1c8203ef13e35f` 加第六轮当前工作区，2026-10-21。范围为统一请求版本校验及相邻会话/文件入口、runtime 租约、终端建立预算与生命周期交接、WS epoch、错误主密钥迁移保护。

**本范围内无高置信新增 P1/P2 findings，未观察到已修后端保护回退。没有发现本范围内阻止合入发布的问题。** 整体发布结论仍需合并前端、文件后端及主审验证结果。

## 当前保护复核

| 保护 | 静态复核与本轮运行结果 |
| --- | --- |
| 会话 create/rename/delete 的连接版本 | 三个入口仍在副作用前调用共用校验器。匹配版本放行，过期版本 409；创建、改名、删除定向测试通过。 |
| 非法或相互冲突的版本来源 | query/header 解析失败 400；body/query/header 不一致 400；body snake/camel 别名冲突拒绝。输入验证测试通过。 |
| 相邻文件变更接口 | 写入和元数据修改仍接入同一版本校验，不因会话修改而漏校验。写入/修改版本冲突测试通过。 |
| runtime 租约与主机隔离 | 版本化租约及主机隔离测试通过；本轮未发现修复重新引入按显示版本错误撤销连接的代码路径。 |
| 终端建立预算 | setupCtx 保留有限预算，channel、PTY、exec 挂起时可取消关闭专用 transport；三个子场景通过。 |
| 终端成功交接 | 成功后锁内设置 handover，停止并等待 setup watcher；handler 返回和 setup context 随后取消不再关闭已建立终端。两项存活测试通过。 |
| WS 删除交错 | epoch 仍在 resolve/兑换之前捕获，附件注册保留 epoch 检查；兑换期间删除的交错测试通过。 |
| 能力探测预算 | 使用有预算的独立 probe transport；channel open 不应答的取消测试通过。 |
| 错误密钥迁移保护 | Registry 仍传入凭据 validator，迁移事务提交前验证；错误密钥保持既有状态、阻止 v1 迁移两项测试通过。 |

上述结果支持保持此前相关问题的关闭状态，不把历史已修问题再次列为 finding。版本字段省略时保留既有兼容行为，并非本轮新回归。

## 本轮验证

执行定向竞态回归：

```sh
go test -race ./internal/host ./internal/transport/http ./internal/transport/ws -run 'TestRemoteTerminal(SurvivesServeHTTPReturn|SetupCancellation|HandoverImmunity)|TestCapabilitiesChannelOpenCancellation|TestRevisionedLeasesAndHostIsolation|TestRegistryWrongKey(PreservesState|PreventsV1Migration)|TestWSDeletedHostUsesNewEpoch|TestSession(Create|Rename|Kill)ExpectedConnectionRevisionConflict|TestExpectedConnectionRevisionInputValidation|TestFiles(WriteExpectedConnectionRevisionConflict|MutateExpectedConnectionRevisionConflict)' -count=1 -v
```

**14 个顶层测试及终端建立的 3 个子测试全部通过，未报告数据竞争。** 本轮没有以“复现缺陷”为成功断言的临时测试；PASS 表示当前被检查的预期行为通过。

测试使用仓库既有的本地临时夹具与 ParentGuard，经工具授权在 sandbox 外运行；没有 ad hoc tmux 操作，没有连接用户运行服务或真实 SSH 主机。未修改业务代码或仓库测试，仅新增本报告。

## 范围限制

本轮属于已修关键边界的发布回归审查，不是扩大到整个系统的安全审计。没有执行真实 OpenSSH、跨平台迁移、进程崩溃恢复或长期压力测试；没有据此宣称所有并发交错均已覆盖。文件完整事务、前端迟到响应与用户交互由其他独立报告覆盖，全库测试、lint 和构建由主审汇总。
