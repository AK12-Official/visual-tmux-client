# Visual Tmux Client

简体中文 | [English](README.md)

**Visual Tmux Client** 是一个轻量级、自托管的 Web 界面，用于通过浏览器管理和使用 tmux 会话。Vue 前端直接打包嵌入在单个 Go 二进制文件中，因此运行环境只需要该二进制文件和本机已安装的 `tmux`。

> 本项目目前处于早期的 `v0.3` 版本。它管理运行 Visual Tmux Client 的同一台机器上的 tmux；暂未实现多主机汇聚。

## 特性

- 查看、创建（一键生成自动命名的会话）、重命名及终止本地 tmux 会话，支持单会话或批量操作。
- 在由 xterm.js 驱动的完整浏览器终端中连接会话，支持在断开或主动 detach 后随时重新连接。
- 默认启用鼠标支持，连接终端可直接使用鼠标滚轮浏览 tmux 历史输出。
- 观察哪个会话正在输出：有新输出的后台会话卡片会高亮提示；当前查看的会话会在终端顶部栏及浏览器标签页标题中显示活动圆点。
- 终端顶部栏提供字号调节、全屏切换及关闭面板操作。
- 支持通过拖拽手动调整会话顺序与置顶固定，左侧会话栏可自由折叠。
- 操作反馈与终端事件通过分级、自动过期的 Toast 浮层提示。
- 内置中文 tmux 指南，点击应用内的帮助按钮即可查阅。
- 会话名称支持包括中文在内的非 ASCII 字符（最多 64 字符）；保留/禁用 `:`、`.`、`/`、`\`、全角易混淆符号、边缘空白及控制字符。
- 浏览器断开不会影响后台会话，tmux 会话持续运行。
- 采用 Bearer Token 认证与短期、一次性的 WebSocket 凭据（ticket）。
- Web 前端完整内嵌于单一无依赖的 Go 应用程序二进制中。
- 支持结构化外部 YAML 配置文件，并能与环境变量、CLI 参数有序覆盖。
- 退出时执行两阶段优雅停机预算，安全回收资源且不断开底层 tmux 会话。

## 运行要求

- macOS 或 Linux
- tmux 3.3 或更高版本（推荐并测试通过 3.7b）
- 现代主流浏览器

若从源码构建，额外需要 Go 1.26.3+ 与 Node.js 24+。

## 快速开始

从项目的 Releases 页面下载适合当前平台的压缩包，然后执行：

```sh
tar -xzf visual-tmux-client_0.3.0_linux_amd64.tar.gz
cd visual-tmux-client_0.3.0_linux_amd64
./visual-tmux-client
```

服务默认监听在 `http://127.0.0.1:7690`。启动时，无论 Token 是随机生成、从配置文件读取还是来自环境变量，都会在终端打印访问 Token 及访问地址。在浏览器中打开该地址并将 Token 粘贴到登录页面即可。

通过环境变量指定固定 Token：

```sh
VISUAL_TMUX_CLIENT_TOKEN="$(openssl rand -base64 32)" ./visual-tmux-client
```

或通过指定自定义 YAML 配置文件启动：

```sh
./visual-tmux-client --config configs/visual-tmux-client.example.yaml
```

运行 `./visual-tmux-client --help` 可查看全部参数和环境变量。

## 配置说明

Visual Tmux Client 支持外部 YAML 配置文件、环境变量和命令行参数。

### 优先级顺序

配置解析严格遵循以下优先顺序：
1. **命令行参数**（最高优先级，如 `--addr`、`--config`）
2. **环境变量**（`VISUAL_TMUX_CLIENT_*`）
3. **配置文件**（`--config <path>`、`VISUAL_TMUX_CLIENT_CONFIG` 或当前目录下默认的 `./visual-tmux-client.yaml`）
4. **内置默认值**（最低优先级）

若修改了 YAML 配置文件，需要重启服务使后端变更生效；前端浏览器页面在刷新后会自动通过接口获取最新的运行配置。

### 命令行参数

| 参数 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-addr` | `string` | `127.0.0.1:7690` | HTTP 监听地址（`host:port`） |
| `-config` | `string` | `""` | YAML 配置文件路径 |
| `-version` | `bool` | `false` | 打印版本信息并退出 |

### 环境变量

| 变量名 | 默认值 | 说明 |
| --- | --- | --- |
| `VISUAL_TMUX_CLIENT_CONFIG` | 未设置 | YAML 配置文件路径 |
| `VISUAL_TMUX_CLIENT_ADDR` | `127.0.0.1:7690` | HTTP 监听地址（`host:port`） |
| `VISUAL_TMUX_CLIENT_TOKEN` | 启动时随机生成 | 浏览器界面使用的共享 Bearer Token |
| `VISUAL_TMUX_CLIENT_ORIGIN` | 未设置 | 允许发起 WebSocket 连接的浏览器来源；默认匹配请求的主机与端口 |
| `VISUAL_TMUX_CLIENT_TMUX_PATH` | 从 `PATH` 中查找 | 指定 tmux 可执行文件路径 |

### YAML 配置文件示例

`configs/visual-tmux-client.example.yaml` 提供了包含详细注释的完整配置模板：

```yaml
version: 1

