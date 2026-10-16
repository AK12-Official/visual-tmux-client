# Visual Tmux Client

简体中文 | [English](README.md)

**Visual Tmux Client** 是一个轻量级、自托管的 Web 界面，用于通过浏览器统一管理和使用本地以及多台远程 SSH 服务器上的 tmux 会话与文件。Vue 前端直接打包嵌入在单个 Go 二进制文件中，无任何外部运行时依赖，只需系统具备标准 `tmux` 及远端具备 SSH/SFTP 服务。

## 会话保留默认值

VTC 创建或连接会话时，会在所连接的 tmux 服务上设置全局默认值：
`exit-unattached off`、`destroy-unattached off`、`remain-on-exit failed`。
断开浏览器或关闭 VTC 不会因此销毁会话；窗格中的程序异常退出时保留输出和退出状态，正常 `exit` 仍会关闭窗格。
这些全局默认值也影响同一 tmux 服务上没有单独覆盖设置的其他会话和窗口。
已有的会话、窗口或窗格级覆盖值保持优先。空服务仍沿用 tmux 的 `exit-empty` 设置。

异常退出后，可在 tmux 命令提示符（`Ctrl+b` 后按 `:`）中运行 `respawn-pane` 重新启动当前窗格。
这些设置不能阻止显式的 `kill-session`、`kill-server` 或系统重启；测试使用独立 socket，清理只针对测试服务。

## 特性

- 多主机统一管理：在同一个 Web 界面中无缝管理本地机器与多台远程 SSH 主机。
- 查看、创建（一键生成自动命名的会话）、重命名及终止 tmux 会话，支持单会话或批量操作。
- 在由 xterm.js 驱动的完整浏览器终端中连接会话，支持在断开或主动 detach 后随时重新连接。
- 默认启用鼠标支持，连接终端可直接使用鼠标滚轮浏览 tmux 历史输出。
- 观察哪个会话正在输出：有新输出的后台会话卡片会高亮提示；当前查看的会话会在终端顶部栏及浏览器标签页标题中显示活动圆点。
- 终端顶部栏提供字号调节、全屏切换及关闭面板操作。
- 支持通过拖拽手动调整会话顺序与置顶固定，左侧会话栏可自由折叠。
- 操作反馈与终端事件通过分级、自动过期的 Toast 浮层提示。
- 在终端内浏览、预览、编辑与整理本地或远程 SFTP 文件：目录树、多标签编辑器、Markdown 与图片预览，以及新建/重命名/删除/下载的右键菜单。
- 可直接把文件路径插入终端输入行，无需离开文件管理器。
- 安全远程主机注册：支持 SSH 密码与加密私钥认证（ed25519、RSA、ECDSA，支持密钥口令 Passphrase），严格实行首次使用信任（TOFU）主机公钥指纹 Pinning。
- 静态加密凭据存储：使用 AES-256-GCM 与认证主密钥安全加密保存远程主机凭据。
- 主机工作空间相互隔离：独立会话命名空间、连接池管理及自动 Keepalive 心跳探测。
- 内置中文 tmux 指南，点击应用内的帮助按钮即可查阅。
- 会话名称支持包括中文在内的非 ASCII 字符（最多 64 字符）；保留/禁用 `:`、`.`、`/`、`\`、`;`、全角易混淆符号（`：`、`．`、`；`）、边缘空白及控制字符。
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
tar -xzf visual-tmux-client_0.3.1_linux_amd64.tar.gz
cd visual-tmux-client_0.3.1_linux_amd64
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
| `VISUAL_TMUX_CLIENT_HOSTS_STATE_DIR` | 系统用户配置目录（Linux 下为 `~/.config/visual-tmux-client`） | 保存持久化 SQLite 主机数据库（`hosts.db`）的目录 |
| `VISUAL_TMUX_CLIENT_HOSTS_MASTER_KEY_FILE` | 从 state dir 自动派生（`master.key`） | 用于加密存储凭据的 32 字节 AES-256 主加密密钥文件路径 |

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

files:
  enabled: true
  # 省略即表示不限目录，边界仅为 hub 进程自身操作系统用户的访问权限。
  # roots:
  #   - "/srv/projects"
  max_file_size: 104857600 # 100 MiB
  max_dir_entries: 1000

hosts:
  # 多主机 SSH 注册中心与远程传输配置
  state_dir: "" # 为空时使用系统用户配置目录（Linux 下为 ~/.config/visual-tmux-client）
  master_key_file: "" # 为空时使用 state_dir 内的 master.key
  dial_timeout: "10s"
  handshake_timeout: "10s"
  operation_timeout: "30s"
  keepalive_interval: "15s"
  max_backoff: "30s"
  max_connections: 16
```

