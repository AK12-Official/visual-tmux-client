# Web 国际化第四轮独立交叉审查报告

审查日期：2026-10-26。

## 1. 结论

**第三轮的两项具体问题已修复；建议补齐本轮确认的规格缺口后合入。** 本轮保留 **1 项 P2、2 项 P3**。没有发现新的 P0/P1，也未发现语言切换导致终端重新连接、编辑器草稿或撤销历史丢失的回归证据。

P2 是原有应用存储读取路径未满足本次新增的启动降级规格，**不是本轮修复新引入的回归**。两个 P3 是整分支国际化行为与覆盖的剩余缺口。报告将既有行为、规格缺口和新增回归分开，不将它们混为同一结论。

## 2. 快照、分工与交叉方法

- 分支：`feat/web-internationalization`。
- 已提交实现：`3ba6782575024f991105a42c4e04258a49d644b3`。
- 评估对象：该提交及第四轮审查时的未提交工作区修复；完整功能审阅参考 `origin/main` 到工作区的改动。
- 历史报告：[第二轮](web-internationalization-code-review-round2-2026-10-26.md)、[第三轮](web-internationalization-code-review-round3-2026-10-26.md)。

按用户要求安排三位独立子 Agent，并进行第二阶段交叉复核：

| 审查者 | 独立检查范围 | 交叉复核内容 |
| --- | --- | --- |
| locale_review | 文案覆盖、错误映射、语义、语言响应式更新 | 复核启动错误页语言变化和存储启动候选问题；运行会话行可访问名称探针 |
| state_review | 终端/编辑器状态、帮助异步请求、启动及生命周期 | 运行启动错误页探针；独立复核存储禁用时应用挂载失败 |
| test_review | 测试有效性、mock 边界、规格与修复闭环 | 运行存储失败探针；与状态审查者交叉确认启动错误页候选问题 |
| 主 Agent | 第三轮修复复查、候选问题归因及最终汇总 | 独立运行存储与启动语言变化探针；运行全套前端检查，合并重复发现 |

本次“多轮”指第一阶段独立审阅、第二阶段候选问题交叉验证、第三阶段主 Agent 复核汇总。不是多份互不核实的意见简单相加。所有审查者只读检查产品代码，没有实施修复。

## 3. 第三轮问题关闭情况

| 编号 | 原问题 | 结果 | 依据 |
| --- | --- | --- | --- |
| I18N-R3-01 | 会话列表刷新失败直接显示错误码 | 已关闭 | SessionList 在展示边界映射错误，空列表与缓存列表分支共用翻译；保留原始错误用于业务判断。新增组件测试覆盖已知错误、诊断和语言切换 |
| I18N-R3-02 | 删除确认框插入英文 file/directory | 已关闭 | 按目标类型选择 `files.deleteFilePrompt` 或 `files.deleteDirPrompt` 完整词条，新增双语文件/目录确认测试 |

第三轮建议同时检查的主机发现错误也增加了本地化说明包装。旧报告作为历史快照保留，其发现状态以本报告复核结果为准。

## 4. 本轮发现

### I18N-R4-01：存储不可用时语言模块降级成功，但应用启动仍失败

**级别：P2。归因：原有行为未满足本次新增规格；非本轮新增回归。**

位置：

