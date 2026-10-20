# Qoder Global（qoder.com）CPA 插件 — 设计文档

> **日期：** 2026-10-21
> **状态：** 已与用户对齐，待实现
> **仓库：** https://github.com/Sliverkiss/cpa-plugin（新子目录 `qoder/`）
> **上游协议依据：** bzym2/QoderGateway `docs/qoder-protocol-research.md`、shuishuipingan/qoder2api-hub、Zhengyuuuui/qoder2api（社区逆向，2026-08~10 实测）

---

## 1. 背景与目标

现有 `qoderwork` 插件覆盖 Qoder CN（`gateway.qoder.com.cn` COSY agent 端点）。该端点要求把客户端请求重建进 Qoder 的 agent 模板，导致第三方 agent（ZCode / Claude Code）的 `tools` / `tool_calls` / `tool_call_id` 被丢弃或改写，工具循环中断。

Qoder **国际版**（qoder.com）提供 OpenAI 兼容端点，纯 Bearer、无 COSY 签名、标准 SSE：

```
POST https://api2-v2.qoder.sh/model/v1/chat/completions
```

**目标：** 新建独立 CPA provider 插件 `qoder`，用国际版账号透传客户端请求，v1 只暴露一个模型：

| 客户端可见/调用名 | 上游 key | 说明 |
|---|---|---|
| `claude-opus-5-5` | `ultimate`（极致） | 插件内做别名映射；上游真实来源是官方未确认的内置模型，社区普遍推测为 Claude Opus 5.5，插件不做来源断言，仅命名 |

附带与 qoderwork 同级的账号能力：OAuth 设备授权 / PAT 双登录、token 保活、每日签到（campaign）、积分面板、最近请求、usage 上报。

**核心价值：** 透传模式下客户端的 system prompt、tools、tool results 原样到达上游，第三方 agent 的工具调用链路完整可用。

## 2. 范围

**做（v1）：**
- `claude-opus-5-5` → `ultimate` 单模型暴露与调用（流式/非流式）
- OAuth 设备授权 + PAT 双登录，dt-/drt-/jt-/jrt- 全家族刷新与 22:00 保活
- 每日签到（campaign 平台，面板手动 + 10:00/21:00 自动）
- 积分/套餐面板、最近 20 条请求、CPAMP usage 上报
- credits 耗尽自动 disable、签到恢复 re-enable（沿用 qoderwork lifecycle/scheduler 逻辑）

**不做（v1 非目标）：**
- 其他国际版模型别名（sonus/cantus/efficient 等）——后续按需加，架构预留白名单
- 动态 model/list 拉取（单模型用静态表；权益问题由 executor 上游报错如实透出）
- COSY 签名推理（agent 端点）与请求体重建
- 签到指纹导入器（auth attributes 预留字段即可）
- 单插件双区（CN 继续走 qoderwork，两插件并存隔离）
- BYOK 自定义模型

## 3. 插件身份与隔离

| 项 | 值 |
|---|---|
| 目录 | `qoder/`（与 `qoderwork/`、`workbuddy/` 平级） |
| providerName | `qoder` |
| auth 文件 | `qoder-<uid>.json`，顶层 `"type": "qoder"` |
| 构建产物 | `qoder.so` / `qoder.dylib` |
| config 节 | `plugins.configs.qoder.enabled: true` |
| 面板 | `/v0/resource/plugins/qoder/panel` |

**隔离规则（照抄 qoderwork 的 ownership 契约，改前缀）：**
- ParseAuth 只认 `type == "qoder"`，或文件名前缀 `qoder-`；`qoderwork-` 因连字符不匹配 `qoder-` 前缀，天然不会被误吞。
- hostAuthList / keepalive / models 按文件名前缀 `qoder-` 过滤。
- auth 文件落盘带 `region: "global"`、`domain: "qoder.com"`，面板显示 Global 标识。

## 4. 端点（国际版）

| 用途 | URL |
|---|---|
| 主站 / OAuth 授权页 | `https://qoder.com` |
| OpenAPI（业务，纯 Bearer） | `https://openapi.qoder.sh` |
| 推理（OpenAI 兼容，纯 Bearer） | `https://api2-v2.qoder.sh/model/v1/chat/completions` |
| 签到 campaign | `https://openapi.qoder.sh/sash/api/v1/me/campaigns` + `/campaigns/{id}/claim` |
| Pro 升级包 | `https://openapi.qoder.sh/sash/api/v1/me/pro-upgrade/{eligibility,claim}` |
| 额度/套餐 | `https://openapi.qoder.sh/api/v2/quota/usage`、`/api/v2/user/plan` |
| 图片上传（备用，二期） | `https://api1.qoder.sh/algo/api/v2/image/upload`（需 COSY，仅当透传 data URL 被拒时启用） |

