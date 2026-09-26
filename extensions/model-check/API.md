# 模型智检管理员 API

本地预览地址：`http://localhost:3000/api/v1/model-check`。按 [部署说明](./DEPLOYMENT.md) 启用后，接口位于主站域名下的 `/api/v1/model-check`；下文示例中的 `http://localhost:3000` 替换为自己的主站地址即可。发布仓库不会自动启用线上服务。

生产请求统一通过 Go 主站入口。启用身份桥的智检内部端口只接受回环上的签名请求，不能绕过主站直连调用。

使用「系统设置 → 管理员 API Key」生成的全局管理员密钥，放在 **`x-api-key`** 请求头。网页登录的管理员 JWT 也可使用 `Authorization: Bearer ...`。两者同时提供时只校验 `x-api-key`，不会回退到 JWT。

管理员密钥只用于鉴权；生成 HTML 使用所选监测任务里已经配置的站内或外部模型 Key。鉴权会实时请求现有 Sub2API，密钥撤销后不能继续访问。接口不需要把管理员密钥填写到模型 Base URL、模型 Key 或图片链接中。

## 接口

以下路径均相对于上面的 API 地址。JSON 响应统一为 `{ "code": 0, "data": ... }`；HTTP 错误响应包含 `message` / `reason`。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/admin/channels` | 获取管理员已配置的监测任务、任务 ID 和参数 |
| DELETE | `/admin/channels/任务ID` | 删除监测任务及其检测记录；仅支持管理员 JWT，详见下文 |
| POST | `/admin/tests` | 提交一次检测，复用监测任务的模型 Key |
| GET | `/admin/runs?channel_id=任务ID&limit=30` | 获取检测记录；两个查询参数均可省略，`limit` 为 1–60 |
| GET | `/admin/runs/latest?channel_id=任务ID` | 获取最新完成记录，包含判定和完整图片；失败记录也会如实返回 |
| GET | `/admin/runs/结果ID` | 获取指定记录，可用于轮询进度 |
| GET | `/admin/runs/结果ID/image` | 直接返回完整 WebP 图片，`Content-Type: image/webp` |
| GET | `/admin/runs/latest/image?channel_id=任务ID` | 直接获取最新完成记录的图片；最新检测失败时不会用旧图片代替 |
| POST | `/admin/runs/结果ID/review` | 管理员人工复核作品质量；JSON 为 `{"verdict":"normal"}` / `degraded` / `clear` |

这些接口均要求管理员鉴权，包括公开记录的管理员接口和图片接口。图片请求也要带相同请求头。删除任务只接受管理员 JWT，其余上表接口支持全局管理员 API Key 或管理员 JWT。能读取管理员监测记录及公开作品；用户个人自测的私有权限仍按所有者判断。单次 API 检测是否公开，遵循所选监测任务的公开设置。

### 监测任务预览

`GET /admin/channels` 每项包含 `latest`（最新检测，可能仍在执行或已失败）和 `preview`（最近一条已完成且有图片的记录摘要，无图时为 `null`）。`preview.thumbnail` 是 WebP 缩略图 Data URI，同时包含 `id`、`status`、`created_at`、`finished_at` 等记录字段，不包含完整 `image` 或 `html`。显示预览时请同时标注这条记录的时间，不要把上一次图片当作当前正在执行或失败的检测结果。

### 删除监测任务

```sh
curl --request DELETE 'http://localhost:3000/api/v1/model-check/admin/channels/任务ID' \
  -H 'Authorization: Bearer 管理员JWT'
```

无请求体。成功返回 HTTP 200：`{ "code": 0, "data": { "deleted": true, "id": "任务ID" } }`。删除将立即停止后续调度，并在同一事务中移除该任务的配置、加密模型 Key、所有检测记录和基准图片；个人自测以及其他任务不受影响。运行中或排队中的任务返回 `409 already_running`，需要等待本次检测结束后再删除。任务不存在或已经删除时返回 `404 not_found`。仅提供 `x-api-key` 不能删除任务，需使用管理员登录 JWT。

删除不清除 24 小时内的 API 幂等记录，防止重复计费；已删除的任务不能继续提交检测，原 `channel_id` 返回 `404 not_found`。

## 发起一次检测

先用 `GET /admin/channels` 取得所需任务的 `id`，作为 `channel_id`。任务可以暂停定时执行，手动 API 检测仍可使用它；调用一次会按模型接口的实际用量计费。

```sh
curl 'http://localhost:3000/api/v1/model-check/admin/channels' \
  -H 'x-api-key: admin-你的管理员密钥'
```

请求体最少只需 `channel_id`。下面的例子等待最多 120 秒，完成后会直接返回结果及图片：

```sh
curl --request POST 'http://localhost:3000/api/v1/model-check/admin/tests' \
  -H 'x-api-key: admin-你的管理员密钥' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: check-20260923-001' \
  --data '{"channel_id":"替换为任务ID","wait_seconds":120}'
