## 1. 集中配置入口

- [x] 1.1 建立 internal/config 强类型配置和 design.md 完整默认值清单，引入严格 YAML 解码依赖；以默认值及全部字段往返解析测试验证清单无遗漏。
- [x] 1.2 实现 --config、当前目录默认文件、相对路径、显式 CLI/环境/YAML 覆盖及来源记录；用临时目录和可注入环境测试文件缺失、权限错误、省略/空值/显式默认值及优先级。
- [x] 1.3 实现版本、未知/重复键、多文档、null、类型、范围和跨字段校验；测试无效 Origin、token、时长、上下水位、staging、字号和重连边界，断言错误不回显凭据。
- [x] 1.4 将现有启动流程接入 loader，移除 auth/tmux 对应用环境变量的重复读取，保留 help/version 提前返回和 tmux 缺失时操作报错；通过启动参数、token 来源和 tmux 不可用测试验证。
- [x] 1.5 将后端超时、票据、API/WS 限制、终端和关闭参数注入各 Options；通过非默认 TTL、请求大小、输出背压及关闭预算行为测试验证配置实际生效。

## 2. 会话与基础模块拆分

- [x] 2.1 提取 internal/auth 的凭据与票据逻辑，将 HTTP middleware 留在传输侧；迁移并通过常量时间比较、单次消费、过期、错误会话、sweep 和空凭据拒绝测试。
- [x] 2.2 提取 internal/session 的模型、名称校验、业务错误、服务及 Backend 接口；用 fake backend 验证无效名称不调用后端、操作编排和错误保持可区分。
- [x] 2.3 提取 internal/tmux 命令、解析、环境清理及全局选项同步，实现 session Backend；迁移并通过隔离 socket 的 Unicode、精确定位、冲突、创建目录和无冗余 repaint 测试。

## 3. 终端与传输边界

- [x] 3.1 定义 terminal Process/ProcessFactory 边界并由 tmux 封装 PTY 创建、resize、关闭及进程回收；验证 resize/close 并发安全、Wait 仅一次、断开只结束 attach 客户端且秘密不进入子进程。
- [x] 3.2 将输出队列、staging 和 pump 协调迁入 internal/terminal，定义 Peer 接口并去除 attachment 对 server 的引用；迁移 buffer/outputpump 测试并验证 ready/输出/exit 顺序和非默认阈值下有界缓冲。
- [x] 3.3 实现 terminal.Manager 的注册、注销和关闭状态，关闭期间拒绝新注册并回收资源；增加注册与关闭竞争测试，通过静默会话、ghost client 和超时强制关闭回归。
- [x] 3.4 提取 transport/ws 的握手、Origin、消息编解码与 Peer adapter；通过完整 Origin 矩阵、拒绝不消费票据、错误消息/关闭代码、大段粘贴、resize 和并发 attach 测试。
- [x] 3.5 提取 transport/http 路由、DTO、状态码映射和鉴权中间件，注入 session/auth/WS handler；通过原 REST API 测试确认路径、响应和无鉴权不执行 tmux 行为兼容。
- [x] 3.6 建立 web/embed.go 并将 fs.FS 注入静态处理器；验证打包后 assets 缓存、Markdown MIME、SPA fallback 和未知 API/WS 404。
- [x] 3.7 建立 app composition root 与 cmd/visual-tmux-client 入口，统一配置注入、信号与两阶段关闭；迁移入口测试，构建新入口并通过 hub 重启后 tmux 会话仍可重新连接的测试。

## 4. 浏览器运行配置

