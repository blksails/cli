// Package dokkutest 提供一个有状态的进程内「假 Dokku 主机」，用于驱动 bk
// 应用全生命周期的端到端测试（create → config → deploy → 生产测试 → destroy）。
//
// 与 cmd/app_integration_test.go 里的「单发」假 SSH 服务端不同，本包维护一个
// 跨多次 SSH 连接持续存在的应用注册表（apps:create 登记、apps:list 列出、
// config:* 读写、ps:* 状态、apps:destroy 注销），从而能像真实 Dokku 那样对一连串
// 命令产生连贯的状态迁移——这是「全生命流程测试系统」的基础设施层。
//
// 设计：
//   - 启动一个进程内 SSH 服务端（监听 127.0.0.1:0），公钥认证，接受 Start 时
//     自生成的客户端密钥；客户端经 SSHConfig→appClient→dokku.New→sshx.Dial 的
//     真实生产路径接入（与集成测试一致）。
//   - 每个 exec 请求携带一条 dokku 命令字符串（可带 `sudo dokku ` 前缀），交由
//     dispatch 在共享状态上解释执行，按 dokku 习惯的文本格式应答并设置退出码。
//   - 不依赖 docker、外部 dokku 或系统 ssh，可在 CI 中 hermetic 运行。
//
// 本包仅被 _test.go 引用，不进入 bk 生产二进制的导入图。
package dokkutest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// fakeApp 是假 Dokku 后端中单个应用的状态。
type fakeApp struct {
	name     string
	config   map[string]string
	scale    map[string]int
	deployed bool
	running  bool
	logs     string
}

// FakeDokku 是一个有状态的进程内假 Dokku 主机。零值不可用，须经 Start 构造。
// 所有方法对并发安全（bk 每条命令新建一个 SSH 连接，可能并发）。
type FakeDokku struct {
	mu      sync.Mutex
	apps    map[string]*fakeApp
	history []string

	ln         net.Listener
	srvCfg     *ssh.ServerConfig
	clientPriv []byte // 客户端私钥（OpenSSH PEM），WriteConfig 时落盘
	wg         sync.WaitGroup
}

// Start 启动假 Dokku 主机：生成主机密钥与一对客户端密钥（服务端授权该公钥），
// 在 127.0.0.1:0 监听并开始接受连接。用完须调用 Close。
func Start() (*FakeDokku, error) {
	// 主机密钥。
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成主机密钥失败: %w", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		return nil, fmt.Errorf("构造主机 signer 失败: %w", err)
	}

	// 客户端密钥（服务端只授权这一把）。
	clientPub, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成客户端密钥失败: %w", err)
	}
	clientPEM, err := ssh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		return nil, fmt.Errorf("序列化客户端私钥失败: %w", err)
	}
	sshClientPub, err := ssh.NewPublicKey(clientPub)
	if err != nil {
		return nil, fmt.Errorf("构造客户端 ssh.PublicKey 失败: %w", err)
	}
	authorized := sshClientPub.Marshal()

	srvCfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), authorized) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("公钥未授权")
		},
	}
	srvCfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("监听失败: %w", err)
	}

	f := &FakeDokku{
		apps:       make(map[string]*fakeApp),
		ln:         ln,
		srvCfg:     srvCfg,
		clientPriv: pem.EncodeToMemory(clientPEM),
	}

	f.wg.Add(1)
	go f.acceptLoop()
	return f, nil
}

func (f *FakeDokku) acceptLoop() {
	defer f.wg.Done()
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return // 监听已关闭
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			f.handleConn(conn)
		}()
	}
}

func (f *FakeDokku) handleConn(nConn net.Conn) {
	defer nConn.Close()
	sConn, chans, reqs, err := ssh.NewServerConn(nConn, f.srvCfg)
	if err != nil {
		return
	}
	defer sConn.Close()
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "only session")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		go f.handleSession(ch, chReqs)
	}
}

type execPayload struct{ Command string }

