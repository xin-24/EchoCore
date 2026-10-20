# EchoCore

以 Go 为核心的 AI 聊天机器人项目，通过 NapCat / OneBot 11 接入 QQ，支持私聊对话、群聊选择性参与、图片与 GIF 理解、表情包发送、MCP 工具和长期记忆。

后端提供聊天链路与管理 API，前端是独立的 React 管理页面。当前实际接入的平台是 **QQ**；消息模型为后续扩展保留边界，不代表已支持其他平台。

## 当前能力

| 模块 | 已实现 |
| --- | --- |
| QQ 消息 | 反向 WebSocket、私聊/群聊回复、Action 与响应的 `echo` 关联 |
| 群聊参与 | 白名单启停、普通消息选择性回复、真实 @ 优先、过滤自身消息 |
| 模型接入 | OpenAI-compatible Chat Completions、Ollama 原生文本接口、FakeLLM 测试 |
| 决策 | 硬规则、参与限额、可选 Jev、Router LLM 兜底、Executor 生成 |
| 上下文与人设 | 会话隔离、历史裁剪、引用目标、YAML 人设及 Markdown 角色文件 |
| 图片理解 | JPEG/PNG、GIF 最多 8 帧时间采样、视觉描述、会话隔离缓存 |
| 图片表情包 | 本地素材、机器人 QQ 收藏同步、描述与审批、工具选择与发送 |
| MCP | 受控远程 Streamable HTTP、工具与使用范围白名单、调用预算 |
| 长期记忆 | SQLite 持久化、手动管理、关键词召回、自动提取、候选审核与删除抑制 |
| 管理后台 | 登录、表情管理、记忆管理、候选审核、自动提取暂停/恢复 |
| 工程保护 | 同会话互斥、超时取消、群关闭取消、脱敏日志及自动化回归 |

模型及大多数扩展能力默认关闭，需要自行配置和授权。当前没有向量检索/Embedding、任意本地 MCP 进程执行或通用的后台配置编辑器。

## 目录导航

