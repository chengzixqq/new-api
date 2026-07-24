# New API 魔改功能与上游同步维护手册

> 用途：供 Codex、Claude 或其他维护者在新对话中快速理解本仓库相对官方
> `QuantumNous/new-api` 的长期魔改，并在同步官方新版时保留这些能力。
>
> 本文档描述的是代码设计和维护约束，不是永久可信的线上状态台账。涉及官方最新
> 提交、生产镜像、容器、数据库和配置时，必须先实时复核，不能仅依赖本文快照。

## 1. 新对话接手顺序

开始任何同步、修复或部署前，按以下顺序执行：

1. 阅读仓库根目录 `AGENTS.md`、本文档，以及涉及阶梯计费时的
   `pkg/billingexpr/expr.md`。
2. 执行 `git status --short --branch`，不得覆盖用户或其他 AI 尚未提交的修改。
3. 执行 `git remote -v`、`git fetch origin`、`git fetch upstream main`，随后锁定一个
   明确的官方提交 SHA。本轮工作中官方再次更新时只报告，不临时追第二个目标。
4. 使用 `git diff upstream/main..HEAD` 判断真实魔改差异；不要仅凭旧文档或提交标题
   推断当前实现。
5. 先完成代码审查、测试、独立镜像构建和完整备份。替换生产容器前必须再次获得
   用户明确确认。
6. 不在文档、Git、命令输出或聊天中写入 API Token、SSH 密码、数据库密码、
   `.env` 内容或备份内的敏感配置。

## 2. 仓库快照（2026-07-24）

| 项目 | 当前快照 |
| --- | --- |
| 本地仓库 | `D:\project\newapi` |
| 用户远端 | `origin = https://github.com/chengzixqq/new-api.git` |
| 官方远端 | `upstream = https://github.com/QuantumNous/new-api.git` |
| 当前候选分支 | `codex/sync-custom-84a79b68` |
| 合并前检查点 | `23008615562c86664f94c2ee7904b3a4f34e6ec5` |
| 本轮代码合并点 | `e2a898cf0cacd34042f636f260b6b348557e9966` |
| 本轮官方锁定基线 | `84a79b6807ac1a679ca86f34c8c6f39175c294d8` |
| 合并前本地源码备份 | `D:\project\newapi-backups\sync-84a79b68-20260724T194105\workspace-source-v2.tgz`；2388 个文件；SHA-256 `42C6D5650E87EA21B99CC9F661CE1698D375BBCE5D8248C0865AAC8A4CD9A328` |
| 候选镜像构建 | `.github/workflows/candidate-image.yml` 使用仓库原始 `Dockerfile` 构建 `linux/amd64` OCI 归档；成功回执写入 `refs/notes/candidate-image` |
| 2026-07-14 生产镜像历史快照 | `new-api:upstream-a6ea9503-20260714T215817Z`；镜像 ID `sha256:f0cf6f5a4227a1d7a66375f583f9bdf919c62c91ad79c9527d50d811c7915a6a` |
| 2026-07-14 回滚镜像历史快照 | `new-api:rollback-pre-health-linear-axis-20260714T213311Z`；上一生产标签 `new-api:upstream-a6ea9503-20260714T160241Z`；当时均指向 `sha256:fc6ba5572a911e74a3f2364444282f47905d9e5f8944d963d19f14913bc4a2b6` |
| 2026-07-14 完整备份历史快照 | 服务器 `/data/new-api/backups/deploy-20260714T213311Z-pre-health-linear-axis.tgz`；本机 `D:\project\newapi\output\deploy\backups\deploy-20260714T213311Z-pre-health-linear-axis.tgz`；SHA-256 `3c8dc3dd490f9f94de165511f0810bd2b607abad334a38dbb772cea34db562f1` |
| 2026-07-14 部署验收历史快照 | `D:\project\newapi\output\deploy\DEPLOYMENT_RESULT-20260714T220700Z.txt`；服务器 `/data/new-api/builds/DEPLOYMENT_RESULT-20260714T220700Z.txt`；SHA-256 `bfe99d500a8534282acd68e6a89903a652a61a5e929901757ba50b7714063a47` |
| 2026-07-14 模型健康池历史快照 | `CCMAX-1 -> CC-MAX / CCMAX-B池 / 渠道 58`；`CCMAX-2 -> CC-MAX / CCMAX-A池 / 渠道 84`；后续以实时配置为准 |
| 2026-07-14 池明细历史快照 | `model_health_setting.pool_details_enabled=true`；两池启用前均已超过 30 次真实渠道尝试 |
| 2026-07-14 传输配置历史快照 | 当时全局 `auto + pool=32`；57 号渠道继承全局 |
| 节点名约束 | `NODE_NAME=newapi-us-1` |

生产镜像与容器状态会变化。下一次部署前必须在服务器上重新核对运行镜像、镜像 ID、
Compose、健康状态、数据库、Redis 和备份，不得把上表当成实时事实。

## 3. 魔改能力总览

| 能力 | 当前状态 | 主要入口 |
| --- | --- | --- |
| 防漏扣、防爆扣与缓存写入结算 | 已实现、生产运行 | `service/text_quota.go`、`service/usage_helpr.go` |
| 模型级及分组级精确定价 | 已实现 | `types/price_data.go`、`model/pricing.go`、`relay/helper/price.go` |
| 模型最低费用与补全倍率解锁 | 已实现 | `setting/ratio_setting/model_ratio.go` |
| 模型广场后台直接编辑价格 | 已实现，已迁移到官方单前端 | `controller/model_meta.go`、`web/src` |
| 上游连接预热及状态 | 已实现 | `service/upstream_warmup*.go` |
| 上游分段 trace 与日志展示 | 已实现 | `relay/common/upstream_trace.go` |
| 上游 HTTP/2 分片池、混合模式和 HTTP/1.1 兼容 | 已实现，待生产灰度 | `service/upstream_transport_pool.go`、`service/http_client.go` |
| 模型健康状态（主动批次探测 + 分组汇总 + 池/渠道尝试明细） | 已实现、生产运行；单轮多次探测待灰度启用 | `controller/model_health.go`、`pkg/perf_metrics`、`service/model_health_*.go` |
| Cowork / Claude Desktop adaptive thinking 兼容 | 已实现、按渠道启用 | `relay/common/cowork_adaptive_thinking.go` |
| 模型请求滑动窗口限流和管理员独立档 | 已实现 | `middleware/model-rate-limit.go` |
| 中文、繁中及单面板显示补全 | 已实现 | `web/src/i18n` |
| Grok Chat Completions 经 Responses 上游转发 | 已实现、本地待部署并按渠道启用 | `relay/chat_completions_via_responses.go`、`service/relayconvert/internal/oai_chat/to_oai_responses_req.go` |
| Responses 推理独占空回复保护 | 已实现、候选分支待灰度 | `relay/channel/openai/relay_responses.go`、`relay/responses_handler.go` |
| 渠道级重试次数覆盖 | 已实现、候选分支待灰度 | `dto/channel_settings.go`、`controller/relay.go` |
| 服务器端自动推断缓存亲和键 | **仅有设计文档，尚未实现** | `CACHE_AFFINITY_SERVER_SIDE_FALLBACK.md` |