OpenAPI 业务端点与 CN 完全同路径，仅域不同（`.com.cn` → `.sh`）。

## 5. 认证

### 5.1 OAuth 设备授权（PKCE）

与 CN 的差异（据 qoder2api-hub 实测配置）：

| 参数 | 国际版 |
|---|---|
| 授权 URL | `https://qoder.com/device/selectAccounts?challenge=…&challenge_method=S256&nonce=…&machine_id=…&client_id=…` |
| redirect_uri | **不带**（CN 带 `qoder-work-cn://`） |
| client_id | `e883ade2-e6e3-4d6d-adf7-f92ceff5fdcb` |
| nonce | 32 位 hex（无横线；CN 是带横线 uuid） |
| poll / refresh | `openapi.qoder.sh/api/v1/deviceToken/{poll,refresh}` |
| token 家族 | dt-（~30d）/ drt-（~1y，旋转） |

登录成功后：userinfo → 建档 → pro-upgrade eligibility → 自动 claim（一次性积分包，若 eligible）。

### 5.2 PAT 导入

`POST /api/v1/jobToken/exchange {"personal_token":"pt-…"}` → jt-（24h）/ jrt-（48h）。

### 5.3 刷新与保活

- 前缀路由：`drt-` → `deviceToken/refresh`；`jrt-` → `jobToken/refresh`，失败回落 PAT 重换；PAT 只做最终兜底。
- host 15 分钟 auto-refresh + 插件 22:00 keepalive，逻辑照搬 qoderwork。
- 推理用 token：OAuth 账号用 dt-，PAT 账号用 jt-（社区实测两者在新端点均 200）。

## 6. 模型暴露与映射

- 静态单模型：

```go
ModelInfo{ID: "claude-opus-5-5", Name: "Qoder Ultimate", ContextLength: 200000, MaxCompletionTokens: 8192, OwnedBy: "qoder"}
```

- 映射：请求模型 `claude-opus-5-5` → 上游 `"ultimate"`；executor 同时接受裸 `ultimate`（调试用），其余一律 400 并返回支持列表（不静默 fallback，避免 CN 端“错误 key 默默走 auto”的坑）。
- host 侧 `oauth-model-alias` / `oauth-excluded-models` 继续生效（复用 qoderwork 的 alias cache + excluded filter 代码）。
- 上下文窗口：catalog 默认 200K（可配 400K/1M，v1 固定报 200K）。

## 7. 执行器（透传，核心新增）

### 7.1 请求组装

以客户端（CPA 转换后的 OpenAI chat-completions payload）为基底，**原样保留** `messages`（含 tool/assistant 消息与 `tool_calls`/`tool_call_id`）、`tools`、`tool_choice`、`temperature`、`top_p`、`max_tokens`、`stop`；附加：

```json
{
  "model": "ultimate",
  "stream": true,
  "stream_options": {"include_usage": true},
  "metadata": {"context": {
    "request_id": "<uuid>", "request_set_id": "<uuid>",
    "session_id": "<uuid>", "task_id": "common", "client_type": "qodercli"
  }}
}
```

- **不注入** Qoder base system prompt，**不重建** body，**不改写**任何工具字段。
- Headers：`Authorization: Bearer <dt-|jt->`、`Content-Type: application/json`、`Accept: text/event-stream`、`User-Agent: qoder/1.1.64`（常量，实测可调）、`X-Request-ID`、`X-Session-ID`。

### 7.2 流式 / 非流式

- 流式：上游返回**标准 OpenAI `chat.completion.chunk` SSE**（`choices` 在顶层），逐 chunk 透传给 host stream emitter；处理 `[DONE]`、最终 usage chunk、`finish_reason`（`tool_calls`/`stop`）。
- 非流式：仍以 `stream:true` 发起，聚合成单个 `chat.completion` 返回（与 qoderwork 现行做法一致，规避上游非流式行为不确定性）。

### 7.3 图片

v1 先原样透传 `image_url`（含 `data:` URL）实测。**2026-10-21 实测：上游对 1x1 PNG data URL 返回 `provider_error: Could not process image`**，因此 data URL 图片在 v1 不可用；二期复用 qoderwork 的上传链路改到 `api1.qoder.sh`（需引入 COSY），把 data URL 换成 OSS URL 再透传。此为唯一可能引入 COSY 的场景。

