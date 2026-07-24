# 渠道额外设置说明

该配置用于设置一些额外的渠道参数，可以通过 JSON 对象进行配置。常用设置项包括：

1. force_format
    - 用于标识是否对数据进行强制格式化为 OpenAI 格式
    - 类型为布尔值，设置为 true 时启用强制格式化

2. proxy
    - 用于配置网络代理
    - 类型为字符串，支持 `http`、`https`、`socks5` 和 `socks5h` 协议
    - 保存时必须包含协议和主机；仅允许空路径或根路径 `/`，不允许 query 或 fragment
    - SOCKS 代理未填写端口时，运行时使用默认端口 `1080`

3. thinking_to_content
   - 用于标识是否将思考内容`reasoning_content`转换为`<think>`标签拼接到内容中返回
   - 类型为布尔值，设置为 true 时启用思考内容转换

4. upstream_warmup_enabled
   - 用于标识是否对该渠道启用上游连接预热
   - 类型为布尔值，设置为 true 时会在全局 `UPSTREAM_WARMUP_ENABLED` 开启时，定时请求该渠道上游的非计费预热路径
   - 预热任务会完整排空响应体后才记录为“连接可复用”，401/403/404 等业务状态码不会直接视为连接失败

5. max_retries
   - 用于覆盖该渠道的中转重试次数，类型为整数，范围 `0-10`
   - 省略时继承系统 `RetryTimes`；显式设置为 `0` 时，该渠道请求失败后不再切换渠道重试

6. responses_empty_output_guard
   - 用于覆盖 Responses API 空输出保护，类型为布尔值
   - 省略时继承系统 `global.responses_empty_output_guard_enabled`；显式 `true` 或 `false` 分别强制开启或关闭
   - 开启后，`/v1/responses` 中仅含 reasoning、没有可见文本/拒答/工具或媒体输出的 completed 响应会返回 `upstream_empty_response`，且不会触发 New API 重试
   - 非流式请求返回 HTTP 424；流式请求以 `response.failed` 终止。已发生的上游消耗仍按 usage 或预扣额度保底结算

--------------------------------------------------------------

## JSON 格式示例

以下是一个示例配置，启用强制格式化、上游连接预热并设置了代理地址：

```json
{
    "force_format": true,
    "thinking_to_content": true,
    "upstream_warmup_enabled": true,
    "max_retries": 0,
    "responses_empty_output_guard": true,
    "proxy": "socks5://proxy.example:1080"
}
```

--------------------------------------------------------------

## 上游预热相关环境变量

- `UPSTREAM_WARMUP_ENABLED`：是否启用进程级预热任务，默认 `true`
- `UPSTREAM_WARMUP_URLS`：手动追加的预热 URL，多个地址可用逗号、分号、空格或换行分隔
- `UPSTREAM_WARMUP_PATH`：渠道基础地址自动拼接的预热路径，默认 `/v1/models`
- `UPSTREAM_WARMUP_INTERVAL`：预热间隔，支持秒数或 Go duration，默认 `30s`，最小 `5s`
- `UPSTREAM_WARMUP_TIMEOUT`：单个预热请求超时，支持秒数或 Go duration，默认 `10s`
- `UPSTREAM_WARMUP_JITTER`：预热间隔抖动比例，范围 `0-0.5`，默认 `0.2`
- `UPSTREAM_WARMUP_CONCURRENCY`：预热并发 worker 数，默认 `8`，范围 `1-32`
- `UPSTREAM_WARMUP_H1_CONNECTIONS`：已确认 HTTP/1.x 上游每轮预热请求数，默认 `1`，范围 `1-32`；HTTP/2 上游始终每轮 `1` 次
- `UPSTREAM_WARMUP_UA`：预热请求 User-Agent，默认 `new-api-upstream-warmup/1.0`

--------------------------------------------------------------

通过调整上述 JSON 配置中的值，可以灵活控制渠道的额外行为，比如是否进行格式化以及使用特定的网络代理。

## 升级兼容性

旧版本会忽略代理地址中的 path、query 和 fragment。为避免升级后中断已有渠道流量，运行时会继续剥离这些遗留后缀，并对同一代理地址每个进程记录一次不含凭证和后缀的警告。该兼容逻辑不会改写数据库；再次保存渠道时必须按上述严格规则修正代理地址。

代理连接使用 30 秒 TCP 拨号超时和 30 秒 KeepAlive；TLS 握手超时为 10 秒。这些超时同样适用于未配置渠道代理的中转请求。
