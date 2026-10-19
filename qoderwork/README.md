# QoderWork CPA 插件

[CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI) 的 QoderWork（qoder.com.cn，CN 区）Provider 插件：多账号 OAuth/PAT 双登录、动态模型、COSY 签名推理、每日签到、积分面板、token 自动保活。

## 功能

| 能力 | 说明 |
|---|---|
| **双登录方式** | ① OAuth 设备授权（PKCE，浏览器授权，dt- 30 天 + drt- 1 年自动旋转）② PAT 导入（pt-，长期有效兜底）——两家族可共存于同一 auth 文件 |
| **登录自动领包** | OAuth 登录成功后自动判断并领取一次性 Pro 升级包（eligibility → claim） |
| **COSY 推理** | RSA 包 AES 会话密钥 + MD5 请求签名，对接 gateway.qoder.com.cn SSE 流式 |
| **图片输入** | 支持 OpenAI `image_url` 和 Anthropic `image` 块；自动上传到 Qoder 的 `/algo/api/v2/image/upload`，再以 `messages[].image_url` 发送。png/jpeg/gif/webp，单图 ≤10MB，最多 5 张 |
| **动态模型** | COSY 拉取 `/algo/api/v2/model/list`（chat scene），14 静态模型兜底；只暴露 Qwen / DeepSeek 系列 |
| **最近请求** | 面板显示最近 20 条调用的时间、模型、输入/输出 token、状态、耗时（进程重启清零） |
| **每日签到** | 面板手动签到（单账号/批量）+ 09:00/21:00 定时自动签到，签到后返回最新积分快照 |
| **积分面板** | 账号卡片：昵称/积分/计划/签到状态/操作（签到/刷新/选用） |
| **token 保活** | 22:00 定时刷新；按 token 前缀路由（drt- → deviceToken/refresh，jrt- → jobToken/refresh），PAT 永不劫持 OAuth 刷新 |
| **auth 隔离** | 文件名前缀 `qoderwork-` 过滤，与 workbuddy 等其他插件互不干扰 |

## 安装

### 从 Release（推荐）

```bash
# 按你的平台下载（示例 linux/arm64）
unzip qoderwork_0.2.6_linux_arm64.zip
cp qoderwork.so /path/to/cliproxyapi/plugins/qoderwork.so
```

### 从源码

```bash
cd qoderwork
CGO_ENABLED=1 go build -buildmode=c-shared -ldflags "-X main.version=0.2.6" -o qoderwork.so .
cp qoderwork.so /path/to/cliproxyapi/plugins/
```

### config.yaml

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    qoderwork:
      enabled: true

# 模型别名（可选）
oauth-model-alias:
  qoderwork:
    - name: qmodel_preview
      alias: qoder/qwen3.8-max
    - name: qmodel_latest
      alias: qoder/qwen3.7-max
```

## 使用

### 方式一：OAuth 登录（推荐）

1. CPA 管理面板 → Auth 文件 → QoderWork OAuth 登录卡片
2. 浏览器打开授权链接 → 登录 qoder.com.cn（阿里云 SSO）→ 点 Continue 授权
3. 插件自动轮询拿 token 落盘（dt-/drt-），并自动领取 Pro 升级包（若 eligible）

### 方式二：PAT 导入

1. qoder.com.cn → 设置 → Personal Access Token → 创建（pt- 开头）
2. 插件面板（`/v0/resource/plugins/qoderwork/panel`）→ 右上角「导入 QoderWork 凭证」→ 粘贴 PAT → 导入
3. 插件自动换 jobToken（jt-/jrt-）落盘

### 面板

`/v0/resource/plugins/qoderwork/panel`（需 management key）

- 账号卡片：积分余额 / 计划 / 签到按钮（签到后返回最新积分）
- 「全部签到」批量签到；自动签到开关（09:00/21:00）

## 凭证家族与刷新

```
auth 文件字段（可共存）：
  accessToken:   dt- (OAuth, ~30d) 或 jt- (PAT 交换, 24h)
  refreshToken:  drt- (~1y, 旋转)   或 jrt- (48h)
  personalToken: pt- (长期兜底，导入即永久)
```

- 刷新路由按 `refreshToken` 前缀：`drt-` → `deviceToken/refresh`；`jrt-` → `jobToken/refresh` → 失败 fallback PAT re-exchange
- `personalToken` 永不主动覆盖活跃 token，只做最终兜底
- host 15 分钟 auto-refresh + 插件 22:00 keepalive 均按此规则

## 模型

本插件只暴露 **Qwen + DeepSeek** 两个系列；GLM 走 `workbuddy-cpa`，Auto/GLM/Kimi/MiniMax 不会出现在 qoderwork 的模型列表里，executor 也会拒绝非 Qwen/DeepSeek 请求。

| 系列 | 上游 key | 客户端兼容名 |
|---|---|---|
| Qwen | `qmodel_38max` | `qwen3.8-max` / `qwen3.8-max-preview` / `qmodel_preview` |
| Qwen | `qfmodel` | `qwen3.8-flash` |
| Qwen | `qmodel_latest` | `qwen3.7-max` |
| Qwen | `qmodel` | `qwen3.7-plus` |
| Qwen | `q37fmodel` | `qwen3.7-flash` |
| DeepSeek | `dmodel` | `deepseek-v4-pro` |
| DeepSeek | `dfmodel` | `deepseek-v4-flash` / `deepseek-v4.1-flash` |

模型清单由 COSY 动态拉取，静态表仅作冷启动兜底。

## 图片输入

`/v1/chat/completions` 和 `/v1/messages` 都支持图片：

- OpenAI：`{"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}`
- Anthropic：`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"..."}}`
- `http(s)` 图片 URL 直接透传；`data:` URL 会先上传到 Qoder，拿到 OSS URL 后放进 `messages[].image_url`。
- 支持 `png` / `jpeg` / `gif` / `webp`，单图 ≤10MB，最多 5 张。
- 图片上传签名与官方 CN 客户端一致：`PUT /algo/api/v2/image/upload?request_id=...`，COSY 签名使用 multipart body 的**字节长度**。

已实测：64x64 红色 PNG 经 `deepseek-v4.1-flash` 和 `qwen3.8-max` 均返回“红色”，OpenAI Chat 与 Anthropic Messages 两条入口都通过。

## 参考文档

- [KNOWLEDGE.md](../KNOWLEDGE.md) — QoderWork API 逆向全记录（COSY 签名/编码/端点）
- [analysis/api-endpoints-scan.md](../analysis/api-endpoints-scan.md) — 客户端全端点扫描
- [analysis/qoderwork-real-oauth.md](../analysis/qoderwork-real-oauth.md) — OAuth 设备授权流程 + 本地测试证据

## License

MIT（based on workbuddy by lovingfish，见 [LICENSE](LICENSE)）
