# EchoCore

EchoCore 是一个以 Go 为核心的可扩展、多平台 AI Agent 框架。已实现 QQ 双向消息、群聊 AI 参与、短期上下文与人设，正在建设 Phase 4 管理基础。

## 当前范围

- Go 工程骨架
- 开发环境彩色 Console 日志、生产环境 JSON 日志
- 可配置的 HTTP 监听地址
- `GET /health` 健康检查
- 优雅停机
- OneBot 11 反向 WebSocket 接入点：`/onebot/v11/ws`
- 可选的 OneBot Access Token 校验
- DEBUG 级别的原始 OneBot JSON 事件日志
- OneBot 11 协议数据结构：`Event`、`MessageSegment`、`Action`、`ActionResponse`
- OneBot Event 到平台无关 `IncomingMessage` 的转换
- 支持 `/ping`、`/help` 与带参数 `/ai on/off` 的平台无关 Dispatcher
- 白名单用户按群启停 AI 参与开关，群状态仅在内存中保存
- 通过 `send_private_msg` / `send_group_msg` 发送 QQ 回复
- 通过 `echo` 一一关联 Action 请求与 `ActionResponse`

已包含 Router Agent、Executor Agent、LLM 接口、离线 FakeLLM、OpenAI 兼容 API 与 Ollama Provider，以及内存短期上下文。Phase 4 Step 4 新增 SQLite 业务配置与每轮配置快照；管理登录、管理 HTTP API、网页后台、长期记忆和 RAG 尚未实现。

## 环境要求

- Go 1.24 或更高版本
- SQLite 驱动使用 CGO：需要 `CGO_ENABLED=1` 和本机 C 编译工具链（macOS 可用 Xcode Command Line Tools）。

## 启动

```bash
go run ./cmd/server
```

默认监听 `127.0.0.1:8080`。通过环境变量可以覆盖默认值：

```bash
ECHOCORE_SERVER_HOST=0.0.0.0 ECHOCORE_SERVER_PORT=9090 go run ./cmd/server
```

默认按开发环境运行，输出包含 DEBUG 的彩色 Console 日志。生产环境使用 JSON 日志：

```bash
ECHOCORE_ENV=production go run ./cmd/server
```

`config.example.yaml` 仅说明配置结构，不自动加载。启动参数来自环境变量；业务配置首次导入 SQLite 后以数据库为准。真实 `config.yaml` 和运行数据库已被 Git 忽略。

### 统一配置服务（Phase 4 Step 4）

默认配置数据库为工作目录下的 `data/echocore.db`，可用 `ECHOCORE_SETTINGS_DB` 指定路径。首次启动从环境导入模型、人设、白名单和执行限额；以后启动读取已保存版本，修改旧业务环境变量不会覆盖数据库。监听地址、OneBot Token 仍在启动时从环境读取。群开关与短期聊天仍仅在内存，重启不会恢复开启状态或聊天历史。

模型密钥仅保存 `environment` 引用，每次进程启动从 `ECHOCORE_LLM_API_KEY` 解析，数据库不保存密钥明文。引用密钥缺失时服务可启动，但日志中的 `model_available` 为 false，AI 不调用模型。该状态表示本地配置可否构造客户端，不代表已经向真实模型验证连接成功。

开发接口 `settings.Service.Update(ctx, revision, values)` 校验完整配置、构造客户端并事务保存，再发布新版本；版本冲突返回 `ErrConflict`，保存失败保留旧版本。每轮任务在 Prepare 时绑定模型、人设、超时和输出限额；旧任务使用原版本，新任务使用新版本。并发上限仅允许无在途任务时更新，发送等待也计入占用。权限更新立即影响下一条 QQ 控制命令。

当前尚未开放管理 HTTP 写入口或配置页面，因此不能通过浏览器修改这些设置。下面的环境示例用于首次导入；开发时如需另建一套环境配置，可显式选择一个尚不存在的数据库路径，例如 `ECHOCORE_SETTINGS_DB=data/trial.db`，原数据库保持不变。生产运行限定单实例管理同一数据库，不直接编辑运行中数据库。