### 3.1 Grok Chat Completions 转 Responses

NewAPI 已有 `global.chat_completions_to_responses_policy`，可按渠道和模型正则把下游
`/v1/chat/completions` 转为上游 `/v1/responses`，再把 Responses 响应转换回原协议。
Grok 渠道使用该策略时保留 `reasoning_effort -> reasoning.effort`，但不额外生成
`reasoning.summary`，避免向上游增加 Chat 请求未表达的 summary 语义；同时保留下游
提供的 `prompt_cache_key` 和 `prompt_cache_retention`，让 Responses 上游获得稳定缓存键。

启用时必须同时满足：全局与渠道请求体透传均关闭；策略只选中目标 Sub2API 渠道；
模型正则只覆盖预期的 `grok-4.5`。该变更当前仅在本地工作区完成测试，生产启用和镜像
替换仍按第 16 节的备份、确认、灰度与回滚流程执行。

### 3.2 Responses 空回复保护、重试与结算

`global.responses_empty_output_guard_enabled` 是全局开关，编译默认值为 `false`。渠道设置
`responses_empty_output_guard` 是可空布尔值：`null` 继承全局，`true` 强制启用，`false`
强制关闭。渠道设置 `max_retries` 同样可空：`null` 继承系统重试次数，显式 `0` 表示不重试，
允许范围为 `0..10`。

保护开启后，Responses 非流式响应若只有 reasoning/encrypted content 而没有可见消息、拒绝、
工具或媒体输出，网关返回 HTTP 424 和 `upstream_empty_response`。流式响应会抑制上游
`response.completed`，只发送一个兼容官方事件结构的 `response.failed`，沿用序列号且不转发
`encrypted_content`。该错误无论全局或渠道普通重试配置如何都不再选取其他渠道重试，避免
同一空回复重复产生上游费用。

上游 usage 有可用 input/output token 时按可信 usage 结算；没有可用 input/output token
（包括只有 `total_tokens`）时使用预扣费作为计费下限。首次结算失败但能够成功锁定预扣费时，
仍按锁定额度正式消费；只有最终无法确认结算时才将本次统计 quota 记为 0、跳过消费统计并在
管理员日志记录未确认状态。未来生产灰度仅计划将渠道 65、95 的
`responses_empty_output_guard` 设为 `true`，两者 `max_retries` 保持 `null` 继承普通错误重试；
本轮没有修改生产配置、镜像或容器。

## 4. 计费安全与缓存写入结算

这是最高优先级魔改。同步官方时宁可暂停合并，也不能在未验证的情况下简化或删除。

### 4.1 解决的历史问题

历史上出现过两类相反风险：

- 客户端中途断开且上游没有完整 usage 时，把 `cache_control` 断点数量错误当成倍率，
  产生远高于真实输入的缓存写入 token，导致爆扣。
- `TotalTokens` 只看普通输入和输出，忽略缓存写入等独立可计费项目，导致纯缓存写入
  请求被“总 token 为 0”保护清零，形成漏扣。

当前实现的原则是：可信 usage 按真实值；缺失 usage 时允许保守估算，但估算输入侧总量
不能超过已观测输入，且任何真实可计费项目都不能被旧零值保护漏掉。

### 4.2 当前调用链

重点沿以下链路审查，不能只检查单个函数：

```text
请求体与 cache_control 观测
  -> relay/channel/claude 与 relay/claude_handler
  -> service/usage_helpr.go 的可靠性标记、估算与 clamp
  -> service/billing_usage.go 的有效 usage 选择
  -> relay/helper/price.go 的模型/分组价格与预扣
  -> service/text_quota.go 的组件结算、最低费用与差额
  -> service/quota.go / task_billing.go / violation_fee.go
  -> 用户额度、Token 额度和消费日志
```

关键文件：

- `common/gin.go`：缓存请求体、识别 cache-control 信息，供异常断流估算使用。
- `constant/context_key.go`：usage 可靠性、观测输入及缓存写入相关上下文键。
- `service/usage_helpr.go`：可信/估算 usage、客户端断开兜底、观测输入上限。
- `service/billing_usage.go`：在不同协议的 billing usage 中选择最终计费 usage。
- `dto/claude.go`：Claude 聚合字段与 5m/1h 字段兼容。
- `service/text_quota.go`：文本、缓存、图片、音频等组件的最终 quota 计算。
- `service/quota.go`：音频和其他独立结算路径。
- `service/log_info_generate.go`：计费路径、quota clamp、trace 等审计信息。
- `relay/helper/price.go`：预扣、分组覆盖、最低费用、阶梯表达式和 `OtherRatios`。

### 4.3 必须长期保持的计费不变量

1. 上游返回可信 usage 时必须按真实 usage 结算，不能用本地估算覆盖。
2. `cache_control` 断点数量只能作为信号或审计信息，绝不能直接作为 token 倍率。
3. usage 缺失且请求异常中断时，可把已观测输入按缓存写入性质保守估算；估算输入侧
   总量不得超过已观测输入。
4. `ChargeableTokens` 是零值保护和最低费用的判断依据。它必须覆盖普通输入、输出、
   缓存读取、缓存写入以及其他实际可计费 token；不能退回只看 `TotalTokens`。
