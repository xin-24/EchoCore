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

已包含 Router Agent、Executor Agent、LLM 接口、离线 FakeLLM、兼容聊天 API 与 Ollama Provider、可选 Jev 决策，以及内存短期上下文。启动配置统一由 YAML 提供，每轮任务绑定只读快照；管理登录、管理 HTTP API、网页后台、长期记忆和 RAG 尚未实现。

## 环境要求

- Go 1.24 或更高版本
- 旧 SQLite 存储兼容测试使用 CGO：运行全量测试需要 `CGO_ENABLED=1` 和本机 C 编译工具链。正常服务启动不再打开配置数据库。

## 启动

首次使用，把 `config.example.yaml` 复制为本地 `config.yaml`（已有文件不要覆盖），填写模型地址、模型名称和白名单后运行：

```bash
go run ./cmd/server
```

默认读取当前工作目录下的 `config.yaml`，监听 `127.0.0.1:8080`。也可以显式指定路径：

```bash
go run ./cmd/server -config /absolute/path/config.yaml
```

### 配置来源与修改方式

- **代码默认值 → YAML 覆盖**：模型、Jev、参与限额、白名单、人设、日志模式、监听地址均在文件设置。修改后重启生效，不支持热更新。
- **环境变量只提供密钥**：`ECHOCORE_LLM_API_KEY`、`ECHOCORE_JEV_API_KEY`、`ECHOCORE_ONEBOT_ACCESS_TOKEN`。不自动加载 `.env`，请通过启动终端或进程管理器注入。
- **数据库不管理启动配置**：正常启动不读写或创建 `data/echocore.db`。旧数据库保留原样，不会覆盖 YAML；存储层暂保留历史兼容代码和测试。未来数据库只承担需要持久化的运行数据，本次不新增聊天持久化。
- **旧非密钥环境变量不再生效**，包括 `ECHOCORE_LLM_PROVIDER`、`ECHOCORE_GROUP_CONTROL_USER_IDS`、`ECHOCORE_JEV_ENABLED`、`ECHOCORE_SETTINGS_DB` 等。升级时把原来的非密钥设置手动填入 YAML，不必删除数据库或换新库。
- `config.example.yaml` 是可提交的模板；实际 `config.yaml`、`.env`、数据库和 `docs/` 均已被 Git 忽略。配置文件不能包含 API Key、Token 等密钥字段。

文件中省略的字段使用默认值；非法值、未知字段、重复字段、空值 `null`、YAML 锚点和多文档会拒绝启动。文件缺失时明确报错，不静默回到旧数据库。只想使用全部默认值时可以写 `{}`。默认模型和 Jev 均关闭。

启动日志 `business settings loaded` 的 `source=yaml` 表示使用文件快照。模型密钥缺失时 `model_available=false`；Jev 开启但缺密钥时 `jev_available=false`，回退原模型判断。可用状态只表示本地客户端能够构造，不证明真实 API 已验证成功。

快照保留每轮任务的模型、人设、白名单和执行限额一致性，但不再把它当作可编辑的数据库配置。文件模式调用内部 `settings.Service.Update` 会返回只读错误；请修改文件并重启。群开关、短期上下文和参与计数仍在内存，重启清空。

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

WebSocket 路径在 `config.yaml` 的 `onebot.path` 修改。NapCat 中的 URL 必须同步修改。

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

在 `config.yaml` 配置授权用户，再重启 Go 服务（替换为实际操作者的 QQ 号，而不是机器人账号）：

```yaml
group:
  control_user_ids: ["20001", "20002"]
```

白名单通过消息发送者 QQ 号校验，与 QQ 群管理员身份无关。省略或设为 `[]` 时任何人都不能修改开关；非法 QQ 号会导致启动报错，重复账号自动去重。

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

仅验收候选路由（使用默认 `llm.provider: disabled`，先重启 Go）：

1. 白名单用户在群 A 发送 `@机器人 /ai on`。
2. 任意成员在群 A 发普通文本，终端应出现 `route=ai_candidate`、`reason=group_enabled`，QQ 不会收到 AI 回复。
3. 在未开启的群 B 发相同文本，不应出现该消息的 `ai_candidate` 路由日志。
4. 在群 A 发送 `@机器人 /ai off`，再发普通文本或直接 @，均不应出现候选日志。原始收消息 INFO 日志仍会出现，这不表示触发了 AI。
5. 群聊 `@机器人 /ping` 和 `/help` 仍正常回复；私聊直接发送 `你好`，应出现 `reason=private_message` 的候选日志；模型关闭时不会生成 AI 回答。

模型启用后的验收见下面的 Step 5；冷却与更精细的频率控制仍留待 Step 6。

## LLM 接口与离线 FakeLLM

