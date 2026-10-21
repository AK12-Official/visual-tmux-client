# 第四轮 CR 与发布准入评估

审查开始：2026-10-19；收尾：2026-10-21。对象：HEAD `93718a1051055553480d40b17b1c8203ef13e35f` 加当前未提交修复。三位独立审查者复核前端、后端和远程文件，主审核对门禁并补充 watcher 实验。仅新增报告及 /tmp 隔离实验，没有修改业务代码、替换服务或访问用户远程主机。

**结论：上一轮 5 项原触发路径全部关闭，未发现新的 P1；本轮仍有 3 项 P2，建议修复后再合入发布。质量门禁本轮全部通过。**

## 当前发现

### R4-1 / P2：会话创建、改名尚未绑定预期连接版本

位置：[router.go:146](../hub/internal/transport/http/router.go#L146)、[改名:168](../hub/internal/transport/http/router.go#L168)、[api.ts:113](../hub/web/src/api.ts#L113)、[App.vue:649](../hub/web/src/App.vue#L649)。

本轮已给文件修改和会话删除补充 connectionRevision，但 createSession/renameSession 的前后端链路仍只有 hostId。另一浏览器修改连接地址、凭据或删除后复用同 ID 时，旧页面可在刷新前向新 runtime 创建会话或改名同名会话。当前 runtime 本身有效，因此仅检查服务器执行中的租约不能识别用户原先看到的目标身份。

**本轮 HTTP 实证：** 当前 mock runtime 的 ConnectionRevision=3，向真实 router 的 PATCH 同时传入 query 和 header 的预期版本1，仍返回200，mock service 内会话实际改名。前端也没有发送这一字段；创建路径为源码核对，未单独执行创建反例。未连接真实服务器或改动真实会话。

建议：创建、改名、删除共用预期连接身份协议，当前浏览器动作开始时捕获并发送版本，后端副作用前拒绝过时版本，完成回调也核对目标版本。若保留旧客户端不传字段的兼容入口，应明确与当前浏览器的强约束区别。

补充输入校验：DELETE/query 显式非法版本被静默当作无约束；当非法 query 优先于有效但过期 header 时，header 也不再检查。独立实验以该组合得到204并执行删除。文件 write query helper 同类处理。此处没有中央认证绕过，当前 UI 也没有产生非法值的证据，故不独立新增问题计数；统一协议时应对显式非法版本返回400并定义多来源冲突规则。

回归：同ID改连接、删除后重建、普通metadata改名不误拒绝，以及过期POST/PATCH不调用service。

### R4-2 / P2：迟到目录响应可以覆盖另一主机的文件树

位置：[FileManagerOverlay.vue:1565](../hub/web/src/components/files/FileManagerOverlay.vue#L1565)、[tree.ts:133](../hub/web/src/files/tree.ts#L133)、[写入缓存:177](../hub/web/src/files/tree.ts#L177)。

切换主机清空 tree.loads；目录请求票据又从每路径1开始，不包含host或连接世代。A与B请求相同路径时可能持有相同票据，A迟到结果通过检查并写入B当前树。

**本轮 DOM 实证：** 挂起A的 `/` 列表，切B并显示 `B-only.txt`，再释放A的 `A-only.txt`，B树被覆盖且不会自动恢复。点击该行实际发出 `GET /api/hosts/host-b/files/read?path=%2FA-only.txt`，即展示来自A，操作访问B。可能得到404或打开B同路径的不同内容；没有实施真实删除，不表述为已证实误删。

建议：目录缓存和加载代数绑定 hostId + connectionRevision，或用全局不复用token；写入children前验证工作区身份。旧工作目录探测的每个await后也应验证身份。新增A慢列表→B完成→A返回的组件回归，检查树内容与点击目标均正确。

### R4-3 / P2：正常保存结束后 watcher 仍可调用 ResponseWriter

位置：[files.go:297](../hub/internal/transport/http/files.go#L297)、[watcher:302](../hub/internal/transport/http/files.go#L302)。

正常退出时defer先关闭stopWatcher，再cancelWrite。未及时调度的watcher会看到两个分支同时可读，select可能选writeCtx.Done，调用SetReadDeadline(time.Now())及Body.Close。handler没有等待watcher结束；这些动作可晚于ServeHTTP返回。ResponseController/ResponseWriter的请求生命周期已经结束，迟到的过期deadline还可能影响同一keep-alive连接下一请求。

**独立报告最初依据静态并发交错，主审于2026-10-21补充真实路由验证：** 临时Go overlay注入测试，使用实际router、mock文件服务和支持SetReadDeadline的spy writer，GOMAXPROCS=1控制调度；100次正常PUT中，51次观察到deadline方法在ServeHTTP返回后执行。这个比例只描述受控实验，不是线上发生概率。

实验没有使用真实TCP连接复现后继请求失败；已证实的是handler返回后仍使用writer的生命周期错误，keep-alive网络后果仍为推断。测试PASS表示观察到缺陷，不是行为符合规格。

建议：正常完成和业务超时明确区分，handler返回前同步等待watcher停止；不能把函数自身的defer cancel当作超时。增加正常快速保存后同连接下一请求的真实HTTP测试，并保留慢正文超时测试，避免修正常路径时重新丢失取消能力。

## 上轮问题处理情况

| 第三轮项 | 本轮判断 | 依据 |
| --- | --- | --- |
| R3-1 等待保存后删除新连接目标 | 原问题关闭 | 文件delete/create/rename捕获并携带连接版本；等待后检查版本，后端对应冲突检查生效。会话create/rename仍有R4-1，但未将文件删除旧问题重复计数 |
| R3-2 Reconcile后仍无法保存 | 关闭 | 按草稿归属读取并验证开始/结束身份，更新revision与connectionRevision，组件重载后再编辑保存回归通过 |
| R3-3 终端建立缺预算 | 原问题关闭 | setupCtx覆盖channel/PTY/exec，取消关闭transport；锁保护handover并等待watcher退出。三阶段取消、成功移交和HTTP返回后存活测试通过 |
| R3-4 HTTP正文超时接线错误 | 原问题关闭 | HTTP边界创建实际操作WithTimeout，同一ctx传入Write，ResponseController deadline可以打断真实TCP正文读取；新增慢正文测试通过。正常结束watcher仍有R4-3 |
| R3-5 活动staging登记过晚 | 原问题关闭 | 先登记活动保护，再发布持久化记录；登记失败中止、撤销保护并清理。新增发布顺序及登记失败测试纳入本轮全量检查 |

另复跑了历史改名租约保护、错误key不迁移、WS epoch、文件和会话DELETE版本冲突等定向检查，未发现对应历史原触发路径回退。关闭仅限所列场景，不等于整个模块完整安全审计通过。

## 本轮质量门禁

| 检查 | 结果 |
| --- | --- |
| golangci-lint v2.13.2，原配置 | **通过：0 issues**，规则配置未放宽 |
| lint配置验证 / git diff --check | 通过 |
| go mod tidy -diff | 通过、无差异，未修改依赖文件 |
| Go全量测试 `-count=1` | 12包、732个测试及子测试通过；无用例跳过；web包没有Go测试文件 |
| 相关6包race检查 `-count=1` | 459个测试及子测试通过，2026-10-21补齐 |
| go vet ./... | 通过 |
| 前端npm test | 18个测试文件通过，无失败/跳过 |
| npm run build | TypeScript/Vue检查及Vite生产构建通过 |
| CGO=0四目标构建 | Linux amd64/arm64、macOS amd64/arm64均通过，2026-10-21补齐 |
| OpenSpec严格校验 | 8 passed, 0 failed（7份主规格、1个活动变更） |

工具为Go1.27.1、Node24.12.0和固定lint2.13.2，使用现有前端依赖安装；没有把结果等同于CI的Go1.26.3/干净npm ci已执行，也没有在四个平台上执行运行测试。前端及全量Go/静态门禁在2026-10-19执行，收尾时核对相关源码修改时间均早于该次全量测试完成，没有发现新的代码修改。

后端测试通过工具授权使用既有隔离夹具和ParentGuard，未执行临时tmux命令。所有额外缺陷实验只在本地mock/overlay/临时前端副本上运行；没有访问实际用户SSH或服务。

## 架构、文档及剩余证据边界

本轮终端建立/attachment寿命移交、上传预算贯通、活动标记先于记录发布，均直接解决了此前边界错误；关键tracking失败也不再静默忽略。lint、依赖与格式保持干净，未发现本轮增加宽泛抑制或降低检查阈值。

剩余架构问题仍是协议一致性：连接身份应覆盖同一类所有副作用接口；树请求代数不应在切换后复用；后台goroutine必须在所属HTTP请求返回前终止其writer副作用。这三项均有定位明确的反例，建议围绕统一接口/所有权修正并加入回归，避免只修单一调用点。

README/SECURITY/示例配置的上轮恢复说明修正保持；没有确认本轮引入新的部署文档阻断项。先前记录的真实OpenSSH兼容性、浏览器视觉/编辑器/终端完整端到端及长期负载证据仍未补齐；名为Real OpenSSH的测试实际采用Go SSH/SFTP夹具，不应作为真实服务端已验收的证明。release tag工作流没有Go lint/test前置依赖，发布选定提交还需确认主线CI成功。

本轮无新P1不等于可以忽略P2；建议修复上述三项再做最终准入检查。本轮未合入、创建tag或发布，也未自动修改这些缺陷。

## 详细报告与日志

- [后端独立复审](cr-round4-backend-2026-10-19.md)：建立生命周期修复验证、stale rename API实证和非法版本补充观察。
- [前端独立复审](cr-round4-web-2026-10-19.md)：原文件mutation/Reconcile回归及跨主机目录树DOM实证。
- [文件独立复审](cr-round4-files-2026-10-19.md)：慢正文/活动登记复核和watcher原静态发现；本汇总补充了其writer返回后调用的实证。

主审watcher实验：`/tmp/vtc-r4-watcher/overlay.json`、`review_test.go`、`results.log`。命令：

```sh
go test -overlay=/tmp/vtc-r4-watcher/overlay.json ./internal/transport/http -run '^TestRound4WatcherUsesWriterAfterReturn$' -count=1 -v
```

常规日志：`/tmp/vtc-r4-go.json`、`/tmp/vtc-r4-race.json`、`/tmp/vtc-r4-lint.log`、`/tmp/vtc-r4-web.log`、`/tmp/vtc-r4-web-build.log`、`/tmp/vtc-r4-spec.log`、`/tmp/vtc-r4-mod.log`、`/tmp/vtc-r4-vet.log`、`/tmp/vtc-r4-whitespace.log`。独立复现的临时路径见各报告。