5. Claude 的缓存写入聚合字段与 5m/1h 拆分字段描述同一批输入：聚合值与拆分和取
   有效最大值，不能相加双扣；同一语义的新旧字段也取最大值。
6. OpenAI 原生缓存写入字段与 Claude 转换兼容字段只取有效最大值，不能重复收费。
7. 所有负 token 归零；token 求和使用饱和加法，不能因整数溢出回绕成零或负数。
8. 正向最终 quota 统一向上取整一次，避免小数截断少扣；负 correction 向零取整，
   避免多退款。
9. 任一可计费组件大于零且价格非零时，最终正向 quota 不得因小数或保护逻辑变成零。
10. 预扣、最终结算、退款、音频、图片断流、任务和违规费用都必须使用受检 quota
    转换；发生饱和时拒绝或审计，不能静默回绕。
11. `OtherRatios` 在相应官方路径只应用一次。
12. 按请求绝对价格不再乘分组倍率；按 token 分组覆盖分别计算输入、输出、缓存、
    图片和音频价格。
13. 模型级最低费用会乘有效分组倍率；分组显式最低费用是该分组的绝对下限，不再
    二次乘倍率。
14. 只要请求存在可计费 token，最低费用和最终结算均不得被普通 token 为零绕过。
15. 上游本身未提供某模型的缓存写入能力且明确返回 `cache_write_tokens=0` 时，这是
    正常数据，不得凭空制造缓存写入。只有日志明确出现
    `cache_write_tokens > 0 && quota <= 0` 才属于缓存写入漏扣候选。

### 4.4 相关修复提交

- `58383319`：加入缓存控制保守计费的早期基础。
- `90ef595e`：删除“输入 token × cache_control 数量”的爆扣公式并加入观测上限。
- `d25913cf`：在 client-gone 且 usage 缺失时保留缓存写入性质，并修复纯缓存写入清零。
- `c56f31e5`：所有零值保护统一使用 `ChargeableTokens`。
- `490e617e`：将上述逻辑维护到官方 `7c28993f` 结构并补齐溢出、取整和双算保护。

这些 SHA 用于理解演变，不代表可以逐个 cherry-pick 到未来官方版本。未来同步应以当时
官方结构为基础重新接入不变量，并通过回归测试证明结果。

## 5. 分组精确定价与模型价格管理

### 5.1 数据模型

`types.ModelGroupPricing` 支持以下字段：

- `ratio`
- `billing_mode`
- `billing_expr`
- `model_price`
- `prompt_price`
- `completion_price`
- `cache_price`
- `create_cache_price`
- `image_price`
- `audio_price`
- `audio_completion_price`
- `min_fee`

分组计费模式：

- `per-token`：按 token 计费，可覆盖各组件绝对单价。
- `per-request`：按请求绝对价格计费，必须显式提供 `model_price`。
- `tiered_expr`：使用计费表达式，必须先通过 `billing_setting.SmokeTestExpr`。
- `billing_mode` 缺失：继承模型默认模式。

兼容要求：旧数据中 `group_pricing` 的分组值可以直接是数字，此时按 `ratio` 解析；只有
倍率且没有其他覆盖时，序列化也保持数字形式。不要破坏现有 JSON/API/数据库兼容。

### 5.2 存储与接口

- `models.group_pricing` 保存每个模型的分组覆盖 JSON。
- `ModelMinFee` 保存在 options 中，单位为美元/次。
- `controller/model_meta.go` 提供按模型 ID 或模型名编辑价格的接口。
- 管理员在模型广场首次按名称改价时，会创建精确模型元数据，不能让自动模型刷新覆盖。
- `controller/pricing.go` 允许管理员查看所有可计费分组，包括普通用户不可自行选择的
  受限分组。

当前新增管理接口：

```text
PUT /api/models/pricing_by_name
PUT /api/models/group_pricing_by_name
PUT /api/models/:id/pricing
PUT /api/models/:id/group_pricing
```

接口必须保持管理员鉴权，不能降为普通用户可写。

### 5.3 计价优先级

维护时保持以下逻辑：

1. 先确定模型默认计费模式与价格。
2. 读取用户/Token 实际使用分组。
3. 应用模型对该分组的 `ModelGroupPricing`。
4. 分组显式计费模式优先；未配置时继承模型。
5. 绝对价格覆盖对应组件；没有覆盖的组件按模型默认价格和有效倍率计算。
6. 仅倍率覆盖必须保持旧版行为。
7. 最低费用参与预扣和最终结算。
8. 官方新增的 `OtherRatios`、动态价格和阶梯表达式只能各应用一次。

### 5.4 其他定价魔改

- `setting/ratio_setting/model_ratio.go` 解除了官方硬编码 completion ratio 的“锁定”。
  管理员配置始终优先，硬编码值只作回退，并允许配置小于 1 的补全倍率。
- default 与 classic 面板均包含模型价格后台、分组价格编辑、动态价格明细和最低费用展示。
- 管理端输入必须拒绝负数、NaN、Inf、缺少按次价格以及无效阶梯表达式。

## 6. 上游连接预热

### 6.1 行为

- `main.go` 在价格和渠道缓存初始化后启动 `service.StartUpstreamWarmupTask()`。
- 全局开关默认开启，但普通渠道还必须在渠道高级设置启用
  `upstream_warmup_enabled` 才会被自动加入预热目标。
- 默认使用非计费的 `/v1/models` 路径，不允许预热生成、聊天等可能产生费用的路径。
- 支持手动追加 URL、并发 worker、超时、间隔抖动和 HTTP/1.x 多连接预热。
- 记录连接成功、可复用成功、响应读取/关闭失败、状态码、协议和延迟。
- 渠道删除、禁用或地址变化时清理过期状态。

### 6.2 配置

系统 Option：

- `UpstreamWarmupEnabled`

渠道 `other_settings`：

- `upstream_warmup_enabled`

环境变量：