备份与恢复首版采用停机复制：先停止 EchoCore，复制完整配置数据库并保持文件权限；恢复时停机替换到指定路径，再提供原环境密钥启动。数据库迁移在事务中执行；程序拒绝打开高于自身支持版本的数据库。

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

开发环境会在 DEBUG 日志中记录 NapCat 发来的 OneBot 事件结构，正文已脱敏，例如：

```text
level=DEBUG msg="OneBot raw frame received" component=onebot.websocket event=...
```

群聊事件的 `message_type` 为 `group`；@ 消息的 `message` 数组中会同时出现 `at` 和 `text` segment。默认未启用模型时，上述 `step4-*` 文本只会记录事件，不会触发回复；已启用模型且开启群后，普通群文本可能触发 AI。生产环境的最低日志级别为 INFO，不会输出完整事件和心跳。

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

Adapter 只负责模型转换；消息分发和命令处理由 Dispatcher 负责，自身消息过滤由 WebSocket 消息处理层负责。

## Dispatcher

Step 7 提供平台无关的 Dispatcher，并注册两个命令 Handler：

- `/ping`：返回 `pong`。
- `/help`：返回当前命令帮助。

私聊命令会回复原用户；群聊确定性命令只有在 `Mentioned=true` 时才会回复原群。未知命令、`/ping` 或 `/help` 的额外参数和未 @ 的群命令会被忽略。Phase 2 增加了带参数的 `/ai` Handler，并为开启群的普通消息建立独立 AI 路径。

## 群 AI 参与开关

Phase 2 Step 2 实现 `/ai on` 与 `/ai off`，仅控制当前群的开关。Step 3 加入 Router，Step 5 加入 Executor 与可切换 Provider：配置好模型后，开启群的普通消息可经判断获得 AI 回复。默认 `disabled` 时，开关仍有效，但不调用模型。

使用逗号分隔的 QQ 号配置授权用户，然后重新启动 Go 服务（将示例 QQ 号替换为实际操作者的 QQ 号）：

```bash
ECHOCORE_GROUP_CONTROL_USER_IDS="20001,20002" go run ./cmd/server
```

白名单通过消息发送者 QQ 号校验，与 QQ 群管理员身份无关。未设置或留空时，任何人都不能修改开关；非法 QQ 号或空列表项会导致启动报配置错误。当前运行时仍只读取环境变量。

- 群内发送 `@机器人 /ai on`：开启当前群，回复“本群 AI 参与开关已开启。”
- 群内发送 `@机器人 /ai off`：关闭当前群，回复“本群 AI 参与开关已关闭。”
- 重复操作会提示“已经开启”或“已经关闭”，不会产生额外状态变化。
- 非白名单用户操作会收到权限提示；私聊使用 `/ai` 会收到群聊用法提示。
- 每个群独立，默认关闭；NapCat 重连不会清空状态，Go 进程重启后所有群恢复关闭。

手动验收：白名单账号在群 A 开启，再次开启应提示“已经开启”；在群 B 首次关闭应提示“已经关闭”。用非白名单账号在群 A 关闭，应被拒绝；白名单账号再次开启群 A，应仍提示“已经开启”。保持群 A 开启后重启 Go，再在群 A 关闭，应提示“已经关闭”。最后测试 `/ping`、`/help`，确认原有回复可用。所有群命令都需要真实 @ 机器人。

## Router Agent 与 AI 候选

Phase 2 Step 3 在 OneBot Adapter 和 Dispatcher 之间加入 `internal/agent.Router`，返回 `ignore`、`command` 或 `ai_candidate`。路由本身不调用模型、不改变群状态；命令由原 Dispatcher 执行。

