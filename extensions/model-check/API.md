# 模型智检管理员 API

本地地址：`http://localhost:3000/api/v1/model-check`（也可直接访问本地服务 `http://127.0.0.1:8096/api/v1/model-check`）。本功能尚未部署到线上。

使用「系统设置 → 管理员 API Key」生成的全局管理员密钥，放在 **`x-api-key`** 请求头。网页登录的管理员 JWT 也可使用 `Authorization: Bearer ...`。两者同时提供时只校验 `x-api-key`，不会回退到 JWT。

管理员密钥只用于鉴权；生成 HTML 使用所选监测任务里已经配置的站内或外部模型 Key。鉴权会实时请求现有 Sub2API，密钥撤销后不能继续访问。接口不需要把管理员密钥填写到模型 Base URL、模型 Key 或图片链接中。

## 接口

以下路径均相对于上面的 API 地址。JSON 响应统一为 `{ "code": 0, "data": ... }`；HTTP 错误响应包含 `message` / `reason`。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/admin/channels` | 获取管理员已配置的监测任务、任务 ID 和参数 |
| POST | `/admin/tests` | 提交一次检测，复用监测任务的模型 Key |
| GET | `/admin/runs?channel_id=任务ID&limit=30` | 获取检测记录；两个查询参数均可省略，`limit` 为 1–60 |
| GET | `/admin/runs/latest?channel_id=任务ID` | 获取最新完成记录，包含判定和完整图片；失败记录也会如实返回 |
| GET | `/admin/runs/结果ID` | 获取指定记录，可用于轮询进度 |
| GET | `/admin/runs/结果ID/image` | 直接返回完整 WebP 图片，`Content-Type: image/webp` |
| GET | `/admin/runs/latest/image?channel_id=任务ID` | 直接获取最新完成记录的图片；最新检测失败时不会用旧图片代替 |

这些接口均要求管理员鉴权，包括公开记录的管理员接口和图片接口。图片请求也要带相同请求头。能读取管理员监测记录及公开作品；用户个人自测的私有权限仍按所有者判断。单次 API 检测是否公开，遵循所选监测任务的公开设置。

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
- `tps` = `usage.output_tokens / (generation_ms / 1000)`，单位 tokens/s。它是包含首字等待的平均生成速度，不计排队或渲染时间，也不是流式首字后的解码速度。缺少有效耗时或用量、以及本地示例时为 `null`；旧记录有耗时与用量的也能计算。

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

画面分数是规则初筛，不是模型身份认证或真实智商分数；接口或渲染失败会记为 `failed`，不会被当成“降智”。