- [x] 4.1 增加 PublicClientConfig 白名单 DTO 与 GET /api/client-config，按约定返回 version/web 和整数毫秒；测试未登录可读、no-store、完整字段集合、敏感值缺失及读取不调用 tmux。
- [x] 4.2 增加 config.ts 和 main.ts 加载/验证/错误重试引导；建立最小前端测试命令，验证失败不启动业务、非法响应被拒绝、重试仅挂载一次且无重复计时器。
- [x] 4.3 将 App.vue 和 terminal.ts 的轮询、活动、resize、重连、scrollback 参数替换为配置消费；通过非默认值的定时器/终端构造测试，保留会话结束不重试及仅最新连接尝试生效。
- [x] 4.4 合并服务端字号默认值/范围与现有 localStorage 偏好，清除 TerminalView/terminal 的重复字号默认来源；测试无存储、非法存储、合法覆盖和越界钳制，并验证字号变化触发 re-fit。
- [x] 4.5 将 toasts.ts 的等级时长和最大堆叠数接入配置，避免模块导入时提前读取；以可控计时器测试非默认过期、并存、手动关闭和最旧淘汰。

## 5. 构建、文档与综合验证

- [x] 5.1 更新 Makefile、scripts/package.sh、CI 和 release workflow 的 Go 构建入口，保留 -X main.version、模块路径和产物名称，并接入前端测试；通过本地构建和构建命令检查验证。
- [x] 5.2 添加 configs/visual-tmux-client.example.yaml，覆盖设计清单且不含真实凭据，将其纳入两条打包路径；用 loader 解析示例并检查发布压缩包内容验证。
- [x] 5.3 更新中英文 README 和相关开发说明，记录配置字段、优先级、空值、路径、重启/页面刷新、源码入口、包职责与回滚方式；核对示例命令和实际帮助输出一致。
- [x] 5.4 执行前端测试/类型检查/构建、hub 下 go test ./...、go test -race ./... 和 go vet ./...；记录结果，确认迁移后的真实 tmux 测试执行而非因环境缺失被跳过。
- [x] 5.5 对构建产物进行无 YAML、部分 YAML、环境与 CLI 覆盖、非法 YAML 的启动 smoke test，并以同一前端构建验证修改 YAML 后重启/刷新生效；确认日志来源、嵌入 UI、滚动、输入、断开与重连符合规格。
- [x] 5.6 检查最终依赖方向与配置读取位置，确认业务包不依赖 transport、attachment 不持有 server、应用环境变量只在 config 读取、旧根 main/ringbuffer 已迁移；使用 go list 依赖输出和源码搜索记录验证。

## 6. golangci-lint 质量门禁

本组 6.1、6.2 在配置/模块实施早期完成，6.3 随各包重构进行，6.4、6.5 在交付前完成。

- [x] 6.1 固定兼容 Go 1.26.3 的 golangci-lint v2 patch 版本，新增根目录 .golangci.yml，按 design.md 配置显式规则、阈值、格式化及窄范围例外；运行 config verify 和 linters 命令，确认配置有效且启用集合正确。
- [x] 6.2 增加固定版本安装入口、make lint、make fmt，在 `.github/workflows/ci.yml` 中于 Go 工具链安装和前端构建之后接入配置校验及同配置全量检查，覆盖现有 push(main) 和 pull_request 触发；验证本地/Actions 使用相同版本、hub 工作目录和根配置，违规返回非零并使 job 失败，不设置 continue-on-error、不仅检查新增问题，检查流程不改写文件。
- [x] 6.3 随模块迁移整改魔法数字、重复字符串、超长行/函数、复杂度和正确性发现，逐个说明必要的 nolint；运行全量 lint 及受影响行为测试，确认无未豁免发现且无宽泛屏蔽。
- [x] 6.4 使用临时 fixture 验证 mnd、goconst、lll、funlen、errcheck 能报告违规并返回失败，验证测试文件仍受行长/正确性检查且 defaults.go 仅豁免 mnd；记录规则名与退出码，fixture 不进入产品源码。
- [x] 6.5 更新 CONTRIBUTING 与相关 README 的工具安装、阈值、例外和检查说明；将 make lint 纳入最终验收，执行全量 lint、Go 测试/race/vet 及前端检查并记录结果。
