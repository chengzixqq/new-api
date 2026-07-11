# New API 官方同步与部署前复核记录

## 1. 当前结论

- 本轮官方基线已锁定为 `QuantumNous/new-api main` 的 `7c28993f6bd9e92616f3f578212577f8b7c40b45`。
- 魔改版已在新分支 `sync/custom-pricing-latest-7c28993f` 完成普通 merge，并已推送至 `chengzixqq/new-api`。
- 最终代码 merge commit 为 `490e617e106909a0b930ada15e51c85c53bc14c3`。
- 源码、服务器完整 Go 测试、前端定点检查和仓库 Dockerfile 构建均已通过。
- 独立生产候选镜像已经构建，但**尚未部署、尚未修改 Compose、尚未重启生产容器**。
- 当前生产仍运行回滚镜像 `new-api:chargeable-tokens-v5-20260708-001343`，状态为 `healthy`。
- 下一步必须得到用户明确确认后，才允许切换生产镜像。

## 2. 仓库与提交关系

| 项目 | 值 |
| --- | --- |
| 原魔改基线 | `d25913cf2364140761860cdd9fc7cc857ce9ce5d` |
| 合并前计费修复 | `c56f31e503f2ca85b35499a9de8c1858b6e4d8df` |
| 锁定官方提交 | `7c28993f6bd9e92616f3f578212577f8b7c40b45` |
| 最终 merge commit | `490e617e106909a0b930ada15e51c85c53bc14c3` |
| 新分支 | `sync/custom-pricing-latest-7c28993f` |
| 合并方式 | 普通 merge，未 rebase，未强推 |

原有 `sync/custom-pricing-latest-becc18e3`、`main` 及其他旧分支未被改写。四份既有未跟踪文档保持原状，没有混入本次代码提交。

## 3. 官方更新合入范围

本轮已接入锁定提交以内的官方新版结构和功能，包括：

- OpenAI `cache_write_tokens` 计费字段与缓存写入路径。
- token 负数保护、quota 饱和保护和严格预扣溢出拒绝。
- 图片流式断流后的 usage 结算与相关测试。
- GPT-5.6 及官方模型、价格和管理端更新。
- Claude、OpenAI、Gemini、Responses 等协议转换注册表重构。
- Advanced Custom 路由、模型端点推导和价格模型筛选更新。
- `OtherRatios`、动态计费表达式、动态分组展示和新版价格页结构。
- 系统实例、渠道、日志、仪表盘、订阅和其他管理端更新。

官方目标仅锁定到 `7c28993f`。部署准备结束前再次执行 `git fetch upstream main`，官方 `main` 仍为同一提交；本轮没有未纳入的新官方提交。

## 4. 保留的魔改能力

以下能力在官方新版结构上重新接回并保留：

- `ChargeableTokens` 作为计费零值保护和最低费用判定依据。
- Claude 客户端断开且缺少可信 usage 时的保守缓存写入估算，并受观测输入上限约束。
- 分组精确定价：按 token、按请求、动态阶梯表达式、最低费用及 JSON/API/数据库兼容。
- 上游预热、上游 trace、HTTP/1.1 客户端、号池状态任务。
- 渠道亲和性、Cowork 兼容和相关请求透传能力。
- default/classic 面板的中文及繁体中文翻译补全。

未增加数据库迁移，未增加生产环境变量；既有 `NODE_NAME=newapi-us-1` 部署约束保持不变。

## 5. 十三个冲突文件的处理结论

| 文件 | 合并结论 |
| --- | --- |
| `main.go` | 保留官方价格预热顺序，同时继续启动上游预热和号池状态任务。 |
| `model/pricing.go` | 保留魔改分组定价结构和有限非负校验，并接入官方 Advanced Custom 端点推导、模型筛选和新版价格元数据。 |
| `relay/helper/price.go` | 在官方严格 quota 转换与 `OtherRatios` 基础上接回分组覆盖；预扣分别计算输入和预计输出，并包含最低费用。 |
| `relay/helper/price_test.go` | 同时保留官方阶梯预扣/溢出测试与魔改分组定价测试，并补充分组覆盖溢出、输出单独预扣和最低费用测试。 |
| `service/text_quota.go` | 合并官方 cache-write/负数保护与魔改精确结算；统一使用可计费 token 判空、饱和求和及正向向上取整。 |
| `service/text_quota_test.go` | 保留双方回归用例，并增加负 correction、负 usage、缓存桶最大值、整数溢出和 OpenAI cache-write 精确结算测试。 |
| `types/price_data.go` | 保留 `ModelGroupPricing`、分组覆盖和公共 quota 工具，同时接入官方 decimal `OtherRatios` 计算。 |
| `web/default/src/features/pricing/components/model-card-grid.tsx` | 保留选择分组向模型卡传递，适配官方新版卡片接口。 |
| `web/default/src/features/pricing/components/model-card.tsx` | 使用官方新版价格摘要布局，同时按选中分组解析实际计费模式及覆盖价格。 |
| `web/default/src/features/pricing/components/model-details.tsx` | 分组明细继续展示缓存写入、图片、音频和按请求覆盖，并适配官方新版表格结构。 |
| `web/default/src/features/pricing/components/pricing-columns.tsx` | 列表价格以具体分组的实际模式和覆盖价格为准，并保留官方动态价格展示。 |
| `web/default/src/features/pricing/lib/dynamic-price.ts` | 动态表达式继续支持分组表达式、分组阶梯和有效分组倍率，避免退回仅模型级倍率。 |
| `web/default/src/features/pricing/lib/price.ts` | 保留分组绝对价格、最低分组价格、固定价和 token 价的统一展示算法，并接入官方新版类型。 |