- `UPSTREAM_WARMUP_ENABLED`
- `UPSTREAM_WARMUP_URLS`
- `UPSTREAM_WARMUP_PATH`
- `UPSTREAM_WARMUP_INTERVAL`
- `UPSTREAM_WARMUP_TIMEOUT`
- `UPSTREAM_WARMUP_JITTER`
- `UPSTREAM_WARMUP_CONCURRENCY`
- `UPSTREAM_WARMUP_H1_CONNECTIONS`
- `UPSTREAM_WARMUP_UA`

状态接口：

```text
GET /api/channel/upstream_warmup/status
```

该接口必须保留渠道读取权限。状态保存在进程内，多节点部署时每个节点的预热和状态相互
独立，不能把单节点状态误解为全局状态。

## 7. 上游分段 Trace

- 使用 `net/http/httptrace` 记录 DNS、连接、TLS、连接复用、首响应字节、首个 SSE、
  首次 flush、本地预处理和整体耗时等分段信息。
- `AttachUpstreamTrace` 必须在 `client.Do` 之前调用，且不能改动请求内容或请求头。
- 全局开启或渠道单独开启均可采集；再由采样率控制成本。
- 日志写入 `logs.other.upstream_trace`，default/classic 使用日志详情均有展示。
- 传输层同时记录请求模式、实际协议、H2 池大小、分片号、选择时活跃流和待上传字节；
  不得把上游 URL、代理地址或凭证写入这些字段。
- trace 是观测功能，不得影响请求成功、计费、重试或流式转发。

配置：

- 环境变量：`UPSTREAM_TRACE_ENABLED`、`UPSTREAM_TRACE_SAMPLE_RATE`
- 系统 Option：`UpstreamTraceEnabled`、`UpstreamTraceSampleRate`
- 渠道 `other_settings`：`upstream_trace_enabled`

采样率必须限制在有效范围；关闭时应保持近似零额外行为成本。

## 8. 上游 HTTP 传输模式与 HTTP/2 分片池

`service/http_client.go` 和 `service/upstream_transport_pool.go` 的魔改包含：

- 默认上游客户端启用 HTTP/2，并配置 read-idle ping 和 ping timeout，减少失效长连接。
- 启用 TLS session cache，默认空闲连接超时为 90 秒；已有环境配置仍可覆盖。
- 普通上游客户端与 SSRF 保护的用户可控 URL 客户端保持隔离，不能为了复用而合并。
- HTTP、HTTPS、SOCKS5、SOCKS5H 代理分别缓存客户端。
- `auto` 优先协商 HTTP/2；池大小为 1 时保留共享客户端，2–64 时按渠道建立独立分片池。
- `http1` 只协商 HTTP/1.1；`hybrid` 让 GET、HEAD、无请求体和小请求走 H2，达到阈值、
  未知长度的有请求体走 H1。
- HTTP/2 分片先比较待上传字节，再比较活跃流；响应 EOF/Close、请求取消和发送失败均须
  释放计数。每个分片启用 `StrictMaxConcurrentStreams`，避免单分片自行扩出大量连接。
- 分片池只以渠道 ID、代理摘要和池大小作缓存键，不保存或暴露代理凭证；最多 256 个池，
  闲置 15 分钟回收。渠道配置变化后新请求切换新池，旧请求自然排空。
- 渠道 `other_settings.force_http1=true` 是旧配置兼容入口：仅当没有显式新模式时映射为
  `http1`。新版界面保存 HTTP/1.1 时仍同步保留该字段，支持旧镜像回滚。
- Provider 请求在发送前只选择一次协议和分片；传输错误不得在 H1/H2 间自动重放。
  AWS SDK 的最大尝试数固定为 1，带请求体的标准库请求对具体 transport 隐藏 `GetBody`。
- 重置代理缓存时，HTTP/2 与 HTTP/1.1 两套缓存都必须关闭空闲连接并清空。
- 预热必须与生效模式一致：H1 预热 H1，H2 分片逐片预热，混合模式额外预热一条 H1；
  手工 `UPSTREAM_WARMUP_URLS` 同样继承全局传输策略，计费路径仍一律拒绝。

配置优先级固定为：渠道显式值 > 系统全局 Option > 环境变量 > 编译默认值。全局空值表示
继承环境变量，渠道空值表示继承全局。相关字段：

- 渠道 `other_settings`：`upstream_http_mode`、`http2_connection_pool_size`、
  `http1_body_threshold_kib`。
- 全局 Option：`global.upstream_http_mode`、`global.http2_connection_pool_size`、
  `global.http1_body_threshold_kib`。
- 环境变量：`RELAY_UPSTREAM_HTTP_MODE`、`RELAY_HTTP2_CONNECTION_POOL_SIZE`、
  `RELAY_HTTP1_BODY_THRESHOLD_KIB`。

编译默认值为 `auto + pool=1 + 256 KiB`，因此首次部署不会改变未配置渠道的行为。生产切换
必须逐渠道灰度，不能批量关闭 HTTP/2；若某渠道的分片 H2 仍不如 H1，可长期保留 HTTP/1.1。

## 9. 模型健康状态