func (f *FakeDokku) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			continue
		}
		if req.WantReply {
			_ = req.Reply(true, nil)
		}
		var p execPayload
		_ = ssh.Unmarshal(req.Payload, &p)
		stdout, stderr, status := f.dispatch(p.Command)
		if stdout != "" {
			_, _ = ch.Write([]byte(stdout))
		}
		if stderr != "" {
			_, _ = ch.Stderr().Write([]byte(stderr))
		}
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
		return
	}
}

// dispatch 在共享状态上解释一条 dokku 命令，返回 stdout、stderr 与退出码。
// 接受可选的 `sudo dokku ` 前缀（普通管理员账号路径）。
func (f *FakeDokku) dispatch(cmdline string) (stdout, stderr string, status uint32) {
	f.mu.Lock()
	f.history = append(f.history, cmdline)
	f.mu.Unlock()

	fields := strings.Fields(cmdline)
	// 剥离 `sudo dokku` 前缀。
	if len(fields) >= 2 && fields[0] == "sudo" && fields[1] == "dokku" {
		fields = fields[2:]
	}
	if len(fields) == 0 {
		return "", "!     空命令\n", 1
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch fields[0] {
	case "apps:list":
		var b strings.Builder
		b.WriteString("=====> My Apps\n")
		for _, n := range f.sortedNamesLocked() {
			b.WriteString(n + "\n")
		}
		return b.String(), "", 0

	case "apps:create":
		if len(fields) < 2 {
			return "", "!     apps:create 需要应用名\n", 1
		}
		name := fields[1]
		if _, ok := f.apps[name]; ok {
			return "", "!     Name is already taken\n", 1
		}
		f.apps[name] = &fakeApp{name: name, config: map[string]string{}, scale: map[string]int{}}
		return fmt.Sprintf("-----> Creating %s... done\n", name), "", 0

	case "apps:destroy":
		if len(fields) < 2 {
			return "", "!     apps:destroy 需要应用名\n", 1
		}
		name := fields[1]
		if _, ok := f.apps[name]; !ok {
			return "", fmt.Sprintf("!     App %s does not exist\n", name), 1
		}
		delete(f.apps, name)
		return fmt.Sprintf("-----> Destroying %s (including all add-ons)\n", name), "", 0

	case "config:show":
		a, errOut := f.requireAppLocked(fields, 1)
		if errOut != "" {
			return "", errOut, 1
		}
		var b strings.Builder
		fmt.Fprintf(&b, "=====> %s env vars\n", a.name)
		keys := make([]string, 0, len(a.config))
		for k := range a.config {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s:  %s\n", k, a.config[k])
		}
		return b.String(), "", 0

	case "config:set":
		args := fields[1:]
		if len(args) > 0 && args[0] == "--no-restart" {
			args = args[1:]
		}
		if len(args) == 0 {
			return "", "!     config:set 需要应用名\n", 1
		}
		a, ok := f.apps[args[0]]
		if !ok {
			return "", fmt.Sprintf("!     App %s does not exist\n", args[0]), 1
		}
		for _, kv := range args[1:] {
			k, v, found := strings.Cut(kv, "=")
			if found {
				a.config[k] = v
			}
		}
		return fmt.Sprintf("-----> Setting config vars for %s\n", a.name), "", 0

	case "config:unset":
		a, errOut := f.requireAppLocked(fields, 1)
		if errOut != "" {
			return "", errOut, 1
		}
		for _, k := range fields[2:] {
			delete(a.config, k)
		}
		return fmt.Sprintf("-----> Unsetting config vars for %s\n", a.name), "", 0

	case "ps:report":
		a, errOut := f.requireAppLocked(fields, 1)
		if errOut != "" {
			return "", errOut, 1
		}
		return f.psReportLocked(a), "", 0

	case "ps:scale":
		a, errOut := f.requireAppLocked(fields, 1)
		if errOut != "" {
			return "", errOut, 1
		}
		for _, kv := range fields[2:] {
			proc, cntStr, found := strings.Cut(kv, "=")
			if found {
				a.scale[proc] = atoiSafe(cntStr)
			}
		}
		return fmt.Sprintf("-----> Scaling %s\n", a.name), "", 0

	case "ps:restart":
		a, errOut := f.requireAppLocked(fields, 1)
		if errOut != "" {
			return "", errOut, 1
		}
		if a.deployed {
			a.running = true
		}
		return fmt.Sprintf("-----> Restarting %s\n", a.name), "", 0

	case "logs":
		a, errOut := f.requireAppLocked(fields, 1)
		if errOut != "" {
			return "", errOut, 1
		}
		// 兑现 dokku `logs <app> [--ps P] [--quiet] [--num N] [--tail]` 的语义。
		// 处理顺序：先按进程过滤，再按 --quiet 去前缀，最后按 --num 限行。
		// --tail 在 hermetic 假主机下退化为返回当前快照后关闭通道（不无限流）。
		out := a.logs
		if ps := strFlag(fields, "--ps"); ps != "" {
			out = filterByProcess(out, ps)
		}
		if hasFlag(fields, "--quiet") {
			out = stripLogPrefix(out)
		}
		if n := numFlag(fields); n > 0 {
			out = lastLines(out, n)
		}
		return out, "", 0

	default:
		return "", fmt.Sprintf("!     Unknown command: %s\n", fields[0]), 1
	}
}

// requireAppLocked 取 fields[idx] 指定的应用；不存在时返回 dokku 风格的 stderr。
// 调用方须持有 f.mu。
func (f *FakeDokku) requireAppLocked(fields []string, idx int) (*fakeApp, string) {
	if len(fields) <= idx {
		return nil, "!     缺少应用名\n"
	}
	name := fields[idx]
	a, ok := f.apps[name]
	if !ok {
		return nil, fmt.Sprintf("!     App %s does not exist\n", name)
	}
	return a, ""
}

func (f *FakeDokku) psReportLocked(a *fakeApp) string {
	web := a.scale["web"]
	if web == 0 && a.deployed {
		web = 1
	}
	runStatus := "stopped"
	if a.running {
		runStatus = "running"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "=====> %s ps information\n", a.name)
	fmt.Fprintf(&b, "       Deployed:                      %t\n", a.deployed)
	fmt.Fprintf(&b, "       Processes:                     %d\n", web)
	fmt.Fprintf(&b, "       Running:                       %t\n", a.running)
	fmt.Fprintf(&b, "       Restart policy:                on-failure:10\n")
	if web > 0 {
		fmt.Fprintf(&b, "       Status web 1:                  %s\n", runStatus)
	}
	return b.String()
}

func (f *FakeDokku) sortedNamesLocked() []string {
	names := make([]string, 0, len(f.apps))
	for n := range f.apps {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// --- 状态控制与观测（测试侧使用）---

// Deploy 模拟一次成功部署（代表 git push 到 dokku 的结果）：标记应用已部署、运行中，
// web 进程置 1，并写入一段示例日志。真实 `bk deploy`/git-push 落地后，部署阶段应改为
// 驱动该命令，而非调用本方法。返回应用不存在时报错。
func (f *FakeDokku) Deploy(app string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.apps[app]
	if !ok {
		return fmt.Errorf("dokkutest: 应用 %q 不存在，无法部署", app)
	}
	a.deployed = true
	a.running = true
	if a.scale["web"] == 0 {
		a.scale["web"] = 1
	}
	a.logs = fmt.Sprintf("app[web.1]: deploying %s\napp[web.1]: Listening on $PORT\napp[web.1]: ready\n", app)
	return nil
}

// Apps 返回当前已登记的应用名（已排序）。
func (f *FakeDokku) Apps() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sortedNamesLocked()
}

// HasApp 报告应用是否存在。
func (f *FakeDokku) HasApp(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.apps[name]
	return ok
}

// ConfigOf 返回应用环境变量的副本；应用不存在返回 nil。
func (f *FakeDokku) ConfigOf(app string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.apps[app]
	if !ok {
		return nil
	}
	out := make(map[string]string, len(a.config))
	for k, v := range a.config {
		out[k] = v
	}
	return out
}

// History 返回收到过的命令字符串副本（按到达顺序）。
func (f *FakeDokku) History() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.history))
	copy(out, f.history)
	return out
}

