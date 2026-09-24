# EchoCore

EchoCore 是一个以 Go 为核心的可扩展、多平台 AI Agent 框架。当前仓库处于 Phase 1：已经完成基础 HTTP 服务，并开始接入 NapCat 与 OneBot 11。

## 当前范围

- Go 工程骨架
- 结构化 JSON 日志
- 可配置的 HTTP 监听地址
- `GET /health` 健康检查
- 优雅停机
- OneBot 11 反向 WebSocket 接入点：`/onebot/v11/ws`
- 可选的 OneBot Access Token 校验
- 原始 OneBot JSON 事件的结构化终端日志
- OneBot 11 协议数据结构：`Event`、`MessageSegment`、`Action`、`ActionResponse`
- OneBot Event 到平台无关 `IncomingMessage` 的转换
- 仅支持 `/ping` 与 `/help` 的平台无关 Dispatcher
- 通过 `send_private_msg` / `send_group_msg` 发送 QQ 回复

OneBot Action 的 `echo` 响应关联将在 Phase 1 的后续步骤中增加。当前不包含 LLM、Agent、Memory、RAG 或管理后台。

## 环境要求

- Go 1.24 或更高版本

## 启动

```bash
go run ./cmd/server
```

默认监听 `127.0.0.1:8080`。通过环境变量可以覆盖默认值：

```bash
ECHOCORE_SERVER_HOST=0.0.0.0 ECHOCORE_SERVER_PORT=9090 go run ./cmd/server
```

`config.example.yaml` 记录 Phase 1 的目标配置结构；初始化步骤的运行时配置使用上述环境变量。真实 `config.yaml` 已被 Git 忽略。

## NapCat 反向 WebSocket

1. 启动 EchoCore：

   ```bash
   go run ./cmd/server
   ```

2. 打开 NapCat WebUI，在 OneBot 11 的网络配置中新增并启用 **WebSocket 客户端（反向 WebSocket）**。
3. 将 URL 设置为：

   ```text
   ws://127.0.0.1:8080/onebot/v11/ws
   ```

4. 保存配置。EchoCore 控制台出现 `OneBot WebSocket connected` 即表示 Step 3 连接成功。

### 查看原始 OneBot 事件

保持 EchoCore 和 NapCat 运行，然后使用另一个 QQ 账号依次执行：

1. 向机器人 QQ 发送私聊文本 `step4-private`。
2. 在机器人所在群发送普通文本 `step4-group`。
3. 在群内发送 `@机器人 step4-at`。

EchoCore 终端会为每个事件输出一行结构化 JSON 日志，其中 `event` 字段是 NapCat 发来的完整 OneBot JSON。例如：

```json
{"level":"INFO","msg":"OneBot event received","component":"onebot.websocket","event":{"post_type":"message","message_type":"private","message":[{"type":"text","data":{"text":"step4-private"}}]}}
```

群聊事件的 `message_type` 为 `group`；@ 消息的 `message` 数组中会同时出现 `at` 和 `text` segment。上述 `step4-*` 文本不是已注册命令，因此只会记录事件，不会触发回复。

本地开发默认不校验 Token。如需启用，EchoCore 和 NapCat 必须配置相同值：

```bash
ECHOCORE_ONEBOT_ACCESS_TOKEN=your-token go run ./cmd/server
```

WebSocket 路径也可通过 `ECHOCORE_ONEBOT_PATH` 修改。NapCat 中的 URL 必须同步修改。

## OneBot 数据结构

Step 5 在 `internal/onebot` 中定义协议层数据结构：

- `Event`：OneBot 事件公共字段，以及 Phase 1 所需的私聊、群聊和元事件字段。
- `MessageSegment`：数组格式消息段，当前声明 `text` 和 `at` 类型常量，同时保留扩展参数。
- `Action`：发送给 OneBot 的动作名称、参数和原始 `echo`。
- `ActionResponse`：动作状态、返回码、原始响应数据和原始 `echo`。

这些类型只描述 OneBot 协议。

## OneBot Adapter

Step 6 由 `internal/adapter/onebot.Adapter` 将 OneBot 消息事件转换为 `internal/message.IncomingMessage`：

- 将 OneBot 数字 ID 转换为平台无关模型使用的字符串 ID。
- 区分私聊和群聊，并设置 `GroupID` 与 `IsGroup`。
- 按顺序拼接 `text` segment，忽略图片等未知 segment。
- 仅当 `at` segment 指向机器人自身 QQ号时设置 `Mentioned`。

Adapter 只负责模型转换；消息分发和命令处理由下一节的 Dispatcher 负责，自身消息过滤将在后续步骤实现。

## Dispatcher

Step 7 提供平台无关的 Dispatcher，并注册两个命令 Handler：

- `/ping`：返回 `pong`。
- `/help`：返回当前命令帮助。

私聊命令会回复原用户；群聊只有在 `Mentioned=true` 时才会回复原群。未知命令、带额外参数的命令和未 @ 机器人的群消息会被忽略。

## OneBot Action Sender

Step 8 将 Dispatcher 生成的 `OutgoingMessage` 转换为 OneBot Action，并通过 NapCat 建立的同一条反向 WebSocket 连接发送：

- 私聊回复使用 `send_private_msg`，目标参数为原消息的 `user_id`。
- 群聊回复使用 `send_group_msg`，目标参数为原消息的 `group_id`。
- 回复文本使用 OneBot 数组格式的 `text` message segment。
- `message_sent` 和发送者为机器人自身的事件会被忽略，避免处理自己的输出。

启动 EchoCore 和 NapCat 后，可使用另一个 QQ 账号进行手动验证：

1. 私聊机器人发送 `/ping`，应收到 `pong`。
2. 私聊机器人发送 `/help`，应收到命令帮助。
3. 在机器人所在群发送 `@机器人 /ping`，应在原群收到 `pong`。
4. 在群里只发送 `/ping` 而不 @ 机器人，不应收到回复。

EchoCore 终端出现 `OneBot reply sent` 表示 Action 已写入 WebSocket。当前步骤只负责发送；NapCat 返回的 Action 响应将在下一步通过 `echo` 进行关联。

## 健康检查

服务启动后执行：

```bash
curl http://127.0.0.1:8080/health
```

期望返回：

```json
{"status":"ok"}
```

## 验证

```bash
go test ./...
go vet ./...
```