- [快速启动](#快速启动)
- [配置与密钥](#配置与密钥)
- [聊天与命令](#聊天与命令)
- [React 管理后台](#react-管理后台)
- [图片和 GIF 识别](#图片和-gif-识别)
- [图片表情包与 QQ 收藏](#图片表情包与-qq-收藏)
- [MCP 工具](#mcp-工具)
- [长期记忆](#长期记忆)
- [日志与故障排查](#日志与故障排查)
- [开发与测试](#开发与测试)

## 快速启动

### 1. 环境准备

- Go 1.24 或更高版本。
- CGO 与 C 编译工具链：项目使用 `go-sqlite3`，收藏缓存、长期记忆及相关测试依赖 SQLite。
- NapCat 和可正常登录的 QQ 账号。EchoCore 不负责安装或启动 QQ。
- 管理网页需要 Node.js **22.12.0 及以上的 22.x 版本**和 npm，范围以 [web/package.json](web/package.json) 为准。
- 真实 AI 需要可用的模型服务；工具和视觉支持情况取决于所选模型及接口。

### 2. 准备配置

在项目根目录执行；已有 `config.yaml` 时不要覆盖：

```bash
cp -n config.example.yaml config.yaml
```

完整模板见 [config.example.yaml](config.example.yaml)。在已有段落中修改，不要重复添加同名 YAML 字段：

```yaml
llm:
  provider: openai-compatible
  base_url: "https://你的服务商地址/v1"
  model: "填写实际可用的模型名称"
  token_field: max_tokens # 按服务商要求，也可能是 max_completion_tokens

group:
  control_user_ids: ["123456789"] # 允许操作群开关的个人 QQ，不是机器人 QQ
```

以上是结构示例，不是可直接调用的地址。聊天兼容客户端在 Base URL 后追加 `/chat/completions`，不要填写完整接口路径。

只测试消息链路时保留 `llm.provider: disabled`；`/ping`、`/help` 和群开关仍可使用。

### 3. 加载密钥并启动后端

将密钥放入项目根目录的本地 `.env`，不要上传或截图公开：

```bash
ECHOCORE_LLM_API_KEY='替换为真实模型密钥'
ECHOCORE_ONEBOT_ACCESS_TOKEN='替换为与NapCat一致的随机Token'
```

程序**不会自动加载 `.env`**。在同一终端执行：

```bash
set -a
source ./.env
set +a
go run ./cmd/server
```

只加载可信的 `.env`，因为 `source` 会执行其中的 Shell 内容。`export` 不是永久保存：换一个终端启动时，需要重新加载，或由进程管理器注入环境变量。

默认读取当前目录的 `config.yaml`，监听 `127.0.0.1:8080`。指定其他配置：

```bash
go run ./cmd/server -config /absolute/path/config.yaml
```

健康检查：

```bash
curl http://127.0.0.1:8080/health
# {"status":"ok"}
```

### 4. 连接 NapCat

1. 启动 NapCat，并完成机器人 QQ 登录。
2. 在 NapCat WebUI 的 OneBot 11 网络配置中启用 **WebSocket 客户端 / 反向 WebSocket**。
3. 地址填写 `ws://127.0.0.1:8080/onebot/v11/ws`。
4. 若后端设置了 `ECHOCORE_ONEBOT_ACCESS_TOKEN`，NapCat 对应连接的 Token 必须一致。
5. 保存后，后端出现 `OneBot WebSocket connected` 表示连接成功。

这里的 Token **不是 NapCat WebUI 登录密钥，也不是 EchoCore 管理后台密钥**。本地未设置 OneBot Token 时可不校验，但不应将未鉴权入口开放到公网。

使用另一个 QQ 账号私聊机器人发送 `/ping`，应收到 `pong`。启用模型后直接发文字即可聊天，无需 `/ask`。

## 配置与密钥

| 位置 | 用途 |
| --- | --- |
| `config.yaml` | 模型地址/名称、开关、限额、白名单、人设、监听配置 |
| 环境变量 / 本地 `.env` | API Key、OneBot Token、管理密钥 |
| 代码 | 默认值、校验、安全边界 |
| SQLite / `data/` | 收藏缓存、长期记忆等运行数据，不重复管理启动配置 |

读取顺序为“代码默认值 → YAML 覆盖”。修改 YAML、角色文件或环境密钥后需重启 Go，不支持配置热更新。未知字段、重复字段、`null`、YAML 锚点和多文档会被拒绝；列表写成 `- "123456789"`，`-` 后必须有空格。

| 环境变量 | 用途 |
| --- | --- |
| `ECHOCORE_LLM_API_KEY` | 聊天生成与 Router LLM |
| `ECHOCORE_JEV_API_KEY` | 可选 Jev，必须匹配对应平台 |
| `ECHOCORE_VISION_API_KEY` | 图片/GIF 识别 |
| `ECHOCORE_MEMORY_API_KEY` | 自动记忆提取与核对；手动保存和关键词召回本身不需要 |
| `ECHOCORE_ONEBOT_ACCESS_TOKEN` | OneBot 连接鉴权 |
| `ECHOCORE_ADMIN_KEY` | EchoCore 管理后台，32～256 字节随机密钥 |
| MCP 的 `token_env` 指定变量 | 对应远程 MCP 服务的密钥 |

视觉、自动记忆不会隐式借用聊天 Key。如果使用同一服务商、同一密钥有对应权限，可在 `.env` 的聊天 Key 定义后显式复用：

```bash
ECHOCORE_VISION_API_KEY="$ECHOCORE_LLM_API_KEY"
ECHOCORE_MEMORY_API_KEY="$ECHOCORE_LLM_API_KEY"
```

不一定需要再购买一份 Key，但视觉、判断、自动提取和工具循环可能产生额外模型费用。

旧 `ECHOCORE_LLM_PROVIDER`、`ECHOCORE_GROUP_CONTROL_USER_IDS` 等非密钥环境配置不再参与启动。旧配置数据库也不会覆盖 YAML。

实际 `config.yaml`、`.env`、`data/` 和本地 `docs/` 均不提交 GitHub。公开使用说明以本 README 和仓库内示例配置为准。

## 聊天与命令

### 消息处理

```text
QQ → NapCat → OneBot Adapter → Router
                                ├─ 命令 → Handler → QQ 回复
                                └─ AI 候选
                                    ├─ 已授权原文 → 后台记忆队列
                                    └─ Executor
                                        ├─ 资源检查、上下文与可选视觉
                                        ├─ 普通群消息：参与规则 → Jev/Router 判断
                                        └─ 生成与工具循环 → Action Sender → QQ
```

- 私聊普通消息直接进入生成路径；图片还需满足视觉开关和来源安全规则。
- 群聊默认关闭。白名单用户发送 **真实 `@机器人 /ai on`** 后，普通消息可进入候选，不要求每条都 @。
- 普通群消息由决策器选择是否回复，不保证有问必答；开启群中的真实 @ 优先生成，绕过参与判断，但仍受资源和输入检查约束。
- 关闭群后，普通消息和 @ 都不触发 AI；命令按各自规则处理。
- 未知斜杠命令不回落到 AI；`/ask` 已取消。自身消息与 `message_sent` 不参与回复。
- 群开关保存在内存。NapCat 重连不清空；Go 重启后需要重新 `/ai on`。

### 常用命令

| 命令 | 说明 |
| --- | --- |
| `/ping` | 链路测试，返回 `pong` |
| `/help` | 当前命令帮助 |
| `/ai on` / `/ai off` | 当前群启停，仅 `group.control_user_ids` 中的用户 |
| `/stickers ...` | QQ 收藏表情管理，仅控制白名单用户私聊使用 |
| `/memory ...` | 长期记忆保存、查看、修改和删除，需对应记忆授权 |

群命令需要 QQ 真正的 `at` 消息段；手打“@昵称”不一定构成有效提及。控制白名单按发送者 QQ 校验，不等同于 QQ 群主/管理员身份。

### 模型与人设

`llm.provider` 支持：

- `disabled`：不调用模型。
- `openai-compatible`：非流式 Chat Completions；工具功能要求接口支持对应协议。
- `ollama`：原生非流式 `/api/chat`，当前按纯文字能力使用。Base URL 如 `http://127.0.0.1:11434`，不要重复包含 `/api/chat`。

外部角色文件：

```yaml
persona:
  prompt_file: "data/persona/角色设定.md"
  name: "EchoCore"
  style: "友善、诚实、自然、简洁"
  group_reply_length: "简短"
```

文件相对配置文件目录解析；角色内容不能授予未注册工具或覆盖运行时授权边界。

### 可选 Jev 与参与限额

Jev 是独立决策服务，不是聊天模型。普通群消息可走“规则/限额 → Jev → 必要时 Router LLM 兜底 → Executor”；私聊和明确 @ 不用于验证 Jev 是否调用。

默认配置对应 OpenRouter；使用 TypeSafe 的 Key 时，需要同时切换接口配置：

```yaml
jev:
  enabled: true
  base_url: "https://api.typesafe.ai/v1"
  path: "/systemone"
  model: "jev-latest"
  min_confidence: 0.60
  timeout_ms: 3000
  proxy_url: "" # 如需本机代理，可显式填写 socks5h://127.0.0.1:7897
```

以上为当前客户端支持的配置形式，实际账号权限、网络及服务可用性需验证。不要混用平台密钥。`proxy_url` 只影响 Jev，不影响聊天、视觉、MCP 或 OneBot。

`participation` 可配置冷却、窗口回复数和重复过滤，完整字段见模板。低置信度、不确定或 Jev 失败时可回退 Router LLM；兜底失败保守沉默，不无限重试。

### 上下文与并发边界

- 同一私聊或同一群只允许一个在途 AI 回复；不同会话可独立处理，全局模型并发默认 4。
- 忙碌消息不进入短期历史；已授权自动记忆在 Router 原文入口独立收集，不以是否成功回复作为唯一条件。
- 短期历史仅存内存：每会话最多 100 条，模型最多接收最近 20 条，总输入受 8000 字符预算约束；闲置 30 分钟清理。
- 只有明确发送成功的 AI 回复进入后续历史。群历史在该群内共享，不跨会话混用。
- 群关闭取消该次开启周期的任务、清理短期历史；已交给 NapCat 的消息无法撤回。
- 默认聊天总超时 30 秒，输出预算 512 Token。需要更多预算时修改 `llm` 对应字段，不与视觉、记忆的独立预算混淆。

## React 管理后台

Go 仅提供管理 API，**不会嵌入或自动启动 React 页面**。

1. 在 `.env` 设置独立的 `ECHOCORE_ADMIN_KEY`，使用 32～256 字节随机密钥。
2. 修改现有配置段，重新加载环境变量并重启 Go：

   ```yaml
   admin:
     enabled: true
     frontend_origin: "http://127.0.0.1:5173"
   ```

3. 在另一个终端启动前端：

   ```bash
   cd web
   npm ci
   npm run dev
   ```

4. 打开 `http://127.0.0.1:5173`，使用 **EchoCore 管理密钥**登录。

开发代理将 `/api/admin/v1/` 转发至 `http://127.0.0.1:8080`。使用配置中一致的主机名，不要随意混用 `localhost` 和 `127.0.0.1`；修改端口时也需检查前端代理和后端 Origin。

已支持表情审批/整理、记忆查看/修改/清理、候选审核、自动提取暂停/恢复及抑制解除。当前不是通用监控或配置编辑后台，不能在网页中直接修改所有 YAML 设置。

管理接口采用回环入口、登录会话与 CSRF 保护。本机管理员可查看全部已保存会话资料，包括私聊；不应直接暴露到公网。`npm run build` 只生成前端产物，不自动提供部署服务。

## 图片和 GIF 识别

在现有 `vision` 段配置视觉模型并设置 `ECHOCORE_VISION_API_KEY`：

```yaml
vision:
  enabled: true
  auto_group: false
  base_url: "https://你的视觉服务地址/v1"
  model: "支持图片输入的模型名称"
  token_field: max_tokens
  allowed_hosts: [] # 核验下载失败日志中的实际图片来源后填写
  timeout_ms: 30000
  max_concurrent: 2
  max_output_tokens: 2048
  gif:
    enabled: true
    max_frames: 8
  debug_local: false
```

- `auto_group: false` 时，仅私聊及开启群内的 @ 触发范围可使用视觉；开启普通群图片识别可能增加费用。
- `allowed_hosts` 是**图片下载来源域名**，不是模型地址。按 `source_host` 核验并加入精确域名，不使用通配符，不为排错直接关闭安全校验。
- JPEG/PNG 按静态图处理；真实 GIF 经安全校验、画面合成和时间采样，最多提交 8 帧及时间标记进行描述。
- `[动画表情]` 摘要不能证明文件是 GIF，依据下载字节判断。不能保证捕获所有短暂动作，也不把抽样当成完整视频理解。
- 不承诺 APNG、动态 WebP、视频或所有 QQ 商城/原生表情兼容；Adapter 不会把任意本地 `file` 路径当作可读图片。
- 图片识别数据不作为聊天图片档案落盘；描述缓存按会话隔离。视觉描述属于不可信资料，不是系统指令。

关注 `Vision image processed` 的 `recognized`、`cache_hit`、`download_failed`、`unsupported_or_invalid`、`vision_failed`。

开发排查可显式打开 `vision.debug_local` 查看帧时间和视觉描述；它可能暴露图片内容，只在本地使用，排查后关闭。不要公开粘贴含隐私的调试日志。

## 图片表情包与 QQ 收藏

模型选择已有素材，不实时生成图片，也不能直接发送任意 URL、文件路径或 CQ 码。当前工具流程是 `list_stickers` → `select_sticker` → 统一发送。

### 本地素材

1. 在 `data/stickers/` 放入有权使用的 PNG、JPEG 或 GIF。
2. 参考 [stickers.example.yaml](stickers.example.yaml)，创建 `data/stickers/catalog.yaml`，填写名称、描述、标签和相对文件名。
3. 开启素材条目及 `stickers.enabled`，重启 Go。

单图最多 2 MiB，路径、格式和尺寸受安全检查。模型主要依据名称/描述选择，素材库没有描述不等于模型会自动理解图片。

### 机器人 QQ 收藏

同时开启 `stickers.enabled` 与 `qq_stickers.enabled`。只使用 QQ 收藏时，可不创建本地素材目录；完整配置见模板。

控制白名单用户私聊机器人：

| 命令 | 用途 |
| --- | --- |
| `/stickers status` | 同步状态、数量、失败分类和资源主机 |
| `/stickers sync` | 请求后台同步 |
| `/stickers list 1` | 查看第一页素材和 ID |
| `/stickers label ID 猫猫大笑 \| 开心、好笑时使用` | 补充本地名称与描述 |
| `/stickers approve ID` | 批准使用 |
| `/stickers disable ID` | 停用 |
| `/stickers forget ID` | 删除本地记录及可回收缓存，不删除 QQ 原收藏 |

首次 `qq_stickers.allowed_hosts` 为空时，下载被拒绝是安全默认行为。根据状态中的资源主机核验后配置精确白名单，再同步。

同步的是 NapCat 当前登录账号的收藏，不是其他群友的收藏。查询结果不保证全量；未出现不代表已删除。详情或下载失败时保留其他可用能力，不自动猜测数据。

批准意味着允许**该机器人所有可回复会话**使用该素材，内容变化会撤销审批。每轮最多一张，成功回执后才记录历史及冷却；`Sticker reply planned` 不等于已发送，`OneBot reply sent` 才表示明确成功。

收藏能力不依赖新的模型 Key；缓存使用 SQLite 和本地图片文件。GIF 能否正常播放仍取决于 QQ/NapCat 实际表现。

## MCP 工具

当前支持受控的公网 **HTTPS Streamable HTTP** 服务，不支持任意 `stdio` 子进程、本机内网服务或未授权工具。

```yaml
mcp:
  enabled: true
  servers:
    - name: knowledge
      transport: streamable_http
      url: "https://example.com/mcp" # 占位地址，替换为实际审核过的服务
      enabled: true
      token_env: ECHOCORE_MCP_KNOWLEDGE_TOKEN # 无鉴权服务可留空
      allowed_tools: [search] # 必须是服务真实暴露的工具名
      allowed_users: ["123456789"]
      allowed_groups: [] # 空列表表示不允许群聊使用
      timeout_ms: 10000
```

填写 `token_env` 后，在后端环境中提供对应变量；不要将 Key 放进 URL。群内使用同时需要用户授权、群授权和群 AI 开关。

一个 MCP 服务可提供多个工具，**服务数量不等于工具数量**。当前最多 4 个服务；工具经过白名单和参数结构检查后才提供给模型，并非白名单中的任何工具都必然可用。应只批准已审核的只读工具：客户端不会替你证明远程工具没有副作用，不能仅凭工具名称认定安全。

- 工具定义总数统一上限 **12**，包含记忆、表情和 MCP。
- 未启用记忆时，MCP 活跃服务的批准工具合计最多 10 个；启用记忆时配置额外预留名额，批准列表合计最多 9 个。
- `tools.max_rounds` / `max_calls` 限制一次回复中的模型轮数与实际工具调用数，与定义数量是不同预算。
- 新增服务需修改 YAML、准备对应密钥并重启，不需要为每个合规远程工具重新写一个 Go Handler。

`MCP tool completed` 表示一次工具调用完成，不代表最终 QQ 回复已成功。工具返回与网页内容是不可信资料，不可覆盖提示词、扩大权限或诱导泄露其他会话内容。

## 长期记忆

### 手动保存与读取

默认关闭。按会话授权后开启：

```yaml
memory:
  enabled: true
  database: "data/memory/memory.db"
  allowed_private_users: ["123456789"]
  allowed_groups: []
  recall_top_k: 3
  max_injected_chars: 1500
  max_entries_per_scope: 500
  max_entries_total: 10000
```

以上只开启手动管理与读取，不会自动收集全部聊天。

```text
/memory add 内容
/memory add preference 偏好内容
/memory add temporary 临时任务
/memory add agreement 群约定
/memory list
/memory update ID 新内容
/memory forget ID
```

群聊仍需授权和 `/ai on`，群命令需要 @。个人记录由归属用户修改/删除；群约定由控制白名单管理。后台管理员可整理全部范围，包括已撤销读取授权的资料。

当前通过 `recall_memory` 进行**关键词召回**，不是向量 RAG。每条正文最多 300 字，偏好长期保存、临时记录 7 天、事实和约定 90 天。相同用户在不同机器人/群/私聊中的资料不会自动合并。

### 自动提取与受控保存

在现有 `memory` 段内增加或修改 `auto`，不要再创建第二个 `memory`：

```yaml
  auto:
    enabled: true
    mode: review_all # 先审核验证；auto_safe 允许低风险无冲突内容自动保存
    grants:
      - self_id: "987654321" # 机器人 QQ
        type: private
        conversation_id: "123456789" # 对方 QQ；群聊时为群号
        user_ids: ["123456789"] # 本人同意收录后填写，群内也逐人授权
    model:
      provider: openai-compatible
      base_url: "https://你的记忆模型地址/v1"
      model: "填写可用模型名称"
      token_field: max_tokens
      timeout_ms: 30000
      max_output_tokens: 2048
    trigger_count: 20
    max_wait_seconds: 600
    batch_size: 20
    max_candidates_per_batch: 5
    max_batches_per_scope_hour: 6
    max_batches_total_hour: 30
    pending_ttl_days: 7
```

另需设置 `ECHOCORE_MEMORY_API_KEY`。读取白名单、精确 `grants`、运行时未暂停必须同时满足；群还需当前 `/ai on`。缺少模型配置或密钥时不启动原文收集。

```text
已授权原始发言 → 有界队列 → 提取候选 → 核验证据与已有记忆
                                      ├─ 低风险、无冲突 → 自动保存（auto_safe）
                                      ├─ 不确定/冲突 → 待审核，不参与召回
                                      └─ 敏感/无效来源 → 拒绝
```

- 默认累计 20 条合格消息或非空队列等待 10 分钟触发；可在管理页“立即处理队列”，仍受预算限制。
- 只收集原始用户文字；命令、引用消息、机器人输出、视觉描述、MCP 结果及明显敏感文本不作为收录来源。
- 后台每批最多两次模型请求，不占用正常回复执行槽，不保证每句话都会保存，也不会仅因用户说“记住”就绕过规则。
- 冲突不自动覆盖旧资料；人工批准替换需确认旧条目 ID 和版本。群关闭时不能批准。
- 删除/修改会取消旧任务并抑制原主题；没有主题的旧记录会保守暂停该会话自动收录。清空会话也会暂停，防止删除后被旧任务重新保存。
- 暂停不等于停止聊天或禁止读取既有记忆；恢复收录也不自动解除主题抑制。
- 原文队列仅存内存，退出不恢复；候选最多保留 7 天，批次元数据最多 30 天/10000 条。失败不自动重试，队列满或争锁时可能丢弃提取机会。

数据库 v1 会迁移到 v2，保留原记录。升级前建议停机备份，旧程序不能打开未来版本。不要多个进程共用同一数据库。

**隐私提醒：**自动提取会将授权原文发送给独立模型，召回资料也可能发送给聊天模型。敏感过滤并非完备隐私识别；建议先用测试私聊和 `review_all`，再决定是否启用 `auto_safe`。本地删除不能撤回已发给外部服务或 QQ 的内容。

## 日志与故障排查

`environment: development` 使用彩色 Console，最低 DEBUG；`production` 使用 JSON，最低 INFO。

INFO 记录关键标识、状态、耗时和已知用量；DEBUG 提供脱敏事件与心跳。通常不打印原始聊天正文、签名 URL、Key 或思考正文；显式视觉本地调试是需要谨慎开启的例外。

| 现象 / 日志 | 排查方向 |
| --- | --- |
| `OneBot WebSocket authorization failed` | 后端与 NapCat 的 OneBot Token 不一致，不是管理后台 Key |
| `model_available=false` | 检查聊天 Provider、地址、模型和环境密钥是否加载 |
| `管理 API 已禁用` | 检查开关、管理密钥长度及回环入口配置 |
| `group_disabled` | 当前群未 `/ai on`；Go 重启后会关闭 |
| `model_ignore` | 判断器正常选择不回复，不等于连接失败 |
| `fallback_error` / `jev_error` | 查看具体 Jev / Router 错误分类与 HTTP 状态 |
| `authentication` / 401 | 检查对应服务的 Key 和接口平台是否匹配 |
| `rate_limit` / 429、`upstream` / 5xx | 限流或上游错误，不应无限重试 |
| `host_not_allowed` | 核验实际图片来源，再补对应模块的精确域名白名单 |
| `unsupported_or_invalid` | 格式、解码或资源限制问题，不是单纯域名问题 |
| `output_truncated` / `output_limit`，`finish_reason=length` | 查看正文与思考 Token 用量，调整发生错误模块的输出预算 |
| `too_many_tools` | 查看 `tool_count` / `max_tools`，当前共享上限 12 |
| `tool_history_limit` | 查看 `tool_history_count` / `max_tool_history`，当前上限 32 |
| `invalid_tool_history` / `invalid_tool_schema` | 本地历史角色或工具 Schema JSON 构造错误 |
| `invalid_json` / `invalid_schema` / `empty_content` | 检查兼容接口的实际响应格式，不先假定为 Key 问题 |
| 自动记忆没有保存 | 检查独立授权、模型、暂停、队列阈值、候选审核和主题抑制 |

`*_available=true` 只表示本地配置可构造客户端，不证明真实网络、权限或模型效果已经验证。`reasoning_present=true` 只说明响应有思考字段，不代表启用了联网搜索；搜索需要实际工具调用。

生成阶段失败看 `LLM request failed`，记忆提取看 `Memory extraction batch ...` / `Memory batch committed`，视觉看 `Vision ...`，不要混淆不同调用链。

重启前退出原终端中的 Go 进程，避免重复占用 8080。启动后确认 OneBot 重连，群聊重新开启，再复测。

## 开发与测试

### 目录结构

```text
cmd/server/              服务启动与依赖装配
internal/adapter/onebot/ OneBot 接入、消息归一化与回复发送
internal/agent/          Router、Executor 与回复编排
internal/handler/        确定性命令
internal/llm/            模型客户端、决策、诊断与工具契约
internal/tools/          工具注册、分发与循环预算
internal/mcpclient/      受控远程 MCP 接入
internal/contextstore/   内存短期上下文
internal/memory/         持久记忆、自动提取与审核
internal/media/          图片/GIF 安全预处理
internal/vision/         视觉请求与描述缓存
internal/sticker/        本地素材、QQ 收藏与选择逻辑
internal/admin/          管理 API、登录及访问保护
internal/config/         YAML 配置和环境密钥加载
web/                     独立 React 管理前端
```

后端验证：

```bash
go test ./...
go test -race ./...
go vet ./...
```

前端验证：

```bash
cd web
npm ci
npm test -- --run
npm run build
npm run test:e2e
```

浏览器验收按当前 Playwright 配置使用本机 Chrome，并启动隔离测试后端与 Vite（18081 / 15173）。测试使用临时数据库、模拟模型和模拟协议，不读取真实 QQ 配置；运行需要允许本地端口和浏览器启动。

FakeLLM 仅用于显式注入的自动测试，不是生产配置中的模型选项。自动测试通过不等于真实供应商、QQ/NapCat 版本或费用已完成验收。

建议实际验证顺序：`/ping` → 私聊文字 → 群开关与选择性回复 → 静态图 → GIF → 表情发送 → 一个只读 MCP 工具 → 测试会话记忆提取、审核、重启召回与删除保护。按需逐项启用，不一次开放所有授权。