// --- 连接信息与配置写入 ---

// Addr 返回 "host:port" 监听地址。
func (f *FakeDokku) Addr() string { return f.ln.Addr().String() }

// Host 返回监听主机。
func (f *FakeDokku) Host() string {
	host, _, _ := net.SplitHostPort(f.Addr())
	return host
}

// Port 返回监听端口。
func (f *FakeDokku) Port() int {
	_, portStr, _ := net.SplitHostPort(f.Addr())
	return atoiSafe(portStr)
}

// WriteConfig 在 dir 下写入客户端私钥与一个最小 .bs.yaml（指向本假主机的全局 ssh 块，
// insecure:true 跳过 known_hosts，user 留空使 dokku.New 默认 "dokku"），返回配置文件路径。
// 该路径可直接作为 bk --config 的实参，使命令经真实生产路径连到本假主机。
func (f *FakeDokku) WriteConfig(dir string) (cfgPath string, err error) {
	identity := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(identity, f.clientPriv, 0o600); err != nil {
		return "", fmt.Errorf("写客户端私钥失败: %w", err)
	}
	yaml := strings.Join([]string{
		"ssh:",
		"  host: " + f.Host(),
		fmt.Sprintf("  port: %d", f.Port()),
		"  identity: " + identity,
		"  insecure: true",
	}, "\n") + "\n"
	cfgPath = filepath.Join(dir, "bs.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		return "", fmt.Errorf("写 .bs.yaml 失败: %w", err)
	}
	return cfgPath, nil
}

