# EchoCore

EchoCore 是一个以 Go 为核心的可扩展、多平台 AI Agent 框架。当前仓库处于 Phase 1 的项目初始化阶段：先建立可稳定启停的 HTTP 服务和健康检查，再接入 NapCat 与 OneBot 11。

## 当前范围

- Go 工程骨架
- 结构化 JSON 日志
- 可配置的 HTTP 监听地址
- `GET /health` 健康检查
- 优雅停机

OneBot、消息模型、Dispatcher 和命令处理器将在 Phase 1 的后续步骤中增加。当前不包含 LLM、Agent、Memory、RAG 或管理后台。

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
