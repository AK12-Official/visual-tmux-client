# 第四轮发布级 CR：前端与异步工作区

日期：2026-10-19；独立审阅者：`review_web`。重新读取当前工作区源码并执行测试，未沿用旧报告作为当前事实。本轮只读业务源码，无真实 SSH、tmux 或用户服务访问。

结论：R3-1 文件删除和 R3-2 Reconcile 原问题关闭。发现 **1 个独立 P2**：迟到目录列表能覆盖当前主机的树，并影响后续文件操作。会话 create/rename 的连接版本缺口已移交后端审阅统一计数，本报告不重复登记。

## 旧项关闭矩阵

| 原问题 | 当前结论 | 验证 |
|---|---|---|
| R3-1：删除等待保存期间连接变更 | 关闭 | deleteTarget 入口冻结 connectionRevision，等待后比对，并将预期版本传给 deleteEntry；DOM 回归通过 |
| 文件 create/rename/delete 缺少请求目标版本 | 链路已补齐 | 3 个前端 API payload 均携带 expected_connection_revision，对应 UI 在入口捕获；后端处理链路由独立后端审阅核验 |
| R3-2：Reconcile 不更新连接身份 | 关闭 | 按 tabHost 读取并比较开始/结束版本，重载后更新 revision + connectionRevision；新增回归验证再次编辑可保存，执行通过 |
| RC-2：跨主机切换绕过旧草稿版本保护 | 保持关闭 | 按草稿所属主机检查，文件保存携带连接版本；现有 RC-2 回归通过 |
| RC-3：发现主机失败导致草稿卸载 | 保持关闭 | 初始化后的错误使用横幅，现有 503→Retry 草稿保留回归通过 |
| trust revision conflict | 保持关闭 | HostManagerModal DOM 文件执行通过 |

## R4-W1 / P2：切主机后迟到目录列表覆盖当前文件树，点击访问另一主机的同路径

位置：`hub/web/src/components/files/FileManagerOverlay.vue:1565`；`hub/web/src/files/tree.ts:133-135`、`:176-178`。

切换主机时复用同一 tree 并清空 loads。loadDirectory 的票据是每个路径的计数，清空后重新从 1 开始，且票据不包含 hostId。A 和 B 同时请求同一路径时，两次请求可持有相同票据。A 的迟到结果于是通过检查，覆盖 B 的 children。

本轮隔离 DOM 实验使用真实 FileManagerOverlay 与项目既有 Vue mock：

1. A 的 `/` 列表请求挂起。
2. 切到 B，B 的列表返回 `B-only.txt`，界面正常显示。
3. 释放 A 列表，返回 `A-only.txt`。B 的树变成 A-only.txt，B-only.txt 消失；没有后续请求会自动纠正这一状态。
4. 点击这条来自 A 的列表行。实际捕获：

```text
GET /api/hosts/host-b/files/read?path=%2FA-only.txt
```

这不仅是短暂显示：当前 B 文件树显示 A 的枚举结果，后续访问却按 B 执行。在真实服务上可能得到 404，或打开 B 的同路径不同文件。此实验没有实施真实删除，因此不把风险扩大表述为已证实跨主机误删。

建议：树缓存与请求身份按主机/连接代数隔离；不要清空后复用仍在途的序号。可为每次加载使用不复用的 token，并在写缓存前确认工作区身份。startDirectory 也应在每个 await 后验证原工作区，避免旧工作目录探测继续影响新主机。加入“同路径 A 慢列表、B 已完成、A 后完成”DOM 回归，并断言树和点击目标均仍为 B 的真实条目。

## 交叉审阅事项

App 的 onCreate/onRename 和 api.ts:113-139 仍未传 connectionRevision，后端 createSession/renameSession 对应路径也没有预期版本检查，而 kill 已增加。证据已发送给 `review_host_backend`；由其报告统一评估和计数，不属于上表“文件 mutation 已补齐”的范围。

## 执行记录与局限

- 原工作区执行 workspaceGuards.dom.test.ts、HostManagerModal.dom.test.ts、api.test.ts、files/api.test.ts，4 个测试文件均通过。
- `/tmp/cr-round4-web` 为本轮前端副本，node_modules 链接原安装；只在副本追加 R4-W1 实验，未修改产品源码。
- 副本 FileManagerOverlay.dom.test.ts：48 项通过，即 47 个现有用例及 1 个错误行为复现。该新增用例“通过”表示确定复现问题，不代表产品正确。日志 `/tmp/cr-round4-web-results.txt`。
- 未执行真实浏览器端到端、SSH/SFTP/tmux 集成或压力测试。后端版本校验、HTTP 生命周期由其他独立审阅覆盖。
