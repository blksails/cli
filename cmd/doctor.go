/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"pkg.blksails.net/bk/internal/auth"
	"pkg.blksails.net/bk/internal/sshx"
)

// doctor.go 实现 `bk doctor` 诊断命令（Requirement 12.1–12.7）：依次检查
// `.bs.yaml` 可解析、当前 profile 登录态与会话有效性、（已配置 ssh 块时）SSH 主机
// 可达性。每项失败给出可执行修复建议；全部通过则零退出，存在关键失败则非零退出；
// 输出绝不含 token 明文。
//
// 设计要点（design：doctor 诊断流程）：检查逻辑被抽成纯核心 runDoctorChecks，
// 它接收完全注入的 doctorInputs（配置解析结果、会话指针+now、ssh 是否配置+config+
// 可达性探针），不触达任何真实网络或文件系统，因此单元测试可在不联网的前提下覆盖
// 全部分支。cobra 命令 doctorCmd 负责装配真实依赖：从全局 viper/auth.json 读取，
// 用 sshx.Dial（带超时）作为真实探针，并在整体非通过时返回 error 触发非零退出。

// doctorSkew 是判定会话有效性时的安全余量，与 whoami 保持一致：临近过期的会话被视
// 为已过期，避免报告一个马上失效的会话为「有效」。
const doctorSkew = 30 * time.Second

// doctorDialTimeout 是真实 SSH 可达性探针的连接超时，避免不可达主机长时间阻塞诊断。
const doctorDialTimeout = 8 * time.Second

// 检查项名称常量，供命令与测试以稳定标识引用。
const (
	doctorCheckConfig = ".bs.yaml 配置"
	doctorCheckLogin  = "登录态与会话"
	doctorCheckSSH    = "SSH 主机可达性"
)

// checkResult 是单项诊断检查的结构化结果。Skipped 为真表示该项不适用（如未配置
// ssh 块）——跳过不计为失败。Suggestion 在失败时给出可执行修复建议。Detail 不包含
// 任何 token 明文（Requirement 12.7）。
type checkResult struct {
	Name       string
	OK         bool
	Skipped    bool
	Detail     string
	Suggestion string
}

// sshProbe 是一个 SSH 可达性探针函数类型：给定连接配置，返回 nil 表示可达，否则返回
// 失败原因。把它做成可注入的函数缝，使测试可模拟可达/不可达而无需真实网络。
type sshProbe func(sshx.Config) error

// doctorInputs 汇集 runDoctorChecks 所需的全部注入输入，使纯核心不依赖全局状态、
// 文件系统或网络。
type doctorInputs struct {
	// 配置解析（Requirement 12.1）。
	ConfigOK  bool
	ConfigErr error

	// 登录态与会话（Requirement 12.2）。Session 为 nil 表示当前 profile 未登录。
	Profile string
	Session *auth.Session
	Now     time.Time

	// SSH 可达性（Requirement 12.3）。SSHConfigured 为假时跳过该项检查。
	SSHConfigured bool
	SSHConfig     sshx.Config
	SSHProbe      sshProbe
}

// runDoctorChecks 是 doctor 的可测纯核心：按 design 的诊断流程依次执行三项检查并返回
// 结构化结果与整体是否健康（overallOK）。各项检查相互独立——单项 SSH 失败不会跳过或
// 影响配置/登录态检查（Requirement 12.x / design：SSH 连接超时→doctor 仅报告该项失败，
// 不影响其它检查）。任一关键检查失败（非跳过）都会令 overallOK 为假，以便脚本据退出码
// 判定健康状态（Requirement 12.6）。
func runDoctorChecks(in doctorInputs) ([]checkResult, bool) {
	results := []checkResult{
		checkConfig(in),
		checkLogin(in),
		checkSSH(in),
	}

	overallOK := true
	for _, r := range results {
		if !r.OK {
			overallOK = false
		}
	}
	return results, overallOK
}

