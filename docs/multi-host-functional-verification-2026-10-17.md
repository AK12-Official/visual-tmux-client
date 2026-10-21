# 两次主机管理迭代：功能测试清单与核验记录

核验日期：2026-10-17。分支：`proposal/multi-host-support`，HEAD：`93718a1`。对象是当前工作区（包括统一本地/远程主机管理的未提交实现与归档文件），不是仅 HEAD。未改动业务代码或已有测试。

依据：

- [multi-host-support 提案](../openspec/changes/archive/2026-10-16-multi-host-support/proposal.md)及同目录 tasks、五份 delta specs。
- [unify-host-management 提案](../openspec/changes/archive/2026-10-17-unify-host-management/proposal.md)及同目录 tasks、三份 delta specs。
- 当前 `openspec/specs`、后端集成/单元测试、前端 DOM/状态测试。

验收以第二次迭代覆盖后的行为为准：本地主机可以删除、允许空注册表、不自动补回 local；正常启动必须有可写持久化状态；默认主机不是隐式操作目标。第一次迭代中的“不可删除 local”和“纯本地只读部署可启动”不再是正确预期。

## 后续状态（2026-10-18）

遗留规格校验问题已修复：旧活动变更的文件边界 MODIFIED 块已与当前主规格对齐，保留本地限定及远程场景。重新运行 `openspec validate --all --strict`：8 项通过、0 项失败。下文的失败记录保留为首次核验的历史结果。独立代码审查另见 [两次迭代 CR 汇总](multi-host-code-review-2026-10-18.md)；自动化通过不代表异步边界缺陷已排除。

## 本次执行结果

| 检查 | 结果 | 说明 |
| --- | --- | --- |
| `go test -count=1 -json ./...` | 通过 | 沙箱外执行，12 个测试包、716 个测试及子测试通过；无测试用例跳过。`hub/web` 无 Go 测试文件，不计入通过包数 |
| 相关包 `go test -race -count=1` | 通过 | host、auth、files、terminal、HTTP、WS 共 6 包，443 个测试及子测试通过 |
| `npm test` | 通过 | Node 汇总 18 个测试文件通过，0 失败/跳过；不能误报成仅有 18 条功能用例 |
| `npm run build` | 通过 | Vue/TypeScript 检查及 Vite 生产构建 |
| `go vet ./...` | 通过 | 静态检查 |
| `golangci-lint run --config ../.golangci.yml ./...` | 失败 | 沙箱外复核报告 637 项：errcheck 412、funlen 1、goconst 9、gocyclo 17、gofmt 4、lll 135、mnd 59；当前 CI lint 门禁未满足 |
| `go build -trimpath -o /tmp/vtc-functional-client ./cmd/visual-tmux-client` | 通过 | 当前 Linux 环境构建；未执行跨平台矩阵 |
| `openspec validate --all --strict` | 未全通过 | 7 份主规格通过，1 个旧活动变更失败，见下文 |

后端首次沙箱内执行因 TCP/Unix socket 权限和组变更限制失败；随后沙箱外全量重跑通过，不能把首轮环境失败当成功能缺陷。tmux 行为只经现有测试及隔离夹具执行，没有临时手写 tmux 命令。ParentGuard 通过也不代表能检测所有 pane/option 改动。

日志保存在本机 `/tmp/vtc-functional-go-unrestricted.json`、`/tmp/vtc-functional-race.json`、`/tmp/vtc-functional-web.log`、`/tmp/vtc-functional-build.log`、`/tmp/vtc-functional-spec.log`、`/tmp/vtc-functional-lint-unrestricted.log`。临时日志不随 Git 保存。

## 功能测试清单

下列“自动通过”表示对应已有自动化测试在本次运行中通过，不代表真实浏览器和生产远程服务器端到端验收完成。表内测试名可在 `hub` 下检索。P0 是目标隔离、数据完整性和关键生命周期；P1 是兼容性、交互与降级。

### 第一轮：SSH 多主机支持及持续回归