```

`Idempotency-Key` 的实际值只能由英文字母、数字、`.`、`_`、`:`、`-` 组成，长度 1–128。每次新的检测用一个新值，同一次检测发生网络超时或重试时复用原值。

可选参数如下；覆盖参数只作用于这一轮，不修改定时任务配置。两种题目均由服务端生成提示词，不接受自行传入 `prompt`。

当前题目版本为 `svg-observation-v3`。鹈鹕骑行使用新版题目与离线 SVG 动画约束；随机创作正文保持不变。每条记录保留实际 `prompt`、`prompt_hash`、`prompt_version` 和 `seed`。旧版鹈鹕基准与新题目的哈希不同，不能用于新题目基准比较，应在新版结果中重新设置基准。

| 字段 | 说明 |
| --- | --- |
| `channel_id` | 必填，监测任务 ID |
| `wait_seconds` | 等待时间，整数 0–120；默认 0，立即返回任务 ID |
| `topic` | `pelican`（鹈鹕骑行）或 `creative`（随机创作）；默认使用任务设置 |
| `model` | 模型名称；默认使用任务设置 |
| `protocol` | `responses` 或 `chat`；默认使用任务设置 |
| `reasoning` | `default` / `low` / `medium` / `high`；默认使用任务设置 |
| `max_tokens` | 输出上限，1024–16000；默认使用任务设置 |

### 响应与图片

- **HTTP 202**：已接受但尚未完成。`data.id` 为结果 ID，`data.result_url` 为查询路径；`Retry-After: 2` 建议每 2 秒查询。等待超时返回 202，任务继续执行，无需重新发起检测。
- **HTTP 200**：提交等待结束后检测已完成，或成功查询到记录。**HTTP 200 不代表画面通过**，请判断 `data.status` / `data.assessment`。
- `status`：`queued` → `generating` → `rendering` → `normal`（通过）/ `review`（待复核）/ `failed`（请求或渲染失败）。
- 完成记录的 `image` 是 `data:image/webp;base64,...`；`image_url` 是可下载图片的相对路径，需带管理员鉴权访问。失败或尚未生成图片时两者为 `null`。
- `assessment` 包含总分、四项画面检查分、判定依据和基准变化；`prompt` / `conditions` 保留本次使用的题目。
- `tps` = `usage.output_tokens / (generation_ms / 1000)`，单位 tokens/s。`generation_ms` 仅为最终成功那次请求的耗时，包含该次首字等待；不计先前失败尝试、重试等待、排队或渲染时间，也不是流式首字后的解码速度。`total_ms` 包含所有尝试、重试等待和渲染，不含排队。缺少有效耗时或用量、以及本地示例时 TPS 为 `null`；旧记录有耗时与用量的也能计算。

返回字段示例（图片 Base64 省略）：

```json
{
  "code": 0,
  "data": {
    "id": "结果ID",
    "channel_id": "任务ID",
    "completed": true,
    "status": "normal",
    "model": "所选模型",
    "topic": "creative",
    "group_name": "所选分组",
    "generation_ms": 112200,
    "usage": { "input_tokens": 428, "output_tokens": 5229 },
    "tps": 46.60427807486631,
    "assessment": { "score": 100, "verdict": "normal", "method": "render-rules-v1", "semantic_review": "human_required" },
    "image": "data:image/webp;base64,...",
    "result_url": "/api/v1/model-check/admin/runs/结果ID",
    "image_url": "/api/v1/model-check/admin/runs/结果ID/image"
  }
}
```

查询及下载：

```sh
curl 'http://localhost:3000/api/v1/model-check/admin/runs/结果ID' \
  -H 'x-api-key: admin-你的管理员密钥'

curl --fail 'http://localhost:3000/api/v1/model-check/admin/runs/结果ID/image' \
  -H 'x-api-key: admin-你的管理员密钥' \
  --output model-check.webp
