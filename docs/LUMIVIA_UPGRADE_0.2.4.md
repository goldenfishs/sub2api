# Lumivia 0.2.4 升级说明

同步官方稳定版 `v0.2.4`（`d681d0798064ee0ffff376d19687d12f09fe600f`），
以及其后的唯一版本同步提交 `98d86915becae9fe9491a91ffc6defd5235c8d2b`。
相对 0.2.3 纳入 MiniMax、Image 2.5、长流 HTTP/2 保活及官方修复。

## 定制保留

周限额手动重置、独立自动扣时开关、提前跳周同步推进月周期、事务行锁、缓存维护、
后台停机及 Fork 更新来源均保持原实现。订阅 UI、原首页和已执行订阅迁移不变。

## 迁移和回退

- 本次只有新增 `237_add_minimax_platform.sql`，将四处平台 CHECK 扩展为包含 MiniMax
  的超集。没有业务行更新、列删除或更名，不修改订阅期限、窗口和额度。
- 已执行 235/236 和定制 `235_subscription_auto_advance_week.sql` 不改名、不改校验和。
- 2026-09-09 生产预检：四处约束是官方原集合；`proxies.backup_proxy_id` 无唯一索引，
  `groups.model_allowlist` 和 `user_subscriptions.auto_advance_week` 均存在。
- 从本次 0.2.4 回退到刚保存的 0.2.3 应用快照时，扩展后的 CHECK 可保留，无需反向 SQL。
  如果升级后已创建 MiniMax 账号、分组、路由或监控，旧版不具备对应功能，回退前须评估这些配置。
- 不要套用旧版 0.2.1 的列名回退步骤；只有回退跨越 0.2.1 时才参考
  [0.2.3 说明](LUMIVIA_UPGRADE_0.2.3.md)。不要删除迁移记录或恢复整库来追平版本号。

## 运行行为

- 官方改为由运行时日志配置独立控制系统日志保留天数；普通 HTTP access 日志默认不再
  复制入 PostgreSQL，警告、错误和审计日志仍保留。业务用量记录不受此设置影响。
- 生产已显式关闭自动清理，运行时日志保留为 30 天；此次升级继续尊重现有配置。
- 官方新增 `SUB2API_IMAGES_MAIN_MODEL`，缺省使用后端内置的 `gpt-5.6-luna`。
  现有生产 Compose 无需整体替换；如以后需要覆盖该参数，单独增加环境变量。

## 发布约定

使用 PR 合并保留上游祖先，等待完整 CI、Lumivia verify 与 publish 成功，
再以本 Fork 固定 SHA 镜像通过 `deploy/lumivia-update.sh` 备份并部署。
上线后核对版本、登录后的分组和订阅查询以及迁移记录，不使用真实订阅试扣时。
