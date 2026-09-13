## Context

动机见 proposal.md。当前 Go module 位于 hub，除 ringbuffer 外后端都在 package main；main.go 读取配置但 auth.go 再次读取 token，tmux.go 自行读取二进制路径。server 同时持有路由、票据、tmux 和 attachment 集合；attach.go 持有 server 指针，并处理握手、PTY、输出队列和进程回收。web/dist 由 server.go 嵌入。前端 main.ts 直接挂载 App，运行参数分布在 App.vue、terminal.ts、toasts.ts。

已有针对 Origin、票据、精确会话定位、大段粘贴、静默会话断开、PTY 回收、滚动及关闭的测试。现有 session-hub 规格直接引用环境变量作为 Origin 来源，必须修改为有效配置；其他安全语义保留。web-session-manager 要求活动提示在数秒内消退，因此可配置的活动时长应有上限。

## Goals / Non-Goals

**Goals:** 配置在进程边界解析一次；模块仅接收自身强类型 Options；跨包依赖无环；终端资源所有权可测试；同一前端构建可使用不同运行参数。

**Non-Goals:** 不把协议常量、CSS、主题色、会话名语法、生成名称格式、tmux 强制选项或构建工具配置转成 YAML；不新增热更新、数据库、多主机或框架式依赖注入。

## Decisions

### 1. 单文件与显式优先级

采用标准 flag 与一个支持严格解码的 YAML 库，不引入自动环境绑定框架。config.Load 接收 args、工作目录和可注入的环境查询，执行：信息参数提前返回、选择路径、读取单文档 YAML、字段/结构验证、按字段合并、最终值及关系校验、返回配置与来源元数据。模块不得读取 VISUAL_TMUX_CLIENT_*；tmux 子进程环境构造仍可读取并清理普通操作系统环境。

优先级为显式 CLI > present 环境变量 > YAML > defaults。使用字段存在性区分省略和零值，环境使用 LookupEnv；显式空 token/origin/path 分别表示生成凭据、默认 Origin、PATH 查找。YAML null、重复键、多文档、未知字段拒绝；version 缺省为 1。覆盖不会掩盖 YAML 结构或类型错误；最终跨字段关系在合并后验证。文件不支持 include、模板、插值或多目录搜索，避免引入隐式来源。

YAML 相对 tmux.path 按配置目录解析；环境变量路径沿用当前工作目录语义。空路径使用 PATH。缺少 tmux 仍允许 hub 启动，操作返回 tmux_unavailable，保留现有延迟报告语义；配置语法校验不等同于执行依赖可用性检查。

### 2. 运行配置清单与边界

下表定义本轮全部外部化字段；默认值来自当前实现。配置字段均为 snake_case，时长为带单位字符串，大小为整数 bytes。

| YAML 字段 | 默认值 | 约束 / 使用方 |
| --- | --- | --- |
| version | 1 | 仅支持 1 |
| server.addr | 127.0.0.1:7690 | 合法 host:port，端口 1..65535 |
| server.read_header_timeout | 10s | 正时长 / HTTP |
| server.idle_timeout | 120s | 正时长 / HTTP |
| server.max_request_body_bytes | 65536 | 1..8388608 / JSON API |
| auth.token | 空 | 空时生成；非空必须可被现有浏览器凭据流程原样提交，无首尾空白或非法 header 字符 |
| auth.ticket_ttl | 30s | 1ms..5m / 单次票据 |
| auth.ticket_sweep_interval | 10s | 正时长 / 过期清理 |
| tmux.path | 空 | 空使用 PATH；文件解析与执行失败保持既有语义 |
| websocket.origin | 空 | 空为默认策略；非空为 HTTP(S) origin，无用户信息、查询、片段或路径，比较保持精确匹配 |
| websocket.max_input_message_bytes | 8388608 | 1..67108864 / WS 入站 |
| websocket.control_write_timeout | 5s | 正时长 / JSON 控制帧 |
| websocket.output_write_timeout | 10s | 正时长 / 二进制输出 |
| websocket.exit_write_timeout | 3s | 正时长 / 最终 exit 帧 |
| terminal.max_dimension | 1000 | 80..65535，兼容固定 80x24 缺省尺寸及 PTY uint16 |
| terminal.staging_buffer_bytes | 2097152 | 32768..67108864 |
| terminal.output_high_water_bytes | 1048576 | 1..67108864 |
| terminal.output_low_water_bytes | 131072 | 0 < low < high |
| terminal.backpressure_poll_interval | 100ms | 正时长 |
| shutdown.attachment_timeout | 3s | 正时长 / attachment 集合关闭阶段 |
| shutdown.http_timeout | 10s | 正时长 / HTTP Shutdown 阶段 |
| web.session_poll_interval | 5s | 浏览器正时长 |
| web.activity_decay | 2s | 1ms..5s，保持数秒内消退 |
| web.activity_throttle | 500ms | 1ms..activity_decay |
| web.resize_debounce | 100ms | 浏览器正时长 |
| web.reconnect.initial_delay | 500ms | 浏览器正时长，<= max_delay |
| web.reconnect.max_delay | 3s | 浏览器正时长 |
| web.terminal.scrollback | 5000 | 0..100000 |
| web.terminal.font_size | 13 | 整数，位于 min/max 内 |
| web.terminal.min_font_size | 8 | 1..256 |
| web.terminal.max_font_size | 24 | min..256 |
| web.notifications.max_toasts | 5 | 2..100，保留多通知并存 |
| web.notifications.error_lifetime | 8s | 浏览器正时长 |
| web.notifications.warning_lifetime | 5s | 浏览器正时长 |
| web.notifications.info_lifetime | 3s | 浏览器正时长 |