```

完整图片也可以直接把 JSON 的 `image` 字段在逗号后进行 Base64 解码保存。不要将管理员密钥作为图片 URL 的查询参数。

## 重试、保留与错误

### 暂时性上游错误的自动重试

一次检测最多尝试 **3 次（初次 + 2 次重试）**，两次等待分别为 **2 秒和 5 秒**。仅重试上游 HTTP `429`、`502`、`503`、`504`、`520`–`524`，以及 `upstream_timeout`、`upstream_disconnected`、`connection_failed`。HTTP 500 不在重试白名单。鉴权/参数错误、输出截断、缺少 HTML、渲染失败、画面待复核或质量下降均不重试。

每次上游请求超时上限为 **600 秒**，三次请求及重试等待最多约 **1807 秒**，另外可能有排队、连接准备和渲染耗时。`wait_seconds` 仍最多 120 秒；提交等待超时后应继续轮询原任务。

所有尝试复用同一结果 ID、题目、随机种子、模型参数和内存中的模型 Key，不创建新的检测记录；等待重试时仍为 `generating`。同任务的并发和 API 幂等保护在整个重试周期内持续有效。服务关闭时停止后续重试，硬重启后将中断记录标记为 `failed / service_restarted`，不会恢复或自动重放上游调用。

新增字段在列表和详情中均可读取：

| 字段 | 含义 |
| --- | --- |
| `attempt_count` | 已开始的尝试次数，排队时为 0；旧记录无数据时为 `null` |
| `max_attempts` | 新检测为 3，本地示例为 0；旧记录为 `null` |
| `retry_at` | 下一次重试预计开始的 Unix 毫秒时间；非等待状态为 `null` |
| `last_attempt_error` | 最近一次失败的稳定错误码；重试成功后仍保留此前失败原因，无失败为 `null` |
| `attempts` | 已结束的尝试摘要数组，每项含 `attempt`、`started_at`、`finished_at`、`duration_ms`、`error`、`usage`；不含凭证或原始错误正文 |
| `usage_scope` | 新检测为 `successful_attempt`，表示顶层 `usage` 仅统计成功那次请求；示例和旧记录为 `null` |

失败尝试的用量无法可靠取得，`attempts[].usage` 为 `null`，并不代表没有消耗额度。自动重试可能产生额外模型费用，顶层 `usage` 与 TPS 不代表全部尝试的总消耗或总耗时。

### 客户端重试与记录保留

`Idempotency-Key` 可选，去重期限 **24 小时**。同一个键与相同任务及参数返回同一条记录；若更换参数或任务配置则返回 `409 idempotency_conflict`。24 小时内即使结果已被保留策略清理，也只返回 `410 result_expired`，不会自动再次生成。超过 24 小时后该键会被视为新请求。未提供此请求头时，每次提交都代表一次新检测。

每个监测任务保留最近 30 条结果，基准另外保留；全局最多 600 条完成记录。请及时保存需要长期留存的图片。去重记录和任务一起保存在本地 SQLite，重启后仍有效；运行中被硬性中断的检测不会自动重发。

| HTTP | `reason` | 含义 |
| --- | --- | --- |
| 400 | `channel_required` / `invalid_request` / `invalid_topic` / `invalid_wait_seconds` | 参数缺失或不符合接口约定，未发起检测 |
| 401 | `invalid_admin_key` / `login_required` | 缺少或无效的管理员鉴权 |
| 403 | `admin_required` | 使用了普通用户的网页登录凭证 |
| 404 | `not_found` / `image_unavailable` | 记录不存在、不可读，或该次检测没有图片 |
| 409 | `already_running` | 同一监测任务已有检测正在执行 |
| 409 | `result_not_ready` | 检测未完成，暂时不能下载图片 |
| 409 | `idempotency_conflict` | 同一个幂等键被用于不同参数或已变更的任务配置 |
| 410 | `result_expired` | 幂等键对应的图片/结果已过期，未重新调用模型 |
| 429 | `queue_full` | 执行与等待队列已满 |
| 503 | `account_service_unavailable` | 主服务不可用，无法确认管理员身份 |

画面分数是基础规则初筛，不是模型身份认证或真实智商分数；接口或渲染失败会记为 `failed`，不会被当成“降智”。`normal` 只表示基础检查通过，质量需要单独复核。

## 作品质量复核

`POST /admin/runs/{id}/review` 支持管理员 JWT 或全局管理员 API Key，仅适用于已完成且有图片的监测作品，不支持个人自测。请求体只接受 `verdict`：`normal` 表示人工确认正常，`degraded` 表示人工标记退步，`clear` 撤销当前复核。成功响应返回完整 `apiRun`，不会重新调用模型。

结果新增 `quality_review: { verdict: "normal" | "degraded", reviewed_at: 毫秒时间戳 } | null`，与 `status` / `assessment` 的基础规则结果分别保存。null 表示质量未复核，不能算作质量通过。修改和撤销保留服务端审计记录，公开响应不包含审核人身份。将基准作品标记退步会清除该基准。

非法复核参数返回 `400 invalid_review`；执行中、失败、无图或非监测作品返回 `409 result_not_reviewable`（不可读记录仍返回 404）。未复核作品不会被自动判为质量正常。

管理员详情页可以从历史记录设置符合当前检测条件的基准。提示词、模型、推理强度、输出上限、协议、分组和密钥来源必须匹配，才计算规则分差；不同参数的作品只能作视觉参考。基础规则满分不代表角色、题意与动作均正确。