| ID / 优先级 | 操作与预期 | 自动化证据 | 结果 |
| --- | --- | --- | --- |
| M01 / P0 | 注册两台 SSH 主机，重启 Hub；ID、配置、凭据可恢复，无须重新输入 | `TestTwoRemoteHostsAndLocalAcceptanceFlow`、`TestStoreTransactionsAndRestart` | 自动通过 |
| M02 / P0 | 分别用密码、私钥、加密私钥连接；错误凭据拒绝，旧凭据在无效更新后保留 | `TestSSHAuthAdapters`、`TestCredentialRetentionAndReplacement` | 自动通过 |
| M03 / P0 | 未信任时探测指纹，不发送凭据；主机密钥变化、过期或旧端点确认被拒绝 | `TestProbeDoesNotOfferCredentials`、`TestTrustChallengeLifecycle`、`TestPinnedHostKeyChecking` | 自动通过 |
| M04 / P0 | 检查凭据加密、跨主机密文替换、缺失/错误主密钥、响应和日志脱敏 | `TestCredentialBindingAndRestart`、`TestMissingKeyDoesNotCreateReplacement`、`TestRegistryWrongKeyPreservesState`、`TestDiagnosticsContainNoSavedSecrets` | 自动通过 |
| M05 / P0 | 未登录访问 CRUD、探测、能力接口，应拒绝且不发起远程工作；公开配置不泄露拓扑 | `TestUnauthenticatedEndpointsInitiateNoRemoteWork`、`TestPublicClientConfigContainsNoHostTopology` | 自动通过 |
| M06 / P0 | 两台主机创建同名会话，分别列出、改名、删除；互不影响；特殊字符按字面值处理 | `TestRemoteSessionOperationsAndIsolation`、`TestPOSIXEncoderLiteralFidelity` | 自动通过 |
| M07 / P0 | 使用过期、已兑换、跨主机/会话或旧连接版本 ticket，均拒绝 | `TestTicketLifecycle`、`TestTicketTargetBinding`、`TestTicketStoreInvalidation`、WS 测试 | 自动通过 |
| M08 / P1 | 终端输入输出、控制字符、resize、断开重连；关闭附件不结束 tmux 会话 | `TestRemotePTYProcess`、`TestSmokeTerminalInputOutputAndReconnect`、`TestE2ERestartPreservesSession` | 自动通过；视觉效果待验 |
| M09 / P0 | 并发连接同一主机、握手停滞、取消、离线恢复；有超时/退避，其他主机保持可用 | `TestSingleFlightDialing`、`TestStalledHandshakeTimeout`、`TestContextCancellationStopsDial`、`TestDialBackoff` | 自动通过 |
| M10 / P0 | 请求发出后断连，变更结果无法确认；返回 outcome-unknown，不自动重放 | `TestMutationOutcomeClassificationAndNoReplay` | 自动通过 |
| M11 / P1 | SSH/tmux/SFTP 独立可用性；SFTP 缺失不禁用终端，离线主机不阻塞其他主机 | `TestCapabilitiesIndependentStatuses`、`TestCapabilitiesAuthenticationFailureAndOffline` | 自动通过 |
| M12 / P0 | 列目录、读写/下载文件、新建文件/目录、无覆盖改名、递归删除、空白路径 | `remote_files_test.go` 中 List/Read/Create/Rename/Delete/StartDirectory 测试 | 自动通过 |
| M13 / P0 | 本地 roots 不作用于远端；伪文件系统和危险路径拒绝；禁用文件功能不访问后端 | `TestRemoteFilesRemoteLocalPathSeparation`、`TestCanonicalizeRemotePathSymlinkToPseudoFilesystem`、`TestHostScopedRemoteFileRoutesAndGlobalDisablement` | 自动通过 |
| M14 / P1 | 超大目录、超限文件、二进制内容、非普通文件、停滞传输；工作量有界，可取消 | `TestSFTPAdapterBoundedDirectoryIterationDoesNotScaleWithOversizedDir`、`TestSFTPAdapterEffectiveCancellation`、RemoteFiles Read/List 测试 | 自动通过 |
| M15 / P0 | 同时间戳同大小但内容已变，两浏览器同时保存，以及保存与祖先改名/删除冲突；拒绝过时写入或串行协调 | `TestRemoteFilesContentDerivedPreconditions`、`TestRemoteFilesSerializationConflictingHubOperations` | 自动通过 |
| M16 / P0 | 不支持安全替换时禁止覆盖；暂存、长度校验、强制保存重检、提交断连及重启清理不破坏原文件 | `TestRemoteFilesWriteStagingAndAtomicReplacement`、`TestRemoteFilesPendingStagingAndOutcomeUnknown`、`TestRemoteFilesOutcomeUnknownOnReplacementDisconnect` | 自动通过；真实 OpenSSH 待验 |
| M17 / P0 | 快速切主机、迟到响应、同名会话和同路径草稿隔离；拒绝跨主机插入终端路径 | `workspaceGuards.dom.test.ts`、前端文件/会话状态测试 | 自动通过 |
| M18 / P0 | 远程认证错误保持 Hub 登录；离线不丢缓存/草稿；保存确认丢失不把未确认内容标记为已保存 | 前端 workspace/file 状态测试、`TestHostCapabilitiesAndRemoteAuthFailure` | 自动通过 |
| M19 / P1 | 大文件传输、慢终端消费者和离线主机同时运行；取消文件请求不切断终端 | `TestSimultaneousTransferSlowTerminalAndOfflineHost`、`TestTransportLifecycleSeparation` | 自动通过；非生产负载基准 |