- 公共页为 `/health`，公共接口为 `/api/model-health/catalog|overview|series`，统一受 `HeaderNavModuleAuth("health")` 控制；顶部导航默认关闭，可配置公开或需登录。
- 主动探测（`probe`）与真实流量（`observed`）保持独立口径。主动探测决定主状态，`perf_metrics` 的请求加权成功率、延迟、TTFT 和 TPS 只作为同页佐证。
- 公共页的顶层身份仍为“公开分组 × 公开模型”，相同别名继续合并为一个汇总；可选池明细的身份为“公开分组 × 公开模型 × 公开池”，按探测目标隔离，不参与顶部可用率和状态数量，避免重复放大统计。
- 公共接口保持原 `rows` 契约，并增量提供 `catalog.pools`、`overview.pool_rows`、`series.pool_rows` 与可重复的 `pool=KEY` 筛选。池 Key 只由已经公开的分组/池别名生成，公共响应不得出现目标 ID、渠道 ID/名称或内部模型/分组。
- A/B/C 查询统一为模型与分组的二维多选，不维护三套聚合逻辑。目标可保存最多 20 个 `observed_groups`，首项同步到旧 `observed_group` 以便版本回退；多分组真实流量按请求数、TTFT 样本数和生成时长等原始分母加权，聚合前按“内部分组 × 模型”去重。
- 本站目标引用 Token ID，但选择器只列出当前 Root 明确标记、状态可用的“专用探测令牌”。标记存放在 `model_health_probe_tokens`，不复制 Key；取消标记或删除仍被目标引用的令牌会返回冲突。旧本站目标的令牌在启动迁移时自动标记。
- 本站探测通过 `ServerAddress` 发起完整请求，正常计费并写入带 `health_probe` 标记的日志；服务端 HMAC 标记在认证后删除，样本不进入 `perf_metrics`。调度执行时仍会校验专用标记，避免绕过管理接口后使用普通令牌。
- `service/text_probe_core.go` 是模型健康与渠道测试共用的文本探测语义核心；请求构建和 challenge 校验复用，渠道自动禁用/恢复仍由原渠道任务独立控制。
- 本站探测的中转错误只记录脱敏分类，不写入上游响应正文、端点或渠道名称，也不触发渠道自动禁用；普通业务流量和原渠道测试的禁用/恢复行为保持不变。
- 上游目标仅接受公网 HTTPS，禁止重定向，并使用 DNS 固定的 SSRF 客户端阻断私网、回环、云元数据与 DNS rebinding。URL 与 Key 使用版本化 AES-256-GCM 信封保存；管理接口只返回脱敏端点、`has_key` 和凭证指纹。
- 上游凭证要求显式配置稳定的 `CRYPTO_SECRET`。本站探测 HMAC 也由运行时 `CRYPTO_SECRET` 派生；多节点必须使用相同密钥。解密失败会停用目标并要求重新填写凭证。
- 公开模型/分组别名只在探测目标上维护，公共目录也只由公开目标生成。旧全局别名 Option 仅在一次性迁移中为目标空别名补值，之后保留回查且不再参与运行时展示。
- `model_health_targets`、`model_health_histories` 与 `model_health_probe_tokens` 由 GORM 迁移，支持 SQLite、MySQL 和 PostgreSQL。System Task 每分钟扫描到期目标，数据库租约跨节点去重，手动与定时任务互斥。
- 单轮多次探测由全局 `model_health_setting.multi_sample_enabled` 控制，编译默认关闭。新目标默认保存 `confirm_on_failure / 3 次 / 至少 2 次成功 / 3 秒间隔`；已有目标迁移为 `fixed / 1 / 1 / 3`，因此首次部署不会改变现有费用和探测频率。目标配置还必须满足 `次数 x 超时 + 间隔总时长 <= 调度周期`。
- 每个目标执行生成一个内部 `probe_run_id`，同一模型的尝试按序执行并分别写入 `model_health_histories`；历史同时保存执行时的模式、计划次数和最低成功数快照。旧历史没有运行 ID 时按一个单样本批次解释，公共接口和页面不返回运行 ID、目标 ID 或单次错误明细。
- `fixed` 每轮固定执行配置次数；`confirm_on_failure` 首次成功即以 `1/1` 完成本轮，首次非确定性失败后再完成剩余确认。鉴权、凭证、重定向、确定性 4xx 和 429 会提前结束；网络、超时、5xx 与无效响应继续确认。每次尝试重新生成 challenge，并只在实际 HTTP 探测期间占用全局并发槽；定时任务、手动运行和管理页验证共享同一个可动态调节的进程级 limiter，缩容时会等待已有请求退出后再放入新请求。
- 批次可用率为当轮成功数除以实际尝试数，最近 12 小时主动可用率按批次等权，不按尝试数加权。最终状态取历史阈值状态与最新批次状态的较差者，再应用原延迟 SLO；主动最小样本按完整批次数计算。固定三次时图表可出现 `0 / 33.33 / 66.67 / 100%`。同一图表桶包含多轮时，tooltip 必须同时标注轮数、尝试成功数和“每轮等权”百分比，不得用汇总尝试数重新计算曲线值。
- 本站模式的每次尝试都正常计费并带 `health_probe`，上游模式的每次尝试都消耗所填上游额度。尝试间隔不占并发槽；任务取消时整轮不落半批数据，也不更新 `last_checked_at`。公共 `overview`/`series` 仅增量公开批次数、尝试数和成功数，旧页面可继续读取原字段。
- `model_health_targets` 的池级字段为 `publish_pool_detail`、`public_pool_alias` 和 JSON `observed_channel_ids`。同一公开分组内公开池名唯一；旧目标默认不发布池明细，渠道停用或删除后保留 ID 绑定供管理页告警，池级真实流量转为空闲。
- `perf_channel_metrics` 按 `channel_id + model_name + group + bucket_ts` 保存真实上游尝试的原始计数。重试 A 失败、B 成功时分别写一次；原 `perf_metrics` 仍只写最终用户请求结果。健康探测、客户端取消及真正访问上游前的错误不进入渠道指标。
- 渠道实时桶使用 `perf-channel:v1` Redis 命名空间；无 Redis 时与原公共指标相同，只读取已完成桶。池绑定多个渠道时按尝试数、TTFT 样本数和生成时长等真实分母聚合，不平均平均值。
- 被动指标的新安装默认 5 分钟桶、30 天保留；已有明确配置继续按原值加载。Redis 部署合并当前桶的全节点计数；无 Redis 的公共页只读取已完成桶。
- `model_health_setting.pool_details_enabled` 默认关闭，但渠道指标随性能指标任务立即采集。只有全局开关和目标 `publish_pool_detail` 同时开启时才发布池目录与池行。
- default/classic 设置页均使用令牌、分组、模型和渠道搜索选择器；公共健康页使用“汇总卡片 + 默认展开池明细”，并提供分组、模型、池和状态筛选。七项指标以 `repeat(auto-fit, minmax(8rem, 1fr))` 自适应换行，TTFT 与 TPS 分开显示。趋势图固定使用主动探测绿色实线与真实流量/渠道尝试成功率蓝色虚线，图表、悬浮提示和可见图例共用同一语义映射，不能按数据首次出现顺序动态换色。两类信号必须先按时间排序、同桶去重，再使用独立数据源和独立折线渲染；公共时间轴上的空值保留并断线，不得使用跨信号 `seriesField` 或 LTTB 抽样。default 的 VChart 底部时间轴必须显式使用 `linear`，关闭 `zero` 与 `nice`，并以两类信号全部有限时间戳的首尾值固定 `min/max`；不得依赖自动推断的 `band` 轴，否则主动探测桥接缺桶后会把真实流量独有的中间时间戳追加到分类域末尾，造成蓝线横向往返。主动探测与真实流量均采用限幅的无过冲单调曲线，移除面积填充并保留轻网格和最新样本端点；单次失败应平滑下探，连续失败仍须保留真实谷底，不得为美观伪造波动。曲线顶部锚定 100%，下界按当前数据保守缩放且至少保留 12 个百分点跨度。池级蓝线的单位始终是百分比，图例必须显示“渠道尝试成功率”，不能显示表示计数的“渠道尝试次数”。
- 当前生产探测周期为 300 秒，调度抖动会让相邻检查落入非连续的 5 分钟桶。主动探测趋势只桥接前后都有有效样本的 1–2 个连续缺桶（包括 API 完全缺少时间点的情况），连续缺 3 个及以上仍断线；真实流量遇任何空桶继续断线。顶层行固定显示“公开分组名（粗体）/ 公开模型名（灰字）”，池明细保持“公开池名（粗体）/ 公开分组 · 公开模型”。
- default 趋势图的 VChart 悬浮提示固定使用 `dimension` 模式：标题显示本地化时间，内容显示“主动探测 / 真实流量（或渠道尝试成功率）”及两位小数百分比。不得重新启用端点 `mark` 的默认提示，否则数值型 `ts` 会以 Unix 秒出现在标题和条目名称中。classic 趋势图是无悬浮提示的静态 SVG。

