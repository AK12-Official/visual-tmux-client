# 第六轮 CR 与发布准入评估

日期：2026-10-21。对象：HEAD `93718a1051055553480d40b17b1c8203ef13e35f` 加当前未提交修复。三位独立审查者复核前端、后端关键保护及文件/HTTP生命周期，主审执行质量门禁并独立重跑缺陷复现。没有修改业务代码、替换运行服务或操作真实用户主机。

**结论：上轮两项原触发路径关闭，门禁全部通过；仍有 1 项高置信 P1，暂不建议合入发布。** 本轮后端和文件/HTTP审查范围没有新增P1/P2 findings；当前阻断来自前端处理旧连接的迟到删除响应。

## R6-1 / P1：旧连接删除的迟到成功响应会丢弃新连接草稿

位置：[FileManagerOverlay.vue:1527](../hub/web/src/components/files/FileManagerOverlay.vue#L1527)、[变更记录:1536](../hub/web/src/components/files/FileManagerOverlay.vue#L1536)、[移除标签:1555](../hub/web/src/components/files/FileManagerOverlay.vue#L1555)。

删除发出前已经验证并携带connectionRevision，解决了旧请求误执行到新目标的问题。但await deleteEntry成功后，tabs清理仍只比较hostId和path，不比较连接版本；getChanges也仅按hostId记录删除。直到目录刷新阶段才检查连接版本，晚于草稿移除。

可复现顺序：

1. A的连接版本1打开`/srv/work/a.txt`，用户确认删除。
2. 版本1的删除在原目标成功完成，仅成功响应延迟。
3. 同一hostId切换到连接版本2；目录按最新修复正确清空并重载。
4. 用户对保留标签执行Reconcile，标签升级到connectionRevision=2，再输入`NEW CONNECTION UNSAVED`。
5. 版本1的删除响应到达，按hostId/path过滤掉版本2的标签，新草稿丢失。

**证据：实际Vue组件的DOM mock实验。** 子Agent在`/tmp/cr-round6-web`副本挂起delete响应，确认Reconcile后标签版本为2且存在新编辑；释放旧响应后tabs从1变为0。主审又单独运行该用例，结果一致。没有真实删除文件，问题不是后端请求重定向，而是前端错误应用旧成功结果。

临时实验通过表示成功观察到错误行为，不能当作功能验收通过。代码位置也明确解释了结果：`:1555–1557`的过滤条件缺少连接身份；`:1563`的世代检查只保护settleMutation，无法挽回已销毁草稿。

### 建议修复范围

将“操作结果只影响捕获的目标世代”作为完整约束：

- 删除结果只能影响相同hostId + connectionRevision的标签/变更记录；旧结果不能关闭或标记新连接草稿。
- 成功回调对changes、缓存、pending和标签的处理都应限定世代，而不是仅保护最后一次刷新。
- 相邻rename的retargetTabs以及其他异步完成回调按相同规则核对；不能只为delete过滤条件补一个局部判断，而保留其他旧结果更新新状态的路径。
- finally应正确释放原操作的pending记录，不能误解除同路径新世代操作的保护。

加入“旧变更成功但响应延迟→重配置→Reconcile→新编辑→旧响应到达”的DOM回归，并断言草稿文本、标签身份、变更记录和后续保存行为都保留。保留同一连接世代内删除成功正确清理标签的现有行为。

## 上轮关闭矩阵

| 上轮问题 | 本轮判断 | 依据 |
| --- | --- | --- |
| R5-1 旧目录缓存直接贴新连接版本 | 原问题关闭 | 检测connectionRevision变化后关闭菜单、重置树、清空当前目录并重新发现；重载中阻止操作，删除使用树的来源版本；新增R5-W1组件回归通过 |
| R5-2 旧create响应选中新连接会话 | 原问题关闭 | onCreate在响应后、await refresh后都检查hostId、connectionRevision和removed；onRename的主路径也增加版本判断；R5-W2回归通过 |
| 历史跨主机迟到列表 | 保持关闭 | 全局不可复用ticket、树身份检查及startDirectory每阶段身份核对仍在，相关回归通过 |
| 历史请求版本、终端预算/交接、epoch、错误key迁移 | 保持关闭 | 后端14个顶层测试及3个终端子场景带race通过，未见保护回退 |
| 历史上传预算、watcher退出、staging登记、权限与锁 | 保持关闭 | 文件侧静态复核及7个HTTP测试入口带race通过，未见相邻回退 |

这些关闭只覆盖报告描述的原触发路径，不把其外推为所有异步结果都已正确隔离。R6-1正是请求入口已保护、完成阶段仍缺身份约束的反例。

## 本轮质量检查

| 检查 | 结果 |
| --- | --- |
| 固定golangci-lint v2.13.2原配置 | **0 issues**，未放宽配置 |
| lint配置验证 / git diff --check | 通过 |
| go mod tidy -diff | 通过，无差异；未修改文件 |
| Go全量测试 `-count=1` | 12包、737个测试及子测试通过，无用例跳过；web包没有Go测试文件 |
| 相关6包race `-count=1` | 464个测试及子测试通过 |
| go vet ./... | 通过 |
| npm test | 18个测试文件通过，无失败/跳过 |
| npm run build | Vue/TypeScript与Vite构建通过 |
| CGO=0四目标构建 | Linux amd64/arm64、macOS amd64/arm64均通过 |
| OpenSpec严格校验 | 8 passed, 0 failed：7份主规格及1个活动变更 |

工具为Go1.27.1、Node24.12.0、固定lint2.13.2。没有另行运行CI的Go1.26.3/干净npm ci，也没有在四种目标平台执行运行测试。网络测试继续使用授权的隔离夹具和ParentGuard，未执行临时tmux命令。常规全绿没有覆盖本次DOM实验的可控交错。

## 非阻断观察与证据边界

- 文件keep-alive测试已补响应drain和httptrace.GotConn.Reused断言，上一轮连接复用的覆盖限制关闭，本轮实际通过。
- onRename的新guard同时要求当前选中主机等于目标。若A连接不变、改名等待时切到B，A成功响应不会搬移后台面板身份；DOM观察到返回A选择新名称时组件uid改变。建议补后台面板连续性回归。该证据只说明面板未沿用，没有证明真实终端重连失败或数据损失，本轮不另计发布finding。
- 未确认本轮新增部署文档阻断项。真实OpenSSH兼容、完整浏览器终端/编辑器交互、长期负载和崩溃恢复不在本轮实测范围；GoSSH/SFTP夹具不等于真实OpenSSH验收。
- 本轮主要架构缺口是“源身份”未贯穿变更结果的全部副作用。建议按上述统一约束处理，而非继续逐调用点只加刷新判断。

## 独立报告与复现记录

- [前端独立报告](cr-round6-web-2026-10-21.md)：R6-W1的精确时间顺序、DOM实证及原问题关闭矩阵。报告在Agent额度中断前已完整落盘；主审读取副本与日志并重跑缺陷用例后汇总。
- [后端独立报告](cr-round6-backend-2026-10-21.md)：限定范围无新增P1/P2，关键保护定向race通过。
- [文件独立报告](cr-round6-files-2026-10-21.md)：限定范围无新增P1/P2，上传和复用连接回归通过。

主审单项重放（在`/tmp/cr-round6-web`执行）：

```sh
node --import ./test/register.mjs --experimental-strip-types --test --test-name-pattern='^CR6: old delete success' src/components/files/FileManagerOverlay.dom.test.ts
```

日志`/tmp/vtc-r6-parent-dom.log`，观察结果为旧delete响应移除connRev2脏标签。副本包含49条原用例和1条缺陷实验，完整日志`/tmp/cr-round6-file-repro.txt`；另一个后台rename观察日志`/tmp/cr-round6-workspace-repro.txt`。

常规检查日志前缀：`/tmp/vtc-r6-`，包括go.json、race.json、lint.log、web.log、web-build.log、spec.log、mod.log、vet.log、whitespace.log。本轮仅审查与记录，没有修复R6-1、合入、创建tag或发布。