// checkConfig 实现 Requirement 12.1：报告 `.bs.yaml` 是否存在并能被解析。
func checkConfig(in doctorInputs) checkResult {
	if in.ConfigOK {
		return checkResult{
			Name:   doctorCheckConfig,
			OK:     true,
			Detail: "配置文件存在且可被解析",
		}
	}
	detail := "配置文件无法解析"
	if in.ConfigErr != nil {
		detail = fmt.Sprintf("配置文件无法解析: %v", in.ConfigErr)
	}
	return checkResult{
		Name:       doctorCheckConfig,
		OK:         false,
		Detail:     detail,
		Suggestion: "请检查 .bs.yaml 是否存在且为合法 YAML（默认查找主目录与当前目录），或用 --config 指定路径",
	}
}

// checkLogin 实现 Requirement 12.2：报告当前 profile 是否已登录、会话是否过期。
// 失败给出可执行建议（运行 `bk auth login`）。输出不含 token 明文（Requirement 12.7）。
func checkLogin(in doctorInputs) checkResult {
	if in.Session == nil {
		return checkResult{
			Name:       doctorCheckLogin,
			OK:         false,
			Detail:     fmt.Sprintf("profile %q 当前未登录", in.Profile),
			Suggestion: "请运行 `bk auth login` 登录",
		}
	}
	if auth.IsExpiredAt(*in.Session, in.Now, doctorSkew) {
		return checkResult{
			Name:       doctorCheckLogin,
			OK:         false,
			Detail:     fmt.Sprintf("profile %q 会话已过期（用户 %s）", in.Profile, in.Session.User.Email),
			Suggestion: "请运行 `bk auth login` 重新登录，或刷新会话",
		}
	}
	return checkResult{
		Name: doctorCheckLogin,
		OK:   true,
		Detail: fmt.Sprintf("profile %q 已登录且会话有效（用户 %s，过期时间 %s）",
			in.Profile, in.Session.User.Email,
			time.Unix(in.Session.ExpiresAt, 0).Format(time.RFC3339)),
	}
}

// checkSSH 实现 Requirement 12.3/12.5：仅当 ssh 块已配置时检查目标主机可达性；未配置
// 则标记为跳过（不计为失败）。已配置时用注入的探针执行可达性检测，失败给出检查
// ssh.host 的建议。单项 SSH 失败不影响其它检查项（由 runDoctorChecks 的独立调用保证）。
func checkSSH(in doctorInputs) checkResult {
	if !in.SSHConfigured {
		return checkResult{
			Name:    doctorCheckSSH,
			OK:      true,
			Skipped: true,
			Detail:  "未配置 ssh 块，跳过 SSH 可达性检查",
		}
	}
	probe := in.SSHProbe
	if probe == nil {
		probe = realSSHProbe
	}
	if err := probe(in.SSHConfig); err != nil {
		return checkResult{
			Name:       doctorCheckSSH,
			OK:         false,
			Detail:     fmt.Sprintf("无法连接 SSH 主机 %s:%d: %v", in.SSHConfig.Host, in.SSHConfig.Port, err),
			Suggestion: "请检查 ssh.host / ssh.port / ssh.user 与网络连通性，或确认私钥与 known_hosts 配置",
		}
	}
	return checkResult{
		Name:   doctorCheckSSH,
		OK:     true,
		Detail: fmt.Sprintf("SSH 主机 %s 可达", in.SSHConfig.Host),
	}
}

// realSSHProbe 是真实的 SSH 可达性探针：用 sshx.Dial 建立带超时的连接，成功后立即
// 关闭。它仅在 cobra 命令路径中使用；测试注入 fake 探针以避免真实网络。
func realSSHProbe(cfg sshx.Config) error {
	if cfg.Timeout == 0 {
		cfg.Timeout = doctorDialTimeout
	}
	client, err := sshx.Dial(cfg)
	if err != nil {
		return err
	}
	return client.Close()
}

