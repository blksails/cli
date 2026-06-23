/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/supabase-community/supabase-go"
	"go.uber.org/zap"
)

var (
	cfgFile     string
	apiEndpoint string
	apiKey      string
	profile     string
	schema      string = "blacksail"
)

// defaultAPIKey 是生产 Supabase（https://supabase.blksails.cn）的 anon（public）key。
// role=anon、受 RLS 约束，设计上即可公开（与前端浏览器包内联的同一把），仅作为默认值
// 让 CLI 开箱即用；可被 --api-key / BK_API_KEY / .bs.yaml 覆盖。它不是用户身份凭据——
// 用户身份由 `bk auth login` 产生的会话承载。
const defaultAPIKey = "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJvbGUiOiJhbm9uIiwiaWF0IjoxNzcxOTE4MDIzLCJleHAiOjEzMjgyNTU4MDIzfQ.KKmVibxmRTLp7TyvjHbjn2fhW_gCzvkG-5uzi2pAOEI"

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:     "bk",
	Version: versionString(),
	Short:   "BlackSails Cloud CLI",
	Long: `BlackSails Cloud CLI is a tool to manage your BlackSails Cloud resources. 
	黑帆云 cli 是黑帆云的命令行工具，用于管理黑帆云的资源，用于管理黑帆云的资源。发布应用程序，部署网络资源`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("api_endpoint", viper.GetString("api_endpoint"))
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "配置文件路径 (默认在主目录与当前目录查找 .bs.yaml)")
	// api endpoint
	rootCmd.PersistentFlags().StringVar(&apiEndpoint, "api-endpoint", "https://supabase.blksails.cn", "API 端点")
	// api key
	// 默认值为生产 Supabase 的 anon（public）key：role=anon，受 RLS 行级安全约束，
	// 设计上即可公开（与 webapp 内联在浏览器包中的 NEXT_PUBLIC_SUPABASE_ANON_KEY 相同），
	// 使 `bk auth login` 开箱即用。真正的用户身份由登录会话承载，与此公钥无关。
	// 如需指向其它项目，用 --api-key 标志、BK_API_KEY 环境变量或 .bs.yaml 覆盖。
	rootCmd.PersistentFlags().StringVar(&apiKey, "api-key", defaultAPIKey, "API 密钥")
	// viper config api_endpoint
	rootCmd.PersistentFlags().StringVar(&profile, "profile", "default", "配置文件名称")

	// viper config api_endpoint, api_key
	_ = viper.BindPFlag("api_endpoint", rootCmd.PersistentFlags().Lookup("api-endpoint"))
	_ = viper.BindPFlag("api_key", rootCmd.PersistentFlags().Lookup("api-key"))

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

// initConfig reads in config file and ENV variables if set.
//
// Precedence (Requirement 1.5): command-line flags > environment variables >
// config file. Flag→viper binding (see init) makes an explicitly set flag win;
// AutomaticEnv lets env vars override the config file; the config file is the
// lowest-priority source. The active config file path is reported to stderr
// (Requirement 1.6), and unrecognized keys in .bs.yaml are tolerated because
// values are read via viper.Get* rather than a strict Unmarshal (Requirement 2.8).
func initConfig() {
	configureConfigSources(viper.GetViper(), cfgFile)

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		// Report the active config file path to stderr (not stdout).
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}

// configureConfigSources wires the .bs.yaml lookup and environment-variable
// reading onto the given viper instance. When cfgFile is non-empty it is used
// verbatim; otherwise .bs.yaml is searched in the home directory first, then the
// current directory (Requirement 1.4).
func configureConfigSources(v *viper.Viper, cfgFile string) {
	if cfgFile != "" {
		// Use config file from the flag.
		v.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search config named ".bs.yaml" in the home directory, then the
		// current directory.
		v.AddConfigPath(home)
		v.AddConfigPath(".")
		v.SetConfigType("yaml")
		v.SetConfigName(".bs")
	}

	v.AutomaticEnv() // read in environment variables that match
}

// loadConfig loads a .bs.yaml file into a fresh viper instance and returns it.
// It tolerates unrecognized keys (Requirement 2.8): recognized keys such as
// api_endpoint / api_key still resolve via viper.Get* even when the file
// contains keys the CLI does not know about. Returns the viper instance even if
// the file is absent, so callers can rely on a non-nil result.
func loadConfig(cfgFile string) *viper.Viper {
	v := viper.New()
	configureConfigSources(v, cfgFile)
	// Ignore a missing config file; other read errors are surfaced by callers
	// that care. Unknown keys never cause an error here because viper stores
	// them in its backing map without strict decoding.
	_ = v.ReadInConfig()
	return v
}

var log *zap.Logger

func init() {
	// Default to a production/Info-level logger so that debug-level output
	// (which can carry sensitive details) is not emitted by default
	// (Requirements 3.3, 11.3).
	log, _ = zap.NewProduction()
}

func DefaultClient() (*supabase.Client, error) {
	return newSchemaClient(schema)
}

// newSchemaClient 构造一个绑定到指定 PostgREST schema 的 Supabase client。
// 应用域数据用默认 schema（blacksail）；CLI 工具专属数据用独立 schema（cli）。
func newSchemaClient(s string) (*supabase.Client, error) {
	apiEndpoint := viper.GetString("api_endpoint")
	apiKey := viper.GetString("api_key")
	return supabase.NewClient(apiEndpoint, apiKey, &supabase.ClientOptions{Schema: s})
}
