# 修复后第二轮 CR 与发布准入评估

日期：2026-10-18。结论：**暂不建议合入主线或发布新版**。

审查对象为 HEAD `93718a1051055553480d40b17b1c8203ef13e35f` 加当前未提交工作区，包含 work agent 根据上一轮报告进行的修复。以当前整体交付内容为边界，不将所有问题归因于某个尚未提交的修复。三位独立子 Agent 分别复核后端、远程文件和前端；主审检查 CI、lint、构建、依赖与文档，并汇总证据。本轮仅新增评审报告，没有修复产品代码、替换本机服务或连接用户真实 SSH 主机。

## 发布阻断结论

存在 **3 项 P1 功能问题**：远程终端立即断开、过期草稿可绕过连接版本校验、主机发现失败会丢弃草稿。三项均有本轮隔离实验或真实 Vue DOM 测试证据。另有 **7 项 P2 后端/文件问题**，以及文档、依赖清单不一致。CI 必需的 lint 同样失败，不能因测试通过而忽略。

上一轮 14 项中，**9 项原触发路径关闭，5 项部分修复未关闭**。关闭不等于整个功能无缺陷。特别是文件取消的修复改善了文件请求，却引入了远程终端生命周期回归。

## 本轮执行的检查

| 检查 | 本轮结果 | 说明 |
| --- | --- | --- |
| `golangci-lint config verify --config ../.golangci.yml` | 通过 | 使用仓库固定版本 v2.13.2 |
| `golangci-lint run --config ../.golangci.yml ./...` | **失败：655 项** | errcheck 419、funlen 1、goconst 9、gocyclo 17、gofmt 3、lll 139、mnd 67 |
| `go test -count=1 -json ./...` | 通过 | 12 个包，716 个测试及子测试；无用例跳过，web 包无 Go 测试文件 |
| 相关 6 包 `go test -race -count=1` | 通过 | host/auth/files/terminal/HTTP/WS，共 443 个测试及子测试 |
| `go vet ./...` | 通过 | 当前工具链检查 |
| `npm test` | 通过 | 18 个测试文件；不能写成只有 18 条功能断言 |
| `npm run build` | 通过 | vue-tsc 和 Vite 生产构建 |
| CGO=0 Linux amd64/arm64、macOS amd64/arm64 构建 | 4 个目标均通过 | 与 release workflow 的目标和关键构建参数一致；没有在 macOS/arm64 上执行运行测试 |
| `openspec validate --all --strict` | 通过 | 8 项：7 份主规格及 1 个活动变更；不等于审查工具验证所有历史归档 |
| `go mod tidy -diff` | **非零退出，有差异** | 未修改 go.mod/go.sum，见依赖清单问题 |
| `git diff --check` | **失败** | 4 处文件末尾空行，见下文 |

工具版本：Go 1.27.1、Node 24.12.0、golangci-lint 2.13.2。CI 从 go.mod 选择 Go 1.26.3，本轮没有另下载该工具链重跑，不将本机运行等同于远端 CI 已通过。本轮使用已有前端依赖安装，没有重新执行干净环境 `npm ci`。

后端网络测试经授权在 sandbox 外使用仓库既有隔离夹具执行。未使用临时 tmux 命令、未关闭无关服务器。临时实验中某些 PASS 的断言是“错误确实发生”，与上表正常功能测试通过的含义不同。

## P1：必须优先修复

### RC-1：远程终端建立后会立即断开

