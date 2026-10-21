# 两次 OpenSpec 迭代代码审查汇总

审查日期：2026-10-17 至 2026-10-18。范围：`deb831d` 之后的 `multi-host-support`、`unify-host-management`，包括当前工作区未提交实现和指纹确认版本同步修复。报告针对代码快照，不代表这些问题已在用户主机上发生。

## 结论

正常使用流程已得到此前自动化及用户试用的支持，但独立 CR 发现了跨主机误操作、草稿丢失、连接版本混用和远程文件权限等边界缺陷。建议暂不把当前版本作为已完成质量验收的发布版本，优先处理下列 P1。自动化全绿与这些发现并不矛盾：已有测试没有覆盖相应异步调度、失败路径或真实服务器差异。

本次按用户要求安排三位独立子 Agent，分别审阅后端主机生命周期、远程文件/终端、前端工作区；主 Agent 负责规格修复、复核证据和汇总。审查中未访问用户远程主机，也未改动正在运行的服务。除遗留规格对齐及报告外，本轮没有修复业务代码。

合计 **14 项：7 项 P1、7 项 P2**。三位独立审查者报告 13 项，主 Agent 补充 1 项回滚文档问题；同根因不重复计数。审查曾因额度中断，前端和文件报告已落盘，后端审查恢复后完成报告。

## 遗留规格问题已处理

修改 [旧变更的 runtime-configuration delta](../openspec/changes/2026-09-16-file-manager-cr-hardening/specs/runtime-configuration/spec.md)：

- 将文件根目录约束明确限定到本地主机，与当前主规格一致。
- 补回 `Remote host with local roots configured` 场景，避免 MODIFIED 整块替换时删除后续引入的远程隔离约束。
- 没有为通过校验而删除主规格要求，也没有擅自归档旧变更。

2026-10-18 重新运行 `openspec validate --all --strict`：**8 passed, 0 failed**（7 份主规格、1 个活动变更）。归档目录不在这个命令的活动变更计数中；不能把它说成重新逐一验证了所有历史归档。日志：`/tmp/vtc-spec-reconciled-final.log`。

## 优先修复的问题

P1 表示可能误操作其他目标、损失数据、泄露内容或破坏关键可用性，应优先修复；P2 表示特定条件下的可靠性、性能或恢复问题。实验复现是 mock/隔离夹具验证，不是生产环境事故。

