# bk 全生命周期 e2e 测试系统

一个**小型化**的端到端测试系统，覆盖一个应用从无到有再到销毁的完整生命周期：

```
create ──▶ generate ──▶ config ──▶ deploy ──▶ 生产测试 ──▶ destroy
```

设计上对齐仓库既有约定（进程内假后端 + 真实 cobra 命令树，**不依赖 docker / 真实 dokku / 系统 ssh**），因此默认可在 CI 直接 `go test` 跑通；同时提供环境变量门控的**真实主机模式**做真正的生产烟测。

## 组成

| 层 | 文件 | 职责 |
|----|------|------|
| 基础设施 | `internal/dokkutest/fakedokku.go` | **有状态**的进程内假 Dokku：维护应用注册表 / config / ps 状态，解析真实 dokku 子命令（`apps:create`/`apps:list`/`config:*`/`ps:*`/`apps:destroy`，含可选 `sudo dokku ` 前缀）并按状态连贯应答 |
| 驱动 | `cmd/lifecycle_e2e_test.go` | 经真实 `rootCmd`（复用 `app_integration_test.go` 的 `runApp`/`resetFlag` 缝）逐阶段驱动生命周期并断言状态迁移；含 hermetic 与真实主机两个测试 |
| 样例应用 | `e2e/sample-app/` | 最小可部署 Go HTTP 应用（监听 `$PORT`，带 `/` 与 `/healthz`）+ 模板清单 `.bk-template.yaml`。独立嵌套 module，不进入 bk 主模块包图 |

## 阶段与实现状态

| 阶段 | 驱动方式 | 状态 |
|------|----------|------|
| 1. create | `bk app create` → 远端 `apps:create`，注册表新增、`apps:list` 可见；重复创建走失败路径 | ✅ 真实命令 |
| 2. generate | 从模板渲染最小样例应用到本地（clone+变量替换的等价物） | 🟡 占位：`bk app new`（app-templates spec）落地后替换为真实命令 |
| 3. config | `bk app config:set` / `bk app config` 写入并回读生产环境变量 | ✅ 真实命令 |
| 4. deploy | `fake.Deploy` 模拟 git push 结果（标记 deployed/running） | 🟡 占位：bk 暂无 deploy/git-push（dokku-management 列为 Out），落地后替换 |
| 5. 生产测试 | hermetic：`bk app ps` 断言 Deployed/Running、`bk app logs` 断言启动日志（含 `-n N` 限行与 `-n 0` 非法值拒绝）；真实主机：对 `BK_E2E_APP_URL` 发 HTTP 探测 | ✅ 真实命令（+ 真实 HTTP） |
| 6. destroy | `bk app destroy --force` → 远端 `apps:destroy`，注册表移除、`apps:list` 不再可见；重复销毁走失败路径 | ✅ 真实命令 |

🟡 占位阶段已用进程内等价物打通骨架；随 app-templates / 部署链路落地，把对应阶段替换为真实命令即可，断言与骨架不变。

## 运行

### 默认（hermetic，无需任何外部依赖）

```bash
cd bk
go test ./cmd/ -run TestAppLifecycle_E2E -v
```

输出会打印 `──▶ 阶段 N` 轨迹，便于人读整条生命周期。

### 真实主机模式（真·生产烟测，默认 SKIP）

对一台真实 Dokku 主机跑 create → config → ps →（可选 HTTP 探测）→ destroy：

```bash
cd bk
BK_E2E_DOKKU_HOST=dokku.example.com \
BK_E2E_DOKKU_USER=dokku \
BK_E2E_SSH_KEY=$HOME/.ssh/id_ed25519 \
BK_E2E_APP_URL=https://bk-e2e-smoke.example.com/healthz \
go test ./cmd/ -run TestAppLifecycle_RealHost_E2E -v
```

环境变量：

| 变量 | 必需 | 默认 | 说明 |
|------|------|------|------|
| `BK_E2E_DOKKU_HOST` | 是 | — | Dokku 主机地址；未设置则整测试 SKIP |
| `BK_E2E_DOKKU_USER` | 否 | `dokku` | SSH 用户 |
| `BK_E2E_DOKKU_PORT` | 否 | `22` | SSH 端口 |
| `BK_E2E_SSH_KEY` | 否 | ssh-agent/默认身份 | 私钥路径 |
| `BK_E2E_APP_URL` | 否 | — | 设置后在生产测试阶段 HTTP GET 断言非 5xx |
| `BK_E2E_APP_NAME` | 否 | `bk-e2e-smoke` | 应用名 |

> 真实主机模式聚焦 bk **已实现命令**对真实主机的连通性。generate/deploy（脚手架与 git push）需要真实仓库与部署链路：把 `e2e/sample-app/` push 到 Dokku（`git push dokku main`）即可让 `BK_E2E_APP_URL` 可达，该步骤目前为人工/CI 步骤，待 app-templates 与部署链路落地后纳入测试驱动。

## 扩展

- **app-templates 落地后**：把阶段 2 的 `scaffoldSampleApp` 换成 `runApp(t, cfg, "app", "new", "go-min", appName)`，模板清单复用 `e2e/sample-app/.bk-template.yaml`。
- **app-schema-provisioning 落地后**：在阶段 1 之后加一个 schema 断言（`apps_<appname>` 已创建），并在假后端扩展对应 RPC 行为。
- **部署链路落地后**：把阶段 4 的 `fake.Deploy` 换成真实部署命令。
