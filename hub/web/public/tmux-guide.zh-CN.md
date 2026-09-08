# tmux 使用指南

> 本指南随 Visual Tmux Client 内置，供在应用内随时查阅。
> tmux 部分基于 tmux 官方 Wiki 的 Getting Started 文档整理：
> <https://github.com/tmux/tmux/wiki/Getting-Started>

---

## 一、使用本程序（Visual Tmux Client）

本程序是一个浏览器端的 tmux 客户端：左侧是会话列表，右侧是真正的终端——你附加(attach)到的就是真实的 tmux 会话，**所有 tmux 快捷键照常可用**，分屏、状态栏都由 tmux 自己绘制。

### 连接

启动程序后，终端会打印访问 token 和打开地址（形如 `http://127.0.0.1:7690`）。浏览器打开该地址，输入 token 即可连接。token 会保存在浏览器本地，刷新页面无需重新输入。

### 会话管理（左侧列表）

| 操作 | 做法 |
|------|------|
| 新建会话 | `+ New session` 一键创建，自动分配默认名（`session-日期-时间`） |
| 改名 | 卡片上的 ✎；支持中文等非 ASCII 字符，但不能包含 `:` 和 `.` |
| 杀死会话 | 卡片上的 ✕（会确认） |
| 批量管理 | `Select` 进入多选模式，勾选后可批量杀死 |
| 排序 | `Default / Manual` 切换；手动模式下可拖拽排序、★ 置顶（浏览器本地保存） |

### 终端面板（右侧）

点击左侧会话即附加。顶部栏从左到右：会话名、连接状态、活动指示点，右侧 `A−` / `A+` 调整字号、`⛶` 全屏、`✕` 关闭面板。

### 分离、关闭、杀死的区别

| 操作 | 会话是否继续运行 |
|------|------------------|
| `C-b d` 分离（detach） | 继续。面板提示 Detached，可点 Reconnect 重新附加 |
| `✕` 关闭面板 | 继续。只是收起视图；该会话有输出时，列表卡片会亮绿框提醒 |
| `✕` 杀死（kill） | **终止**。窗口和窗格全部销毁 |

### 活动提示

某个后台会话产生输出时，它的列表卡片亮绿色边框；当前正在看的会话有输出时，顶栏出现呼吸点，浏览器标签页标题前出现 `●`。输出停止约 2 秒后提示自动消退。

---

## 二、tmux 是什么

tmux 是一个**终端复用器(terminal multiplexer)**,它运行在终端里,可以在其中同时运行多个终端程序。它的核心价值:

- **防断线**:在远程服务器上用 tmux 跑程序,即使 SSH 连接中断,程序仍在后台继续运行。
- **多端接入**:可以在公司电脑上开启会话,回家后用家里的电脑重新连上,继续之前的工作。
- **多窗口管理**:在一个终端里管理多个程序和 shell,类似一个"窗口管理器"。

---

## 三、核心概念

tmux 有一套必须先理解的术语层级:**Server → Session(会话) → Window(窗口) → Pane(窗格)**。

| 术语 | 说明 |
|------|------|
| **Server(服务器)** | tmux 的主进程,所有状态都保存在这里,后台运行,通过 `/tmp` 下的 socket 与客户端通信 |
| **Client(客户端)** | 用户附加(attached)到服务器时启动的进程,占用一个外部终端 |
| **Session(会话)** | 一组窗口的集合,有唯一名字,可被一个或多个客户端附加,也可完全分离 |
| **Window(窗口)** | 一组窗格的集合,占满终端,每个窗口在会话中有索引号 |
| **Pane(窗格)** | 窗口里的一个矩形区域,包含一个终端和运行中的程序 |
| **Active pane(活动窗格)** | 当前窗口里接收你输入的那个窗格,边框为绿色 |
| **Current window(当前窗口)** | 当前会话里显示并接收输入的窗口 |
| **Prefix key(前缀键)** | 控制 tmux 本身命令的特殊键,默认为 `C-b`(即 Ctrl+b) |

> 记忆链路:程序运行在**窗格**里 → 窗格属于**窗口** → 窗口属于**会话** → 会话被**客户端**附加。

---

## 四、状态栏(Status Line)

附加 tmux 后,屏幕底部会出现一条绿色状态栏,显示:

- **左侧**:当前会话名,如 `[0]`
- **中间**:窗口列表(索引:名字),当前窗口标 `*`,上一个窗口标 `-`
- **右侧**:窗格标题(默认是主机名)+ 时间日期

窗口太多放不下时,边缘会出现 `<` 或 `>` 表示有隐藏窗口。

---

## 五、会话管理

### 创建会话

```bash
$ tmux new                      # 创建默认会话(名为 0)并附加
$ tmux new -s mysession         # 创建名为 mysession 的会话
$ tmux new 'emacs ~/.tmux.conf' # 创建会话并运行指定命令
$ tmux new -n mytopwindow top   # 用 -n 给窗口命名并运行 top
```

