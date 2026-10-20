# Qoder Global CPA 插件

CLIProxyAPI 的 Qoder 国际版（qoder.com）Provider 插件：OAuth/PAT 双登录、OpenAI 兼容透传、单模型 `claude-opus-5-5`、每日签到、积分面板、token 保活。

## 模型

| 客户端调用名 | 上游 key | 说明 |
|---|---|---|
| `claude-opus-5-5` | `ultimate` | Qoder 国际版“极致”模型；上游真实来源以官方为准 |

v1 只暴露这一个模型；其他模型请求会返回 `model_not_supported`，不会静默回退到 Auto。

## 安装

```bash
cd qoder
CGO_ENABLED=1 go build -buildmode=c-shared -ldflags "-X main.version=0.1.0" -o qoder.dylib .
cp qoder.dylib ~/.cli-proxy-api/plugins/
```

```yaml
plugins:
  configs:
    qoder:
      enabled: true
      priority: 200   # 其他 provider 也暴露 claude-opus-5-5 时，用它钉住 qoder
      # checkin_auto: true
      # token_keepalive: true
      # checkin_fingerprint: '{"machine_id":"...","machine_token":"...","machine_type":"..."}'
```

若 CPA 中已有其他 provider 暴露同名 `claude-opus-5-5`（例如 notion），把本插件的 `priority` 调高即可稳定路由到 qoder。

## 登录

- OAuth：CPA 管理面板 → Auth → Qoder 登录卡片 → 浏览器授权
- PAT：插件面板顶部「导入 Qoder Global 凭证」→ 粘贴 `pt-` 开头的 Personal Access Token

## 签到

面板支持单账号/全部签到，自动签到 10:00 / 21:00。国际版新号可能因设备指纹限制看不到活动：请先在官方 Qoder 客户端领取一次，或在配置中提供 `checkin_fingerprint`。

## 透传

执行器把 OpenAI chat-completions 请求原样转发到 `https://api2-v2.qoder.sh/model/v1/chat/completions`，保留 `tools`、`tool_calls`、`tool_call_id`，可直接用于 ZCode / Claude Code 的工具循环。工具定义需带非空 `description`，上游会校验 `tools[*].function.description` 为字符串。

## 已知限制（2026-10-21 实测）

- **Ultimate 权益不稳定**：同一账号的部分请求会返回上游 `provider_error`（如 `model "claude-opus-4-6" is not available for this account`、Bedrock `InvokeModelWithResponseStream` 显式拒绝、`Required egress proxy not available`）。这是 Qoder 上游路由/套餐权益问题，插件会把原始错误透出；失败请求可能触发 CPA 60 秒账号冷却。建议使用有 Ultimate/极致权益的付费账号。
- **图片 data URL**：上游对 `data:image/...` 返回 `Could not process image`。v1 未实现 COSY 图片上传，需要图片输入时走二期上传链路。
- **签到**：国际版活动按设备指纹下发；新号先在官方客户端领取一次（之后懒下发），或在配置中提供真实 `checkin_fingerprint`。

## 实测记录（2026-10-21）

- 非流式 `claude-opus-5-5`：200，上游 model=`ultimate`，usage 正常。
- 流式简单对话：200，标准 OpenAI chunk。
- 流式工具调用：200，`tool_calls` 增量透传；非流式工具往返（tool_call → tool result → 最终回答）通过。
- Anthropic `/v1/messages`：200，`ANTHROPIC_OK`。
- 签到 campaign：上游返回当日 `CLAIMED`；派生机器头可正常拉取活动列表。
- qoderwork 回归：`qwen3.8-max` 仍 200。