// writeDoctorReport 将各项检查结果以人类可读形式写入 w，并给出整体健康汇总。失败项附带
// 修复建议（Requirement 12.4）。本函数仅打印 Name/状态/Detail/Suggestion，绝不打印
// access/refresh token（Requirement 12.7）——这些字段从不进入 checkResult。
func writeDoctorReport(w io.Writer, results []checkResult, overallOK bool) {
	fmt.Fprintln(w, "bk doctor 诊断报告")
	fmt.Fprintln(w, "==================")
	for _, r := range results {
		status := "OK"
		switch {
		case r.Skipped:
			status = "SKIP"
		case !r.OK:
			status = "FAIL"
		}
		fmt.Fprintf(w, "[%s] %s\n", status, r.Name)
		if r.Detail != "" {
			fmt.Fprintf(w, "      %s\n", r.Detail)
		}
		if !r.OK && !r.Skipped && r.Suggestion != "" {
			fmt.Fprintf(w, "      建议: %s\n", r.Suggestion)
		}
	}
	fmt.Fprintln(w, "------------------")
	if overallOK {
		fmt.Fprintln(w, "整体状态: 健康 (所有关键检查通过)")
	} else {
		fmt.Fprintln(w, "整体状态: 存在问题 (请根据上面的建议修复后重试)")
	}
}

// gatherDoctorInputs 从真实运行环境装配 doctorInputs：解析 `.bs.yaml`、读取当前 profile
// 的会话、读取 ssh 块。它把真实依赖收敛到命令边界，使 runDoctorChecks 保持纯净可测。
func gatherDoctorInputs(profile string, now time.Time) doctorInputs {
	in := doctorInputs{
		Profile:  profile,
		Now:      now,
		SSHProbe: realSSHProbe,
	}

	// (1) 配置可解析性：用独立的 viper 实例尝试读取 .bs.yaml。文件缺失或解析失败
	// 都视为该项不通过（Requirement 12.1）。
	v := viper.New()
	configureConfigSources(v, cfgFile)
	if err := v.ReadInConfig(); err != nil {
		in.ConfigErr = err
		in.ConfigOK = false
	} else {
		in.ConfigOK = true
	}

	// (2) 登录态与会话：读取当前 profile 的会话条目（Requirement 12.2）。缺失/不可读
	// 均视为未登录（Session 为 nil）。
	if cfg := lookupProfile(authConfig, profile); cfg != nil {
		s := cfg.Session
		in.Session = &s
	}

	// (3) SSH 块：仅当能成功映射出非空 host 时视为已配置（Requirement 12.3/12.5）。
	if sshCfg, err := sshConfigFrom(viper.GetViper(), profile); err == nil && sshCfg.Host != "" {
		in.SSHConfigured = true
		in.SSHConfig = sshCfg
	}

	return in
}

// doctorExitError 是 RunE 退出码映射的纯缝：把整体健康状态映射为 RunE 的返回值——
// 全部关键检查通过（overallOK 为真）返回 nil（零退出，Requirement 12.5）；存在关键
// 失败（overallOK 为假）返回非 nil error（非零退出，Requirement 12.6）。返回的 error
// 仅承载非敏感的可读消息（修复建议已在报告中给出），绝不含 token 明文（Requirement 12.7）。
func doctorExitError(overallOK bool) error {
	if overallOK {
		return nil
	}
	return fmt.Errorf("doctor: 存在关键检查未通过")
}

// doctorCmd 实现 `bk doctor`：一站式诊断配置、登录态与 SSH 可达性。整体非通过时
// RunE 返回 error，使 Execute() 以非零退出码结束，便于脚本判定（Requirement 12.6）。
var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "诊断环境健康度（配置 / 登录态 / SSH 可达性）",
	Long: `一站式检查 bk 运行环境健康度，便于快速定位问题来源。

依次检查：
- .bs.yaml 配置是否存在并能被解析
- 当前生效 profile 的登录态与会话是否有效
- 已配置 ssh 块时，目标 SSH 主机的可达性（未配置则跳过）

每项失败都会给出可执行的修复建议。全部关键检查通过时以零退出码结束，
存在关键失败时以非零退出码结束，便于脚本化判定健康状态。

输出不包含 access token / refresh token 等会话敏感字段。

示例用法：
  bk doctor
  bk doctor --profile production`,
	RunE: func(cmd *cobra.Command, args []string) error {
		in := gatherDoctorInputs(profile, time.Now())
		results, overallOK := runDoctorChecks(in)
		writeDoctorReport(cmd.OutOrStdout(), results, overallOK)
		if err := doctorExitError(overallOK); err != nil {
			// 静默返回（已在报告中给出建议），仅用非零退出码表达失败，避免 cobra
			// 再次打印冗余的 error/usage。
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			return err
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