### 分离与附加(Detach / Attach)

- **分离**:`C-b d` —— 退出客户端,会话与程序继续在后台运行
- **附加最近一个会话**:`tmux attach` 或 `tmux a`
- **附加指定会话**:`tmux attach -t mysession`
- **附加时踢掉其他客户端**:`tmux attach -d -t mysession`
- **不存在就创建,存在就附加**(最常用):`tmux new -As mysession`

### 列出与杀死会话

```bash
$ tmux ls                                  # 列出所有会话
$ tmux kill-server                         # 彻底杀掉 tmux 服务器
```

在命令提示符里也可输入:`:kill-server`

---

## 六、前缀键与帮助

- **默认前缀键**:`C-b`(Ctrl+b)
- 按键记法:`C-` = Ctrl,`M-` = Alt,`S-` = Shift
- **`C-b c`** 表示先按 `C-b`,松开后再按 `c`(注意要松开 Ctrl)
- 按 **两次 `C-b`** 会把 `C-b` 原样发给当前窗格里的程序

### 帮助

| 按键 | 作用 |
|------|------|
| `C-b ?` | 列出所有快捷键及说明(可用 ↑↓ 滚动,`q` 退出) |
| `C-b /` | 查询某个键的说明 |
| `C-b :` | 打开命令提示符,可直接输入 tmux 命令(多命令用 `;` 分隔) |

---

## 七、窗口操作

| 按键 / 命令 | 作用 |
|------|------|
| `C-b c` | 创建新窗口 |
| `C-b 0` ~ `C-b 9` | 切换到窗口 0~9 |
| `C-b '` | 输入索引号切换窗口 |
| `C-b n` / `C-b p` | 下一个 / 上一个窗口 |
| `C-b l` | 切到上一个访问过的窗口(last) |
| `C-b &` | 关闭当前窗口(会确认) |
| `C-b ,` | 重命名当前窗口 |
| `C-b .` | 修改当前窗口的索引号 |
| `C-b w` | 树状浏览所有会话/窗口/窗格(tree 模式) |
| `C-b s` | 列出并选择会话 |

命令提示符示例:

```
:neww -dn mynewwindow     # 后台创建名为 mynewwindow 的窗口
:neww top                 # 创建运行 top 的新窗口
:movew -r                 # 重新编号窗口,填补索引空隙
```

---

## 八、窗格(Pane)操作

### 分割

| 按键 | 作用 |
|------|------|
| `C-b %` | 左右分割(横向切分) |
| `C-b "` | 上下分割(纵向切分) |

### 切换与查看

| 按键 | 作用 |
|------|------|
| `C-b ↑↓←→` | 切换到上/下/左/右窗格(会环绕) |
| `C-b q` | 短暂显示各窗格编号,再按数字即可跳转 |
| `C-b o` | 按编号顺序切到下一个窗格 |
| `C-b C-o` | 把当前窗格与下一个窗格互换位置 |

### 调整与缩放

| 按键 | 作用 |
|------|------|
| `C-b C-↑↓←→` | 小步调整窗格大小 |
| `C-b M-↑↓←→` | 大步调整窗格大小 |
| `C-b z` | 当前窗格全屏缩放/还原(状态栏标 `Z`) |
| `C-b Space` | 在几种预设布局间轮换 |
| `C-b {` / `C-b }` | 与上/下窗格交换位置 |

### 预设布局(也可直接选)

| 按键 | 布局 |
|------|------|
| `C-b M-1` | even-horizontal(横向均匀) |
| `C-b M-2` | even-vertical(纵向均匀) |
| `C-b M-3` | main-horizontal(上方一个大窗格) |
| `C-b M-4` | main-vertical(左侧一个大窗格) |
| `C-b M-5` | tiled(平铺) |

### 关闭

| 按键 | 作用 |
|------|------|
| `C-b x` | 关闭当前窗格(会确认) |

---

## 九、复制与粘贴

tmux 有自己的剪贴板系统,称为 **paste buffer**。

### 进入复制模式(Copy Mode)

- `C-b [` 进入复制模式(冻结输出,可滚动选择文本)
- `C-b ]` 粘贴最近一次复制的内容
- `C-b =` 进入 buffer 模式,列出所有缓冲并预览,可选择性粘贴/删除

复制模式默认用 **emacs 风格**按键;若环境变量 `VISUAL`/`EDITOR` 含 `vi`,则用 **vi 风格**。常用 emacs 键:

| 键 | 作用 |
|------|------|
| `C-Space` | 开始选择 |
| `C-w` | 复制选择并退出 |
| `C-g` | 取消选择 |
| `C-a` / `C-e` | 行首 / 行尾 |
| `C-r` | 向后增量搜索 |
| `q` | 退出复制模式 |

buffer 管理命令(在 `:` 提示符中):

