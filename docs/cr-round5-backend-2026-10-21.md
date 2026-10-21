# 第五轮后端 CR：会话请求连接版本

审查对象：HEAD `93718a1051055553480d40b17b1c8203ef13e35f` 加第五轮工作区修复，2026-10-21。范围为第四轮 R4-1 的会话创建/改名身份约束、非法版本处理，以及共用校验器对相邻文件接口的影响。

**本轮后端范围内无新增 P1/P2 findings。R4-1 的后端请求入口及非法版本放行路径可以关闭。** 这不是整个产品的发布结论；前端迟到响应如何更新状态由前端 CR 独立判断。

## 关闭依据

| 项目 | 当前状态与证据 |
| --- | --- |
| 会话创建缺少连接版本 | 已接入 body/header/query 共用校验，过期版本返回 409，副作用前结束。前端 API 已接受并发送捕获的 connectionRevision。 |
| 会话改名缺少连接版本 | 已接入相同校验。重放上轮“当前版本 3、请求版本 1”的 PATCH，现为 409；原会话仍在，未改名。 |
| 非法 query 覆盖有效旧 header 后仍删除 | 已修复。重放 `expected_connection_revision=invalid` 加 header 版本 1，现为 400，会话未删除。 |
| 多来源版本不一致 | body、query、header 不一致返回 400；snake_case 与 camelCase body 别名互相冲突也拒绝。 |
| metadata-only 改名误伤 | 比较目标为 ConnectionRevision，不是记录 Revision；测试使用记录版本 5、连接版本 3，携带版本 3 可以成功。 |
| 文件接口采用同一校验器的相邻回归 | 文件写入和元数据修改的版本冲突既有测试通过，未发现因共用函数接入而漏掉校验。 |

主要实现位置：[router.go:148](../hub/internal/transport/http/router.go#L148) 的共用校验器及 create/rename/kill 调用点，[dto.go:90](../hub/internal/transport/http/dto.go#L90) 的可选版本字段，[files.go:275](../hub/internal/transport/http/files.go#L275) 的写入校验。

共用校验器没有在执行途中重新解析目标，仍使用本请求捕获的 runtime；旧版本被拒绝而不会主动切换到另一个连接版本。版本字段仍可省略以兼容旧调用者，本轮不把这一既有兼容策略误报为缺陷；当前浏览器创建和改名路径已经发送版本。没有在本轮有限检查中确认新的后端身份重定向问题。

## 验证

重跑六个现有测试函数：

```sh
go test ./internal/transport/http -run 'TestSession(Create|Rename|Kill)ExpectedConnectionRevisionConflict|TestExpectedConnectionRevisionInputValidation|TestFiles(WriteExpectedConnectionRevisionConflict|MutateExpectedConnectionRevisionConflict)' -count=1 -v
```

全部通过。覆盖会话创建/改名/删除的匹配与过期版本，输入解析失败、多来源冲突，以及文件接口冲突。

另外通过临时 overlay 运行独立验证：

```sh
go test -overlay=/tmp/vtc-round5-cr/overlay.json ./internal/transport/http -run '^TestRound5' -count=1 -v
```

- 重放上轮 PATCH 反例，断言 409 且原会话未改名。
- 重放上轮非法 query + 旧 header 的 DELETE，断言 400 且会话未删除。
- 五组 body 输入：相同别名成功；冲突别名、字符串版本、负数版本拒绝；旧 snake_case 版本返回 409。拒绝路径同时断言没有创建会话。

临时测试名称有部分沿用历史诊断名，但断言已经反转为符合预期的安全行为。**本轮 PASS 均表示验证预期行为通过，没有以“成功观察到缺陷”为断言的测试。**

## 范围限制

本轮没有修改业务或仓库测试代码，只新增本报告；临时文件位于 `/tmp/vtc-round5-cr/`。测试使用实际 HTTP router 与内存 mock service，不启动真实 tmux、不访问用户服务或 SSH。

未重复验证真实终端、迁移、全库 race/lint 或跨浏览器场景。异步请求成功后若主机连接版本已改变，前端回调是否仍会修改新世代的选中会话、面板身份和活动状态，是另外一个边界；已告知主审交由前端 CR 覆盖，不能因后端入口通过就一并宣布关闭。