部署验证：

1. 配置并固定 `CRYPTO_SECRET`；先在令牌页创建 Root 自有令牌，再在模型健康页将其标记为专用探测令牌，随后创建一个本站目标与一个测试上游目标。
2. 连续取得至少 3 个主动样本，确认 `/health` 的主动/被动百分比分层展示，公共 JSON/DOM 与探测系统错误日志不含内部名称、URL、Key 或响应正文；正常计费使用日志只保留计费审计所需字段并带 `health_probe` 标记。
3. 校验本站探测正常扣费、日志含 `health_probe`，同时 `perf_metrics` 请求数不被探测增加。
4. 验证定时与手动运行冲突返回已有任务，历史清理按保留天数执行。
5. 新页面连续运行稳定后再停止外部 Uptime Kuma；通用 HTTP/TCP/DNS/通知能力不由本功能复制。
6. 池级采集版首次上线时先保持全局池开关关闭。配置目标的公开分组、公开池名和渠道 ID 后，分别确认至少 30 次真实渠道尝试，再开启公共池明细；历史渠道数据不伪造回填。
7. 单轮多次探测版首次上线时保持 `multi_sample_enabled=false`，确认所有旧目标仍每轮一次。随后只选择一个非关键目标设为 `fixed / 3 / 2 / 3 秒` 并开启总开关，至少观察 24 小时的探测费用、429、超时、任务耗时和误报，再逐目标调整；不要批量覆盖现有目标。

回滚只切回上一应用镜像并保留 `perf_channel_metrics` 新表及模型健康新增列。旧镜像会忽略目标采样配置列，并把多次探测历史当作普通单样本历史读取，因此回滚前应先关闭多次探测总开关并等待正在执行的批次结束；旧 Uptime Kuma 与 `POOL_STATUS_*` Option 行是惰性历史数据，运行时不读取也不返回；回滚到旧镜像前先恢复当时的外部服务与环境备份。

## 10. Cowork / Claude Desktop 兼容

渠道开启 `cowork_adaptive_thinking_fix` 后，只对同时满足以下条件的请求处理：

- 请求使用 adaptive thinking。
- 请求中存在 Cowork、Claude Desktop 3P 等明确标记。
- 历史消息包含 `thinking` 或 `redacted_thinking` block。

处理会把旧 thinking block 转成普通文本包装，避免历史签名不兼容导致上游拒绝。正常
Claude 请求、未启用渠道以及不符合标记的请求必须原样透传。相关实现和测试位于：

- `relay/common/cowork_adaptive_thinking.go`
- `relay/common/cowork_adaptive_thinking_test.go`
- `relay/channel/claude/relay-claude.go`

## 11. 模型请求限流

该魔改修复并扩展了官方模型请求限流：

- 总请求数使用 Redis Lua 或内存滑动窗口，保证任意滚动窗口内放行数不超过硬上限。
- Redis key 使用 `rateLimit:sw:` 独立前缀，避免与旧令牌桶遗留 hash key 发生
  `WRONGTYPE`。
- 总请求数包含失败请求；成功数只在响应状态小于 400 后记录。
- 被限流响应携带 `Retry-After`。
- 支持按分组覆盖限流。
- 管理员默认跟随普通用户规则；关闭跟随后使用管理员独立总数/成功数，配置 0 表示
  该项不限制。
- 配置“成功数小于总数”只告警不阻断，因为成功数 check-then-act 在高并发下不是
  严格原子硬闸。

系统 Option：

- `ModelRequestRateLimitEnabled`
- `ModelRequestRateLimitDurationMinutes`
- `ModelRequestRateLimitCount`
- `ModelRequestRateLimitSuccessCount`
- `ModelRequestRateLimitGroup`
- `ModelRequestRateLimitAdminFollowUser`
- `ModelRequestRateLimitAdminCount`
- `ModelRequestRateLimitAdminSuccessCount`

单 `web/` 设置页必须保留管理员档配置。

## 12. 前端、会话与翻译

- 官方已收敛为单 `web/` 前端；旧 `web/default`、`web/classic` 不再是维护目标，所有本地
  定价、日志、预热、模型健康、传输、Cowork、限流和 Responses 保护设置均已迁移到
  `web/src`。
- 用户可见字符串必须走 i18n；`web/src/i18n` 的 7 个 locale 必须保持键集合一致，中文和
  繁中不得留下英文键值占位。
