# 模型智检容器与发布

智检以独立容器运行，由同一版本的 Sub2API 转发 `/api/v1/model-check/*`。本次流水线只验证并发布 GitHub / GHCR 产物，不连接服务器部署。主应用默认关闭智检；上线时才通过 Compose 叠加配置启用。

## 固定构建与验证

`extensions/model-check/Dockerfile` 使用微软官方 `mcr.microsoft.com/playwright:v1.62.1-noble`，同时固定镜像索引摘要 `sha256:dcc5531e97840b9b5e794f2814476b21571c5124a3fca2267d73041f56e7580e`。该镜像包含 Node 24、Playwright 1.62.1 对应的 Chromium、字体和系统库；应用依赖以 `npm ci --omit=dev --engine-strict` 从 `package-lock.json` 安装。更新 Playwright 时必须同步锁文件、镜像版本与摘要，并重新验证真实渲染。

来源：[Playwright 官方容器说明](https://playwright.dev/docs/docker)、[1.62.1 官方 Dockerfile](https://github.com/microsoft/playwright/blob/v1.62.1/utils/docker/Dockerfile.noble)、[微软镜像元数据](https://mcr.microsoft.com/v2/playwright/manifests/v1.62.1-noble)。锁定的 jsdom 依赖要求 Node `^22.22.2 || ^24.15.0 || >=26.0.0`；手动运行时也需满足此版本条件。

PR 会执行 Go 桥接回归、前端构建和智检验证。智检测试镜像运行全部 `test/*.test.mjs`，以 `--network none` 验证真实 Chromium 动画采样、SQLite、sharp / WebP 和本地模拟接口。生产镜像另做启动、仅回环监听、健康检查、非 root、只读文件系统、桥接鉴权及数据卷重建恢复验证。测试只使用临时数据和模拟 Key，不调用付费模型。

`main` 的全部验证通过后，发布两组同一提交的 Linux amd64 镜像：

| 组件 | 固定版本 | 跟随主分支 |
| --- | --- | --- |
| 主应用 | `ghcr.io/goldenfishs/sub2api:sha-<40 位提交>` | `ghcr.io/goldenfishs/sub2api:lumivia-latest` |
| 智检 | `ghcr.io/goldenfishs/sub2api-model-check:sha-<相同提交>` | `ghcr.io/goldenfishs/sub2api-model-check:lumivia-latest` |

每组镜像都有对应的 GitHub Actions `.tar.gz` 归档与 SHA-256 校验文件。发布不会自动部署；生产配置使用相同提交的固定标签，避免两个 `lumivia-latest` 在发布期间先后变化。部署前应确认整个发布任务成功，并核对两幅镜像的 `org.opencontainers.image.revision` 标签。

## 叠加到现有 Compose

使用 Docker Compose 2.20 或以上。保留现有生产 `docker-compose.yml`、应用镜像覆盖文件、`.env`、数据库与 Redis 配置，将 `deploy/docker-compose.model-check.yml` 作为最后一层加入原文件列表。此文件只追加桥接环境变量和 `model-check` 服务，不设置主应用、PostgreSQL 或 Redis 镜像。

在现有 `.env` 中加入以下两项；主应用镜像由现有发布配置选定为相同提交：

```dotenv
MODEL_CHECK_IMAGE=ghcr.io/goldenfishs/sub2api-model-check:sha-<与主应用相同的40位提交>
MODEL_CHECK_BRIDGE_SECRET=<独立随机密钥>
```

用 `openssl rand -hex 32` 生成专用桥接密钥，保存后限制 `.env` 为 0600。有效密钥为 32–256 个不含空格的可见 ASCII 字符；Go 与 Node 必须使用同一个值。它与 SQLite 的 `encryption.key` 用途不同，不复用 JWT Secret 或模型 Key。

例如现有部署已经使用 `docker-compose.override.yml` 时，先做不启动服务的合并检查：

```sh
docker compose --env-file .env \
  -f docker-compose.yml -f docker-compose.override.yml \
  -f docker-compose.model-check.yml config --quiet
```

没有原覆盖文件时省略对应 `-f`，有其他生产配置时保留其原有顺序。不要把完整 `config` 输出写进日志，它包含环境密钥。后续拉取、启动、检查和回滚都应沿用这份完整文件列表及原 Compose 项目名。

主站容器内保持 `8080`；智检使用 `network_mode: service:sub2api`，仅监听该网络命名空间的 `127.0.0.1:8096`，不发布端口。智检验证账户走 `http://127.0.0.1:8080`，站内模型请求走 `http://127.0.0.1:8080/v1`。外部只访问原主站地址，主站负责转发和签名真实客户端 IP / User-Agent。`MODEL_CHECK_ENABLED=true`、回环上游和同值桥接密钥由叠加文件同时配置；`MODEL_CHECK_DEMO=0` 固定关闭本地示例。

健康检查请求 `http://127.0.0.1:8096/api/v1/model-check/health`，只在容器回环上免桥接签名，其余直接请求均被拒绝。主站未启用桥接时不显示智检导航；主站启用但伴随服务失效时返回服务不可用，不返回前端页面代替 API 响应。

模型生成请求最多等待 600 秒，适用于高推理强度和较长 HTML 输出。账户鉴权仍使用独立的 6 秒超时。管理员接口的 `wait_seconds` 最多为 120 秒；等待结束后返回任务状态，可继续轮询，不会终止后台生成。

Responses 与 Chat Completions 均以 `stream: true` 请求上游，服务端增量接收 SSE，完整生成后再渲染；Chat 同时请求最终 usage，继续提供 Token / TPS 统计。兼容返回普通 JSON 的上游，不会自动重发或回退付费请求。Responses 必须收到完成事件，Chat 必须收到正常结束原因及 `[DONE]`；中途报错、输出截断或缺少结束标记均记为失败。流式网络数据上限 16 MB，单事件和累计输出文本上限各 2 MB，总超时不会被心跳重置。

流式可以减少长时间不返回完整结果触发的代理超时，但仍依赖上游及时发送数据或心跳；不能解决上游拒绝检测、Key 分组权限不足等错误。

## 数据与资源

`model_check_data` 命名卷挂载到 `/app/data`，同卷保存 `model-check.sqlite`、SQLite 的 WAL / SHM 文件及权限为 0600 的 `encryption.key`。容器固定以 UID / GID `10001:10001` 运行，镜像预设新卷目录所有权；若改用绑定目录，需事先创建并设置该所有权及 0700 目录权限。不要将现有开发 `.data` 挂入生产。

备份时先停止智检服务，再备份整个卷并保留文件权限；恢复时同时恢复数据库和原始加密密钥。丢失密钥后无法解密定时任务的模型 Key。不要使用 `docker compose down -v` 更新应用。主应用与智检镜像回滚应成对执行，保留此卷及桥接密钥。

默认每个智检容器最多 2 CPU、1 GiB 内存、256 个进程；禁用额外交换内存，`/dev/shm` 和临时目录各限制 256 MiB。根文件系统只读，移除 Linux capabilities，启用 `no-new-privileges` 和 init 进程。日志每个文件 10 MiB、保留 3 个，不在 Compose 配置中启用请求头或正文调试日志。可用 `MODEL_CHECK_CPUS` 与 `MODEL_CHECK_MEMORY` 调整额度；同一个数据卷只运行一个智检实例。

智检共享主应用的网络命名空间：更换主应用容器时必须同时重建智检容器，并等待主应用健康。`depends_on.restart: true` 支持 Compose 协调重启，但不能替代成对发布。现有 `lumivia-update.sh` 只更新主应用，未包含智检卷备份和成对回滚；启用此功能后，应在受控的双服务发布流程中处理这些步骤。

## 本地复现容器验证

在仓库根目录、可用 Docker context 中执行；测试创建自己的容器和临时卷，不使用生产端口、密钥或数据：

```sh
python3 deploy/tests/model-check-compose-test.py
docker build -f extensions/model-check/Dockerfile --target test -t lumivia-model-check:test .
docker run --rm --init --network none --read-only \
  --cap-drop ALL --security-opt no-new-privileges:true \
  --cpus 2 --memory 1g --memory-swap 1g --pids-limit 256 --shm-size 256m \
  --tmpfs /tmp:rw,nosuid,nodev,noexec,size=268435456,mode=1777 \
  lumivia-model-check:test
docker build -f extensions/model-check/Dockerfile --target runtime -t lumivia-model-check:runtime .
bash extensions/model-check/test/container-smoke.sh lumivia-model-check:runtime
```
