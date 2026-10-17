# M1 管理 API 契约

所有路径前缀 `/api/admin/v1`。除登录外需要 Cookie；写请求同时需要精确 Origin 和 `X-CSRF-Token`。响应禁止缓存，错误包含 `error.code`、`error.message`、`request_id`。QQ ID 始终为字符串。

| 方法 | 路径 | 请求 / 结果 |
| --- | --- | --- |
| POST | /login | `{key}` → `{data:{csrf_token}}`，HttpOnly / SameSite=Strict 会话 Cookie |
| GET | /session | 恢复内存 CSRF；闲置 30 分钟、最长 8 小时 |
| POST | /logout | 注销当前会话并删除 Cookie |
| GET | /stickers | `page,q,source,account,approval`；固定每页 24 项；返回 items,total,page,page_size,accounts,favorites_enabled |
| GET | /stickers/{id}/preview | 必填 source；QQ 必填 account；`still=1` 返回首帧 PNG，否则原图 |
| PATCH | /stickers/{id} | `{account,revision,name,description}`；只支持 QQ 收藏 |
| POST | /stickers/batch | `{approved,items:[{id,account,revision}]}`，1～50 项；逐项返回 id,account,code |
| GET | /sticker-sync | `account`；返回 enabled,busy,queued,at,received,ready,skipped,failed,error,resource_hosts |
| POST | /sticker-sync | `{account}` → 202 requested；不代表同步完成 |

素材字段：id,name,description,tags,source,account,revision,approval,cache,fetched_at。approval 为 pending / approved / disabled；cache 为 cached / missing / expired。cached 只表示有缓存元数据，不保证文件未损坏，预览和批准时重新验证。旧记录的 Approved=false 且无 Disabled 标记时显示待批准；旧记录不自动授权。

revision 为不透明字符串；每次网页 / QQ 编辑及同步发布都会改变。同批逐项执行，HTTP 200 不代表全部成功。冲突返回 conflict，其他不可批准项返回 unavailable；单项描述无效为 422。不能把批准失败当作已批准。

会话最多 16 个，登录每分钟最多 10 次（本机统一额度，不信任代理 IP）。JSON 写请求最多 32 KiB，拒绝未知字段和多份 JSON。前端 Host 与 Go Host 必须精确匹配配置；代理保留浏览器 Origin，不信任 X-Forwarded-*。

本机 HTTP Cookie 不设 Secure；不支持远程部署。任何 HTTPS / 远程扩展需独立设计，不能直接更改监听地址暴露服务。

本批不修改群连接 / 换号语义；群管理与其身份隔离契约仍须在 M2 实现前完成。未完成的事件审计、细分下载错误与部署验收不因本 API 文档存在而视为已交付。