- 开启群：任何成员的非空普通文本都可进入候选，不要求 @；启用模型时经 Decide 判断是否值得回复。直接 @ 的问题优先生成，不调用 Decide。
- 关闭群：普通文本与直接 @ 都不会进入 AI 候选；`@机器人 /ai on`、`/ai off`、`/ping`、`/help` 仍可处理。
- 群命令继续要求真实 @；未知 `/` 命令、空文本、纯图片等无文本消息及自身消息不会进入候选。已知命令参数错误也不会回落到 AI。
- 私聊普通文本直接进入 AI 候选，并直接生成回答，不经过群聊参与判断；普通斜杠命令仅处理已注册命令，`/ask` 已取消并按未知命令忽略。
- 候选和命令在 INFO 输出 `Router message routed`，仅带路由、原因与标识字段；忽略原因在 DEBUG 输出。完整事件继续只在 DEBUG 输出。

仅验收候选路由（使用默认 `ECHOCORE_LLM_PROVIDER=disabled`，先重启 Go）：

1. 白名单用户在群 A 发送 `@机器人 /ai on`。
2. 任意成员在群 A 发普通文本，终端应出现 `route=ai_candidate`、`reason=group_enabled`，QQ 不会收到 AI 回复。
3. 在未开启的群 B 发相同文本，不应出现该消息的 `ai_candidate` 路由日志。
4. 在群 A 发送 `@机器人 /ai off`，再发普通文本或直接 @，均不应出现候选日志。原始收消息 INFO 日志仍会出现，这不表示触发了 AI。
5. 群聊 `@机器人 /ping` 和 `/help` 仍正常回复；私聊直接发送 `你好`，应出现 `reason=private_message` 的候选日志并收到回答。

模型启用后的验收见下面的 Step 5；冷却与更精细的频率控制仍留待 Step 6。

## LLM 接口与离线 FakeLLM

Phase 2 Step 4 新增 `internal/llm`，不依赖具体服务商 SDK，也不依赖 OneBot 协议：

- `Decider.Decide(ctx, DecideRequest)`：返回 `reply` 或 `ignore` 及简短原因标签。
- `Generator.Generate(ctx, GenerateRequest)`：接收当前文本、可选历史、系统提示和输出 token 上限，返回文本与可选用量。
- `Client`：组合上述两个接口；调用方只有在判断成功且明确为 `reply` 时才继续生成。
- `FakeLLM`：显式配置固定判断与固定文本，默认忽略；支持分别注入判断/生成错误与延迟，响应 context 取消及超时，并记录并发安全的调用次数。

FakeLLM 不理解语义、不模拟 token 截断、不保存聊天历史、不读取密钥、不请求网络。空文本、超长回复等可作为测试数据；真正的输出校验由 Executor 负责。历史的群隔离与裁剪由调用方负责，当前 Executor 不收集或传递历史。

离线验收（已安装 Go 且项目依赖已准备好时，无需外网）：

```bash
GOPROXY=off GOSUMDB=off go test -count=1 -v ./internal/llm ./internal/agent
```

测试覆盖“回复 / 忽略”、固定文本、默认忽略、错误透传、取消、超时、并发调用及结果隔离；Router 组合测试验证关闭群和确定性命令不会调用模型，忽略结果不会继续生成。

**服务启动路径未装配 FakeLLM**，生产环境也无法通过配置启用它。FakeLLM 仅在自动测试中显式注入；启用真实模型请使用以下配置。

## Executor 与可切换 Provider

支持 `disabled`、`openai-compatible`、`ollama`，当前每轮判断与生成共用一个 Provider。以下环境变量用于首次导入数据库；后续配置更新经统一配置服务生效，不自动回退或同时调用两家服务。

### 云端兼容 API

```bash
export ECHOCORE_LLM_PROVIDER=openai-compatible
export ECHOCORE_LLM_BASE_URL=https://api.openai.com/v1
export ECHOCORE_LLM_MODEL='填写服务商支持的模型名'
export ECHOCORE_GROUP_CONTROL_USER_IDS='填写你的QQ号'
# 在本地安全地设置 ECHOCORE_LLM_API_KEY，不要把真实密钥提交到 Git 或发到聊天。
go run ./cmd/server
```