### 7.4 错误处理

| 场景 | 行为 |
|---|---|
| 401/403 | 按 refreshToken 前缀刷新一次并重试；再失败则透传上游错误 |
| 429 / 5xx / 排队 JSON | 透传上游状态码与原文（截断脱敏），绝不返回空 200 |
| 上游 SSE `provider_error`（如 `All models failed`、Bedrock 拒绝、egress proxy） | 在流式与非流式路径都解析 `code`/`message`/`details` 并透出；v1 不自动重试 |
| 空 completion（无 content 无 tool_calls） | guard：返回显式错误而非空响应 |
| 未知模型 | 400 + 支持列表 |
| usage 缺失 | 记 0 并照常上报，不阻塞响应 |

### 7.5 用量与记账

- 解析最终 chunk 的 `usage`（prompt/completion/total tokens；`credits`/`original_credits` 若上游下发则一并取）。
- 写最近 20 条请求（时间/模型/ tokens/状态/耗时/credits），并按 qoderwork 同款机制上报 CPAMP `/v0/management/usage/import`。

## 8. 签到与积分

- campaign：`GET /sash/api/v1/me/campaigns` → 选 `CLAIMABLE` 的 Credits 活动 → `POST /campaigns/{id}/claim`（幂等，已领返回 replayed）。请求带 `cosy-clienttype: 10` + 官方 UA + `cosy-version`，**无需 COSY 签名**。
- **国际版指纹限制**：新号首次领取前，服务端要求真实设备指纹（`cosy-machinetoken`/`cosy-machinetype`），自造无效；官方客户端领过一次后转为懒下发。处理：
  - 无 CLAIMABLE 活动时，面板明确提示「国际版新号请先在官方 Qoder 客户端领取一次」；
  - 插件配置字段 `checkin_fingerprint`（JSON：machine_id/machine_token/machine_type），存在时请求附加真实指纹六头；未配置时用 UID 稳定派生的机器头，不做导入 UI。
- 面板手动单号/全部签到 + 10:00/21:00 自动（10:00 UTC+8 活动刷新）；签到后刷新 quota 快照；credits 耗尽 disable、签到恢复 re-enable。
- legacy `/daily-check-in` 国际版 404，不做 fallback。
- 额度：`/api/v2/quota/usage`（userQuota + addOnQuota 求和）、`/api/v2/user/plan`。

## 9. 面板与管理

复用 qoderwork panel 换品牌（Qoder Global）：账号卡片（昵称/积分/plan/签到/选用）、导入 PAT、OAuth 登录、最近 20 条请求、自动签到/保活开关。management 路由与 config 字段（checkin_auto / lifecycle_auto / token_keepalive / scheduler_mode / usage_report_*）与 qoderwork 对齐。

## 10. 数据流

```
ZCode / Claude Code
  → CPA (127.0.0.1:8317)
    → /v1/chat/completions 或 /v1/messages（CPA 统一转 chat-completions）
      → plugin "qoder" executor
        → 映射 model: claude-opus-5-5 → ultimate
        → POST api2-v2.qoder.sh/model/v1/chat/completions (Bearer, 透传 body + metadata)
          ← 标准 OpenAI SSE（content / tool_calls / usage）
        ← 逐 chunk 透传 / 聚合
      ← CPA 回 translated 格式
  ← 客户端（工具调用原样往返）

定时: 10:00/21:00 campaign 签到 · 22:00 token 保活 · credits 耗尽 disable
```

## 11. 测试计划

**单测（Go）：**
- 模型映射与白名单拒绝（claude-opus-5-5 / ultimate 通过，其余 400）
- 透传 body 组装：messages/tools/tool_choice/参数逐字段保留快照断言；metadata 注入正确
- ParseAuth region 隔离：`type:qoder` 认领、`type:qoderwork`/`workbuddy` 拒绝、前缀判定
- 刷新路由：drt-/jrt-/PAT 三家族各走对端点
- SSE 透传：chunk 顺序、tool_calls 增量、`[DONE]`、usage 提取、空 completion guard
- 401 → 刷新 → 重试路径
- campaign 解析：CLAIMABLE/CLAIMED/无活动三分支