### 文件访问边界

`files.enabled: false` 会关闭整个文件能力：所有文件操作在**不触碰文件系统**的前提下被拒绝，终端与会话功能不受影响。配置的 `roots` 同样不会被解析或打开，因此一个缺失或不可读的 root 不会阻止一个关闭了文件管理器的 hub 启动——开启文件管理器时，同一个 root 会导致启动失败。当 hub 进程无法访问它被指到的那些目录时，这正是应当采用的设置。

默认**不做任何目录限制**。文件操作的上界就是 hub 进程自身操作系统用户已有的访问权限。这是刻意的，且不会带来新的能力：同一枚 bearer token 同时授权了以该用户身份运行的全交互终端，因此持有 token 的人无论是否配置 `roots`，都能 `cat` 该账号可读的任何文件。若默认限制在 `$HOME`，只会让 `/srv`、`/data`、`/var/log` 下的正常工作失败，却无法对 token 持有者构成任何阻隔。

配置 `files.roots` 可把全部操作限制在一组目录内：

```yaml
files:
  roots:
    - "/srv/projects"
    - "/var/log"
```

每个根必须是已存在目录的绝对路径。根会在启动时解析软链，鉴权针对解析后的真实路径判定，因此无法借由软链越出根目录。根不存在、不是目录、无法解析，或解析后落在下面这些始终拒绝的路径上，都会导致启动失败——而不是启动一个每次文件操作都被拒绝的 hub。`/proc`、`/sys`、`/dev` 在两种模式下都一律拒绝，任何配置都无法放开；匹配不区分大小写，因此在大小写不敏感的文件系统上换一种拼写同样会被拒绝。

配置了 `roots` 时，操作不仅会**按边界鉴权**，还会**通过一个打开在所属 root 上的句柄、以相对该 root 的路径执行**。这正是让边界对同机其他进程也成立、而不只是对调用方成立的关键：鉴权会解析目标路径，而在这之后、操作之前被替换为、且指向 root 之外的软链，会被句柄拒绝，而不是被悄悄跟随；若替换后的链接仍指回同一个 root 内，则会被跟随——句柄拒绝的是越出边界，而不是被重定向。在两个已配置 root 之间移动条目会被拒绝：句柄只能在自己的树内移动，而按两个绝对路径执行意味着在移动发生的那一刻重新解析两端，期间被替换的路径段会把这个条目带出边界，或把边界外的东西带进来。在浏览器里，只有当输入的目标名经软链下行到另一个 root 时才会遇到这一拒绝，它会作为错误提示出来；跨 root 的移动请用终端里的 `mv`。

没有配置 `roots` 时就不存在边界，也不做此声明：操作直接按路径执行，上界仅为 hub 自身操作系统用户的访问权限。

`max_file_size` 限制单次读取、写入或下载的大小；`max_dir_entries` 限制单次目录列举的条数，超出部分会被标记为已截断——它同时限制 hub **实际读取**的条数，因此列举十万条与列举一千条的目录开销相同。也正因如此，被截断的列举结果不保证是字典序最靠前的那些条目。

浏览器不会只凭文件名就断定一个文件是什么。扩展名暗示二进制、或图片大到无法渲染时，会先向 hub 问一次（一次不携带文件内容的请求），因此名为 `notes.dat` 的文本文件会在编辑器中打开，而不是因为名字被拒。图片预览上限按读取时 hub 报告的大小判定，而不是目录列举时给出的旧大小；超限的文件作为文件信息呈现并提供下载，且不会被传输超过上限的部分——hub 每次读取都会报告长度，浏览器因此在响应体到达之前就能拒绝。hub 的回答只回答「这些字节能否按文本解码」这一个问题，因此它决定的是哪些文件进编辑器：上限之内的图片仍然按图片预览，与 hub 是否把它的内容判为二进制无关（图片本来都是二进制）。