- 官方新版认证使用短期 access token、refresh cookie、`user_sessions` 和 `auth_flows`。
  从旧版部署到本候选版后，已有面板会话需要重新登录；不得为保留旧会话而恢复旧认证结构。
- 合并后检查自动合并产生的重复参数、重复 state、重复 import 和锁文件漂移。
- 旧双前端中的许可证头、换行、格式或构建兼容补丁不作为单前端结构下的长期魔改保留。

## 13. 不是当前已实现魔改的内容

### 13.1 服务器端缓存亲和兜底

`CACHE_AFFINITY_SERVER_SIDE_FALLBACK.md` 是设计方案，当前工作区中未跟踪，尚未实现。
现有亲和仍主要依赖下游传入的：

- Claude `metadata.user_id`
- Responses / Codex `prompt_cache_key`
- `Session_id` / `X-Session-Id` 等规则配置的请求头

不要声称系统已经能在缺少这些字段时自动从提示词推导会话，也不要把 API Token 单独
作为所有下游会话的亲和键。若未来实施该方案，应另建分支、单独评审隐私、路由、
`channel_id + multi_key_index` 绑定、Redis TTL、故障迁移和回归测试。

### 13.2 官方能力

官方系统任务、多节点实例页、SSRF、安全修复、协议转换注册表、GPT-5.6、图片断流
结算、`OtherRatios` 等不是本仓库原创魔改。同步时必须纳入并保留，但文档中不要把它们
归功于本地定制。

## 14. 上游同步规则

### 14.1 分支策略

1. 保持当前魔改分支不动。
2. 在开始时锁定官方 SHA，例如：
   `codex/sync-custom-<upstream-short-sha>`。
3. 使用普通 merge 合入锁定的 `upstream/main`，不 rebase、不强推、不直接覆盖 `main`。
4. 冲突以官方新结构为骨架，重新接回本文档的不变量和功能，不机械选择 ours/theirs。
5. 提交前检查 `git diff upstream/main..HEAD`，区分真实功能差异与许可证头、格式、翻译
   生成文件或换行噪声。

### 14.2 高冲突文件及合并重点

| 文件/区域 | 必须保留的本地语义 | 同时必须接入的官方语义 |
| --- | --- | --- |
| `service/text_quota.go` | `ChargeableTokens`、缓存去重、正负取整、最低费用、分组覆盖 | 新 usage 字段、新计费组件、溢出保护 |
| `service/usage_helpr.go` | client-gone 保守估算与观测上限 | 官方 usage 可靠性和新协议字段 |
| `service/billing_usage.go`、`dto/claude.go` | 跨协议缓存字段取最大值、防双算 | 官方 billing usage 数据结构 |
| `relay/helper/price.go` | 分组模式、绝对价格、最低费用、严格预扣 | 官方 `OtherRatios`、阶梯计费、严格溢出拒绝 |
| `types/price_data.go` | `ModelGroupPricing` 与兼容 JSON | 官方 `PriceData` 新字段和安全校验 |
| `model/pricing.go` | 分组价格缓存和展示 | 官方模型筛选、端点推导、价格元数据 |
| `controller/model_meta.go` | 模型广场直接改价接口 | 官方模型元数据 API 结构与鉴权 |
| `main.go` | 上游预热、模型健康 System Task | 官方启动顺序和新任务注册 |
| `service/http_client.go` | H2 保活、强制 H1、代理双缓存 | 官方 SSRF 客户端和重定向保护 |
| `relay/channel/api_request.go` | H1 客户端选择、trace attach | 官方重试、pinger、stream 行为 |
| `model/option.go` | 魔改配置键 | 官方 OptionMap 和新配置框架 |
| `web/src` pricing 与设置页 | 分组真实售价、后台编辑、渠道空回复和重试设置 | 官方新版价格页布局、动态价格和单前端组件结构 |
| `web/src/i18n` | 7 个 locale 键集合一致，中文、繁中完整 | 官方新键和同步工具输出 |

### 14.3 不得被回退的官方安全能力

同步冲突时至少确认以下官方能力仍存在：

- quota/token 负数和整数溢出保护。
- 严格预扣溢出拒绝与 quota saturation 审计。
- SSRF 防护及用户可控 URL 专用客户端。
- 流式断开后的可靠清理和结算，避免 stale stream writes。
- 图片、音频、任务、退款和订阅重置等新版修复。
- 协议转换注册表及 Claude/OpenAI/Gemini/Responses 新字段。
- zh-TW/i18n、安全补丁和依赖升级。

## 15. 测试与放行清单

### 15.1 后端基础检查

在仓库根目录运行：

```powershell
git diff --check
go test ./...
go build ./...
go vet ./...
```

完整 `go vet ./...` 若命中官方基线已有问题，必须对同一官方 SHA 做对照并记录；所有本地
改动相关包的定点 `go vet` 必须通过，不能笼统以“官方也有”放行新问题。

### 15.2 必须保留的定点回归

- 纯缓存写入且普通 prompt/completion 为零仍扣费。
- OpenAI cache-write、Claude 聚合/5m/1h、新旧兼容字段不会双算。
- client-gone 无完整 usage 的保守估算不漏扣也不超过观测输入。
- 可信上游 usage 不被本地估算覆盖。
- 负 token、超大 token、quota saturation、正负 correction 取整。
- 图片断流、音频明细、任务、违规费用和退款路径。
- 分组 per-token、per-request、tiered_expr、最低费用和只覆盖输出价格。
- 按请求绝对价格不重复乘分组倍率。
- `OtherRatios` 只应用一次。
- 上游预热目标过滤、代理凭证脱敏、状态清理、H1/H2 分片请求数、异常恢复。
- auto/http1/hybrid 边界、配置优先级、旧 `force_http1` 兼容、代理客户端选择、H2 keepalive、
  分片独立连接、计数释放、热切换、无传输重放和 SSRF 客户端隔离。
- upstream trace 全局/渠道开关、采样和日志挂载。
- Cowork 开关与非 Cowork 请求原样透传。
- Redis/内存滑动窗口、管理员独立档和 Redis key 隔离。
- 单前端价格 UI 的分组计算与显示。
- Responses reasoning-only 空回复的非流式 424 与流式 `response.failed` 结构。
- 空回复禁止重试、渠道 `max_retries` 的 `null/0/1..10` 语义和配置优先级。
- 有 usage、仅 `total_tokens`、usage 缺失和结算未确认时的计费下限及统计行为。