位置：[connection.go:517](../hub/internal/host/connection.go#L517)，关联 [WS handler:188](../hub/internal/transport/ws/handler.go#L188)、[handler:219](../hub/internal/transport/ws/handler.go#L219)。

`dialDedicated` 新增 ctx 取消关闭连接；远程 terminal 使用 handler 的 `r.Context()` 创建专用连接。handler 启动 attachment 后正常返回，HTTP server 随即取消该请求 context，刚建立的 SSH transport 因而关闭。用户无需切换、断网或执行特殊操作。

本轮用真实 HTTP/WebSocket handler、实际 RemoteProcessFactory 和既有 mock PTY SSH 夹具复现，约 0.03 秒收到正常关闭。应将握手请求预算、文件操作预算和长寿命 attachment 生命周期明确分离，由 attachment 拥有终端 transport；不能简单删除文件请求取消逻辑。修复后必须有穿过真实 ServeHTTP 返回边界、ready 后继续读写的回归测试。

### RC-2：切换到 B 可使 A 的过期草稿重新获准保存

位置：[FileManagerOverlay.vue:247](../hub/web/src/components/files/FileManagerOverlay.vue#L247)。

A 草稿持有旧 revision；A 连接配置变化后，当前页面能禁用保存。但切到 B 后，`revisionStale` 对非当前主机草稿直接返回 false。用户可提交 A 的旧草稿，请求没有预期 connectionRevision，后端只能解析 A 当前的新目标。文件 mtime 不是主机身份校验，不能防止旧内容发向新服务器。

本轮真实 Vue DOM mock 测试确认“在 A 禁用 → 切 B 启用 → 实际 PUT A 文件端点”，不是仅推测按钮状态。应让草稿身份固定为 hostId + connectionRevision，按归属主机验证，并让修改 API 校验客户端预期连接身份；不能只依赖当前页面的 hostRevision 或后端请求执行期间的租约有效性。

### RC-3：主机列表刷新失败会卸载编辑器、丢失未保存草稿

位置：[App.vue:140](../hub/web/src/App.vue#L140)、[错误分支:914](../hub/web/src/App.vue#L914)。

使用过程中刷新主机列表遇到 503/网络错误，`hostDiscoveryError` 会切换顶层 v-if 分支，卸载整个工作区及 FileManagerOverlay。Retry 成功后组件重新创建，内部 tabs 为空。原 F4 的“删除最后主机”分支已修，但这个错误分支仍产生同样的数据损失。

本轮 App DOM 测试建立含真实草稿文本的 tab、注入列表 503、重试后确认 tabs 为空，未出现丢弃确认。发现错误应在已有工作区上展示非破坏性提示，草稿状态应独立于主机发现的条件渲染生命周期。

## P2：剩余可靠性与文件完整性问题

| 编号 | 问题、触发及影响 | 定位与验证方式 | 必要修正 |
| --- | --- | --- | --- |
| RC-4 | 能力探测在 `NewSession`/SFTP 初始化阶段仍忽略取消；对已认证但不确认 channel 的主机，测试连接持续等待 | [capabilities.go:209](../hub/internal/host/capabilities.go#L209)；40ms deadline、150ms 观察窗仍不返回的本地 SSH 实证 | 独立有总预算的探测 transport，预算覆盖 channel open、exec、subsystem 和 SFTP 初始化 |
| RC-5 | ticket 兑换后才捕获 manager epoch；删除恰在两者之间，旧 runtime 会取到新 epoch 并注册附件 | [handler.go:185](../hub/internal/transport/ws/handler.go#L185)；定向 WS 交错实验最终 ActiveCount=1 | 世代在可失效操作之前捕获，与 runtime/connection revision 绑定，注册时原子校验 |
| RC-6 | 合法长度的错误主密钥先通过 LoadVault，schema 迁移提交后才因解密失败拒绝启动；失败仍修改数据库 | [registry.go:66](../hub/internal/host/registry.go#L66)；临时 v1 数据库+不同32字节key实验，报解密失败后 user_version 已为2 | 持锁后先验证原状态凭据，再提交迁移；失败保持 schema/内容不变 |
| RC-7 | Chown 失败仍被忽略，文件替换返回成功但 owner/group 可变，影响共享权限 | [remote_files.go:689](../hub/internal/host/remote_files.go#L689)；静态确认 `_ = Chown` 及继续 PosixRename | 按规格报告保留失败，明确拒绝或有提示的降级，不可静默成功 |
| RC-8 | 词法锁之后追加 canonical 锁可等待本操作自身；例如 `/home/u/self -> /home/u` 时删除 `/home/u/self/self` | [remote_files.go:1108](../hub/internal/host/remote_files.go#L1108)、[第二次锁:1141](../hub/internal/host/remote_files.go#L1141)；原样协调器纯 Go 实验50ms后超时，路径解析映射为静态核对 | 一次原子取得规范锁集合，或设计明确锁升级/owner协议；覆盖自冲突和交叉rename |
| RC-9 | 操作预算关闭 SFTP，却无法打断正在等待 HTTP body 的 `io.Copy`；客户端只上传部分正文会持续占用路径锁/handler | [remote_files.go:627](../hub/internal/host/remote_files.go#L627)、[files handler:253](../hub/internal/transport/http/files.go#L253)；静态 I/O 调用链 | HTTP 正文读取 deadline/关闭与 operation context 联动，覆盖真实网络慢正文场景 |
| RC-10 | RunPendingCleanups 仅测试显式调用，生产启动/重连没有入口；故障遗留的暂存内容和记录不会自动恢复清理 | [remote_files.go:767](../hub/internal/host/remote_files.go#L767)；全仓生产调用点搜索 | 有预算的安全清理调度，绑定主机身份并排除活跃上传，不能直接无条件调用现有方法 |

RC-4/5 是旧问题部分修复，RC-6 为本轮识别；RC-7 对应旧权限问题未处理部分，RC-8 是追加锁修复引出的新反例，RC-9 是输入端取消缺口，RC-10 是恢复流程接线缺失。没有把全部静态发现说成已经集成复现。

## CI、架构和文档一致性

### Q1：当前 lint 是明确的合入门禁失败

[CI workflow](../.github/workflows/ci.yml) 无条件运行全量 lint，[CONTRIBUTING.md](../CONTRIBUTING.md) 要求 0 issues。当前实际 655 项，上一轮记录 637 项。配置及固定 lint 版本未被放宽；本轮数量差异不用于归因每条 issue。不能以“主要是测试代码/风格问题”认定可以合入。

需要检查而非批量抑制的代表项：

- Registry.Save 圈复杂度 78、RemoteFileService.Write 为 80、OpenStore 为 55，显著超过项目阈值 15；这些函数正好集中了连接身份判断、事务、锁、权限、异常与清理的复杂分支。
- 419 项 errcheck 中包含有行为后果的 Chown 错误忽略，不能全部按 best effort 处理。
- 3 处 gofmt：`registry_test.go:652`、`store.go:508`、`lifecycle_routing_test.go:55`。
- diff whitespace 另报 `registry_test.go:652`、`api.ts:357`、`HostManagerModal.dom.test.ts:967`、`workspaceGuards.dom.test.ts:1119` 文件末尾新增空行。

架构改进应围绕已证实的约束分离，而非机械拆小函数：transport 创建预算与终端生命周期分离；草稿身份端到端校验；主机连接世代与附件注册原子绑定；路径锁一次取得；保存事务明确“提交前失败/结果未知/已提交但元数据降级”。这些边界对应 RC-1/2/5/7/8，可用行为测试验证。

### Q2：恢复文档仍然互相矛盾

上一轮 D1 在两份 README 的 schema 降级说明已修复，正确要求恢复升级前配套备份。但仍有以下过时指引，应在发布前统一：

- [SECURITY.md:61](../SECURITY.md#L61) 仍建议删除 `hosts:` 配置即可进入无需数据库/密钥的本地模式。当前版本启动强制初始化持久状态，即使只使用本地也一样；这与 README、主规格和实际 app 初始化冲突。若只指历史旧二进制，需要明确版本及降级步骤，不能作为当前版本可选运行模式。
- [README.md:280](../README.md#L280)、[README.zh-CN.md:278](../README.zh-CN.md#L278) 说密钥遗失后仍可访问元数据并重新录入凭据；当前 Registry 初始化在缺失 key 或密文无法解密时直接失败，服务不会启动，没有此管理界面恢复入口。应明确恢复原密钥/配套备份的可执行流程，不应暗示可以直接登录重新输入。RC-6 也说明错误key失败前不一定完全不动数据库。
- [示例配置:112](../configs/visual-tmux-client.example.yaml#L112) 的 key 不存在就自动生成应限定为新状态首次初始化，已有数据库缺 key 会失败。

这些是会影响实际部署恢复的 P2 文档缺陷，不是措辞美化。不要通过削弱加密状态保护来迎合旧文档。

### Q3：依赖清单尚未整理一致

`go mod tidy -diff` 非零退出，显示直接使用的 `github.com/pkg/sftp`、`golang.org/x/sync` 仍列为 indirect，并存在依赖测试所需校验和和旧版本 checksum 差异。[go.mod](../hub/go.mod) 与 [go.sum](../hub/go.sum) 应在保持选定版本的前提下整理，再检查差异。该问题当前没有阻止四目标构建，不将其描述为运行故障或既有 CI 必然失败。

### Q4：测试证据需补足真实风险路径

本轮常规套件全部通过，但后端 host/auth/files/terminal/HTTP/WS 的全量测试数量仍与上轮相同。文件修复没有给 `remote_files_test.go` 或 `sftp_adapter_test.go` 增加对应故障回归；新增前端 F4 用例仅断言组件没有卸载，未实际键入草稿，因而没覆盖 RC-3。

“Real OpenSSH” 测试仍使用 Go SSH/SFTP 夹具，不能作为真实 OpenSSH 验收证据。前端 mock 测试也不能证明真实 xterm/编辑器交互。本轮并未测试真实远端、完整浏览器视觉、生产负载或 macOS 运行行为。可保持这些快速测试，但应把本轮定向实验转为仓库内可失败的回归测试，并补至少一个真实 OpenSSH 验收流程。

发布 workflow 本身只构建并跑前端测试，没有依赖 Go lint/test 作 tag 发布前置检查；主线 CI 虽有这些门禁，实际发版时还应确保所选提交已通过主线检查。本轮没有创建 tag、合入或发布。

## 上轮 14 项的处理状态

| 旧项 | 本轮状态 | 说明 |
| --- | --- | --- |
| F1–F3 | 原路径关闭 | 冻结目标 ID，读取、批量删除及文件删除不再随当前主机漂移；DOM 回归通过 |
| F4 | 原路径关闭 | 删除最后主机仍保留工作区；错误发现分支仍有 RC-3 |
| HB-1 | 原路径关闭 | 池访问统一连接版本；隔离实验验证纯改名不撤销 lease |
| HB-2 | 部分修复 | 指纹握手预算验证通过；能力 channel/协议握手仍有 RC-4 |
| HB-3 | 原路径关闭 | 真实记录决定连接类型，local ID 为 SSH 的夹具验证通过 |
| HB-4 | 部分修复 | 加入 epoch，但捕获时机仍有 RC-5 |
| RF-1 | 部分修复 | 远端 I/O 随 dedicated ctx 取消；输入端正文仍有 RC-9，终端用途引出 RC-1 |
| RF-2 | 原路径关闭，静态验证 | 正文前 Chmod0600，失败停止；尚缺真实服务端回归 |
| RF-3 | 部分修复 | 当前权限重读、Chmod 检查已补；Chown 仍有 RC-7 |
| RF-4 | 部分修复 | 常规 alias 加锁已补，新锁组合有 RC-8 |
| RF-5 | 原路径关闭，静态验证 | 类型掩码已精确匹配；尚缺仓库内新增解析回归 |
| D1 | README 原问题关闭 | 旧 schema 不支持直接降级已写明；其他恢复指引仍需 Q2 统一 |

## 建议下一轮准入条件

1. RC-1 至 RC-3 修复，并用真实 handler/DOM 验证本轮复现反转为符合规格的行为。
2. RC-4 至 RC-10 完成修复或有明确、与规格一致的产品取舍；涉及数据完整性与恢复的条目不应作为无说明的已知限制发布。
3. 按现有配置清理 lint 至 0，整理依赖、格式和文档，不以大范围 nolint/降低阈值代替修复。
4. 在最终提交上重跑前端、后端、race、四目标构建及 OpenSpec，并取得对应 CI 结果；新增真实 OpenSSH 与正常远程终端验收记录。

## 独立报告与日志

- [后端独立复审](cr-release-backend-2026-10-18.md)：4 项问题全有本轮隔离实验；另3项原问题修复验证。
- [远程文件独立复审](cr-release-files-2026-10-18.md)：4 项 P2，区分纯协调器实证与静态调用链。
- [前端独立复审](cr-release-web-2026-10-18.md)：2 项 P1，均 DOM 实证。

本机临时日志：`/tmp/vtc-release-cr-go.json`、`/tmp/vtc-release-cr-race.json`、`/tmp/vtc-release-cr-lint.log`、`/tmp/vtc-release-cr-web.log`、`/tmp/vtc-release-cr-web-build.log`、`/tmp/vtc-release-cr-spec.log`、`/tmp/vtc-release-cr-vet.log`、`/tmp/vtc-release-cr-mod-unrestricted.log`、`/tmp/vtc-release-cr-whitespace.log`。临时复现文件见独立报告，未作为产品代码提交。
