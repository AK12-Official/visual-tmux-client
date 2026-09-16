## Why

会话列表目前只显示会话名、窗口数和 attached 状态，无法一眼看出每个会话里正在跑什么。同时开着多个会话时（例如一个跑 `claude`、一个跑 `nvim`、一个跑构建），用户必须逐个点开才能确认目标，而点开会切换 attach 目标并触发重绘。参考项目 tmuxhub 在会话名下方以副标题小字呈现当前窗口/标题/进程，正是为了解决这个问题。

## What Changes

- hub 的会话轮询在既有 `list-sessions` 之外增加一次 `list-panes -a` 查询，为每个会话解析出「最佳 pane」的窗口名、pane 标题、当前命令与活跃状态。
- 「最佳 pane」按优先级选取：跳过已死亡的 pane，优先 `window_active && pane_active`，其次 `pane_active`，再次 `window_active`。
- 会话列表响应新增一个可选的 pane 摘要字段；该查询失败时降级为不含摘要，不影响会话列表本身的可用性。
- 前端在左侧会话项名称下方渲染副标题，取值优先级与参考项目一致：`窗口名[*]: pane 标题` → `窗口名[*]` → `当前命令`；三者皆空时不显示副标题。
- 右侧终端头部的会话名保持不变（侧栏折叠时它是唯一的会话标识），本变更不移动该内容。
- 列表项只显示会话名和可用的副标题；副标题替换原有的「N windows · attached/detached」元信息行。

## Capabilities

### New Capabilities

无。本变更修改既有能力的行为，不引入新能力。

### Modified Capabilities

- `session-hub`: 「Session listing」在既有字段之外增加每个会话的 pane 摘要，并明确该摘要在查询失败时的降级行为。
- `web-session-manager`: 「Session list view」在列表项中增加副标题的展示与取值规则。

## Impact

- `hub/internal/tmux/` 新增 pane 列举与解析（格式串、可打印多字符分隔符 `|vtc-pane|`、严格九字段校验）；包含完整分隔符的字段会导致该记录被丢弃，因为字段内容无法与边界区分。
- `hub/internal/session/` 的 `Backend` 接口与 `Service` 增加 pane 摘要能力，沿用既有 `fastGetter` 式的可选能力断言，避免强制所有 backend 实现。
- `hub/internal/session/model.go` 的会话模型增加可选字段，并由现有 JSON 序列化直接暴露；既有字段与响应结构保持兼容。
- `hub/web/src/components/SessionList.vue` 增加副标题渲染与样式；新增一个纯函数模块负责副标题取值，便于单测。
- 每次轮询多一次 tmux 调用（一次 `list-panes -a` 覆盖全部会话，而非按会话逐个查询）；轮询间隔不变。
- 不改变 WebSocket 协议、终端 attach 行为、会话增删改语义或既有 REST 响应字段；会话列表仅新增可选的 `pane` 字段。