读取文件时，文本/二进制的判定基于**整个文件内容**而不是开头若干字节：仅在开头看起来像文本、后面夹带二进制数据的文件，否则会被当成文本解码，下一次保存就会把这些替换字符写回、覆盖掉原本的字节。可以读取的只有普通文件——目录、命名管道、设备文件一律拒绝，而不是尝试打开。

保存采用乐观并发：观察到的修改时间不再匹配时 hub 会拒绝写入，并在真正替换文件之前再确认一次目标仍是本次写入开始时的那一个。这最后一道检查同样作用于强制覆盖，因此在传输途中被改名或删除的文件不会被原路径重新创建出来。修改时间的比较使用文件系统自身的精度，因此同一毫秒内的两次改动会被视为两次改动。管理器还会让自己的两个请求互相避让：删除会等待已在途的保存结束后才发出；而对一个文件、或它所在的某个目录已发出删除时，保存会被拒绝而不是发出。这个次序覆盖的是本浏览器自己发出的写入；其他程序发起的写入不受它约束，上面那道最终检查正是为此存在。

保存会拒绝**非普通文件**的目标（命名管道、socket、设备节点）：替换写入的是一个普通文件，写到这些节点上等同于删除该节点。保存是**替换**文件而不是就地改写，这正是让失败或中断的写入不破坏原有内容的原因：替换会保留目标的权限位，以及在 hub 有权设置时保留其 owner；而文件其余随原 inode 存在的东西——ACL 与扩展属性——不会随之保留。一个文件若有多个名字（硬链接）指向它，写入会被拒绝，直到你确认：替换写入的是你保存的那个名字，其他名字仍保留它们原有的内容。


文件管理器会打开在当前会话活动 pane 的工作目录。该目录只是一个起点，且只读取一次：之后可以自由浏览，切换活动 pane 也不会移动已经打开的管理器。

配置了 `roots` 且 pane 的工作目录落在这些 root 之外时，管理器会改为打开**第一个 root** 并明确告知——浏览器无从得知边界允许哪些目录，只能由 hub 给出。若该目录因其他原因不可用（例如已被删除），则改用最近一个仍能打开的祖先目录，并同样给出提示，而不是显示一棵空树。

### 远程主机文件访问（SFTP）

在已注册的远程主机上执行文件操作时：
- 操作通过 SFTP (v3) 以认证 SSH 用户身份执行。操作边界受该远程用户的操作系统文件权限约束；本地配置的 `files.roots` 仅约束本地主机，不约束远程主机。
- **原子替换**：当远端 SFTP 服务支持 OpenSSH `posix-rename@openssh.com` 扩展时，文件保存采用原子替换与乐观并发检查。若远端不支持原子替换，覆盖已存在的目标文件将被拒绝（除非显式强制覆盖），以防止由于网络中断导致旧文件被损坏。
- **硬链接防护**：如果远程文件的硬链接计数大于 1，替换已有目标需经显式确认，因为原子替换会解除该名称与原有 inode 的关联。
- **时间戳精度**：标准 SFTP v3 传输整数秒级修改时间。远程文件的乐观并发检查基于粗粒度的 1 秒时间戳。
- **临时上传文件**：文件保存先写入同级临时文件（`.vtc-tmp.*`），写入完毕后再重命名替换，确保未完成的网络传输绝不会覆盖破坏旧文件。

## 多主机部署与安全

### 单管理员权限与服务器信任
Visual Tmux Client 遵循单管理员安全模型。持有共享 Bearer Token 即拥有对本地 hub 及全部已注册远程主机的完全管理权限（包括会话管理、文件读写及任意命令执行）。

在注册远程主机时，VTC 严格采用首次使用信任（TOFU）主机密钥固定：
1. 首次连接前向管理员展示远端主机的公钥 SHA-256 指纹，待管理员确认。
2. 确认后，公钥指纹安全持久化保存在 SQLite 数据库（`hosts.db`）中。
3. 后续连接严格核验已 Pinning 的公钥指纹；一旦出现指纹不匹配或非预期的变更，连接将立即被拒绝阻断。