通用后端正时长限制为 1ms..24h；浏览器时长必须是整数毫秒且在 1..2147483647ms 内，另受表中更窄范围限制。这些范围为本设计选定的运维边界，并非声称当前已有校验。

PTY 读块与输入写块保留 32KiB 内部常量；暂存容量必须容纳一个读块。高水位是暂停阈值，不宣称精确内存硬上限：单次读取和 ready 暂存转移允许有限超量，应在测试中验证有界性。现有 ringbuffer 整 chunk 淘汰不证明 chunk 本身就是完整 UTF-8/escape 单元；本次不重写流算法，也不强化这一保证。保留既有字节顺序测试，将已有注释的过强表述作为文档修正，不扩大本轮为终端解析器开发。

### 3. 包结构与依赖

```text
hub/
  cmd/visual-tmux-client/main.go
  internal/
    app/                 # composition, serve, shutdown
    config/              # defaults, load, validation, provenance
    session/             # model, service, validation, errors, Backend port
    auth/                # credential primitives and tickets
    tmux/                # argv, parsing, options, PTY process, scrubbed env
    terminal/            # manager, attachment, output, queue, staging, ports
    transport/
      http/              # router, REST, middleware, static, client config
      ws/                # upgrade, origin, protocol, connection adapter
  web/embed.go
  web/src/config.ts
  web/src/...
  go.mod
  go.sum
configs/visual-tmux-client.example.yaml
```

app 为 composition root；cmd 只负责进程入口、版本/帮助和退出码，version 变量继续位于 main 以兼容 -X main.version。HTTP transport 依赖 session service、auth 与可注入的 WS handler；WS transport 依赖 auth 和 terminal。session 定义 Backend 接口；terminal 定义 Process/ProcessFactory 与面向终端的 Peer 接口；tmux 实现后端和进程工厂，WS connection 实现 Peer。业务层不 import transport，tmux 不 import app，config 不 import 业务模块。app 将根配置转换为模块 Options。

session service 承担创建/重命名/查询的业务编排、名称校验和业务错误；tmux adapter 处理命令输出和平台错误，并映射到业务错误。HTTP DTO 与 tmux 输出格式分开，避免业务模型携带传输策略。全局选项检查、互斥和按需 set 归 tmux adapter，创建及 attach 路径继续调用。

只抽象实际边界，不为每个类型添加接口。相比仅按文件移动，这能去掉 server 的隐式依赖；相比引入 repository/domain/usecase 多层模板，对当前本地 tmux 服务更适中。

### 4. 终端资源所有权

tmux adapter 创建并封装 PTY 与 attach 子进程，提供 Read/Write/Resize/Close/Wait；它封装 resize/close 的文件描述符互斥，关闭只针对 attach 客户端，不终止 tmux 会话。terminal attachment 负责 pump 协调，通过幂等关闭路径停止读写并回收进程；Wait 必须只执行一次，结果可共享，不能让多个 pump 直接调用 exec.Cmd.Wait。