// Close 关闭监听并等待在途连接结束。
func (f *FakeDokku) Close() error {
	err := f.ln.Close()
	f.wg.Wait()
	return err
}

// numFlag 从 dokku 命令字段里取 `--num N` 的值；缺省或非法返回 0。
func numFlag(fields []string) int {
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "--num" {
			return atoiSafe(fields[i+1])
		}
	}
	return 0
}

// strFlag 从字段里取 `name VALUE` 形式标志的值；缺省返回空串。
func strFlag(fields []string, name string) string {
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == name {
			return fields[i+1]
		}
	}
	return ""
}

// hasFlag 判断布尔标志是否出现（如 --quiet/--tail）。
func hasFlag(fields []string, name string) bool {
	for _, f := range fields {
		if f == name {
			return true
		}
	}
	return false
}

// filterByProcess 仅保留属于指定进程类型的日志行。假主机日志形如
// `app[web.1]: ...`，进程类型取方括号内点号前的部分（如 web）。
func filterByProcess(s, ps string) string {
	prefix := "app[" + ps + "."
	out := make([]string, 0)
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(ln, prefix) {
			out = append(out, ln)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// stripLogPrefix 兑现 --quiet：去掉每行 `app[...]: ` 这样的进程名/时间前缀，
// 仅保留原始消息。无前缀的行原样保留。
func stripLogPrefix(s string) string {
	out := make([]string, 0)
	for _, ln := range strings.Split(s, "\n") {
		if ln == "" {
			continue
		}
		if _, rest, ok := strings.Cut(ln, "]: "); ok {
			out = append(out, rest)
		} else {
			out = append(out, ln)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// lastLines 返回 s 的最后 n 个非空行（保留行序，末尾补换行）。n<=0 或行数不足时
// 退化为返回全部内容。
func lastLines(s string, n int) string {
	lines := make([]string, 0)
	for _, ln := range strings.Split(s, "\n") {
		if ln != "" {
			lines = append(lines, ln)
		}
	}
	if n <= 0 || n >= len(lines) {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n") + "\n"
}

// atoiSafe 把十进制数字串转 int，非法字符止于其前（足够测试用）。
func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}
