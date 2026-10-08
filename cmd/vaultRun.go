package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"pkg.blksails.net/bk/internal/vault"
)

var processEnvironmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// processOptions 描述子进程继承的运行上下文。Env 由调用方提供父进程环境，Vault
// secrets 在 runVaultRun 内合并并覆盖同名变量；其余字段原样传给目标进程。
type processOptions struct {
	Env    []string
	Dir    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// processRunner 隔离 os/exec，使解密与注入逻辑可以在不创建真实进程的条件下测试。
type processRunner interface {
	Run(ctx context.Context, name string, args []string, options processOptions) error
}

type execProcessRunner struct{}

func (execProcessRunner) Run(ctx context.Context, name string, args []string, options processOptions) error {
	child := exec.CommandContext(ctx, name, args...)
	child.Env = options.Env
	child.Dir = options.Dir
	child.Stdin = options.Stdin
	child.Stdout = options.Stdout
	child.Stderr = options.Stderr
	return child.Run()
}

// runVaultRun 在启动子进程前完成全部记录的解密与环境构造。List、任一 Decrypt 或环境
// 校验失败时 runner 绝不会被调用，因而不会以不完整的 secrets 启动目标命令。
func runVaultRun(
	ctx context.Context,
	app string,
	command []string,
	masterKey []byte,
	lister vaultListerFull,
	decrypt func(key []byte, ciphertext string) (string, error),
	options processOptions,
	runner processRunner,
) error {
	if len(command) == 0 || command[0] == "" {
		return fmt.Errorf("缺少要执行的命令；用法：bk vault run <app> -- <command> [args...]")
	}

	secrets, err := lister.List(app)
	if err != nil {
		return fmt.Errorf("列出 app %q 的 secret 失败：%w", app, err)
	}

	overrides := make(map[string]string, len(secrets))
	for _, secret := range secrets {
		if !processEnvironmentName.MatchString(secret.Key) {
			return fmt.Errorf("secret key %q 不是合法的环境变量名", secret.Key)
		}
		plaintext, err := decrypt(masterKey, secret.Value)
		if err != nil {
			return fmt.Errorf("解密 secret key %q（app %q）失败：%w", secret.Key, app, err)
		}
		if strings.IndexByte(plaintext, 0) >= 0 {
			return fmt.Errorf("secret key %q 的值包含环境变量不支持的 NUL 字节", secret.Key)
		}
		overrides[secret.Key] = plaintext
	}

	options.Env = mergeProcessEnvironment(options.Env, overrides)
	return runner.Run(ctx, command[0], command[1:], options)
}

// mergeProcessEnvironment 保留父环境的原顺序，移除将被 Vault 覆盖的同名变量，再以 key
// 升序追加 overrides，使测试与诊断结果稳定。它不会修改传入切片。
func mergeProcessEnvironment(parent []string, overrides map[string]string) []string {
	merged := make([]string, 0, len(parent)+len(overrides))
	for _, entry := range parent {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, overridden := overrides[key]; overridden {
				continue
			}
		}
		merged = append(merged, entry)
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		merged = append(merged, key+"="+overrides[key])
	}
	return merged
}

var vaultRunCmd = &cobra.Command{
	Use:   "run <app> -- <command> [args...]",
	Short: "将 app 的 secrets 注入本地子进程并执行",
	Long: `取回当前身份在指定 app 下的全部密文，在内存中用本机主密钥全部解密成功后，
将 secrets 作为环境变量注入本地子进程。Vault 值覆盖父进程中的同名环境变量；BK 不输出
secret 明文，也不创建明文 env 文件。

命令直接执行而不经过 shell，并透传 stdin、stdout、stderr、当前工作目录和退出状态。
建议始终用 -- 分隔 BK 参数与目标命令：

  bk vault run domain-ops -- cdncert -domain example.com
  bk vault run myapp -- sh -c 'exec ./server'

如果列举、解密或环境变量校验失败，目标命令不会启动。`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newVaultStore(profile)
		if err != nil {
			return err
		}
		masterKey, err := vaultMasterKey()
		if err != nil {
			return err
		}
		workingDirectory, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("读取当前工作目录失败：%w", err)
		}
		ctx, stop := signalContext(cmd.Context())
		defer stop()
		return runVaultRun(ctx, args[0], args[1:], masterKey, store, vault.Decrypt, processOptions{
			Env:    os.Environ(),
			Dir:    workingDirectory,
			Stdin:  cmd.InOrStdin(),
			Stdout: cmd.OutOrStdout(),
			Stderr: cmd.ErrOrStderr(),
		}, execProcessRunner{})
	},
}

func init() {
	vaultCmd.AddCommand(vaultRunCmd)
}