server:
  addr: "127.0.0.1:7690"
  read_header_timeout: "10s"
  idle_timeout: "120s"
  max_request_body_bytes: 65536 # 64 KiB

auth:
  token: "" # 为空时启动自动生成随机 Token
  ticket_ttl: "30s"
  ticket_sweep_interval: "10s"

tmux:
  path: "" # 为空时从系统 PATH 查找

websocket:
  origin: "" # 为空时仅允许同源；亦可指定完整公网来源（如 "https://tmux.example.com"）
  max_input_message_bytes: 8388608 # 8 MiB，支持超长终端粘贴
  control_write_timeout: "5s"
  output_write_timeout: "10s"
  exit_write_timeout: "3s"

terminal:
  max_dimension: 1000
  staging_buffer_bytes: 2097152 # 2 MiB 环形暂存缓冲
  output_high_water_bytes: 1048576 # 1 MiB 输出高水位
  output_low_water_bytes: 131072 # 128 KiB 输出低水位
  backpressure_poll_interval: "100ms"

shutdown:
  attachment_timeout: "3s"
  http_timeout: "10s"

web:
  session_poll_interval: "5s"
  activity_decay: "2s"
  activity_throttle: "500ms"
  resize_debounce: "100ms"
  reconnect:
    initial_delay: "500ms"
    max_delay: "3s"
  terminal:
    scrollback: 5000
    font_size: 13
    min_font_size: 8
    max_font_size: 24
  notifications:
    max_toasts: 5
    error_lifetime: "8s"
    warning_lifetime: "5s"
    info_lifetime: "3s"
```

默认只监听回环地址是有意的安全设计。如需通过其他机器访问，请在服务前部署 HTTPS 反向代理、使用高强度固定 Token，并将 `origin` 设置为准确的公网 HTTPS 来源。暴露到网络前请先阅读 [SECURITY.md](SECURITY.md)。

## 从源码构建

```sh
make build
./hub/visual-tmux-client
```

`make build` 会安装锁定版本的前端依赖、构建 Vue 应用，并将其嵌入编译到 `hub/cmd/visual-tmux-client` 中。

其他常用命令：

```sh
make test       # 前端单元测试、Go 单元/集成测试和 go vet
make lint       # golangci-lint 质量检查（固定 v2.13.2）
make fmt        # 按照项目代码风格格式化 Go 源码
make package    # 在 release/ 中打包当前平台发布压缩包
make clean      # 清理生成的构建产物和临时目录
```

如需一次生成多个平台的发布包：

```sh
TARGETS="darwin/arm64 darwin/amd64 linux/arm64 linux/amd64" make package
```

推送 `v*` 标签后，GitHub Actions 会自动触发发布工作流，构建以上四个平台并发布 GitHub Release。

## 架构说明

Visual Tmux Client 采用严格的分层设计与单一方向依赖：

```text
cmd/visual-tmux-client/  主入口，CLI 参数解析与退出码处理
internal/app/            应用组装根（composition root）、信号捕获与两阶段优雅关闭
internal/config/         强类型配置结构、严格 YAML 解码、多源加载器与来源追踪
internal/auth/           Bearer Token 校验、抗时序攻击恒定时间比较、一次性票据池
internal/session/        会话业务模型、名称规范校验、业务领域服务与 Backend 接口
internal/tmux/           Tmux 命令执行、PTY 进程管理、Socket 隔离与环境敏感信息清理
internal/terminal/       终端输出背压缓冲、环形暂存队列、连接 Pump 调度协调
internal/transport/http/ REST API 路由、DTO 映射、鉴权中间件与嵌入式 SPA 静态托管
internal/transport/ws/   WebSocket 握手、Origin 校验矩阵、二进制流协议与 Peer 适配器
web/                     Vue 3 浏览器前端（Vite + TypeScript + xterm.js）
configs/                 示例配置文件模板
```

浏览器启动时首先访问 `GET /api/client-config` 获取公开运行参数再挂载界面。终端连接前通过 REST API 用长期 Token 兑换 30 秒单次有效的临时票据，确保敏感凭据绝不泄露在 WebSocket URL 或进程参数中。

### 版本回滚方案

Visual Tmux Client 保持严格的向前与向后兼容性：
- 无 YAML 配置文件启动时，系统自动应用完整内置默认值，与旧版本行为一致。
- 历史版本的所有命令行参数与环境变量保持 100% 兼容。
- 若需要回滚至早期版本，只需停止服务并直接替换二进制文件。已有的 tmux 会话在服务启停和版本切换期间保持完整运行。

## 参与贡献

欢迎提交 Bug 报告和 Pull Request。开发流程请参阅 [CONTRIBUTING.md](CONTRIBUTING.md)，安全漏洞请按照 [SECURITY.md](SECURITY.md) 中的方式报告。

## 许可证

MIT © 2026 AK12-Official 及贡献者。详见 [LICENSE](LICENSE)。