```
:setb -bfoo bar              # 创建名为 foo、内容为 bar 的缓冲
:saveb -bbuffer0 ~/saved     # 把缓冲保存到文件
:loadb -bbuffername ~/a/file # 从文件加载缓冲
```

> 复制的内容最多保留 50 个自动缓冲(超过则删除最旧的);命名缓冲不会自动删除。可配置把复制内容同步到系统剪贴板。

---

## 十、鼠标支持

开启鼠标(在 `:` 提示符中):

```
:set -g mouse on
```

开启后:

- 左键点击窗格 → 切换活动窗格
- 左键点击状态栏窗口名 → 切换窗口
- 在窗格边框上左键拖动 → 调整大小
- 在窗格内左键拖动 → 选中文本(松开即复制)
- 右键点击窗格 → 弹出菜单

---

## 十一、配置文件 `~/.tmux.conf`

tmux 服务器**启动时**会读取 `~/.tmux.conf`(注意:只在服务器启动时执行,不是每次建会话)。运行中重新载入:

```
:source ~/.tmux.conf
```

`#` 开头为注释,每行一条命令。常见配置示例:

### 1. 修改前缀键为 `C-a`

```
set -g prefix C-a
unbind C-b
bind C-a send-prefix
```

### 2. 开启鼠标 / vi 模式

```
set -g mouse on
set -g mode-keys vi
set -g status-keys vi
```

### 3. 自定义状态栏

```
set -g status-position top
set -g status-style bg=blue
set -g status-right '%H:%M'
set -g window-status-current-style underscore
```

### 4. 窗格边框

```
set -g pane-border-style fg=red
set -g pane-active-border-style 'fg=red,bg=yellow'
set -g pane-border-status top
set -g pane-border-format '#[bold]#{pane_title}#[default]'
```

---

## 十二、选项(Options)体系

tmux 选项分几类:

- **server 选项**(`-s`):影响整个服务器
- **session 选项**(`-g` 全局会话):影响会话
- **window 选项**(`-wg`):影响窗口
- **pane 选项**:影响窗格

常用操作:

```bash
$ tmux show -s              # 查看服务器选项
$ tmux show -g              # 查看全局会话选项
$ tmux show -wg             # 查看全局窗口选项
$ tmux show -g status       # 查看单个选项
```

设置(在配置文件或 `:` 提示符):

```
set -g status off              # 关闭状态栏
set -s default-terminal 'tmux-256color'
set -gu status                 # 取消设置,恢复默认
```

### 常用选项速查

| 选项 | 类型 | 说明 |
|------|------|------|
| `prefix` | session | 前缀键,默认 `C-b` |
| `base-index` | session | 窗口起始索引(默认 0,常设为 1) |
| `history-limit` | session | 每个窗格保留的历史行数 |
| `mouse` | session | 是否启用鼠标 |
| `mode-keys` | window | 复制模式用 emacs/vi 键 |
| `status` | session | 是否显示状态栏 |
| `status-position` | session | 状态栏位置 top/bottom |
| `renumber-windows` | session | 关闭窗口时自动重新编号 |
| `escape-time` | server | Escape 键等待时间(常调小,如 0) |
| `default-terminal` | server | 内部 `TERM` 值 |
| `synchronize-panes` | window | 输入同时发到所有窗格(慎用) |
| `set-clipboard` | server | 是否同步到系统剪贴板 |

---

## 十三、样式与格式(进阶)

### 颜色样式(style)

颜色可用 `black red green yellow blue magenta cyan white`、`brightred` 等、`colour0`~`colour255`,或十六进制 `#882244`。

```
set -g status-style 'bg=blue'
set -g status-left 'default #[fg=red] red #[fg=blue] blue'
set -g status-left-length 100
```

`#[]` 是内嵌样式,`#{}` 是格式(format,如 `#{session_name}`),`#()` 是内嵌 shell 命令(如 `#(uptime)`)。

---

## 十四、速查表(最常用快捷键)

| 操作 | 按键 |
|------|------|
| 创建会话 | `tmux new -s 名字` |
| 附加会话 | `tmux a -t 名字` |
| 分离 | `C-b d` |
| 列出会话 | `C-b s` 或 `tmux ls` |
| 新建窗口 | `C-b c` |
| 切换窗口 | `C-b 数字` / `C-b n` / `C-b p` |
| 左右分屏 | `C-b %` |
| 上下分屏 | `C-b "` |
| 切换窗格 | `C-b 方向键` |
| 全屏窗格 | `C-b z` |
| 关闭窗格 | `C-b x` |
| 复制模式 | `C-b [` |
| 粘贴 | `C-b ]` |
| 命令提示符 | `C-b :` |
| 帮助 | `C-b ?` |

---

## 参考来源

- [tmux Wiki - Getting Started](https://github.com/tmux/tmux/wiki/Getting-Started)
- [tmux 手册页 man.openbsd.org/tmux](https://man.openbsd.org/tmux)
