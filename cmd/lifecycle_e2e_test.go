/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
	"time"

	"pkg.blksails.net/bk/internal/dokkutest"
)

// lifecycle_e2e_test.go 是「小型化全生命流程 e2e 测试系统」的驱动层：经真实 cobra
// 命令树（rootCmd，复用 app_integration_test.go 的 runApp/resetFlag 驱动缝）跑通一个
// 应用从无到有再到销毁的完整生命周期，并断言每一步在有状态假 Dokku 后端
// （internal/dokkutest）上产生的状态迁移。
//
// 生命周期六阶段（与目标一致）：
//  1. create  —— bk app create：远端 apps:create，注册表新增、apps:list 可见。【已实现命令】
//  2. generate —— 脚手架：从模板渲染最小样例应用到本地（clone+变量替换的等价物）。
//     注：bk app new（app-templates spec）尚未实现，本阶段以进程内模板渲染占位，
//     待该命令落地后替换为 runApp(... "app","new",...)。【占位缝】
//  3. config  —— bk app config:set / config：写入生产环境变量并回读校验。【已实现命令】
//  4. deploy  —— 部署：以 fake.Deploy 模拟 git push 结果（标记 deployed/running）。
//     注：bk 无 deploy/git-push 命令（dokku-management 明确列为 Out），故以状态迁移占位，
//     待真实部署链路落地后替换。【占位缝】
//  5. 生产测试 —— bk app ps / logs：断言应用报告 Deployed/Running 且日志可见（hermetic）；
//     真实主机模式下改为对应用 URL 发起 HTTP 探测（见 TestAppLifecycle_RealHost_E2E）。【已实现命令】
//  6. destroy —— bk app destroy --force：远端 apps:destroy，注册表移除、apps:list 不再可见，
//     且重复销毁走失败路径（非零退出）。【已实现命令】

// e2eStage 在测试输出里打一条阶段分隔，便于人读「全生命流程」轨迹。
func e2eStage(t *testing.T, n int, title string) {
	t.Helper()
	t.Logf("──▶ 阶段 %d：%s", n, title)
}

