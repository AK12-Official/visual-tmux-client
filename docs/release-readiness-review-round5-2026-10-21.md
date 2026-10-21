# 第五轮 CR 与发布准入评估

日期：2026-10-21。对象：HEAD `93718a1051055553480d40b17b1c8203ef13e35f` 加 work agent 第五轮未提交修复。三位独立审查者分别复核请求版本、目录工作区和 HTTP watcher；主审核对实现与证据并执行全套质量检查。没有修改业务代码、替换运行程序或操作真实 SSH 主机。

**结论：上轮三个原触发路径已关闭，质量门禁全部通过；当前仍有 1 项 P1、1 项 P2，建议修复后再合入发布。** 两项都来自本轮连接身份修复的相邻路径，均有真实 Vue 组件的隔离 DOM 实证，不是复述历史问题。

## R5-1 / P1：旧目录条目被赋予新连接版本，可能删除新服务器文件

位置：[FileManagerOverlay.vue:252](../hub/web/src/components/files/FileManagerOverlay.vue#L252)、[删除目标版本:1331](../hub/web/src/components/files/FileManagerOverlay.vue#L1331)、[tree.ts:190](../hub/web/src/files/tree.ts#L190)。

availableHosts 更新后，watcher 直接把 tree.connectionRevision 改成最新版本，却不清除 children/expanded、不重新读取目录。hostId 没变，负责 resetTreeState 的 hostId watcher 不执行。于是原服务器读取的可见条目保留，但后续操作按最新连接身份提交。loadDirectory 缓存命中也只验证 hostId/path，不验证缓存来源版本。

**实证：** 在连接版本1显示 `/srv/work/a.txt`，更新同一主机为版本2，不刷新目录。DOM 实验确认没有新增列表请求、旧条目仍在；执行该旧条目的删除，实际捕获：

```text
POST /api/hosts/remote-1/files/delete
{"path":"/srv/work/a.txt","recursive":false,"expected_connection_revision":2}
```

这是新版本的合法请求，后端预期版本校验不会拒绝。若新服务器存在同路径文件，删除将作用于它。实验仅 mock 网络，未实际删除文件；证实的是“旧列表生成面向新连接的合法删除”，不声称用户数据损失已经发生。

应将当前树身份视为其内容来源，而非可直接改写的标签。连接版本变化时清除或替换树，重新发现/加载目录，并在新列表就绪前禁用基于旧条目的修改。缓存命中与每个操作都应绑定来源身份；metadata-only 改名不应不必要地清空工作区。补充同 ID 改连接的 DOM 回归，验证旧行消失或禁用，不能产生面向新目标的请求。

## R5-2 / P2：旧连接创建成功的迟到响应会污染新工作区选中项

位置：[App.vue:650](../hub/web/src/App.vue#L650)。相关改名回调 [App.vue:681](../hub/web/src/App.vue#L681) 也只使用 hostId 搬移状态，需一并核对；本项实证为创建。

请求入口已携带 connectionRevision，但 onCreate 成功后仅比较 hostId；同 ID 已改连接时，旧响应仍会刷新当前新目标，然后 select 旧目标返回的会话名。await refresh 后也没有再次验证目标身份。

**DOM 实证：** 挂起版本1的创建成功响应，将同一hostId更新为版本2并刷新主机信息，新目标列表只有 `new-target-session`。释放旧响应 `{name:"old-target-session"}` 后，TerminalView 实际选中 `old-target-session`。不存在该名称时显示错误/结束状态，存在同名会话时则选中用户未在该目标创建的会话。

应在每个 await 后、选中/面板缓存/终端状态变更前，验证捕获的 hostId + connectionRevision。服务端入口校验保护执行目标，不能替代客户端迟到结果隔离。增加旧成功响应在连接变化、主机切换、refresh期间切换后均不污染新工作区的回归。

## 上轮关闭矩阵

| 上轮项 | 本轮判断 | 依据 |
| --- | --- | --- |
| R4-1 会话 create/rename 缺少预期版本 | 请求入口原问题关闭 | 前端捕获并发送版本；后端create/rename/kill与文件修改共用checkExpectedConnRev。上轮过期PATCH反例现409且无改名副作用。成功回调另有R5-2 |
| R4-1补充：非法query覆盖旧header放行 | 关闭 | 非法版本400；body/query/header不同值400；snake/camel别名冲突400。独立重放和输入矩阵通过 |
| R4-2 A迟到目录覆盖B | 原跨hostId路径关闭 | 全局递增ticket不复用，落地校验hostId/connectionRevision；startDirectory await后检查host；新增DOM/tree回归通过。同ID连接变化仍有R5-1 |
| R4-3 handler返回后watcher使用writer | 关闭 | stop关闭停止信号并等待watcherDone；成功响应前stop，错误出口defer覆盖；stop先于cancelWrite，取消分支操作writer前二次检查停止信号。四项相关HTTP测试带race通过 |

后端及文件独立审查在各自本轮范围内没有新增高置信P1/P2 findings。不能把这两部分通过等同整个工作区通过发布评审。

## 本轮质量检查

| 检查 | 结果 |
| --- | --- |
| golangci-lint v2.13.2 原配置 | **0 issues**，未降低规则阈值 |
| lint配置验证 | 通过 |
| git diff --check | 通过 |
| go mod tidy -diff | 通过，无差异，未修改文件 |
| Go全量测试 `-count=1` | 12包、737个测试及子测试通过，无用例跳过；web包无Go测试文件 |
| 相关6包race `-count=1` | 464个测试及子测试通过 |
| go vet ./... | 通过 |
| npm test | 18个测试文件通过，无失败/跳过 |
| npm run build | Vue/TypeScript检查及Vite构建通过 |
| CGO=0四目标构建 | Linux amd64/arm64、macOS amd64/arm64均通过 |
| OpenSpec全库严格校验 | 8 passed, 0 failed：7份主规格及1个活动变更 |

工具：Go1.27.1、Node24.12.0、固定lint2.13.2，前端使用现有依赖安装。本轮没有另行运行CI的Go1.26.3或干净npm ci，不声称远端CI已通过；四目标仅构建，没有在四种平台执行运行测试。Go网络测试继续使用授权的隔离夹具与ParentGuard，没有临时tmux命令或对实际用户服务的操作。

## 架构、测试和文档一致性

本轮将预期连接版本解析收敛为共用函数，明确多来源冲突和非法输入，避免会话与文件各自实现不一致；HTTP watcher增加同步退出，修复了可实证的请求寿命越界。这些修复值得保留。

剩余问题是“请求入口身份已冻结，但已有视图和完成回调仍可把旧结果贴上新身份”。建议使用不可变目标引用贯穿目录数据、操作与响应处理，而不是单独为各条API补参数。R5-1尤其需要把来源版本绑定到树内容，不能在连接更新时仅给缓存改标签。

新增keep-alive测试未断言实际连接复用，且响应正文未读到EOF即关闭；它证明多次请求成功，不能单独证明同一TCP连接连续请求成功。同步watcher代码和writer-spy/race测试足以支持本轮关闭原问题，此处作为可选测试增强，不计为产品缺陷。可用httptrace.GotConn.Reused或服务端连接计数增强。

本轮没有确认新的部署文档阻断项，先前恢复/降级说明修正保持。真实OpenSSH兼容、真实浏览器终端/编辑器端到端及长期负载证据仍未补齐，不能以Go SSH/SFTP夹具名称代替实际验收。release tag流程仍需确认选定提交已通过主线Go测试/lint检查。

## 报告与证据

- [前端独立CR](cr-round5-web-2026-10-21.md)：两项DOM实证。副本组件49项与workspace21项的通过包含各1项观察错误的断言，**不代表两项缺陷已修复**。
- [后端独立CR](cr-round5-backend-2026-10-21.md)：现有定向测试与独立反例重放均符合预期，无新增P1/P2。
- [文件/HTTP生命周期独立CR](cr-round5-files-2026-10-21.md)：watcher关闭依据及keep-alive测试边界，无新增P1/P2。

主审日志：`/tmp/vtc-r5-go.json`、`/tmp/vtc-r5-race.json`、`/tmp/vtc-r5-lint.log`、`/tmp/vtc-r5-web.log`、`/tmp/vtc-r5-web-build.log`、`/tmp/vtc-r5-spec.log`、`/tmp/vtc-r5-mod.log`、`/tmp/vtc-r5-vet.log`、`/tmp/vtc-r5-whitespace.log`。DOM副本及日志路径见独立报告。

本轮只完成审查与报告，没有修复上述两项、合入、创建tag或发布。