terminal.Manager 独占活动集合和关闭状态。注册与开始关闭互斥，关闭期间拒绝新 attachment 并回收已创建资源，避免 snapshot 后漏登记。attachment 不持有 server，只持有进程、Peer、Options、context 与注销回调。ready、暂存转移、输出、exit 的顺序及关闭代码保持现有协议。

Origin 拒绝必须发生在 upgrade、票据兑换、进程创建之前；允许的握手继续使用现有先 upgrade 后 redeem 的错误帧语义。handler 返回不能取消交给后台 pump 的 context。app 先触发 manager 停止接纳/关闭，再进行有界 HTTP Shutdown；超时强制关闭 socket。总关闭预算为两个阶段预算之和及有限调度开销，不把 http_timeout 误写成整个进程预算。

### 5. 前端配置引导与公开投影

GET /api/client-config 返回 `{version: 1, web: {...}}`；web 层次和字段名沿用 YAML，所有时长字段值改为整数毫秒。定义单独 PublicClientConfig DTO，手工列出所有 web 字段，禁止从完整 Config 序列化后删字段。设置 no-store，不访问 tmux，不受 bearer 中间件保护；现有会话路由仍受保护。

main.ts 在配置成功前显示最小加载/失败重试状态，取得并验证响应后才挂载 App。失败不启用独立默认值，防止隐藏部署错误。用单次启动状态避免重试重复挂载。config.ts 提供只读、强类型浏览器配置；terminal、App 和 toasts 在初始化后消费，避免模块 import 时提前读取。字号存储键不变，保留合法个人设置，越界钳制并保存；排序和侧栏偏好仍在 localStorage。

轮询、重连等设置在页面生命周期内固定。hub 配置修改需要重启，已有页面通过刷新获取新值；不承诺旧页面重连时同步新配置。初始化错误使用独立页面状态，不依赖尚未初始化的 Toast 配置。相比把配置编译进 Vite，这保留单构建多部署的能力。

### 6. 静态资源、测试与交付

web/embed.go 使用 go:embed all:dist 并导出 fs.FS；transport/http 接收文件系统，保留 assets 缓存、帮助 Markdown MIME、SPA fallback 和未知 API/WS 404 行为。不通过相对父路径 embed。ringbuffer 实现及测试迁入 terminal 的 staging 文件，避免暴露无外部消费者的公共包。

包内测试跟随职责移动；跨模块行为放在 app/transport 集成测试，使用隔离 tmux socket。共享 tmux 测试辅助仅在需要时放 internal/testutil，不给生产包暴露内部状态以迁就旧测试。

增加配置表驱动测试、公共投影泄漏测试、非默认值行为测试和浏览器配置引导/字号/定时参数测试；前端当前没有 test script，应增加最小自动化测试入口并纳入 CI。完整终端行为继续依靠既有真实 tmux 集成测试及针对性浏览器验证。

### 7. golangci-lint 质量门禁

采用 golangci-lint v2 配置格式（`version: "2"`），根目录 `.golangci.yml` 是检查规则的唯一来源。工具安装版本固定到明确 patch 版本，实施时选择支持项目 Go 1.26.3 的版本并验证构建工具链兼容性；本地和 CI 共用版本声明，禁止使用 latest。此开发工具配置不放进运行 YAML。

规则采用 `linters.default: none` 加显式 enable 列表，避免升级改变默认检查集合。单行检查解释为单行长度检查。初始规则与项目选定阈值如下：

| 规则 | 配置策略 |
| --- | --- |
| mnd | 检查业务逻辑中的魔法数字，覆盖 argument、case、condition、operation、return、assign；不把所有数字都机械提为常量 |
| goconst | 重复字符串长度至少 3、出现至少 3 次时建议有语义的常量；数字由 mnd 负责 |
| lll | 单行最多 120 字符，tab-width 为 4；适用于生产与测试代码 |
| funlen | 函数最多 80 行、50 条语句，忽略注释行；通过职责拆分解决超限 |
| gocyclo | 圈复杂度超过 15 报告 |
| govet、staticcheck、unused、ineffassign | 常见正确性、未使用代码及无效赋值检查 |
| errcheck、errorlint | 未处理错误及错误包装/比较检查；errcheck 检查空白标识符丢弃错误 |
| nolintlint | 抑制必须指定具体 linter、说明理由，并检查无效抑制 |