兼容 Provider 使用非流式 Chat Completions，Base URL 应是 API 根地址，程序追加 `/chat/completions`。默认输出限制字段是 `max_completion_tokens`；第三方只支持旧字段时设置 `ECHOCORE_LLM_TOKEN_FIELD=max_tokens`。兼容服务的参数支持各异，不保证所有模型可直接使用。接口实现参照 [OpenAI Chat Completions 官方文档](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)。不使用 Responses API，也不绑定具体模型或 SDK。

### 本地 Ollama

先自行启动 Ollama 并准备模型，然后在启动 EchoCore 的终端配置：

```bash
export ECHOCORE_LLM_PROVIDER=ollama
export ECHOCORE_LLM_BASE_URL=http://127.0.0.1:11434
export ECHOCORE_LLM_MODEL='填写本机已安装的模型名'
export ECHOCORE_GROUP_CONTROL_USER_IDS='填写你的QQ号'
unset ECHOCORE_LLM_API_KEY
go run ./cmd/server
```

Ollama Provider 使用原生非流式 `/api/chat`，不要在 Base URL 中重复写 `/api/chat`。接口实现参照 [Ollama 官方文档](https://docs.ollama.com/api/chat)。本机服务通常不需要密钥；若使用受保护的远端部署，可显式配置 `ECHOCORE_LLM_API_KEY`。远端 URL 要求 HTTPS，仅回环地址允许 HTTP；禁止重定向，防止消息或密钥被发送到意外地址。

### 运行边界与保护

- 未配置 Provider 时默认关闭，不发送模型请求；`/ping`、`/help`、群开关保持可用。
- 普通群消息先 Decide，只有严格返回 `reply` 才 Generate；无效判断保守忽略。私聊普通消息与开启群中的真实 @ 直接 Generate。
- 判断与生成在独立协程运行，共用 `ECHOCORE_LLM_TIMEOUT`（默认 `30s`，最大 `2m`）；生成完成后才开始 OneBot 的 5 秒 Action 等待。
- 服务级 `ECHOCORE_LLM_MAX_CONCURRENT` 默认 4；按平台、机器人账号、会话类型和目标 ID 隔离 AI 任务。同群（不论发送者）或同一私聊只允许一个在途任务，占用持续到回复发送成功、失败、超时或取消；不同会话可在全局模型并发上限内独立运行。发送等待不占模型执行槽，不无限排队；忙碌时普通群消息静默跳过，显式提问收到繁忙提示。`/ping`、`/help`、`/ai on/off` 不受 AI 会话占用限制。
- 关闭群会取消该次开启周期的任务，发送器在写入前复核周期。关闭再开启不会恢复旧回复；已写入 NapCat 的消息无法撤回。连接断开或服务退出也会取消模型任务。
- `ECHOCORE_LLM_MAX_INPUT_CHARS` 默认 4000，超长输入不调用模型；`ECHOCORE_LLM_MAX_OUTPUT_TOKENS` 默认 512；`ECHOCORE_LLM_MAX_REPLY_CHARS` 默认 2000，超过字符上限的回复会截断。
- 短期上下文只在内存保存：每会话最多 100 条，模型最多接收最近 20 条历史且总输入不超过 8000 个 Unicode 字符；闲置 30 分钟清除，最多 500 个会话／5000 条记录。重启清空；群关闭立即清空，再开启从空历史开始。忙碌消息不排队、不入历史。
- 只有 OneBot 明确确认发送成功的 AI 回复才进入后续历史。私聊按用户隔离，群聊由同群成员共享；模型不会收到 QQ 号。DEBUG 事件日志会隐藏 `raw_message` 和文本 segment 正文。
- 人设首次导入可通过 `ECHOCORE_PERSONA_NAME`、`ECHOCORE_PERSONA_STYLE`、`ECHOCORE_PERSONA_GROUP_REPLY_LENGTH` 设置，默认分别为 `EchoCore`、`友善、诚实、自然、简洁`、`简短`；每项不能为空且最多 500 个字符，之后通过统一配置服务更新，新任务使用新值。
- 空回复、超时和服务错误不会把内部错误发到 QQ；显式提问得到通用失败提示，普通群消息保持沉默。INFO 不记录对话正文、密钥或服务端错误正文。
- 普通群候选可能各消耗一次判断请求，肯定判断再消耗一次生成请求；先在小测试群验证费用和体验，不要直接在高流量群启用。

### 手动验收真实模型

1. 配置一种实际可用的 Provider 并重启 Go，确认日志显示所选 Provider，NapCat 已连接。
2. 私聊直接发送 `用一句话介绍你自己`，应得到模型回答。
3. 白名单用户在群 A 发送 `@机器人 /ai on`，再 `@机器人 用一句话解释什么是反向 WebSocket`，应在原群收到回答。
4. 不 @ 发送普通问题，应由模型决定是否回复；“哈哈”等无明确问题的文本应倾向忽略，但语义判断不是确定性规则。
5. 群 B 未开启，即使 @ 也不触发 AI；两群 `/ping`、`/help` 的原规则保持不变。
6. 群 A 发起较慢的生成时发送 `/ai off`（需要 @），不应补发未发送的旧答案；再开启也不应恢复旧任务或历史。

自动测试用本地 HTTP 模拟服务覆盖两种真实 Provider 的请求格式、判断/生成、错误与取消，并通过 WebSocket 验证回复链路；这不等同于付费云模型或本机 Ollama 的真实端到端验收。当前环境未提供模型服务与凭证，仍需完成以上手动步骤。

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

EchoCore 终端出现 `OneBot reply sent` 表示 Action 已执行成功并收到 NapCat 响应。

## Echo 请求响应关联

Step 9 为每个 OneBot Action 生成当前 WebSocket 连接内唯一的 `echo`，并在发送前登记等待请求。NapCat 返回 `ActionResponse` 后，EchoCore 根据 `echo` 将响应交给对应请求：

- 支持多个并发 Action 以及乱序返回的响应。
- 响应状态或返回码表示失败时，发送方会收到明确错误。
- 请求超过 5 秒未收到响应时自动超时并清理等待记录。
- WebSocket 断开时取消当前连接上的等待请求。
- 未知、缺失或已经超时的 `echo` 不会被错误关联。

手动发送 `/ping` 或 `/help` 后，终端应依次出现：

```text
OneBot Action response matched
OneBot reply sent
```

两条日志中的 `echo` 应相同，表示本次请求与响应已成功对应。

## 触发规则与日志环境

Step 10 完善消息触发规则和工程日志基础：

- 私聊中的已注册命令可以直接触发。
- 群聊必须包含指向机器人自身 QQ 的 `at` 消息段，否则不触发。
- `message_sent` 事件以及 `user_id` 等于 `self_id` 的消息会被忽略，避免机器人处理自己的输出。
- INFO 只记录消息类型、用户、群、消息 ID、是否 @、文本字符数、Action 状态等关键字段；对话正文仅在 DEBUG 事件日志中出现。
- 心跳和完整 OneBot 帧只写入 DEBUG。

日志模式由 `ECHOCORE_ENV` 控制：

| 环境 | 日志格式 | 最低级别 | 用途 |
| --- | --- | --- | --- |
| `development`（默认） | 彩色 Console | DEBUG | 本地开发和协议排查 |
| `production` | JSON | INFO | 生产采集和日志检索 |

开发环境启动：

```bash
go run ./cmd/server
```

生产环境启动：

```bash
ECHOCORE_ENV=production go run ./cmd/server
```

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
