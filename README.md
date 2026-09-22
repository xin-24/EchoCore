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

OneBot 事件的数据结构定义、消息模型、Dispatcher 和命令处理器将在 Phase 1 的后续步骤中增加。当前不包含 LLM、Agent、Memory、RAG 或管理后台。

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

群聊事件的 `message_type` 为 `group`；@ 消息的 `message` 数组中会同时出现 `at` 和 `text` segment。当前 Step 4 只观察并记录事件，不会自动回复 QQ 消息。

本地开发默认不校验 Token。如需启用，EchoCore 和 NapCat 必须配置相同值：

```bash
ECHOCORE_ONEBOT_ACCESS_TOKEN=your-token go run ./cmd/server
```

WebSocket 路径也可通过 `ECHOCORE_ONEBOT_PATH` 修改。NapCat 中的 URL 必须同步修改。

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
