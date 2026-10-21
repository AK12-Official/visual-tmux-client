# 第七轮后端有限发布回归

审查对象：HEAD `93718a1051055553480d40b17b1c8203ef13e35f` 加当前第七轮工作区，2026-10-21。本轮主要变更位于前端，因此后端仅复核关键保护，不扩展为新的全系统审计。

**无高置信新增 P1/P2 findings；本轮范围未发现已修保护回退。** 后端有限复核没有提出新的合入阻塞，完整发布判断仍由主审结合其他报告作出。

| 检查项 | 当前结果 |
| --- | --- |
| 会话创建、改名、删除的连接版本 | 保持使用共用校验器；匹配/过期版本测试通过。 |
| 非法或冲突版本输入 | 仍拒绝解析失败及不同来源冲突；输入验证测试通过。 |
| 终端建立预算 | setupCtx 和关闭专用连接的 watcher 保留，channel/PTY/exec 三阶段取消测试通过。 |
| 终端生命周期交接 | handover 同步及停止建立 watcher 保留；HTTP handler 返回和随后取消 setup context 的存活测试通过。 |
| WS epoch | 仍在 resolve/票据兑换前捕获；兑换期间删除主机的测试拒绝旧附件。 |
| 错误主密钥迁移 | validator 仍在迁移提交前执行；错误密钥阻止 v1 迁移的测试通过。 |

本轮执行：

```sh
go test ./internal/host ./internal/transport/http ./internal/transport/ws -run 'TestRemoteTerminal(SurvivesServeHTTPReturn|SetupCancellation|HandoverImmunity)|TestRegistryWrongKeyPreventsV1Migration|TestWSDeletedHostUsesNewEpoch|TestSession(Create|Rename|Kill)ExpectedConnectionRevisionConflict|TestExpectedConnectionRevisionInputValidation' -count=1 -v
```

**9 个顶层测试及 3 个终端建立子测试全部通过。** 这些测试的 PASS 均表示预期行为通过，没有以观察缺陷为断言的实验。本轮没有重跑全库或 race；第六轮 race 记录不冒充为第七轮新结果。

只新增本报告，未修改业务或仓库测试代码。运行使用既有本地临时夹具和 ParentGuard，通过工具授权执行；没有 ad hoc tmux 操作，没有连接用户运行服务、真实 SSH 或读取用户凭据。

范围限制：未重新审查全部文件事务、前端异步回调、真实 OpenSSH 兼容、跨平台迁移或崩溃恢复；以上由其他审查或发布验证覆盖。本报告不把有限回归通过等同于全产品无缺陷。