### 第二轮：统一本地与 SSH 主机管理

| ID / 优先级 | 操作与预期 | 自动化证据 | 结果 |
| --- | --- | --- | --- |
| U01 / P0 | 首次启动持久化注册 local；正常启动状态不可写、密钥错误、损坏/较新 schema、并发占用均失败且不替换数据 | registry/store/app 测试，`TestStoreExclusiveOwnership`、`TestStoreRefusesUnknownSchemaWithoutChangingIt` | 自动通过 |
| U02 / P0 | 旧库迁移保留 SSH ID、密文、信任、revision；重复/中断迁移不重复初始化 | `TestStoreSchemaMigrationV1ToV2`、`TestStoreInterruptedAndRepeatedMigration` | 自动通过 |
| U03 / P0 | local 改名持久化；拒绝 SSH 专属字段和原地改连接类型；并发新增 local 至多成功一次 | `TestLocalMetadataEdits`、`TestLocalIrrelevantFieldRejection`、`TestImmutableConnectionType`、`TestConcurrentSingletonLocalRegistration` | 自动通过 |
| U04 / P0 | 删除 local/最后一台主机，重启保持空；显式重新注册才恢复；不杀原 tmux 会话 | `TestPersistentHostLifecycleAndSettings`、`TestUniformRemovalInvalidationAndRevisionAllocation` | 自动通过；完整浏览器串联待验 |
| U05 / P0 | 设置 SSH 为默认、清空默认、重启；过时设置更新拒绝，删除默认时原子清空，不自动补选 | `TestPersistentHostLifecycleAndSettings`、`TestHostCRUDAndLocalIdentity` | 自动通过 |
| U06 / P0 | 空/未知/已删除目标不得执行；任意 ID 的 local 类型走本地适配器；旧 local 路由不跟随新默认 | `TestLocalResolutionNeverFallsBack`、`TestMultiHostResolverTargetResolution`、`TestAuditCompatibilityRoutesAndTargetRequirement` | 自动通过 |
| U07 / P0 | local/SSH 仅改名，终端和编辑器保持；连接配置变更持久化成功后才使旧运行时失效 | `TestMetadataNotificationsSeparatedFromConnectionInvalidation`、前端 rename connection revision 测试 | 自动通过 |
| U08 / P0 | 删除主机后旧附件关闭；删除、重启、复用 ID 后旧票据、文件观察值和请求不能复活 | `TestUniformRemovalInvalidationAndRevisionAllocation`、auth/terminal 生命周期测试 | 自动通过 |
| U09 / P1 | local 能力反映真实 tmux、files 配置；SSH/SFTP 为不适用；unknown/unavailable 不因对象为真而显示成功 | `TestLocalCapabilities`、HostManagerModal structured capability DOM 测试 | 自动通过 |
| U10 / P1 | 主机管理弹窗对两种类型均能编辑、测试、默认、删除；只有 SSH 展示凭据和信任流程 | `HostManagerModal.dom.test.ts` | 自动通过 |
| U11 / P0 | 启动优先有效最近主机，再选注册表中的默认；都无效则提示选择，选择前不发目标请求 | `workspaceGuards.dom.test.ts` recent/default/no valid preference 测试 | 自动通过 |
| U12 / P0 | 空注册表显示添加入口；发现接口失败显示重试，不伪造 local | 同文件 empty registry / discovery failure 测试 | 自动通过 |
| U13 / P0 | 活跃主机删除后停止输入/保存，保留脏草稿，不自动跳到 local；迟到响应不回填 | 同文件 active host removed 测试及文件状态测试 | 自动通过；跨浏览器实测待验 |
| U14 / P0 | 在 local 和 SSH 页面收到 Hub 401 均回到登录；SSH 凭据拒绝不清中央 token | 同文件 central 401 / remote authentication 测试 | 自动通过 |