// TestAppLifecycle_E2E 是默认 hermetic 的全生命周期回归，无外部依赖（无 docker、无真实
// dokku、无系统 ssh），可在 CI 直接 go test 运行。
func TestAppLifecycle_E2E(t *testing.T) {
	fake, err := dokkutest.Start()
	if err != nil {
		t.Fatalf("启动假 Dokku 失败：%v", err)
	}
	defer fake.Close()

	dir := t.TempDir()
	cfg, err := fake.WriteConfig(dir)
	if err != nil {
		t.Fatalf("写假 Dokku 配置失败：%v", err)
	}

	const appName = "demo"

	// ---- 阶段 1：create ----
	e2eStage(t, 1, "create —— bk app create")
	if _, _, err := runApp(t, cfg, "app", "create", appName); err != nil {
		t.Fatalf("app create 应成功，实际错误：%v", err)
	}
	if !fake.HasApp(appName) {
		t.Fatalf("create 后假后端应登记应用 %q，实际未登记（apps=%v）", appName, fake.Apps())
	}
	lsOut, _, err := runApp(t, cfg, "app", "ls", "--raw")
	if err != nil {
		t.Fatalf("app ls 应成功，实际错误：%v", err)
	}
	if !strings.Contains(lsOut, appName) {
		t.Errorf("create 后 app ls 应包含 %q，实际：%q", appName, lsOut)
	}
	// 重复 create 走失败路径（Name is already taken）。
	if _, _, err := runApp(t, cfg, "app", "create", appName); err == nil {
		t.Errorf("重复 create 同名应用应失败（非零退出），实际成功")
	}

	// ---- 阶段 2：generate（脚手架占位）----
	e2eStage(t, 2, "generate —— 从模板渲染最小样例应用（app-templates 占位）")
	projectDir := filepath.Join(dir, "project")
	if err := scaffoldSampleApp(projectDir, appName); err != nil {
		t.Fatalf("脚手架渲染失败：%v", err)
	}
	mainSrc, err := os.ReadFile(filepath.Join(projectDir, "main.go"))
	if err != nil {
		t.Fatalf("脚手架应产出 main.go，实际读取失败：%v", err)
	}
	if !strings.Contains(string(mainSrc), "hello from "+appName) {
		t.Errorf("脚手架应把应用名 %q 替换进 main.go，实际：%q", appName, string(mainSrc))
	}
	if _, err := os.Stat(filepath.Join(projectDir, "Procfile")); err != nil {
		t.Errorf("脚手架应产出 Procfile，实际：%v", err)
	}

	// ---- 阶段 3：config（生产环境变量）----
	e2eStage(t, 3, "config —— bk app config:set / config")
	if _, _, err := runApp(t, cfg, "app", "config:set", appName, "FOO=bar", "PORT=5000"); err != nil {
		t.Fatalf("app config:set 应成功，实际错误：%v", err)
	}
	got := fake.ConfigOf(appName)
	if got["FOO"] != "bar" || got["PORT"] != "5000" {
		t.Errorf("config:set 后后端应持有 FOO=bar/PORT=5000，实际：%v", got)
	}
	cfgOut, _, err := runApp(t, cfg, "app", "config", appName, "--raw")
	if err != nil {
		t.Fatalf("app config 应成功，实际错误：%v", err)
	}
	if !strings.Contains(cfgOut, "FOO") || !strings.Contains(cfgOut, "bar") {
		t.Errorf("app config 回读应含 FOO/bar，实际：%q", cfgOut)
	}

	// ---- 阶段 4：deploy（部署占位）----
	e2eStage(t, 4, "deploy —— 模拟 git push 结果（部署链路占位）")
	if err := fake.Deploy(appName); err != nil {
		t.Fatalf("模拟部署失败：%v", err)
	}

	// ---- 阶段 5：生产测试 ----
	e2eStage(t, 5, "生产测试 —— bk app ps / logs 断言运行中")
	psOut, _, err := runApp(t, cfg, "app", "ps", appName)
	if err != nil {
		t.Fatalf("app ps 应成功，实际错误：%v", err)
	}
	if !strings.Contains(psOut, "Deployed:") || !strings.Contains(psOut, "Running:") {
		t.Fatalf("ps 输出应含 Deployed/Running 字段，实际：%q", psOut)
	}
	if !strings.Contains(psOut, "true") {
		t.Errorf("部署后 ps 应报告运行中（Running: true），实际：%q", psOut)
	}
	logsOut, _, err := runApp(t, cfg, "app", "logs", appName)
	if err != nil {
		t.Fatalf("app logs 应成功，实际错误：%v", err)
	}
	if !strings.Contains(logsOut, "Listening on") {
		t.Errorf("部署后 logs 应含启动日志，实际：%q", logsOut)
	}
	// logs -n 限行路径：只取最近 1 行，应为最后一行且不含更早的行。
	oneLine, _, err := runApp(t, cfg, "app", "logs", appName, "-n", "1")
	if err != nil {
		t.Fatalf("app logs -n 1 应成功，实际错误：%v", err)
	}
	if lc := strings.Count(strings.TrimRight(oneLine, "\n"), "\n") + 1; lc != 1 {
		t.Errorf("app logs -n 1 应只返回 1 行，实际 %d 行：%q", lc, oneLine)
	}
	if !strings.Contains(oneLine, "ready") || strings.Contains(oneLine, "deploying") {
		t.Errorf("app logs -n 1 应只返回最近一行（ready），实际：%q", oneLine)
	}
	// -n 非正应被命令层拒绝（Requirement 10.2 校验）。
	if _, _, err := runApp(t, cfg, "app", "logs", appName, "-n", "0"); err == nil {
		t.Errorf("app logs -n 0 应报错（行数需为正整数），实际成功")
	}
	// logs -p web 进程过滤路径：经真实 SSH→fake dokku，应只含 web 进程日志。
	psFiltered, _, err := runApp(t, cfg, "app", "logs", appName, "-p", "web")
	if err != nil {
		t.Fatalf("app logs -p web 应成功，实际错误：%v", err)
	}
	if !strings.Contains(psFiltered, "web.1") || !strings.Contains(psFiltered, "Listening on") {
		t.Errorf("app logs -p web 应返回 web 进程日志，实际：%q", psFiltered)
	}
	// logs -q 原始日志路径：应去掉 `app[web.1]: ` 前缀仅留消息。
	quietOut, _, err := runApp(t, cfg, "app", "logs", appName, "-q")
	if err != nil {
		t.Fatalf("app logs -q 应成功，实际错误：%v", err)
	}
	if strings.Contains(quietOut, "app[web.1]:") {
		t.Errorf("app logs -q 应去掉进程名前缀，实际仍含前缀：%q", quietOut)
	}
	if !strings.Contains(quietOut, "ready") {
		t.Errorf("app logs -q 应保留原始消息（ready），实际：%q", quietOut)
	}
	// logs -t 流式路径：hermetic 假主机返回快照后关闭通道，应正常含启动日志。
	tailOut, _, err := runApp(t, cfg, "app", "logs", appName, "-t")
	if err != nil {
		t.Fatalf("app logs -t 应成功，实际错误：%v", err)
	}
	if !strings.Contains(tailOut, "Listening on") {
		t.Errorf("app logs -t 应流式返回日志，实际：%q", tailOut)
	}

	// ---- 阶段 6：destroy ----
	e2eStage(t, 6, "destroy —— bk app destroy --force")
	if _, _, err := runApp(t, cfg, "app", "destroy", appName, "--force"); err != nil {
		t.Fatalf("app destroy --force 应成功，实际错误：%v", err)
	}
	if fake.HasApp(appName) {
		t.Errorf("destroy 后应用 %q 不应再登记，实际仍在（apps=%v）", appName, fake.Apps())
	}
	lsAfter, _, err := runApp(t, cfg, "app", "ls", "--raw")
	if err != nil {
		t.Fatalf("destroy 后 app ls 应成功，实际错误：%v", err)
	}
	if strings.Contains(lsAfter, appName) {
		t.Errorf("destroy 后 app ls 不应再含 %q，实际：%q", appName, lsAfter)
	}
	// 重复 destroy 走失败路径（App does not exist）。
	if _, _, err := runApp(t, cfg, "app", "destroy", appName, "--force"); err == nil {
		t.Errorf("销毁不存在的应用应失败（非零退出），实际成功")
	}

	// ---- 收尾：校验命令轨迹覆盖了生命周期关键远端命令 ----
	hist := strings.Join(fake.History(), "\n")
	for _, want := range []string{"apps:create demo", "config:set", "ps:report demo", "apps:destroy demo"} {
		if !strings.Contains(hist, want) {
			t.Errorf("命令轨迹应包含 %q，实际轨迹：\n%s", want, hist)
		}
	}
}

