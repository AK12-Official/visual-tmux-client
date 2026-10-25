# Web 国际化第六轮修复复查报告

审查日期：2026-10-26。

## 1. 结论

**第五轮唯一 P2（I18N-R5-01）已修复，本轮限定范围内没有发现新的有效问题。** 从本次代码复查与前端验证结果看，可以进入提交和合入流程，不再以第五轮认证降级问题作为阻断项。

这是对当前修复快照的结论，不是全项目或真实部署环境的无缺陷保证；本轮没有推送、提交或合入代码。

## 2. 范围与方法

- 分支：`feat/web-internationalization`。
- 已提交国际化实现：`3ba6782575024f991105a42c4e04258a49d644b3`。
- 审查对象：该提交加第六轮审查时的工作区未提交修复。
- 重点：`api.ts` 的 undefined/string/null 三态内存令牌、已有持久令牌下的写入/删除失败、认证请求头、401 清理、存储恢复及正常存储兼容性。
- 前轮报告：[第五轮报告](web-internationalization-code-review-round5-2026-10-26.md)。

主 Agent 审阅实现并运行定向探针、完整前端测试、构建和规格校验。两位独立子 Agent `test_review`、`state_review` 分别核对状态策略与新增回归测试，运行存储故障矩阵及认证闭环，再交叉对照结论。三方均未发现本轮范围内的明确新回归。

本轮没有扩大到与修复无关的全项目重新审计，未重新打开先前已经关闭且未受本次修改影响的问题。

## 3. I18N-R5-01 关闭依据

代码：[api.ts:39](../hub/web/src/api.ts#L39) 起的 getToken、setToken、clearToken。

修复明确区分：

- undefined：没有当前页覆盖状态，读取持久存储。
- string：当前页显式设置的新令牌，覆盖旧存储值。
- null：当前页显式清除认证，屏蔽删除失败后仍存在的旧值。

setToken 首先保存当前页新令牌，仅当写入成功且读回一致时恢复委托存储；clearToken 首先设置 null，仅当删除成功且读回为空时恢复委托存储。原来的“任何非空旧存储值都优先”的根因已移除。

### 主 Agent 独立探针

存储预置 old-token，读取正常、写入和删除抛错，随后恢复正常存储操作。实际输出：

```text
initial: old-token
write blocked: new-token
header: Bearer new-token
remove blocked: null
header: null
normal: persisted-new persisted-new
normal clear: null 0
```

该探针通过真实 authFetch 检查 Authorization 请求头，未只检查内部变量。

### 独立审查者补充验证

| 场景 | 结果 |
| --- | --- |
| 已有旧令牌且 setItem 失败 | 新令牌覆盖旧值，请求携带新 Authorization |
| removeItem 失败 | getToken 为 null，后续请求不发送旧认证头 |
| 清除后再次设置令牌 | 当前页新令牌可用 |
| getItem 失败且内存有当前令牌 | 继续使用内存令牌 |
| 返回 HTTP 401 | 抛 AuthError，清理认证；随后 getToken 为 null，后续请求不携带认证头 |
| 存储恢复后显式 setToken | 新令牌重新落盘，恢复读取正常存储变化 |
| 正常存储下 clearToken | 内存与持久状态均为空 |

`test_review` 还验证：持久化成功后，正常存储中的后续外部变更仍可由 getToken 读取，未把所有正常场景永久锁定到内存快照。存储故障期间优先当前页显式状态是本次降级设计的预期行为。

## 4. 回归测试评价

新增仓库测试已覆盖第五轮漏掉的核心组合：

- [api.test.ts](../hub/web/src/api.test.ts)：旧令牌可读时，写入失败后的新令牌覆盖，以及删除失败后的显式清除。
- [storageUnavailable.dom.test.ts](../hub/web/src/storageUnavailable.dom.test.ts)：已有旧令牌时写入失败仍发送新认证头；删除失败后请求不携带 Authorization。

本次用例不再仅使用 getItem 恒为 null 的空存储替身，因此能够检测第五轮报告指出的旧值遮蔽缺陷。

401 清理和存储恢复的完整组合由独立审查者的额外运行时探针验证；当前新增仓库用例没有把这两段组合流程分别完整固化。可以后续补充以增强防回归，但本轮没有据此认定实现有缺陷，也不额外列为阻断项。

## 5. 实际执行的检查

| 检查 | 结果 |
| --- | --- |
| `npm test`，目录 `hub/web` | 30 个测试文件通过，0 失败 |
| `npm run build`，目录 `hub/web` | TypeScript 检查及 Vite 生产构建通过 |
| `openspec validate add-web-internationalization --strict` | 通过 |
| `git diff --check` | 通过 |
| 主 Agent 旧令牌/写删失败/请求头/恢复探针 | 通过 |
| 两位独立审查者故障矩阵与认证闭环 | 通过；均未修改产品代码 |

30 是测试运行器报告的测试文件数量，不代表穷尽所有业务场景。OpenSpec 校验验证规划文档结构，不替代代码行为验证。

## 6. 合入建议与验证边界

本次复查没有保留待修的 P0/P1/P2/P3 发现。第五轮认证降级阻断项可以关闭。后续提交应包含未跟踪的新文件，特别是共享 storage 模块、存储故障测试和真实 CodeMirror 测试；历史 CR 报告按仓库约定保留。

本轮没有运行 tmux、真实 SSH 集成测试或完整真实浏览器视觉/窄屏验收。此类验证边界沿用前轮报告，不应将前端测试全绿表述为所有真实环境均已验收。

本轮只新增第六轮报告，未修改产品代码，未提交或合并分支。