**实测（用户提供国际版账号，顺序执行）：**
1. OAuth 设备授权登录 → auth 文件落盘、pro-upgrade 自动领取
2. PAT 导入 → jt- 交换
3. `/v1/models` 出现 `claude-opus-5-5`
4. OpenAI 流式 + 非流式补全（ultimate 有权益；免费号则记录上游报错原文）
5. **工具往返（关键验收）**：自定义 `get_weather` 工具 → tool_calls → tool 结果回传 → 最终回答，多轮无中断
6. Anthropic `/v1/messages` 走 CPA 全链路
7. 图片输入（data URL 透传实测，被拒则记录并排二期）
8. 面板签到（若新号无活动，验证提示文案）
9. ZCode 端到端：选 `claude-opus-5-5` 完成带工具的真实任务；长工具列表（~179 tools）实测上游是否拒收，必要时加可配置 budget

**部署：** `qoder.dylib` → `~/.cli-proxy-api/plugins/`，`config.yaml` 加 `plugins.configs.qoder` 节，`launchctl kickstart -k gui/$(id -u)/sh.brew.cliproxyapi` 重启，逐项验证。

### 11.1 实测绘测结果（2026-10-21）

| 项目 | 结果 |
|---|---|
| 插件加载 / `/v1/models` | `claude-opus-5-5` 出现，`owned_by=qoder` |
| 非流式 | 200，content 正常，上游 `model=ultimate`，usage/credits 正常 |
| 流式简单对话 | 200，标准 OpenAI SSE chunk |
| 流式工具调用 | 200，`tool_calls` 增量透传（4 个 delta） |
| 非流式工具往返 | tool_calls → tool result → 最终回答，成功 |
| Anthropic `/v1/messages` | 200，`ANTHROPIC_OK` |
| 图片 data URL | 上游 `Could not process image`（v1 不支持，二期上传） |
| 签到 campaign | 上游返回当日 `CLAIMED`；派生机器头可拉取活动列表 |
| qoderwork 回归 | `qwen3.8-max` 200 |
| 路由冲突 | notion 也暴露 `claude-opus-5-5`；给 qoder 配置 `priority: 200` 后日志稳定 `provider=qoder` |

**上游稳定性（同一 trial 账号实测）：** Ultimate 请求约半数会返回上游 `provider_error`，典型原文：
- `model "claude-opus-4-6" is not available for this account`
- `bedrock:InvokeModelWithResponseStream ... explicit deny`
- `Required egress proxy not available`
这些错误发生在输出前，插件已如实透出；一次失败会让 CPA 对该 auth 冷却 60 秒。该问题属于 Qoder 上游路由/套餐权益，不是插件透传缺陷；v1 不自动重试，建议使用具备 Ultimate/极致权益的付费账号。

## 12. 风险与开放问题

| 风险 | 缓解 |
|---|---|
| 国际端点无官方文档，可能变动 | 常量集中管理；错误透传上游原文；社区多项目交叉印证 |
| `ultimate` 背后是否 Claude Opus 5.5 未官方确认 | 插件只做命名映射，不断言来源；命名可随时改 |
| 账号无 ultimate 权益（免费/低档套餐） | 上游报错如实透出，面板展示 plan；实测 trial 账号约半数请求失败（`claude-opus-4-6 not available` / Bedrock deny / egress proxy），建议付费账号 |
| data URL 图片可能被拒 | **已实测被拒**（`Could not process image`）；v1 不支持，二期加 COSY 上传 |
| ZCode 长 tools 载荷（~605KB）可能超上游限制 | 实测；必要时加可配置 tool budget |
| 签到指纹限制 | 面板提示 + 可选指纹字段 |
| 与 qoderwork/workbuddy/notion 模型名冲突 | 单模型 `claude-opus-5-5`；给 qoder 插件更高 `priority`，或用 `oauth-model-alias` 增加唯一别名 `qoder-opus` |

## 13. 验收标准

1. CPA `/v1/models` 列出 `claude-opus-5-5`，OpenAI 与 Anthropic 两条入口均可调用。
2. 工具调用多轮往返完整（tool_calls → 结果 → 最终回答），ZCode 真实任务无中断。
3. 面板可见积分/plan/签到状态；签到成功后积分快照刷新。
4. usage 上报与最近请求记录正常；异常请求有明确上游错误，无空 200。
5. qoderwork 插件行为不受影响（回归：CN 模型仍可调用）。

**2026-10-21 验收状态：** 1/2/4/5 已实测通过（工具往返在非流式链路上完成一次完整往返，流式 tool_calls 增量透传已确认；ZCode 真实任务留给用户在本机做最终体验确认）。3 的上游 campaign/额度已确认（当日 `CLAIMED`、add-on 499），面板按钮因 management key 未在自动化中调用，留待用户点击确认。上游 Ultimate 权益不稳定会在输出前失败并透出原文，属账号/上游问题，需用具备权益的账号复测。