## 仍需执行的人工/真实环境验收

准备独立测试部署及独立状态/主密钥、两个可销毁的 SSH 测试环境 A/B、两个浏览器上下文。不得使用开发者当前 tmux 会话、生产主机或真实业务文件。下列项目本次没有执行，不应勾选通过。

| ID / 优先级 | 操作步骤 | 通过标准 |
| --- | --- | --- |
| E01 / P0 | 浏览器首次登录 → 注册 A/B 并核对指纹 → A/B 建同名会话与同路径文件 → 切换后分别编辑保存 | 页头/确认框标识正确主机，内容、会话排序、活动提示、草稿互不串扰 |
| E02 / P0 | local 保持终端和未保存草稿 → 改名 → 设 A 为默认 → 删除 local → 删除其余主机 → 重启 Hub → 显式加回 local | 改名不中断；删除不自动切主机、不丢草稿、不杀会话；重启仍为空；加回后可重新附加原会话，旧 ticket 无效 |
| E03 / P0 | 两浏览器打开同一主机；第二个修改地址/凭据或删除再注册；第一个尝试输入和保存 | 旧连接/文件观察失效；草稿保留；不能写到新注册目标；必须显式确认/选择后恢复 |
| E04 / P0 | 使用真实 OpenSSH sshd + SFTP，完成三种认证、原子覆盖、提交前后断连、恢复重读及暂存清理 | 能力声明与服务端一致；不支持安全覆盖则禁用；结果不确定不重放、不假报已保存；无原文件损坏 |
| E05 / P1 | 真浏览器使用全屏终端程序、中文/组合输入、Ctrl 键、复制粘贴、连续 resize、滚动和断线重连 | 字节/显示/焦点符合预期，终端无错位，断开不终止会话；DOM mock 不能替代此项 |
| E06 / P1 | 真浏览器预览 Markdown/图片/二进制、大文件编辑与下载；键盘操作管理弹窗及窄屏布局 | 编辑器与预览可用、危险 HTML 不执行、限额提示清楚、焦点与 Tab/Escape 正确 |
| E07 / P1 | 多终端持续输出与大文件传输并行，A 离线/B 在线；记录延迟、内存、重连和退出耗时 | B 可交互，资源使用与退出有界；按部署 SLA 设定数值阈值后验收，当前夹具测试不能证明生产容量 |

## 已发现的缺口与迭代判断

1. **规格维护问题**：`openspec validate --all --strict` 的唯一失败为旧活动变更 `2026-09-16-file-manager-cr-hardening`。其 runtime-configuration 的 MODIFIED 块遗漏当前主规格已有的 `Remote host with local roots configured` 场景。7 份主规格通过；这个问题不等于本次主机功能失败，但目前不能宣称全库严格校验通过。建议单独同步/处理该历史变更，本次保留原文件未改。
2. **真实 OpenSSH 证据不足**：`TestSFTPOpenSSHReplacementAndFaultInjection` 虽通过，夹具 `hub/internal/testutil/ssh_fixture.go` 实际调用 `sftp.NewServer`，不是 OpenSSH 的 `sftp-server`/sshd。本次验证了 Go SSH/SFTP 夹具和 Linux/POSIX sh/tmux 3.4 组合，未验证完整真实 OpenSSH 兼容矩阵。
3. **浏览器验收不足**：DOM 测试使用 mock fetch，且 `hub/web/test/loader.mjs` 替换 xterm、CodeMirror、DOMPurify。通过能证明被断言的状态/组件逻辑，不能证明实际终端渲染、编辑体验或真实浏览器清洗行为。
4. **覆盖口径**：OpenSpec tasks 全部勾选不是执行证据。本清单按功能归组，不声称每一个规格 Scenario 都有独立端到端测试。跨平台构建、真实远端网络故障及长期负载仍未执行。
5. **CI lint 未通过**：共 637 项，包含产品代码与测试代码。例如 `hub/internal/host/capabilities.go` 的关闭错误未处理、`acceptance_test.go` 的列表/读取错误未检查。未做基线差分，不把全部条目归因于第二次迭代；也不能仅因大部分是风格规则而忽略错误处理问题。应另行修复并复跑 CI，本次核验不批量改代码。

结论：两轮迭代的核心功能和回归在当前自动化范围内通过，未发现本次可复现的功能失败；当前还不能认定全部验收完成。完整发布验收需要修复 lint、补齐 E01–E07，并处理遗留规格校验问题。
