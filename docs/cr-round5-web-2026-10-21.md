# 第五轮独立 CR：文件树身份与会话变更

日期：2026-10-21；审阅者：`review_web`。本轮基于当前工作区重新核对实现，未修改业务源码，未连接真实服务。

结论：第四轮 R4-2 跨主机迟到列表原问题关闭；session create/rename 的预期连接版本请求链路补齐。仍发现 1 个 P1 和 1 个 P2，均有隔离 DOM 复现，属于本轮指定的连接身份生命周期范围。

## 旧项闭环

| 项目 | 结论 | 依据 |
|---|---|---|
| R4-2：A 迟到目录覆盖 B | 原问题关闭 | tree 带 hostId/connectionRevision，load ticket 全局递增且 reset 不复用；缓存落地检查身份。R4-W1 DOM 新回归通过 |
| startDirectory 的直接 A→B 迟到结果 | 已增加 host 检查 | cwd 请求及列表 await 后检查 targetHost；不把旧主机返回直接作为新主机目录 |
| session create/rename 请求未携带预期版本 | 原入口问题关闭 | App 捕获 connectionRevision，API 同时传 header/body；服务端 create/rename 调用 checkExpectedConnRev。新增 DOM/API 回归通过 |
| 既有草稿、Reconcile、等待删除修复 | 对应回归保持通过 | 本轮 FileManagerOverlay/Workspace 原有用例均执行通过 |

## R5-W1 / P1：同 ID 连接更新时旧目录缓存被赋予新版本，旧条目删除发往新目标

定位：`hub/web/src/components/files/FileManagerOverlay.vue:252-254`、`:1331-1334`；`hub/web/src/files/tree.ts:190-195`。

availableHosts 更新后，watcher 直接把 tree.connectionRevision 改为最新值，但没有清除 children/expanded、取消旧工作区或重新读取目录。hostId 未变化时，hostId watcher 不会调用 resetTreeState。因此从旧服务器读取的可见条目仍留在界面，却被视作新连接的树。loadDirectory 的缓存命中也仅按 hostId/path，不验证缓存来源的连接版本。

复现：

1. 连接版本 1 下打开 `/srv/work`，界面已有 `a.txt`。
2. 主机 ID 不变，将连接配置更新至另一目标，availableHosts 变为 revision=2 / connectionRevision=2。
3. 不刷新目录，执行旧可见条目的删除操作。

**本轮 DOM 实测**：更新 props 后列表请求计数不增加，旧行仍存在；调用同一行的删除处理器，捕获：

```text
POST /api/hosts/remote-1/files/delete
{"path":"/srv/work/a.txt","recursive":false,"expected_connection_revision":2}
```

这是新版本的合法删除请求，因此新增服务端版本校验不会拒绝。如果新服务器有同路径文件，会作用于该文件。实验只 mock 请求，没有实际删除；已证实“旧列表→新目标版本的删除请求”，未宣称实际损失已发生。

建议：检测当前树 connectionRevision 变化时，清空/替换该树并重新发现目录；在新列表返回前禁用目录变更操作。列表条目和操作目标须共享来源身份，不能在提交时把旧条目升级为新连接版本。缓存命中也须验证完整身份。补充同 hostId 连接变更的 DOM 用例，断言旧行消失/禁用，且不能向新目标提交基于旧行的操作。

## R5-W2 / P2：旧连接的创建成功回调仍选择新连接上的旧会话名

定位：`hub/web/src/App.vue:650-653`。同类 onRename 成功后状态搬移在 `:681-691` 也未检查捕获的连接版本，建议一并审查；本条实测复现的是 create。

请求入口虽然携带预期连接版本，但成功响应后的 UI 更新仅比较 hostId。旧连接创建已成功、响应尚未被界面处理时，同 ID 连接发生更新；迟到响应会 refresh 新目标，再直接 select 旧目标返回的会话名。

**本轮 DOM 实测**：挂起创建成功响应；将 host-a 的 registry revision/connectionRevision 更新到 2，并通过 hosts-changed 刷新父组件；新目标会话列表仅有 `new-target-session`。释放旧响应 `{name:"old-target-session"}` 后，TerminalView 的 session 实际为 `old-target-session`。新目标不存在该会话时会出现错误/结束面板；存在同名会话时会选中用户没有在此目标创建的会话。

建议：在任何成功后的选中/缓存/终端状态变更前校验完整目标身份；onCreate 还需在 await refresh 之后再次检查。通过服务端约束请求入口不能替代客户端迟到结果隔离。增加连接变更后旧成功响应不影响新工作区的回归。

## 验证记录与边界

- 原工作区 tree.test.ts、HostManagerModal.dom.test.ts、api.test.ts、files/api.test.ts 共 4 个测试文件通过。
- `/tmp/cr-round5-web` 为当前源码副本，仅副本追加上述 2 个 DOM 实验。FileManagerOverlay 共 49 项通过（48 原有 + 1 错误行为复现），workspaceGuards 共 21 项通过（20 原有 + 1 错误行为复现）。
- 复现实验以断言错误行为发生为成功标准，不表示产品正确。日志分别为 `/tmp/cr-round5-web-repro.txt`、`/tmp/cr-round5-workspace-repro.txt`。
- 没有访问用户服务、真实 SSH/SFTP/tmux，没有进行真实浏览器端到端测试。后端校验实现静态核对并结合前端测试；其 Go 集成验证由后端独立审阅负责。