- [api.ts:34](../hub/web/src/api.ts#L34)：`getToken()` 无保护访问 `localStorage.getItem()`。
- [App.vue:42](../hub/web/src/App.vue#L42)：组件 setup 阶段立即调用 `getToken()`。
- [App.vue:288](../hub/web/src/App.vue#L288)、[App.vue:481](../hub/web/src/App.vue#L481)：字号、侧栏等启动读取也未保护，不能只修复令牌读取就视为完成。
- 约束来源：[国际化增量规格](../openspec/changes/add-web-internationalization/specs/web-internationalization/spec.md) 的 Browser-local language preference / Storage is unavailable 场景。

**复现：** 在 DOM 测试环境中将 `globalThis.localStorage` getter 替换为抛出 `SecurityError`，先调用语言选择，再挂载 App。

主 Agent 的独立探针输出：

```text
locale helper: zh-CN
app mount error: SecurityError Storage blocked
... getToken (.../src/api.ts:34:3)
```

test_review 和 state_review 也分别复现了同类失败。语言存储 helper 可以正常降级，应用却无法呈现登录界面和语言选择器。

**影响：** 浏览器禁用存储时，新增规格承诺的“仍可启动并在当前页面内切换语言”没有成立。现有 `storage.test.ts` 只验证 helper，不覆盖完整 App 启动链，因此测试全绿不能证明该场景满足要求。

**建议：** 对完整启动链的存储读取统一做异常降级；令牌如需在存储禁用时继续认证，应有一致的内存状态策略，而不是只在 getToken 捕获异常后让后续请求再次丢失令牌。若产品决定不支持存储禁用下的完整启动，应先明确调整规格，不能仅凭语言 helper 测试宣称原承诺已完成。

**验收：** getter 抛错、getItem 抛错和写入失败分别验证；挂载 App 不抛异常，登录页可见，当前页面内语言切换有效；检查字号、侧栏、最近主机等读取不再造成后续启动中断。涉及认证降级时，还需验证内存令牌供请求使用及退出清理。

### I18N-R4-02：自动语言变化后启动错误页保留旧语言

**级别：P3。归因：整分支的持久状态语言更新缺口，非本轮修复新引入的回归。**

位置：[bootstrap.ts:27](../hub/web/src/bootstrap.ts#L27)、[main.ts:33](../hub/web/src/main.ts#L33)。

**触发条件：** follow-browser 模式，配置加载失败后错误页仍可见，此时浏览器发出 `languagechange`。

**复现：** 将 `navigator.languages` 设为英文，启用正式语言变化监听器并调用 `renderError()`；随后将浏览器语言改为 `zh-CN`，派发 `languagechange`。主 Agent、state_review 与 locale_review 均验证了该路径。

主 Agent 输出：

```text
before: Configuration Error
after locale: zh-CN document: zh-CN heading: Configuration Error button: Retry
```

**根因：** 共享语言状态和 document.lang 更新，但 bootstrap 使用静态 innerHTML，没有订阅语言变化以更新可见文案。配置重试或重新渲染后才能使用新语言。

**影响：** 特定浏览器偏好变化场景下，页面语言标识与实际文字不一致。发生于应用尚未挂载的错误页，影响低于常规工作区语言切换，故定为 P3。

**建议与验收：** 保存启动页当前状态与原始诊断，在未挂载阶段订阅语言变化并更新展示，挂载后移除订阅。验证错误标题、重试按钮和加载提示随自动语言变化更新；原始诊断仍正确转义，重试不会重复触发或丢失。

### I18N-R4-03：少量应用外围文案与无障碍标签仍未本地化

**级别：P3。归因：国际化覆盖收尾项；同类遗漏合并计数。**

| 位置 | 现象 | 证据 |
| --- | --- | --- |
| [SessionList.vue:400](../hub/web/src/components/SessionList.vue#L400) → [sessionSubtitle.ts:75](../hub/web/src/sessionSubtitle.ts#L75) | `rowLabel()` 固定生成 `Session ...`，中文模式的行可访问名称不随语言变化 | locale_review 组件探针实际得到 `Session dev` |
| [FileManagerOverlay.vue:1828](../hub/web/src/components/files/FileManagerOverlay.vue#L1828) | 主机悬浮提示仍固定 `Host:` 前缀 | 静态检查 |
| [HelpModal.vue:94](../hub/web/src/components/HelpModal.vue#L94) | 指南失败直接通知 `guide: 503 ...` 或其他原始异常，缺少本地化的加载失败说明 | 静态检查；已有 `help.error` 词条未在此路径使用 |
| [terminal.ts:440](../hub/web/src/terminal.ts#L440) | 控制消息的非零退出状态构造 `Process exited with code N`，结束面板直接显示原文 | 静态控制流检查 |
| [terminal.ts:196](../hub/web/src/terminal.ts#L196) | 终端复制失败的应用警告仍为英文 | state_review 与 locale_review 交叉静态检查 |

这里区分应用自行生成的说明与外部内容：主机名、会话名、退出码和诊断应保留；终端输出字节与用户文件内容不应翻译。terminal 的上述文本来自应用处理控制消息时生成的外围说明，不是要求翻译终端字节流。

**建议：** 提取应用生成的前缀和整句，未知诊断保留在本地化上下文中；持久终端退出详情保存稳定的退出码，在展示边界翻译，避免切换后保留旧语言。不要为覆盖词条而改变模块中纯数据函数的职责，可在组件层组合可访问名称。

**验收：** 双语检查会话行可访问名称、主机 tooltip、指南失败通知、非零退出详情；保留名称及原始诊断；已显示的持久详情切换语言后更新。不得改变终端状态机和重连判断。

## 5. 检查与测试记录

以下为主 Agent 在本轮实际运行的结果：

| 检查 | 结果 |
| --- | --- |
| `npm test`，目录 `hub/web` | 29 个测试文件通过，0 失败 |
| `npm run build`，目录 `hub/web` | TypeScript 检查及 Vite 生产构建通过 |
| `openspec validate add-web-internationalization --strict` | 通过 |
| `git diff --check` | 通过 |
| 存储 getter 抛 SecurityError 的组件探针 | 语言 helper 降级成功，App 挂载失败 |
| 自动浏览器语言变化的 bootstrap 探针 | locale/document.lang 更新，静态错误标题与按钮未更新 |

29 是测试运行器报告的测试文件数量，不代表所有内部断言或场景总数。OpenSpec 校验只验证规划文档结构，不证明实现行为完全符合规格。

真实 CodeMirror 测试继续通过。它验证真实编辑器的重配置模式、选区和 undo/redo；Vue 工作区集成测试仍使用编辑器及终端 mock。审查者没有将 mock 通过扩大表述为真实 tmux/SSH 集成验收通过。

测试审查另确认：现有 languagechange 测试没有改变浏览器语言偏好，部分断言仅检查 document.lang 非空，不能可靠发现监听器失效；bootstrap 测试通过手动重新渲染验证翻译，没有验证已显示错误页响应语言变化。这些作为上述发现的覆盖解释，不额外计为产品缺陷。

## 6. 未作为缺陷重复计数的观察

- 第三轮已关闭问题不因测试还有可补充之处而重新打开。
- 未发现帮助内容因语言请求乱序覆盖当前语言的实际竞态；当前 locale/request guard 和分语言缓存方向合理。
- 未发现此次 locale 更新改变终端面板身份或重建编辑器实例的证据。
- 对翻译样式、重复 locale ref/listener 等维护性意见未另列为阻断问题。
- 所有相似的低影响文案遗漏合并为 I18N-R4-03，避免拆分数量放大严重程度。

## 7. 合入建议与验证边界

优先决定并落实 I18N-R4-01 的规格承诺，再修 I18N-R4-02、03 的国际化收尾。补入能在当前代码上失败的回归测试后，复跑前端测试与构建，并完成真实浏览器的双语、窄屏和辅助标签检查。

本轮未运行 tmux 命令或 tmux 集成测试，未连接真实 SSH 主机，未进行完整真实浏览器视觉验收。未发现新增 P0/P1 不构成整个项目无缺陷保证。

修复仍在工作区，最终提交需纳入新增真实编辑器测试文件。产品代码在本轮审查中未被修改；第四轮报告单独保存，不覆盖第二、三轮历史报告。
