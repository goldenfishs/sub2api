# Lumivia 定制版维护

本仓库 `goldenfishs/sub2api` 的 `main` 是包含定制功能的发布来源。
`Wei-Shaw/sub2api` 仅作为源码上游。服务器使用本 Fork 的固定提交镜像；
不要改回 `weishaw/sub2api:latest`，也不要用 reset/强制同步覆盖 Fork 的 main。

## 必须保留的功能

- 用户手动重置周限额：确认后扣除本周剩余时长，清零周用量；日/月用量保持不变。
- 每个订阅独立的自动扣时重置开关，默认关闭；周限额耗尽触发，日/月耗尽时暂停。
- 事务、行锁、旧预览冲突保护以及后台停机处理。
- 数据库的 `auto_advance_week` 字段、迁移、Ent 生成文件和 DTO。
- 新增控件的紧凑布局和“重置周限额”文案；保留原有到期时间及周/月用量展示。
- 应用更新及版本回退来源为本 Fork，避免下载官方二进制覆盖定制功能。

## 官方发布新版本后

1. 在干净的本仓库运行 `bash deploy/lumivia-sync-upstream.sh`。
   脚本从 Fork 的 main 新建同步分支，再合并 upstream/main。
   冲突时会停下，保留现场；逐项处理，不能整批选择官方版本。
2. 检查 SQL 迁移编号冲突。已经发布执行的迁移文件不能修改或重命名，
   必须按数据库迁移框架设计兼容方案。检查上游是否修改额度周期、扣费和缓存行为。
3. 推送同步分支，向 **goldenfishs/sub2api:main** 提 PR。
   `Lumivia Release / verify` 检查定制测试仍然存在，并执行额度、更新来源回归和前端构建。
   测试覆盖不能代替冲突审查与升级兼容性检查。
4. 合并后 `Lumivia Release` 自动构建 amd64 镜像：
   `ghcr.io/goldenfishs/sub2api:sha-<main 的完整 40 位提交号>`。
   必须等 verify 和 publish 全部成功。`lumivia-latest` 仅方便查找，生产使用固定提交号。
5. 将该提交的 `deploy/lumivia-update.sh` 放到服务器已有 Compose 目录，运行：

   ```sh
   cd /home/ubuntu/sub2api-deploy
   sudo bash lumivia-update.sh ghcr.io/goldenfishs/sub2api:sha-完整提交号
   ```

   镜像需允许服务器拉取；若 GHCR 包是私有的，先配置仅限拉取该包的登录凭据。
   脚本先拉镜像，保存旧容器快照，然后暂停应用、备份数据库和 data，再重建应用。
   更新有短暂中断；PostgreSQL、Redis、Caddy 和其他服务不重建。
   脚本拒绝官方镜像和浮动 tag。备份保存在 `backups/lumivia-时间/`，包含敏感配置，不能提交。
6. 验证健康状态、版本、登录、订阅页面和正常 API 请求。
   不要用生产订阅试扣时；扣时测试使用本地或独立测试数据。

## 回退与部署约定

- 更新失败时脚本恢复旧应用容器快照，保留迁移后的数据库。
  当前新增迁移是附加字段与索引；未来升级仍需单独评估是否兼容旧应用。
- 数据库恢复会覆盖备份后的写入，只能在停写、明确评估数据损失后人工执行。
- 每次部署记录完整提交号和备份路径。旧镜像可能曾被容器内更新，必须保留容器快照，
  不能仅靠原镜像标签回退。
- 服务器的 `docker-compose.override.yml` 由更新脚本管理。
  日常使用默认的 `docker compose` 命令加载它；仅指定 `-f docker-compose.yml` 会漏掉覆盖文件。
- 生产以镜像发布为准，不使用网页一键替换二进制。官方更新先合并到 Fork，再构建部署。
- 不提交 `.env`、数据库、缓存、备份、SSH 凭据、本机 Colima 文件。
- CI 不会自动改动生产服务器。官方冲突与迁移兼容性需要审核，不能保证任意上游改动都自动合并。
