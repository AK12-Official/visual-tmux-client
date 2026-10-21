# 第七轮独立 CR：文件副作用与连接版本

日期：2026-10-21；审阅者：`review_web`。本轮基于当前工作区重新检查 R6-W1 及相邻文件异步副作用；未修改业务源码、未访问真实服务。

结论：**R6-W1 的数据丢失已关闭，本轮未发现新增高置信 P1/P2，在本次限定审阅范围内通过。** pending 跨连接版本的保守等待已实测，下文作为非阻断改进建议记录。

## 旧项关闭矩阵

| 检查项 | 结论 | 证据 |
|---|---|---|
| R6-W1：旧删除成功移除新连接草稿 | 关闭 | 删除后按 tabMatchesScope(hostId, connectionRevision) 清理；R6-W1 DOM 回归验证新草稿、标签 ID、连接版本均保留 |
| 删除/重命名的路径变更记录 | 已按连接版本隔离 | getChanges 使用 hostId + connectionRevision，旧结果不再修改新版本 path changes |
| rename 对标签的副作用 | 已限定连接范围 | retargetTabs 的移动、同名冲突处理和清理使用 tabMatchesScope |
| save 的成功/错误结果 | 已增加版本守卫 | live tab 的 connectionRevision 与起始 tab 不同则返回，不更新保存观察或触发旧请求的覆盖提示 |
| readRenamedTab 的迟到内容 | 已增加版本守卫 | 检查 live.connectionRevision 与起始版本一致 |
| R5 目录缓存与 create 回调 | 回归通过 | 文件组件与 workspaceGuards 全文件执行通过，包含 R5 新用例 |
| finally/pending | 计数清理配对，但范围未完整隔离 | begin/end 均传原 hostId；尚未携带 connectionRevision，见下项 |

## 非阻断改进建议：pending 跨连接版本采用保守等待

定位：`hub/web/src/components/files/FileManagerOverlay.vue:821`、`:1556-1558`、`:1627`；`hub/web/src/files/pending.ts:114`。

pending key 只有 hostId + path，没有 connectionRevision。即使标签已经 Reconcile 到新连接，save 仍调用 `deleting.isPending(tab.path, tabHost)`，会命中旧连接的删除记录。

复现是在当前 R6-W1 回归中增加一个步骤：

1. 版本 1 的删除请求已发送，响应挂起。
2. 同 hostId 改为版本 2，Reconcile 标签到版本 2，并编辑新草稿。
3. **在旧删除响应返回之前**点击保存。

**DOM 实测结果**：save 发出“delete of … is in flight”的提示，没有任何 `/files/write` 请求。释放旧删除响应后 pending 才被清理。现有 R6 用例在释放旧响应后才验证保存，因此没有覆盖这段窗口。

影响与判断：旧删除尚未完成时，新连接相同路径的保存被保守拒绝，但草稿完整保留，旧请求结束后可以恢复。实验只人为挂起了 Promise，没有证明产品存在不可恢复状态，也未找到明确规格要求不同连接版本必须并行。因此不把实现未分区本身认定为缺陷，不计入 P1/P2。当前 pending 使用计数，旧 end 不会清除其他尚未完成操作的计数。

若产品要求连接切换后立即独立操作，可考虑让 pending API 及调用者使用 hostId + connectionRevision + path 的完整范围；begin/end、isPending、idle 必须传同一捕获身份。不能简单清空所有 pending，否则旧 finally 可能影响新计数，且可能破坏同一连接内保存/删除的顺序保证。补充“旧连接仍 pending 时，新连接已 Reconcile 的文件可保存；旧 finally 不清除新连接 pending”的回归。

## 验证记录与局限

- 原工作区运行 FileManagerOverlay.dom.test.ts、workspaceGuards.dom.test.ts、files/pending.test.ts、files/tabs.test.ts、files/tree.test.ts：5 个测试文件均通过。
- `/tmp/cr-round7-web` 是当前前端源码副本，仅在副本现有 R6-W1 测试中增加释放旧响应之前的 save 观察；50 项测试通过。该插入断言验证错误行为确实存在，不表示产品正确。
- 日志：`/tmp/cr-round7-web-results.txt`。保留原有旧响应返回后草稿不丢失的断言，仍通过。
- 无真实 SSH/SFTP/tmux 或浏览器端到端测试。没有扩展到无关功能，也没有把暂时阻塞描述为后端错误写入或数据损失。
