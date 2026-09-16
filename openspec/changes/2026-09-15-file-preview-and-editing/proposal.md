## Why

hub 目前只能管理 tmux 会话，浏览器端看不到服务器上的任何文件。用户在终端里定位到报错、日志或配置文件后，必须退回到终端用 `cat`/`vim` 查看和修改，浏览器始终无法成为完整的工作台。补齐文件浏览、预览与编辑能力，才能让「一个 Hub 连接所有主机」的浏览器工作台闭环。

## What Changes

- 新增 `files` 配置段：可选的根目录白名单（用于显式收紧边界）、单文件体积上限、目录列举上限与开关；默认不限定目录，边界即 hub 进程自身的操作系统访问权限。
- 新增受 bearer 鉴权保护的文件 REST API，挂在既有 `/api/hosts/{hostId}/files/...` 形状下（`hostId` 仍只接受 `local`）：列举目录、读取（流式为主）、写入（乐观并发）、新建、重命名、删除。
- 目录列举按需逐层加载，返回 `truncated` 标志；大文件读取走 `application/octet-stream` 流式通道，不复用 64 KiB 的通用请求体上限。
- 写入采用 `expectedMtime` 乐观并发：先比对 mtime（以毫秒表示，避免前端数值精度陷阱），冲突返回 `conflict`；落盘经「同目录临时文件 + `O_EXCL` + fsync + rename 覆盖」，失败即清理临时文件；成功后返回新的 mtime，避免连续保存误报冲突。
- 路径安全以 `realpath` 后的规范路径做包含性判断，阻断软链逃逸；`/proc`、`/sys`、`/dev` 在任何根目录检查之前无条件拒绝。
- 新增浏览器端文件管理器：目录树、多标签编辑、脏标记、Markdown/图片/二进制预览分流、右键菜单（新建文件/新建目录/重命名/删除/下载/复制路径/插入到终端输入行）。编辑器选用 CodeMirror 6。
- 「插入到终端输入行」复用既有的终端 attach 通道，不新增服务端接口；只插入文本、不带换行，因此不会自行执行任何命令，含 shell 元字符的路径会加引号。
- 终端头部新增文件入口按钮，文件管理器以覆盖层形式在终端区域内打开，初始目录取自当前会话的 tmux pane 工作目录。
- 明确不做：文件上传、git diff 预览、远端 agent 文件协议、HTML 预览。

## Capabilities

### New Capabilities

- `file-manager`: 服务器文件的浏览、预览、编辑与基本文件操作，含访问边界、体积约束、并发写入语义和浏览器端交互契约。

### Modified Capabilities

- `runtime-configuration`: 「Configurable operational behavior」新增 `files` 配置段（根目录白名单、体积上限、目录列举上限、开关），并纳入既有的严格校验与默认值不变量。

## Impact

- 新增 Go 包 `hub/internal/files`（服务、路径守卫、错误定义）与 `hub/internal/transport/http/files.go`；`hub/internal/transport/http/router.go` 增加路由与集中式错误映射，`handlerState` 与 `NewRouter` 各增加一个文件服务入参。
- `hub/internal/config/` 的 `config.go`、`load.go`、`defaults.go`、`validate.go` 按既有九处约定接入 `files` 段；YAML 校验保持未知键、重复键、null 值一律拒绝。
- `hub/internal/app/app.go` 增加文件服务组装；`hub/web/embed.go` 与静态资源服务不变。
- 前端新增 `hub/web/src/files/` 模块（API 客户端、路径工具、预览分流、二进制识别）与文件管理器组件；`App.vue` 增加入口按钮与打开状态，复用既有 `authFetch`/`errorText`。
- 前端新依赖 CodeMirror 6 与 DOMPurify；`marked` 已是既有依赖，Markdown 预览不再引入新包。新依赖需进入 `vite.config.ts` 的 `manualChunks`，并补进 `hub/web/test/loader.mjs` 的 mock 映射，否则 `npm test` 会失败。
- 更新 `configs/visual-tmux-client.example.yaml`、`README.md`、`README.zh-CN.md` 与 `hub/web/public/tmux-guide.zh-CN.md`。
- 无 `files` 配置段时行为与现在完全一致：默认不限定目录（边界为 hub 进程的操作系统权限，与用户已通过终端拥有的访问相同），功能开箱可用；任意既有 REST 路径、响应结构与 WebSocket 协议均不变。