Phase 2 Step 4 新增 `internal/llm`，不依赖具体服务商 SDK，也不依赖 OneBot 协议：

- `Decider.Decide(ctx, DecideRequest)`：返回 `reply` 或 `ignore` 及简短原因标签。
- `Generator.Generate(ctx, GenerateRequest)`：接收当前文本、可选历史、系统提示和输出 token 上限，返回文本与可选用量。
- `Client`：组合上述两个接口；调用方只有在判断成功且明确为 `reply` 时才继续生成。
- `FakeLLM`：显式配置固定判断与固定文本，默认忽略；支持分别注入判断/生成错误与延迟，响应 context 取消及超时，并记录并发安全的调用次数。

FakeLLM 不理解语义、不模拟 token 截断、不自行维护聊天历史、不读取密钥、不请求网络。空文本、超长回复等可作为测试数据；真正的输出校验由 Executor 负责。Executor 通过 contextstore 收集并传递短期历史，会话隔离与裁剪不由 FakeLLM 负责。

离线验收（已安装 Go 且项目依赖已准备好时，无需外网）：

```bash
GOPROXY=off GOSUMDB=off go test -count=1 -v ./internal/llm ./internal/agent
```

测试覆盖“回复 / 忽略”、固定文本、默认忽略、错误透传、取消、超时、并发调用及结果隔离；Router 组合测试验证关闭群和确定性命令不会调用模型，忽略结果不会继续生成。

**服务启动路径未装配 FakeLLM**，生产环境也无法通过配置启用它。FakeLLM 仅在自动测试中显式注入；启用真实模型请使用以下配置。

## Executor 与可切换 Provider

### 可选 Jev 分层决策

普通群消息现在支持“规则过滤 → 参与限额 → Jev → 不确定时原 LLM 判断 → 生成回复”。私聊和开启群中的真实 @ 绕过参与判断；群关闭、命令权限、会话互斥和总超时规则保持不变。短消息不会因长度短就一律过滤。

