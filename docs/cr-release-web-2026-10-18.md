# 修复后发布级独立前端 CR

审阅日期：2026-10-18；审阅者：独立子 Agent `review_web`。重新审阅当前工作区，不以上一轮结论代替本轮验证。未修改产品代码，未连接用户服务或真实主机。

结论：上一轮 F1–F4 的原始触发路径及 trust revision 修复通过本轮复查，但发现 **2 个新的 P1 阻断项**，均以实际 Vue DOM mock 测试复现；尚不能给出前端发布通过结论。

## 原问题关闭矩阵

| 项目 | 本轮结论 | 证据 |
|---|---|---|
| F1 批量终止途中切换主机 | 原问题关闭 | onBulkKill 捕获 targetHostId，DELETE 与面板清理使用原目标；workspaceGuards 的 F1 DOM 用例通过 |
| F2 迟到读取被挂到另一主机 | 原问题关闭 | openFile 捕获 targetHostId/targetRevision；openingKeys 与 changes 按主机隔离；F2 DOM 用例通过 |
| F3 等待保存的删除被发到另一主机 | 原问题关闭 | deleteTarget 的等待、删除、清理和 pending end 使用同一 targetHost；F3 DOM 用例通过 |
| F4 删除最后主机卸载草稿 | 原问题关闭 | 空列表保留 activeHostRemoved；模板不再进入空状态分支；F4 DOM 用例通过。该用例只验证组件保持挂载，并未真的键入草稿，不等于覆盖全部丢稿路径 |
| trust revision conflict | 关闭 | confirmHostTrust 返回 revision 写回编辑表单，并清除已消费 challenge；保留用户未保存字段。HostManagerModal DOM 回归通过 |

## R1 / P1：切换主机可绕过旧草稿版本守卫，向已变更连接目标写入

定位：`hub/web/src/components/files/FileManagerOverlay.vue:247-253`，尤其 251；保存判断在该文件 `save()` 中复用 revisionStale。App 只传当前主机的 `hostRevision`，没有向文件工作区提供按主机查询的连接身份。

复现步骤：

1. 在 A 打开文件，草稿记录 revision=1，编辑但不保存。
2. A 的连接配置被修改，工作区收到 hostRevision=2；此时 Save 正确禁用。
3. 切到 B，A 的草稿仍保持打开。revisionStale 因 `tab.hostId !== host.value` 返回 false。
4. 点击 Save。请求实际发到 A 的 host ID，且不携带读文件时的 connectionRevision。

**本轮 DOM 实测**：在 `/tmp/cr-release-web` 副本，使用真实 FileManagerOverlay、项目现有 Vue/CodeMirror mock。断言 A revision 更新后 Save 禁用，切 B 后启用，并实际捕获：

```text
PUT /api/hosts/remote-1/files/write?path=%2Fsrv%2Fwork%2Fa.txt&size=19&expected_mtime=1000&expected_mtime_nanos=1000000000
body: hellomy stale draft
```

该请求只有文件时间戳校验，没有原连接身份。若 A 已改指向另一台服务器，旧草稿会被发送到新的目标。后端对请求执行期间的 runtime 失效检查不能辨认在配置变更完成之后才提交的旧草稿；文件时间戳也不能替代主机身份。

建议：草稿绑定 hostId + connectionRevision；父组件提供全部主机的连接版本映射，保存按草稿归属校验，而非按当前选中主机。服务端修改端点也应验证客户端预期连接身份。避免将改名产生的普通 revision 变化等同于连接变化。回归覆盖“更新 A 连接 → 切 B → 保存 A 草稿”和“其他窗口更新 A 后提交旧草稿”。

## R2 / P1：主机列表刷新暂时失败会直接丢弃全部未保存草稿

定位：`hub/web/src/App.vue:140-146`、`:914-917`、`:942`、`:1069`。

触发：已打开文件且存在草稿时，刷新主机列表（例如主机管理中的 hosts-changed）遇到 503/网络失败。loadAvailableHosts 写入 hostDiscoveryError；顶层 `v-if="hostDiscoveryError"` 改为错误视图，原 `v-else` app__body 被卸载，内部 FileManagerOverlay 的 tabs 一并销毁。该路径与 F4 已修复的“空列表”分支不同。

**本轮 DOM 实测**：挂载真实 App，建立含 `MY UNSAVED DRAFT` 的文件 tab；让下一次 `/api/hosts` 返回 503，触发 hosts-changed。断言错误横幅出现且 FileManagerOverlay 不再存在。恢复接口并点击 Retry 后，新 FileManagerOverlay 重新挂载，但 tabs.length 为 0。过程中没有丢弃确认。

建议：将主机发现错误作为工作区之上的非破坏性提示；已有工作区必须保持挂载，或将草稿存储提升到独立持久状态。首次启动失败与使用中的刷新失败应区别处理。补充真实编辑文本、接口失败、Retry 后仍保留文本的 App DOM 回归。

## 测试与覆盖范围

- 原工作区运行 `node --import ./test/register.mjs --experimental-strip-types --test src/workspaceGuards.dom.test.ts src/components/HostManagerModal.dom.test.ts`，2 个测试文件通过。
- `/tmp/cr-release-web` 为只用于复现的前端副本，node_modules 链接原安装；未修改产品源码。在副本添加两个断言“实际错误行为”的临时测试。
- 直接运行副本 FileManagerOverlay.dom.test.ts：45 项通过（44 项原用例 + R1 复现）；运行 workspaceGuards.dom.test.ts：19 项通过（18 项原用例 + R2 复现）。这里的通过表示错误行为被确定复现，不表示产品符合要求。
- 本轮聚焦异步主机身份、草稿生命周期、信任流程、新回归用例。没有验证真实浏览器渲染、真实 SSH/SFTP/tmux、性能或视觉无障碍。
- 原始 F1–F4 关闭仅针对描述的触发路径；不宣称同一主机 ID 被删除重建、连接切换期间的全部异步动作都已安全。R1 说明 connectionRevision 仍缺少端到端绑定。
