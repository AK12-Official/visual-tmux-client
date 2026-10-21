# 第三轮发布级独立 CR：前端与文件身份边界

日期：2026-10-18；审阅者：独立子 Agent `review_web`。

本轮重新检查当前工作区的实际实现，并运行项目测试及隔离 DOM 复现。未修改业务代码，未连接真实主机或用户正在运行的服务。

结论：RC-2、RC-3 的已报告触发路径均关闭，信任版本修复保持有效；仍发现 **1 个 P1、1 个 P2**。现有连接身份保护仅覆盖文件保存，尚未覆盖等待中的删除；Reconcile 也未更新新引入的连接身份。

## 旧问题关闭矩阵

| 项目 | 结果 | 本轮证据 |
|---|---|---|
| RC-2：切 B 后 A 的过期草稿可保存 | 原问题关闭 | availableHosts 提供全部主机版本；isTabRevisionStale 按 tab.hostId 查询；save 按实际 tab 检查。新增 RC-2 DOM 回归通过 |
| RC-2：提交旧草稿缺乏服务端连接身份约束 | 保存链路已建立 | OpenFile.connectionRevision → beginSave → saveOpenFile → writeFile(expectedConnectionRevision) → expected_connection_revision → HTTP writeFile 对 runtime.Target.ConnectionRevision 比较。后端已有匹配/冲突测试；本子 Agent 静态核对后端，不重复运行 Go 测试 |
| RC-3：discovery 503 导致草稿卸载丢失 | 关闭 | hasInitializedWorkspace 区分首次发现失败与使用中错误；后者显示横幅并保留 app__body。新回归真实持有草稿、503、Retry 后检查内容，执行通过 |
| trust revision flow | 保持关闭 | confirmHostTrust 返回 revision 回填、消费 challenge 清除，HostManagerModal DOM 文件执行通过 |
| 第一轮 F1–F4 | 原路径保持关闭 | workspaceGuards 全文件执行通过，含捕获 hostId 的批量操作、迟到文件读取、保存等待删除、最后主机删除保留组件 |

## R3-W1 / P1：等待中的删除未绑定 connectionRevision，可删除重配置后服务器的文件

位置：`hub/web/src/components/files/FileManagerOverlay.vue:1242`、`:1298-1300`；`hub/web/src/files/api.ts:420-425`；`hub/internal/transport/http/files.go:378-391`。

触发过程：

1. 主机 ID A 的 connectionRevision=1，文件正在保存。
2. 用户确认删除该文件；deleteTarget 等待 saving.idle。
3. 等待期间在主机管理中把 A 的地址或凭据改为另一个目标，connectionRevision=2；hostId 不变。
4. 旧保存请求结束（成功或失败均会释放 saving）；deleteTarget 继续发送不带预期连接版本的删除。
5. 请求经主机 ID 解析到当前 runtime，删除的是新连接目标上的同路径文件。新 runtime 自身并未失效，因此 runtime 的失效检查无法辨认这次确认发生在旧连接上。

**DOM 实测**：在 `/tmp/cr-round3-web` 副本挂载真实 FileManagerOverlay，挂起保存 Promise，确认删除，更新 availableHosts 和 hostRevision 至 2，再释放保存。捕获到：

```text
POST /api/hosts/remote-1/files/delete
{"path":"/srv/work/a.txt","recursive":false}
```

请求未被取消，也不带 expected_connection_revision。仅 mock 网络，无实际删除。

建议：所有有副作用的工作区操作在用户发起/确认时捕获 hostId + connectionRevision；删除等待完成后仍校验该身份，并在 API 请求中携带，服务端在使用 runtime 前验证。仅前端比对不能覆盖其他浏览器改配置。至少增加“等待保存 → 同 hostId 连接变更 → 保存结束 → 删除拒绝”回归，并检查同类批量会话操作和重命名是否也需要统一目标约束。

## R3-W2 / P2：连接变更后 Reconcile 不更新 connectionRevision，文件始终无法保存

位置：`hub/web/src/components/files/FileManagerOverlay.vue:879-896`，关键行 896；版本拒绝在 `:295-298`。

现在文件打开时记录 connectionRevision，保存会检查该版本。但 reconcile 只写回 `revision: props.hostRevision`，result.file 仍带旧 connectionRevision。因此按照界面提示 Reconcile、确认丢弃旧编辑并从新目标重载之后，版本守卫仍拒绝后续修改。

**DOM 实测**：以 revision=1、connectionRevision=1 打开并编辑文件，更新到 2/2，点击 Reconcile 并确认重载。得到 tab.revision=2、tab.connectionRevision=1；继续编辑后 Save 仍禁用，程序调用 save 也不发送请求。临时用例通过对该错误行为的断言。

影响：正常修复流程无法完成；只能关闭重开文件，保留编辑时还需手动复制。跨主机草稿 Reconcile 使用 `props.hostRevision` 还会写入当前选中主机而非草稿所属主机的 revision，属于同一身份更新问题。

建议：Reconcile 按草稿 hostId 捕获并验证实际读取目标的 revision/connectionRevision，确认后成对更新；不要用当前选中主机的 props 代替草稿目标版本。异步读取期间若身份再次变化，应拒绝应用结果或重新读取。增加带真实 connectionRevision 的重载后再次编辑保存测试；当前旧测试只设置 hostRevision，不能覆盖新增字段。

## 验证记录与边界

- 原工作区执行：`node --import ./test/register.mjs --experimental-strip-types --test src/workspaceGuards.dom.test.ts src/components/HostManagerModal.dom.test.ts src/api.test.ts src/files/api.test.ts`，4 个测试文件均通过。
- `/tmp/cr-round3-web` 为当前前端源码副本；node_modules 链接现有安装。只在副本追加 2 个 DOM 复现，不更改业务源码。
- 副本 FileManagerOverlay.dom.test.ts 共 47 项通过：45 项现有测试 + 2 项错误行为复现。通过的含义是错误确实发生，不是修复通过。输出保存在 `/tmp/cr-round3-web-results.txt`。
- RC-2 新用例覆盖 metadata revision 的跨主机守卫，但未设置 availableHosts/connectionRevision，尚不足以证明完整连接身份生命周期；本轮 R3-W2 实验补足了这一空白。
- 未进行真实 SSH/SFTP/tmux、真实浏览器视觉交互、压力测试；后端其他生命周期风险由独立后端审阅覆盖。