另外检查并清理了 `pricing-table.tsx` 与 `pricing/index.tsx` 自动合并产生的重复参数/重复状态；这两个文件不是 Git 报告的冲突文件。

## 6. 计费不变量

Claude 复核时应逐条验证以下红线：

1. 可信上游 usage 存在时按真实 usage 结算，不用本地估算覆盖它。
2. usage 缺失且连接异常时可保守估算缓存写入，但估算不得超过已观测输入上限。
3. `PromptTokens`、`CompletionTokens`、缓存读取、缓存写入、图片和音频中任一可计费项大于零时，不能被 `TotalTokens == 0` 的旧保护漏掉。
4. Claude 聚合缓存写入与 5m/1h 拆分取有效最大值；5m/1h 内部兼容字段也分别取最大值，不能重复相加收费。
5. OpenAI 原生字段与 Claude 转换字段取有效最大值，不能重复收费。
6. 上游负 token 一律按零处理；token 求和使用饱和加法，不能因整数溢出回绕为零或负数。
7. 正向结算 quota 只在最终统一向上取整一次，避免小数被截断少扣；负 correction 向零取整，避免多退。
8. 预扣、结算、违规费用和分组覆盖均使用受检 quota 转换；溢出时拒绝或饱和并留审计信息，不能静默回绕。
9. 按 token 的分组预扣分别使用输入价和预计输出价；仅配置输出覆盖时，输入仍按模型倍率和分组倍率预扣。
10. 分组最低费用必须进入预扣与最终结算；按请求绝对价格不得再次乘分组倍率。
11. `OtherRatios` 只在对应官方计费路径应用一次。
12. 图片断流、音频明细、阶梯计费、退款/补扣路径不得绕过上述保护。

## 7. 本轮额外计费加固

合并复核中额外发现并处理了以下边界问题：

- 分组按 token 和按请求覆盖的预扣改为严格溢出检测。
- 分组 token 预扣由只看输入价改为输入与预计输出分别计算。
- 分组最低费用纳入预扣，降低请求完成后余额反向穿透的风险。
- 上游负 token、缓存拆分和总可计费 token 统一做非负与饱和处理。
- Claude 新旧 5m/1h 字段取最大值，聚合缓存写入与拆分总和取最大值。
- 音频计费对负明细做归零，并按聚合/明细有效最大值判断是否可计费。
- 负 correction 的小数取整由远离零修正为向零。
- 违规费用移除未经检查的直接 `IntPart()` 转换。
- OpenAI cache-write 例 `4884.1` 按自定义“正向最终向上取整”策略结算为 `4885`，防止少扣。

## 8. 验证结果

### 8.1 Go 与仓库检查

| 检查 | 结果 |
| --- | --- |
| `git diff --check` | 通过 |
| 本机 `go test ./...` | 通过，30 个包，0 失败 |
| 服务器隔离源码 `go test ./...` | 通过 |
| `go build ./...` | 通过 |
| 定点 `go vet ./dto ./model ./relay/helper ./service ./types` | 通过 |
| 完整 `go vet ./...` | 存在 17 个官方基线问题；涉及 IPv6 格式、复制 `sync.Mutex` 和旧适配器不可达代码。报告文件均与 `upstream/main` 无魔改差异。 |

### 8.2 前端与 i18n

| 检查 | 结果 |
| --- | --- |
| default `typecheck` | 通过 |
| default 本次价格相关文件定点 lint | 通过 |
| default build | 通过 |
| classic build | 通过 |
| 服务器 Bun 定点价格测试 | 通过 |
| 服务器 i18n 同步前后哈希稳定检查 | 通过 |
| `zh` / `zh-TW` missing、untranslated | 均为 0 |