| 编号 | 级别 | 问题与影响 | 主要位置 | 证据 |
| --- | --- | --- | --- | --- |
| F1 | P1 | A 批量终止会话期间切到 B，后续 DELETE 使用 B，可能终止未经确认的同名会话 | [App.vue](../hub/web/src/App.vue#L779) | 前端审查者提取现有函数，用可控 Promise 复现 |
| F2 | P1 | A 的迟到文件读取被标记成 B 的文件；后续保存会面向 B | [FileManagerOverlay.vue](../hub/web/src/components/files/FileManagerOverlay.vue#L541)、[hostId 赋值](../hub/web/src/components/files/FileManagerOverlay.vue#L571) | 独立实验；主 Agent 重跑确认 |
| F3 | P1 | 删除等待 A 保存结束时切到 B，实际删除 B 同路径文件 | [FileManagerOverlay.vue](../hub/web/src/components/files/FileManagerOverlay.vue#L1196) | 独立实验；主 Agent 重跑确认 |
| F4 | P1 | 删除最后一台主机进入空状态，编辑器被卸载，组件内未保存草稿丢失 | [App.vue](../hub/web/src/App.vue#L102)、[空状态分支](../hub/web/src/App.vue#L889) | 静态控制流/组件生命周期核对，未做浏览器复现 |
| HB-1 | P1 | SSH 主机仅改名后，控制连接仍用记录 revision 查池，误撤销连接身份并关闭已有连接 | [connection.go](../hub/internal/host/connection.go#L332)、[invalidateControl](../hub/internal/host/connection.go#L401) | 后端审查者用临时 Go overlay 与隔离 SSH fixture 复现旧 lease 失效 |
| RF-1 | P1 | 已建立 SSH 后的同步 SFTP 请求没有有效取消，挂起时长期占用连接和路径锁 | [remote_files.go](../hub/internal/host/remote_files.go#L316)、[sftp_adapter.go](../hub/internal/host/sftp_adapter.go#L94) | 静态调用链；已有取消测试仅覆盖部分目录读取路径 |
| RF-2 | P1 | 私有文件正文先写入默认权限的远程暂存文件，上传后才恢复权限，可能被其他远端账号读取 | [remote_files.go](../hub/internal/host/remote_files.go#L555)、[后置 Chmod](../hub/internal/host/remote_files.go#L649) | 实现与已安装 SFTP 依赖行为核对，未做多账号集成复现 |
| RF-3 | P2 | 保存恢复上传前的旧权限，并忽略 Chmod/Chown 失败，可能撤销期间发生的权限收紧 | [remote_files.go](../hub/internal/host/remote_files.go#L649) | 静态证据；应增加上传期间 chmod、SETSTAT 拒绝测试 |
| RF-4 | P2 | 目录符号链接别名按不同词法路径加锁，两个 Hub 写请求可绕过互斥并互相覆盖 | [remote_files.go](../hub/internal/host/remote_files.go#L444) | 静态证据；应增加 alias/real 并发提交夹具 |
| RF-5 | P2 | POSIX 类型掩码判断错误，把普通文件识别为链接，逐条增加串行远程 STAT | [sftp_adapter.go](../hub/internal/host/sftp_adapter.go#L422) | 纯 Go 解析实验；主 Agent 重跑确认 |
| HB-2 | P2 | 指纹探测 TCP 建连后未落实握手 deadline/cancel，静默服务器使探测超时失效 | [trust.go](../hub/internal/host/trust.go#L160) | 后端审查者复现 20ms 预算在 150ms 后仍未返回，仅关闭 peer 才返回 |
| HB-3 | P2 | 删除本地记录后 API 可将 `local` ID 注册为 SSH，但连接租约仍按 ID 特判，导致空 manager 路径及请求 panic | [connection.go](../hub/internal/host/connection.go#L187) | 静态完整调用链；浏览器默认表单通常不触发 |
| HB-4 | P2 | 删除主机发生在 ticket 已兑换、终端创建尚未返回时，晚注册的本地附件可能逃过关闭 | [handler.go](../hub/internal/transport/ws/handler.go#L185)、[manager.go](../hub/internal/terminal/manager.go#L61) | 静态并发窗口，尚未做定向复现 |
| D1 | P2 | README 承诺仅换旧二进制即可回滚，与数据库 schema 2 被前一版拒绝的行为冲突 | [README.md](../README.md#L334)、[README.zh-CN.md](../README.zh-CN.md#L332)、[schemaVersion](../hub/internal/host/store.go#L46) | 主 Agent 比对当前 store 与 `93718a1:hub/internal/host/store.go`；前一版 schemaVersion=1 且拒绝更高版本 |

各独立报告包含更完整的触发步骤、相关路径、测试建议和审阅边界：

- [前端独立 CR](cr-web-2026-10-17.md)
- [远程文件与 SSH 会话独立 CR](cr-remote-files-2026-10-17.md)
- [后端主机生命周期独立 CR](cr-host-backend-2026-10-17.md)

D1 的修复应明确“降级旧 schema 不受支持，须停止服务并恢复升级前配套数据库/密钥备份”，并说明备份之后的改动会丢失；不能简单删除 schema 安全检查来兑现文档承诺。

## 代码质量与测试评价

有价值的基础包括：host/runtime 分层、独立连接身份设计、凭据加密与指纹挑战绑定、专用传输、内容摘要冲突检查，以及本地 tmux 隔离夹具。主要问题是这些约束未贯穿所有调用路径：刷新列表已校验目标，但文件读取和 mutation 完成回调仍读取可变化的当前主机；连接身份已分离，部分池访问仍使用旧 revision；配置有操作预算，底层同步 I/O 未全部绑定取消。

此前全量 Go 测试 716 个测试及子测试通过，相关 race 检查 443 项通过，前端 18 个测试文件通过。这些是前次核验结果，本轮没有重新宣称执行全套测试。race detector 检测内存数据竞争，不会自动发现这里的跨请求逻辑竞态。

此前 lint 输出 **637 项**（errcheck 412、funlen 1、goconst 9、gocyclo 17、gofmt 4、lll 135、mnd 59）。这是前次运行记录，不是本轮重新统计，也未做全部条目的提交归因。它说明 CI lint 尚有欠账；其中权限保留错误被忽略已具备具体行为后果，不能全部视为格式问题。

测试证据仍有两处明显缺口：标为 “OpenSSH” 的集成测试实际采用 Go SSH/SFTP 夹具；前端 DOM 测试替换了网络、终端和编辑器依赖。应保留这些快速测试，同时补充真实 OpenSSH 兼容测试和跨主机可控异步调度测试。

## 建议实施顺序

1. 先修跨主机误操作 F1–F3 与草稿丢失 F4；所有读写和清理都冻结 host/connection revision，草稿生命周期独立于空状态视图。
2. 修 HB-1 的连接身份混用，以及 RF-2 的暂存文件私有权限；补入能在旧代码失败的回归测试。
3. 修 SFTP 取消、探测预算、权限保留、规范路径锁和类型解析；明确“提交结果未知”的处理边界。
4. 更新回滚文档、清理 lint，最后重跑前后端、竞态和真实远端验收。

本报告提出修复建议，不把审查发现自动当作已授权批量修复。当前已完成的修复仅为本轮用户指定的遗留规格校验问题。