相关测试主要分布在：

```text
common/*quota*test.go
common/request_cache_control_test.go
common/limiter/*test.go
dto/billing_usage_test.go
model/pricing_*test.go
relay/helper/price*test.go
relay/common/*upstream_trace*test.go
relay/common/cowork_adaptive_thinking_test.go
relay/channel/openai/*responses*test.go
relay/helper/*stream*test.go
service/text_quota*test.go
service/*billing_session*test.go
service/quota_audio_override_test.go
service/upstream_warmup_test.go
service/http_client_test.go
service/upstream_transport_pool_test.go
service/model_health_probe_test.go
service/model_health_crypto_test.go
service/model_health_status_test.go
model/model_health_test.go
middleware/model_rate_limit_*test.go
types/price_data*test.go
```

### 15.3 前端检查

从 `web` 安装依赖后执行：

```powershell
Set-Location web
bun install --frozen-lockfile
bun run build:check
bun test
bun run i18n:sync
bun run format:check
bun run copyright:check
```

`i18n:sync` 会写文件，运行前后要审查差异，不得直接提交生成器造成的无关翻译覆盖。
全量 lint 可能包含官方基线既有差异，应额外对本次修改文件执行定点 lint，并以
`build:check` 和测试成功作为最低放行条件。

### 15.4 Docker 检查

- 必须使用仓库原始 `Dockerfile` 完整构建单 `web/` 前端和 Go 二进制，不能只以宿主机
  `go build` 代替容器构建。
- 当前 Windows 主机没有可用 Docker、Podman 或 WSL 容器运行时；
  `.github/workflows/candidate-image.yml` 在候选分支 push 后构建 `linux/amd64` OCI 归档，
  artifact 保留 7 天，不推送镜像仓库，也不部署生产。
- 工作流成功后会把 artifact 名、OCI 归档 SHA-256 和 Actions run URL 写入目标提交的
  `refs/notes/candidate-image`。使用以下命令核验 HEAD 对应回执：

```powershell
git fetch origin refs/notes/candidate-image:refs/notes/candidate-image
git notes --ref=candidate-image show HEAD
```

- 构建期间不得修改生产 Compose 或重启生产容器。

## 16. 部署、备份与回滚

部署前必须备份并下载到本机：

- PostgreSQL 全量导出。
- Compose 和 `.env`。
- 数据目录和日志。
- 当前镜像、容器、卷、网络清单。
- 备份压缩包及 SHA256，并验证本机与服务器一致。

替换生产时只修改 `new-api` 服务的镜像标签，保留数据库、Redis、卷、网络、Caddy 和
`NODE_NAME=newapi-us-1`。先运行 `docker compose config`，再启动并核对：

- 容器 `running / healthy`。
- PostgreSQL 和 Redis 健康。
- 本机及公网 `/api/status` 返回成功。
- 实际镜像标签与镜像 ID。
- 启动日志无 fatal/panic。
- 请求错误率和上游错误来源。

正常代码回滚只切回上一镜像和 Compose 备份，不恢复数据库。只有确认出现不可逆数据库
数据问题时才考虑完整恢复。任何部署动作都必须先得到用户明确确认。

## 17. 部署后计费验收

至少积累一段有代表性的真实流量后，再按明确时间窗口检查：

1. 付费日志中 `quota < 0` 的异常记录。
2. `cache_write_tokens > 0 && quota <= 0` 的漏扣候选。
3. quota saturation、NaN/Inf、整数上限或 clamp 审计标记。
4. 同一 `upstream_request_id` 的上下游记录匹配。
5. 分组覆盖请求的手算售价是否与日志一致。
6. 上游错误、客户端取消、长重试和孤立上游成本是否造成利润差异。
7. GPT-5.6 等上游尚不报告缓存写入时，`cache_write_tokens=0` 不应误报。

全站或渠道核账必须使用管理员级日志/统计接口和数据库记录，不使用只显示个人数据的
`/api/log/self` 作为全站结论。利润异常先按 `upstream_request_id` 对账，再判断是计费、
上游孤立成本、重试还是价格配置问题；不能只看倍率表猜测。

## 18. 现有辅助文档

- `CLAUDE_REVIEW_UPSTREAM_SYNC_7C28993F.md`：本轮官方同步、冲突和构建复核记录；其中
  “尚未部署”的描述是历史快照，不能当作当前生产状态。
- `CLAUDE_REVIEW_DEPLOY_CHARGEABLE_TOKENS_V5.md`：上一轮计费防漏扣部署复核。
- `DEPLOY_UPDATE_90ef595e.md`：client-gone 缓存估算爆扣问题的历史分析。
- `REVIEW_TOTALTOKENS_FIX.md`：纯缓存写入被 `TotalTokens` 清零的历史复核。
- `CACHE_AFFINITY_SERVER_SIDE_FALLBACK.md`：未实现的服务器端亲和兜底设计。
- `OPENAI_STREAM_INTERRUPTION_BILLING_RECONCILIATION.md`：OpenAI 流式中断计费历史分析。
- `SECURITY_PERFORMANCE_BILLING_AUDIT_2026-07-13.md`：安全、性能和计费审计历史记录。

除首项官方同步记录外，其余六份本地辅助文档当前仍是未跟踪文件。不要擅自删除、提交或把
其中旧部署状态当成实时事实；`output/` 也必须保持忽略，避免把生产备份或敏感配置纳入 Git。

## 19. 每轮同步后更新本文档

完成下一轮官方同步后，至少更新：

- 新官方锁定 SHA、新分支、最终代码提交。
- 魔改是否新增、删除或被官方等价替代。
- 冲突文件和实际处理结论。
- 计费不变量是否有新增协议或字段。
- 测试、Docker 构建、备份、候选镜像和回滚点。
- 部署后的真实计费观察结果。
- 尚未实现的设计是否已经转为代码。

更新文档不能替代测试和线上核验。对于计费路径，结论必须同时有代码调用链、回归测试
和真实日志三类证据。