### 容器化部署与持久化挂载
在 Docker 或 Kubernetes 中运行时：
- 主机注册数据库（`hosts.db`）和主加密密钥（`master.key`）存放于 `hosts.state_dir`（或由环境变量 `VISUAL_TMUX_CLIENT_HOSTS_STATE_DIR` 指定）。
- **必须为该状态目录挂载持久化存储卷**，以确保容器重启或镜像升级时已注册的主机及加密凭据不会丢失。
- 确保持久化目录具备 `0700` 权限，主密钥文件具备 `0600` 权限。

Docker 持久化挂载示例：
```sh
docker run -d \
  -p 7690:7690 \
  -v /var/lib/visual-tmux-client/hosts:/data/hosts \
  -e VISUAL_TMUX_CLIENT_HOSTS_STATE_DIR=/data/hosts \
  -e VISUAL_TMUX_CLIENT_TOKEN="secret-admin-token" \
  visual-tmux-client:latest
```

### 密钥与状态备份、灾难恢复
- **备份**：定期同时备份状态目录下的 `hosts.db` 与 `master.key`。二者必须一同备份：`hosts.db` 中的敏感凭据使用由 `master.key` 派生的 AES-256-GCM 算法加密。
- **恢复**：恢复到新机器或新容器时，只需在服务启动前将 `hosts.db` 和 `master.key` 放回指定的 `state_dir` 并设置 `0600`/`0700` 权限。
- **主密钥遗失**：若 `master.key` 遗失或损坏，`hosts.db` 将无法解密已保存的密码与私钥。主机元数据和已 Pinning 的公钥指纹依然保留，但需要重新录入各主机的登录凭据。

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
hub/cmd/visual-tmux-client/  主入口，CLI 参数解析与退出码处理
hub/internal/app/            应用组装根（composition root）、信号捕获与两阶段优雅关闭
hub/internal/config/         强类型配置结构、严格 YAML 解码、多源加载器与来源追踪
hub/internal/auth/           Bearer Token 校验、抗时序攻击恒定时间比较、一次性票据池
hub/internal/host/           远程 SSH 主机注册中心、连接池、敏感凭据加密、SFTP 桥接与远程 tmux 适配器
hub/internal/session/        会话业务模型、名称规范校验、业务领域服务与 Backend 接口
hub/internal/tmux/           Tmux 命令执行、PTY 进程管理、Socket 隔离与环境敏感信息清理
hub/internal/terminal/       终端输出背压缓冲、环形暂存队列、连接 Pump 调度协调
hub/internal/transport/http/ REST API 路由、DTO 映射、鉴权中间件与嵌入式 SPA 静态托管
hub/internal/transport/ws/   WebSocket 握手、Origin 校验矩阵、二进制流协议与 Peer 适配器
hub/web/                     Vue 3 浏览器前端（Vite + TypeScript + xterm.js）
configs/                     示例配置文件模板
```

浏览器启动时首先访问 `GET /api/client-config` 获取公开运行参数再挂载界面。终端连接前通过 REST API 用长期 Token 兑换 30 秒单次有效的临时票据，确保敏感凭据绝不泄露在 WebSocket URL 或进程参数中。

### 版本回滚方案

Visual Tmux Client 保持严格的向前与向后兼容性：
- 无 YAML 配置文件启动时，系统自动应用完整内置默认值，与旧版本行为一致。
- 历史版本的所有命令行参数与环境变量保持 100% 兼容。
- 若需要回滚至早期版本，只需停止服务并直接替换二进制文件。已有的本地 tmux 会话在服务启停和版本切换期间保持完整运行。
- 主机注册中心数据库（`hosts.db`）表结构保持向后兼容；回滚二进制文件仍可正常读取已有主机。如需彻底恢复，将预先备份的 `hosts.db` 和 `master.key` 拷回即可。

## 参与贡献

欢迎提交 Bug 报告和 Pull Request。开发流程请参阅 [CONTRIBUTING.md](CONTRIBUTING.md)，安全漏洞请按照 [SECURITY.md](SECURITY.md) 中的方式报告。

## 许可证

MIT © 2026 AK12-Official 及贡献者。详见 [LICENSE](LICENSE)。
