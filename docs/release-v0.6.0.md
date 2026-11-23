# Visual Tmux Client v0.6.0

## 更新内容

- 新增实验性桌面入口，面向 Ubuntu 24.04 amd64 和 macOS arm64、amd64。应用在原生窗口中打开工作区并自动登录，可继续管理本地和 SSH 主机上的 tmux 会话。
- 桌面应用支持原生文件下载、系统浏览器打开外链，并在重启后恢复界面偏好。关闭窗口时可保留未保存的编辑并取消退出。
- 正式发布提供 Linux 和 macOS 的 amd64、arm64 headless 包与 SHA-256 校验和。桌面 DEB 和 macOS 应用包通过 Desktop compatibility 工作流提供为开发构建。macOS 开发包采用 ad-hoc 签名。[桌面构建与运行指南](desktop.md)

## 升级

备份现有可执行文件和私有状态目录，替换对应平台的二进制并重启应用。保留现有配置、启动参数与凭据。在同一配置下切换桌面与 headless 模式前，先退出当前进程以释放主机状态锁。

## 已知问题

macOS 桌面应用长时间运行后可能逐渐失去响应。遇到该问题时，可退出桌面应用并使用原有服务器命令。[桌面运行与恢复说明](desktop.md#closing-and-recovery) · [问题记录](TODO.md#桌面应用)

## 发布步骤

版本更新通过 PR 合入主线并通过 CI 后，在对应主线提交上创建并推送 `v0.6.0` 标签。标签工作流重新运行验证、构建并核对发布包，再发布 GitHub Release。[仓库发布流程](../CONTRIBUTING.md#release-process)
