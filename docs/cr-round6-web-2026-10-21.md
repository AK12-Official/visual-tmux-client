# 第六轮独立 CR：连接身份与异步结果

日期：2026-10-21；审阅者：`review_web`。本轮只读业务源码，使用真实 Vue 组件及项目 mock 在副本验证，不访问实际服务。

结论：R5-W1、R5-W2 原问题已关闭；发现 **1 个高置信 P1**：旧连接删除成功的迟到响应会移除已转到新连接的未保存草稿。该问题已由 DOM 实验复现，当前审阅范围尚不能通过。

## 旧项关闭矩阵

| 原问题 | 当前结论 | 验证 |
|---|---|---|
| R5-W1：同 ID 连接变更保留旧树 | 关闭 | watcher 发现 connectionRevision 变化后关闭菜单、重置树、清空目录、显示加载并重启 startDirectory；新增 R5-W1 用例通过，包含旧 entry 直接调用删除被拒绝 |
| R5-W2：旧连接 create 成功响应污染新目标 | 关闭 | onCreate 在响应后及 await refresh 后均核对 hostId + connectionRevision + removed；R5-W2 DOM 回归通过 |
| R4 跨主机迟到目录响应 | 保持关闭 | 全局 load ticket、树身份校验和重置仍有效；回归通过 |
| startDirectory 连接版本切换 | 已加强 | await cwd/list 之后均核对 targetHost 与 targetConnRev，阻止旧工作区继续更新目录 |
| 已有草稿保存、Reconcile、等待删除 | 原问题回归通过 | 本轮六个相关测试文件全部通过；不表示删除成功回调的所有版本边界均覆盖 |

## R6-W1 / P1：旧删除成功响应清除了新连接的未保存草稿

精确位置：`hub/web/src/components/files/FileManagerOverlay.vue:1555-1557`。`deleteTarget` 在 await deleteEntry 成功后按 hostId + path 清理 tabs，没有比较 tab.connectionRevision。直到 `:1563` 的目录刷新阶段才检查当前连接版本，已经晚于草稿移除。

时间顺序：

1. 主机 A 的连接版本为 1，打开 `/srv/work/a.txt`。
2. 用户确认删除，版本 1 的 DELETE 已发出并在旧目标成功完成；仅成功响应尚未被前端处理。
3. 将同一个 hostId 的连接更新为版本 2。目录已按本轮修复正确清空重载。
4. 对保留的文件标签执行 Reconcile，标签正确更新为 connectionRevision=2；用户写入新草稿 `NEW CONNECTION UNSAVED`。
5. 版本 1 的 DELETE 成功响应到达。过滤条件只匹配 hostId/path，把版本 2 的标签及未保存内容一起删除。

**实际 DOM 结果**：在 `/tmp/cr-round6-web` 副本以 deferred mock 挂起 delete 响应；确认 Reconcile 后 tab.connectionRevision=2 且含新草稿；释放旧成功响应后，tabs.length 从 1 变为 0。不是推测性的网络结论，也不是声称 DELETE 被后端重定向；丢失发生在前端处理旧响应时。

建议：成功结果对 tabs、changes、缓存、pending 的修改须限定到操作捕获的完整目标身份。删除只可清理相同连接版本的标签，不能把新版本草稿视作旧删除的结果。对旧版本结果可给出明确完成提示，但必须保留新版本草稿。相邻 rename 成功后的 retargetTabs 同样应审查身份限定。补充“旧操作成功仅响应延迟 → 重配置 → Reconcile → 新编辑 → 旧响应到达”的 DOM 回归。

## 相邻 rename 的覆盖建议（不计入本轮 findings）

新 onRename guard 同时要求目标等于当前选中主机。如果 A 的连接版本不变，仅在 rename 等待时切到 B，成功结果不会搬移 A 的后台面板身份。隔离 DOM 实验观察：回到 A 并选择新名称时 TerminalView uid 从 189 变为 190，原面板没有沿用。建议补充后台主机改名的面板连续性回归，并区分“只切换当前视图”和“目标连接版本已失效”；本轮按约定不扩展为额外发布发现，也不声称已实测真实终端重连失败。

## 验证与边界

- 原工作区执行 FileManagerOverlay.dom.test.ts、workspaceGuards.dom.test.ts、files/tree.test.ts、HostManagerModal.dom.test.ts、api.test.ts、files/api.test.ts，6 个测试文件均通过。
- `/tmp/cr-round6-web` 为本轮前端副本；只在副本加入实验，不修改业务代码。
- 副本文件组件测试 50 项通过：49 项原有 + 1 项错误行为复现；工作区测试 22 项通过：21 项原有 + 1 项后台 rename 身份观察。
- 实验的“通过”指断言现象得到复现，不代表缺陷已经修复。日志：`/tmp/cr-round6-file-repro.txt`、`/tmp/cr-round6-workspace-repro.txt`。
- 未运行真实 SSH/SFTP/tmux、真实浏览器端到端或压力测试。没有将 mock 结果扩大为真实后端删除或网络重定向事实。
