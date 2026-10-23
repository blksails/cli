# bk 登录保持 365 天

bk 在本地为每个 profile 保存独立的 `login_expires_at`，登录成功后有效 365 天。access token 的签名及短期到期时间仍由 Supabase 决定，未修改共享认证服务的设置。

## 行为

- 有效 access token 直接使用，临近到期或到期后通过 refresh token 换取新 token，并回写本地配置。
- 刷新保留最初的登录期限，不将 365 天不断向后滚动。
- 旧版保存的登录在首次成功使用或续期时获得一个 365 天期限，无需重新输入密码。
- 网络、502、限流或其他未明确拒绝授权的错误保留原凭据，提示稍后重试。该次业务命令仍返回失败，不用过期 token 执行请求。
- 服务端明确返回 refresh token 已失效、已使用、会话不存在或过期等错误时，提示重新登录。用户主动退出或服务端撤销授权仍然生效。
- 本地登录期限届满后，下一次认证操作要求重新登录，即使 access token 尚未到期。
- `bk auth whoami` 分别展示登录保持期限与短期 token 状态。

此次更改不承诺在服务端撤销授权或收紧会话策略后仍能保持登录。

## 验证

```sh
go test ./...
postman simulation run postman/simulations/bk-auth.sim.yaml --port 4919 --no-history --output ndjson
# 另一个终端中执行
BK_AUTH_SIMULATION_URL=http://127.0.0.1:4919/auth go test ./internal/auth -run TestRefreshAgainstSimulation -v
```

本地模拟运行真实 GoTrue 客户端，覆盖正常刷新、代理返回 HTML 502、refresh token 被撤销；测试核对失败时凭据不变。模拟依据 gotrue-go v1.2.0 的 token 请求和 Supabase 的刷新返回字段构建；Postman 未登录，因此未查到团队托管 mock，未发布任何模拟服务。

期限测试覆盖旧登录迁移、反复刷新后期限不变、期限边界，以及有效 JWT 不能绕过本地期限。

## 范围

这是一项 CLI 登录保留策略，不是服务端授权延长，也不会让已经失效的 refresh token 重新有效。365 天从新登录或旧登录首次成功迁移时起算。