Jev 默认关闭，使用独立 Decisions API，不是聊天 Provider。实现依据 [OpenRouter Decisions API](https://openrouter.ai/docs/api/api-reference/alphadecisions/submit-a-decisions-request)。有效 `reply`／`skip` 且 `answers.action.confidence >= min_confidence` 才直接采用；低置信度、`unclear`、非法响应、服务失败或子超时回退一次。若群关闭、断线或整轮已取消，不再回退。兜底判断失败时沉默。

在 `config.yaml` 修改对应部分，不要重复添加同名顶层字段：

```yaml
jev:
  enabled: true
  min_confidence: 0.60
  timeout_ms: 3000

participation:
  cooldown_ms: 5000
  window_ms: 60000
  max_replies: 3
  repeat_window_ms: 1000
```

省略的 Jev 地址、路径和模型使用代码默认值，也可在文件中显式覆盖；完整字段见 `config.example.yaml`。只想验证 Jev 调用时可先保留参与限额为 0，避免限额提前拦截消息。

另外在启动终端安全设置 `ECHOCORE_JEV_API_KEY`（与 Jev 接口平台匹配的密钥，不是 DeepSeek 密钥），保留原生成模型的 `ECHOCORE_LLM_API_KEY`，再 `go run ./cmd/server`。密钥不要写进配置文件、聊天或 Git。无需新建数据库，旧数据库不影响这次配置。

**密钥与接口必须来自同一平台。** 代码默认是 OpenRouter，其地址 `/api/alpha/decisions`、模型 `~typesafe/jev-latest` 要用 OpenRouter 密钥。若使用 TypeSafe 后台创建的密钥，请在现有 `jev` 段同时修改下面三项，其余阈值和超时可保留：

```yaml
jev:
  enabled: true
  base_url: "https://api.typesafe.ai/v1"
  path: "/systemone"
  model: "jev-latest"
  min_confidence: 0.60
  timeout_ms: 3000
```

TypeSafe 直连同样从 `ECHOCORE_JEV_API_KEY` 读取密钥，本项目不会自动读取官方 SDK 示例中的 `TYPESAFE_API_KEY`。依据 [TypeSafe 官方 API 文档](https://docs.typesafe.ai/api)，当前 Choice 请求和响应结构可复用。两种平台均使用本地模拟测试；实际账号权限和可用性仍需实测。不要把 TypeSafe 密钥发给 OpenRouter，或反过来；认证失败时先检查平台匹配，再检查密钥是否有效。

如需让 Jev 单独走本机代理，在 `jev` 段添加 `proxy_url: "socks5h://127.0.0.1:7897"`，保存后重启 Go 服务。SOCKS5 请求由代理解析目标域名，HTTPS 仍校验证书；代理必须已启动且支持 SOCKS5。显式代理优先于环境代理，连接失败不会改为直连，但仍可回退 Router LLM。留空时保留 Go 默认的环境代理行为。支持带明确端口的 HTTP、HTTPS、SOCKS5、SOCKS5H 地址，不允许在地址中保存账号密码。此设置不影响生成模型、Router LLM 和 OneBot，也不保证消除服务商返回的 451。

Jev 开启但缺少环境密钥时，会显示 `jev_available=false` 并回退原判断。`jev_available=true` 仅表示可构造客户端，不证明账号权限或真实网络可用。模型本身未启用时不会仅靠 Jev 开始聊天。使用白名单账号在群内真实 @ 机器人发送 `/ai on`，再发不带 @ 的普通消息；私聊和明确 @ 都绕过 Jev，不能用于验证它是否被调用。

参与限额按会话／群开启周期隔离。冷却从最近成功 AI 发送回执起算；窗口限制只计普通群消息产生的成功自动回复。重复过滤仅针对同用户连续接纳的相同文字，使用短时间窗，不把所有短文本判为刷屏。限额不影响命令；明确 @ 和私聊仍受模型资源限制。状态只保存在有界内存窗口中，关闭群／重启清理。忙碌消息仍不入历史，决策器不会看到完整群聊。

成功回执中的有效消息 ID 会关联助手历史，让后续引用可标记为 `bot`；引用不在窗口或回执没有有效 ID 时为 `unknown`，不凭昵称猜测。Jev 与兜底都接收同一份有限历史及决策上下文；不主动附加 QQ 号／群号等内部标识，但正文和昵称仍可能包含个人信息，启用表示允许把这些有限内容发给额外服务。

INFO 的 `AI participation decided` 记录 `source`、`decision`、`reason`、`fallback_reason`、可用的 `confidence`、耗时与快照版本；`source=jev` 表示采用 Jev 判断，`source=router_llm` 时检查回退原因。用量缺失时省略，绝不伪装成零。回退时已知的 Jev 用量单独记录。日志不记录 Jev 密钥或原始响应正文。每轮绑定同一只读快照，编辑文件不会改变运行中的任务，重启后生效。

自动测试使用本地 Jev HTTP 模拟、FakeLLM、临时 SQLite 和 WebSocket，未调用真实付费服务。上线前需在小群验证：群友互聊少插话、对机器人追问能接话、@ 正常回答、关闭立即停止，以及真实 Jev 可用性、费用和回退比例。

### 不 @ 时不回复：查看判断诊断

`reason=model_ignore` 表示兜底模型正常选择不参与；`reason=fallback_error` 表示判断调用或响应处理失败，程序保守沉默。两者不能混为一谈。Jev 调用失败不等于生成模型失败；`jev_available=true` 也仅代表客户端已构造。

失败或格式异常的 `AI participation decided` 现在输出 WARN，并分别保留 `jev_error_kind`／`jev_http_status` 与 `router_error_kind`／`router_http_status`。生成失败的 `LLM request failed` 带 `stage=generate`、`error_kind` 和可用的 `http_status`。

| 诊断 | 排查方向 |
| --- | --- |
| `authentication` / 401 | 对应服务的密钥是否有效，是否把 DeepSeek 与 OpenRouter 密钥混用 |
| `quota` / 402、`permission` / 403 | 账户额度、接口权限或服务商访问策略 |
| `invalid_request` / 400、`not_found` / 404 | 请求参数、接口路径或模型配置 |
| `rate_limit` / 429、`upstream` / 5xx | 限流或上游故障；程序不会无限重试 |
| `dns`、`tls`、`connection`、`network`、`timeout` | 网络、证书或时间预算 |
| `output_limit` + `finish_reason=length` | 判断被输出上限截断，不应当作有效 reply |
| `empty_content`、`invalid_json`、`invalid_schema`、`invalid_decision` | 空正文、无效响应结构或不符合严格标签协议 |

结束原因和数字用量按白名单记录；`reasoning_present=true` 只表示响应带有思考内容，不记录内容本身。没有提供的用量不填零。错误正文、URL、密钥、聊天正文和思考正文均不会加入这些诊断字段。

当前对官方 `api.deepseek.com` 的 `deepseek-flash`／`deepseek-v4-pro`，短标签 Decide 请求使用 `thinking.type=disabled` 和 `max_tokens=128`，避免默认思考与很小判断预算的兼容性风险；Generate 保持原行为，其他服务商和未验证模型不发送这个专属参数。参数依据 [DeepSeek 官方文档](https://api-docs.deepseek.com/api/create-chat-completion/)。这项兼容处理不等于已证明此前线上故障由思考预算引起，仍以实际诊断为准。

升级后停止旧 Go 进程，在保留环境密钥的终端重新运行；群状态重启会清空，须重新真实 @ 发送 `/ai on`。随后发送不带 @ 的消息并查看上述 WARN。Jev 具体线上错误只有真实调用后的新诊断才能确认。

### 回复生成 Provider

支持 `disabled`、`openai-compatible`、`ollama`，当前兜底判断与生成共用一个 Provider；可选 Jev 是独立的优先判断服务。以下片段写入 `config.yaml` 对应部分，生成阶段不自动切换到其他服务商。

### 云端兼容 API

```yaml
llm:
  provider: openai-compatible
  base_url: "https://api.openai.com/v1" # 改为实际服务商接口根地址
  model: "填写服务商支持的模型名"
  token_field: max_completion_tokens # 原服务商要求 max_tokens 时修改
group:
  control_user_ids: ["填写你的QQ号"]
```

密钥通过 `ECHOCORE_LLM_API_KEY` 提供，再运行 `go run ./cmd/server`。兼容 Provider 使用非流式 Chat Completions，Base URL 应是 API 根地址，程序追加 `/chat/completions`。默认输出限制字段是 `max_completion_tokens`；第三方只支持旧字段时设置 `llm.token_field: max_tokens`。兼容服务的参数支持各异，不保证所有模型可直接使用。接口实现参照 [OpenAI Chat Completions 官方文档](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)。不使用 Responses API，也不绑定具体模型或 SDK。

### 本地 Ollama

先自行启动 Ollama 并准备模型，然后在文件配置：

```yaml
llm:
  provider: ollama
  base_url: "http://127.0.0.1:11434"
  model: "填写本机已安装的模型名"
group:
  control_user_ids: ["填写你的QQ号"]
```

Ollama Provider 使用原生非流式 `/api/chat`，不要在 Base URL 中重复写 `/api/chat`。接口实现参照 [Ollama 官方文档](https://docs.ollama.com/api/chat)。本机服务通常不需要密钥；若使用受保护的远端部署，可显式配置 `ECHOCORE_LLM_API_KEY`。远端 URL 要求 HTTPS，仅回环地址允许 HTTP；禁止重定向，防止消息或密钥被发送到意外地址。

### 运行边界与保护

- 未配置 Provider 时默认关闭，不发送模型请求；`/ping`、`/help`、群开关保持可用。
- 普通群消息先 Decide，只有严格返回 `reply` 才 Generate；无效判断保守忽略。私聊普通消息与开启群中的真实 @ 直接 Generate。
- 判断与生成在独立协程运行，共用 `llm.timeout`（默认 `30s`，最大 `2m`）；生成完成后才开始 OneBot 的 5 秒 Action 等待。
- 服务级 `llm.max_concurrent` 默认 4；按平台、机器人账号、会话类型和目标 ID 隔离 AI 任务。同群（不论发送者）或同一私聊只允许一个在途任务，占用持续到回复发送成功、失败、超时或取消；不同会话可在全局模型并发上限内独立运行。发送等待不占模型执行槽，不无限排队；忙碌时普通群消息静默跳过，显式提问收到繁忙提示。`/ping`、`/help`、`/ai on/off` 不受 AI 会话占用限制。
- 关闭群会取消该次开启周期的任务，发送器在写入前复核周期。关闭再开启不会恢复旧回复；已写入 NapCat 的消息无法撤回。连接断开或服务退出也会取消模型任务。
- `llm.max_input_chars` 默认 4000，超长输入不调用模型；`llm.max_output_tokens` 默认 512；`llm.max_reply_chars` 默认 2000，超过字符上限的回复会截断。
- 短期上下文只在内存保存：每会话最多 100 条，模型最多接收最近 20 条历史且总输入不超过 8000 个 Unicode 字符；闲置 30 分钟清除，最多 500 个会话／5000 条记录。重启清空；群关闭立即清空，再开启从空历史开始。忙碌消息不排队、不入历史。
- 只有 OneBot 明确确认发送成功的 AI 回复才进入后续历史。私聊按用户隔离，群聊由同群成员共享；模型不会收到 QQ 号。DEBUG 事件日志会隐藏 `raw_message` 和文本 segment 正文。
- 人设通过 `persona.name`、`persona.style`、`persona.group_reply_length` 设置，默认分别为 `EchoCore`、`友善、诚实、自然、简洁`、`简短`；每项不能为空且最多 500 个字符，修改后重启生效。
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

日志模式由配置文件的 `environment` 控制：

| 环境 | 日志格式 | 最低级别 | 用途 |
| --- | --- | --- | --- |
| `development`（默认） | 彩色 Console | DEBUG | 本地开发和协议排查 |
| `production` | JSON | INFO | 生产采集和日志检索 |

开发环境启动：

```bash
go run ./cmd/server
```

生产环境先设置 `environment: production`，再使用同一启动命令。

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
