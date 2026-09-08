# Visual Tmux Client

简体中文 | [English](README.md)

**Visual Tmux Client** 是一个轻量、自托管的 tmux 网页客户端，可直接在浏览器中管理和使用 tmux 会话。Vue 前端被嵌入单个 Go 二进制文件中，部署时只需要该二进制文件和本机的 `tmux`。

> 项目目前处于早期 `v0.1` 阶段，只管理 Visual Tmux Client 所在机器上的 tmux，暂不支持聚合多台远程主机。

## 功能

- 查看、创建、重命名和终止本机 tmux 会话。
- 使用基于 xterm.js 的完整浏览器终端连接会话。
- 浏览器断开后保留 tmux 会话及其状态。
- 使用 Bearer Token 认证和短时、一次性的 WebSocket 票据。
- 将整个网页界面打包进单个应用程序二进制文件。
- 服务停止时正常关闭连接，但不终止已有 tmux 会话。

## 运行要求

- macOS 或 Linux
- tmux 3.3 或更高版本（已在 3.7b 上验证）
- 现代浏览器

从源码构建还需要 Go 1.26.3+ 和 Node.js 24+。

## 快速开始

从仓库的 Releases 页面下载对应平台的压缩包，然后运行：

```sh
tar -xzf visual-tmux-client_0.1.0_darwin_arm64.tar.gz
cd visual-tmux-client_0.1.0_darwin_arm64
./visual-tmux-client
```

服务默认监听 `http://127.0.0.1:7690`。如果没有配置 Token，程序启动时会在标准错误输出中打印一个随机生成的 Token。用浏览器打开上述地址，并在登录界面中输入该 Token。

如需使用固定 Token：

```sh
VISUAL_TMUX_CLIENT_TOKEN="$(openssl rand -base64 32)" ./visual-tmux-client
```

运行 `./visual-tmux-client --help` 可查看全部参数和环境变量。

## 配置

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `--addr` | `127.0.0.1:7690` | HTTP 监听地址 |
| `VISUAL_TMUX_CLIENT_TOKEN` | 启动时随机生成 | 浏览器界面使用的共享 Bearer Token |
| `VISUAL_TMUX_CLIENT_ORIGIN` | `http://<监听地址>` | 允许发起 WebSocket 连接的准确浏览器来源 |
| `VISUAL_TMUX_CLIENT_TMUX_PATH` | 从 `PATH` 中查找 | 指定 tmux 可执行文件路径 |

默认只监听回环地址是有意的安全设计。如需通过其他机器访问，请在服务前部署 HTTPS 反向代理、使用高强度固定 Token，并将 `VISUAL_TMUX_CLIENT_ORIGIN` 设置为准确的公网 HTTPS 来源。暴露到网络前请先阅读 [SECURITY.md](SECURITY.md)。

## 从源码构建

```sh
make build
./hub/visual-tmux-client
```

`make build` 会安装锁定版本的前端依赖、构建 Vue 应用，并将其嵌入 Go 二进制文件。其他常用命令：

```sh
make test       # 前端类型/构建检查、Go 测试和 go vet
make package    # 在 release/ 中打包当前系统与架构的产物
make clean      # 清理生成的构建产物
```

如需一次生成多个平台的压缩包：

```sh
TARGETS="darwin/arm64 darwin/amd64 linux/arm64 linux/amd64" make package
```

推送 `v*` 标签后，GitHub Actions 会自动构建以上四个平台、生成校验和并发布 GitHub Release。

## 架构

Go 服务提供一组带认证的会话管理 JSON API，以及由伪终端驱动的 WebSocket 端点。浏览器会先用 Bearer Token 换取一个有效期 30 秒、仅可使用一次的票据；长期 Token 不会出现在 WebSocket URL 中。编译后的 Vue 资源由 Go 的嵌入式文件系统直接提供。

项目结构：

```text
hub/              Go 服务、tmux 集成和测试
hub/web/          Vue + TypeScript 前端
openspec/specs/   行为与设计规格
scripts/          发布打包脚本
```

## 参与贡献

欢迎提交 Bug 报告和 Pull Request。开发流程请参阅 [CONTRIBUTING.md](CONTRIBUTING.md)，安全漏洞请按照 [SECURITY.md](SECURITY.md) 中的方式报告。

## 许可证

MIT © 2026 AK12-Official 及贡献者。详见 [LICENSE](LICENSE)。