格式使用 v2 的独立 `formatters` 配置启用 gofmt、goimports，不将它们误放进 linters。检查命令不自动修改文件；提供独立 `make fmt` 供开发者显式格式化。

例外明确限定：`*_test.go` 仅豁免 mnd、goconst、funlen、gocyclo，保留 lll 和正确性检查，避免表驱动测试为行数限制被切碎；`hub/internal/config/defaults.go` 仅豁免 mnd，因为这里就是命名配置默认值的集中声明；生成文件按标准生成标记排除。不整包排除 terminal、tmux 或 transport，不为长函数添加全局豁免。无法合理拆行的 URL、协议样本和有意忽略的关闭错误使用最小作用域、带理由的 `//nolint:<rule>`。关闭错误是否可忽略必须按生命周期语义判断，不能统一改成静默丢弃。

本地新增 `make lint`：确保 web/dist 已构建，再在 hub 中运行 `golangci-lint config verify --config ../.golangci.yml` 和 `golangci-lint run --config ../.golangci.yml ./...`。独立 lint target 与现有 make test 分开，CI 两者均执行；GitHub Actions 的 `.github/workflows/ci.yml` 在 Go 工具链安装和前端构建后执行同样检查，沿用 push(main) 与 pull_request 触发，工作目录为 hub，显式读取 ../.golangci.yml。安装版本与本地固定版本一致；任何 lint 发现或配置错误使 job 失败，不使用 continue-on-error 或仅新增问题过滤。工具缺失应给出固定版本安装指引，不在检查命令中隐式联网安装。

使用全量检查，不通过 only-new-issues、输出截断或基线隐藏存量问题；配置 issues 的报告数量限制为不截断。安装/配置先建立，再随本次模块重组整改，交付时全量零未豁免发现。保留 Go 测试、race 和 vet，不将 lint 当作行为验证替代。验证规则生效时在临时独立 fixture 中分别制造魔法数字、重复字符串、超长行、超长函数和未处理错误，确认返回失败及对应规则名；fixture 不提交到产品包。

规则和 v2 配置依据官方文档：
- https://golangci-lint.run/docs/linters/configuration/
- https://golangci-lint.run/docs/configuration/file/
- https://golangci-lint.run/docs/configuration/cli/

## Risks / Trade-offs

- [并发拆分引入泄漏或重复 Wait] -> 先迁移行为测试，再拆资源边界，运行 race 和静默会话/关闭回归。
- [严格配置会拒绝过去未检查的错误环境值] -> 文档说明新校验，保留正常值及空值语义，字段错误不泄漏秘密。
- [配置值组合影响终端内存和交互] -> 统一范围/关系校验，对非默认值测真实行为，明确阈值与硬内存界限区别。
- [公开接口泄漏私密字段] -> 独立白名单 DTO，验证完整 JSON 字段集合和敏感值缺失。
- [前端初始化新增网络依赖] -> 同源小响应、no-store、明确重试状态，无双重默认源。
- [目录移动破坏发布] -> 同步 Makefile、CI、release workflow 与 package.sh，验证产物版本和嵌入资源。

- [规则过严导致机械拆分或大量抑制] -> 使用上述窄范围例外，优先按职责拆分；有语义的命名常量不应变成无意义的 number 常量。
- [工具与 Go 版本不兼容] -> 固定兼容版本并在本地和 CI 验证，升级作为显式工具维护变更。

## Migration Plan

1. 先实现配置 loader/Options 并接入现有后端，用兼容测试固定行为。
2. 分步移动 auth/session/tmux，再拆 terminal/transport，最后形成 app/cmd 与 web/embed；每阶段保持可构建。
3. 增加公开配置接口和浏览器引导，替换清单中的前端常量。
4. 更新源码构建入口、测试命令、示例和文档。示例命名为 example，不自动作为真实配置启用；发布包附带该示例。
5. 验证无 YAML、部分 YAML、环境覆盖与 CLI 覆盖，运行 Go 测试/race/vet、golangci-lint 全量检查、前端类型/构建/测试和发布包 smoke test；不实际发布。

回滚可恢复旧二进制，tmux 会话和浏览器存储无需迁移；旧版本不会读取 YAML，必须将地址、token、origin、tmux 路径恢复为原有参数/环境变量。无持久化配置迁移或自动回写。
