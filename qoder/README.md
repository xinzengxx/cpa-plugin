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
      # checkin_auto: true
      # token_keepalive: true
      # checkin_fingerprint: '{"machine_id":"...","machine_token":"...","machine_type":"..."}'
```

## 登录

- OAuth：CPA 管理面板 → Auth → Qoder 登录卡片 → 浏览器授权
- PAT：插件面板顶部「导入 Qoder Global 凭证」→ 粘贴 `pt-` 开头的 Personal Access Token

## 签到

面板支持单账号/全部签到，自动签到 10:00 / 21:00。国际版新号可能因设备指纹限制看不到活动：请先在官方 Qoder 客户端领取一次，或在配置中提供 `checkin_fingerprint`。

## 透传

执行器把 OpenAI chat-completions 请求原样转发到 `https://api2-v2.qoder.sh/model/v1/chat/completions`，保留 `tools`、`tool_calls`、`tool_call_id`，可直接用于 ZCode / Claude Code 的工具循环。