完整 default lint 仍会命中官方仓库既有告警；classic 全量 Prettier 检查仍会报告 117 个既有文件（含生成目录）。这些不是本轮改动引入，定点 lint、typecheck 和两套实际构建均已通过。

### 8.3 Dockerfile

- 在服务器隔离构建目录中使用仓库原始 Dockerfile 完整构建成功。
- Dockerfile 的 default、classic 和 Go 二进制阶段均成功。
- 构建期间生产容器指纹和 Compose SHA256 前后一致。

## 9. 源码归档与候选镜像

### 9.1 源码归档

- 本机文件：`C:\Users\15879\Documents\Codex\2026-07-05\wo\artifacts\upstream-7c28993f\new-api-490e617e-source.tar`
- 来源提交：`490e617e106909a0b930ada15e51c85c53bc14c3`
- 文件大小：`23848960` bytes
- SHA256：`ae38e50e1b585637d0ae3a4185fdd07b888c43f05975b615296adda4d74057f4`

### 9.2 候选镜像

- 标签：`new-api:upstream-7c28993f-20260712-042650`
- 镜像 ID：`sha256:ba7d9730947d6b8973b6827613f2468b6b767dc61576f04a89e16cfeddfd4b30`
- 大小：`76934919` bytes
- 构建记录：`/data/builds/new-api-upstream-490e617e-20260711-133018/PREPARED_IMAGE.txt`
- 当前运行容器数：`0`（镜像仅构建，未部署）

## 10. 部署前完整备份

- 服务器归档：`/data/backups/new-api-20260711-131339.tgz`
- 本机归档：`C:\Users\15879\Documents\Codex\2026-07-05\wo\backups\upstream-7c28993f\new-api-20260711-131339.tgz`
- 文件大小：`1449754974` bytes
- SHA256：`eb5ded9e6849dacb136d8cc40661ca4f95b17d38129becd43467945ae3c1f070`
- 本机与服务器 SHA256 一致。
- 外层 tar、PostgreSQL `pg_dumpall` gzip、数据目录 gzip 和日志目录 gzip 均已完整性校验通过。
- 备份包含数据库、Compose、运行配置、数据目录、日志及当前镜像清单；备份文件不得提交到 Git。

## 11. 当前生产指纹与回滚点

| 项目 | 当前值 |
| --- | --- |
| 生产镜像 | `new-api:chargeable-tokens-v5-20260708-001343` |
| 生产镜像 ID | `sha256:a57b9f95e328ba23fdf11ae75e96564ab281010b63972f9b2c7706ddb44aea74` |
| 生产状态 | `running / healthy` |
| Compose SHA256 | `376e494777af55c93e077dd1b12c867bdef25747c9d82f7850298d18e080f500` |
| 候选镜像是否运行 | `false` |

部署确认后只允许修改 `/data/new-api/docker-compose.yml` 中 `new-api` 服务的镜像标签。数据库、Redis、卷、网络、Caddy 和 `NODE_NAME=newapi-us-1` 必须保持不变。

出现以下任一情况应立即恢复 Compose 备份并切回 `new-api:chargeable-tokens-v5-20260708-001343`：

- 容器无法健康、状态接口异常或 PostgreSQL/Redis 不健康。
- fatal/panic 或请求错误率明显上升。
- 付费请求出现缓存写入大于零但 quota 为零、负 quota、异常饱和或分组价格不一致。

正常镜像回滚不恢复数据库；只有确认发生不可逆数据库数据问题时，才考虑使用完整备份恢复。

## 12. Claude 建议复核清单

1. 以 `490e617e` 为代码基准，不要改写旧分支历史。
2. 检查上述 13 个冲突文件及 `pricing-table.tsx`、`pricing/index.tsx` 的自动合并清理。
3. 重点复核 `effectiveBillingUsage` 到 `calculateTextQuotaSummary`、分组覆盖、阶梯结算、音频和图片断流的完整调用链。
4. 验证缓存聚合/拆分/转换字段只取有效最大值，且不会双算。
5. 验证所有 quota 转换的正负取整、溢出和退款语义。
6. 不读取或提交备份中的敏感配置，不在文档、Git 或命令记录中写入凭据。
7. 在用户明确确认之前，不修改生产 Compose，不重启或替换生产容器。

## 13. 部署后待补记录

本节必须在实际部署和至少 30 分钟计费观察完成后补写：

- 实际运行镜像标签、镜像 ID 和启动时间。
- 本机与公网 `/api/status` 验证结果。
- PostgreSQL、Redis、容器健康和错误日志结果。
- 30 分钟计费日志抽样：cache-write 非零/zero quota、负 quota、异常 saturation、分组倍率手算。
- 是否触发回滚及最终回滚点。