// scaffoldSampleApp 把内嵌的最小样例应用模板渲染到 dst 目录（变量替换 AppName）。
// 这是 generate 阶段（app-templates 的 clone+替换）的进程内等价物；模板内容与
// e2e/sample-app/ 下的磁盘样例保持同形，便于真实主机模式复用。
func scaffoldSampleApp(dst, appName string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	files := map[string]string{
		"Procfile": "web: ./{{.AppName}}\n",
		"app.json": "{\n  \"name\": \"{{.AppName}}\"\n}\n",
		"main.go": "package main\n\n" +
			"import (\n\t\"fmt\"\n\t\"net/http\"\n\t\"os\"\n)\n\n" +
			"// {{.AppName}} 是全生命周期 e2e 测试用的最小样例应用。\n" +
			"func main() {\n" +
			"\thttp.HandleFunc(\"/\", func(w http.ResponseWriter, r *http.Request) {\n" +
			"\t\tfmt.Fprintln(w, \"hello from {{.AppName}}\")\n" +
			"\t})\n" +
			"\thttp.ListenAndServe(\":\"+os.Getenv(\"PORT\"), nil)\n" +
			"}\n",
	}
	vars := struct{ AppName string }{AppName: appName}
	for name, tmpl := range files {
		t, err := template.New(name).Parse(tmpl)
		if err != nil {
			return err
		}
		f, err := os.Create(filepath.Join(dst, name))
		if err != nil {
			return err
		}
		if err := t.Execute(f, vars); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

// TestAppLifecycle_RealHost_E2E 是真实主机模式的生产烟测，默认 SKIP，仅当设置
// BK_E2E_DOKKU_HOST 时运行——对一台真实 Dokku 主机跑 create → config → ps →
// （可选 HTTP 探测）→ destroy，做真正的「生产测试」。
//
// 必需环境变量：
//   - BK_E2E_DOKKU_HOST   Dokku 主机地址
// 可选环境变量：
//   - BK_E2E_DOKKU_USER   SSH 用户（默认 dokku）
//   - BK_E2E_DOKKU_PORT   SSH 端口（默认 22）
//   - BK_E2E_SSH_KEY      私钥路径（默认走 ssh-agent / 默认身份）
//   - BK_E2E_APP_URL      若设置，则在生产测试阶段对该 URL 发起 HTTP GET 断言可达
//   - BK_E2E_APP_NAME     应用名（默认 bk-e2e-smoke）
//
// 注意：generate/deploy（脚手架与 git push）需要真实仓库与部署链路，超出本烟测范围，
// 由 e2e/README.md 描述的人工/CI 步骤覆盖；本测试聚焦 bk 已实现命令对真实主机的连通性。
func TestAppLifecycle_RealHost_E2E(t *testing.T) {
	host := os.Getenv("BK_E2E_DOKKU_HOST")
	if host == "" {
		t.Skip("跳过真实主机 e2e：未设置 BK_E2E_DOKKU_HOST")
	}

	appName := os.Getenv("BK_E2E_APP_NAME")
	if appName == "" {
		appName = "bk-e2e-smoke"
	}

	dir := t.TempDir()
	cfg := filepath.Join(dir, "bs.yaml")
	lines := []string{
		"ssh:",
		"  host: " + host,
	}
	if u := os.Getenv("BK_E2E_DOKKU_USER"); u != "" {
		lines = append(lines, "  user: "+u)
	}
	if p := os.Getenv("BK_E2E_DOKKU_PORT"); p != "" {
		lines = append(lines, "  port: "+p)
	}
	if k := os.Getenv("BK_E2E_SSH_KEY"); k != "" {
		lines = append(lines, "  identity: "+k)
	}
	if err := os.WriteFile(cfg, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("写真实主机配置失败：%v", err)
	}

	// 收尾确保销毁（即便中途失败也尽量清理）。
	defer func() {
		_, _, _ = runApp(t, cfg, "app", "destroy", appName, "--force")
	}()

	e2eStage(t, 1, "create（真实主机）")
	if _, errOut, err := runApp(t, cfg, "app", "create", appName); err != nil {
		t.Fatalf("真实主机 app create 失败：%v（stderr=%s）", err, errOut)
	}

	e2eStage(t, 3, "config（真实主机）")
	if _, _, err := runApp(t, cfg, "app", "config:set", appName, "BK_E2E=1"); err != nil {
		t.Fatalf("真实主机 app config:set 失败：%v", err)
	}

	e2eStage(t, 5, "生产测试（真实主机 ps + 可选 HTTP 探测）")
	if _, _, err := runApp(t, cfg, "app", "ps", appName); err != nil {
		t.Fatalf("真实主机 app ps 失败：%v", err)
	}
	if url := os.Getenv("BK_E2E_APP_URL"); url != "" {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			t.Fatalf("生产测试 HTTP 探测 %s 失败：%v", url, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 500 {
			t.Errorf("生产测试：%s 返回 %d，期望非 5xx", url, resp.StatusCode)
		}
	}

	e2eStage(t, 6, "destroy（真实主机）")
	if _, _, err := runApp(t, cfg, "app", "destroy", appName, "--force"); err != nil {
		t.Fatalf("真实主机 app destroy 失败：%v", err)
	}
}
