// Package mirror 实现 bk proxy 的「HTTP 流量镜像」模式，复用现有的 yamuxproxy Hub。
//
// 工作方式与 tools/ymux-client 一致：作为 Consumer 经 yamux+TLS 拨入 Hub，
// 注册一条路由规则，把 Hub 镜像下来的 HTTP 请求反向代理到本地 target。
// 适用于在开发机上接收/调试线上某类请求的副本（响应不回送线上）。
//
// 注意：这是「流量镜像」语义（单向、响应丢弃），不是 TCP 端口转发。
// 通用 TCP 转发见 internal/proxy（forward 模式），其隧道机制需 Hub 侧支持。
package mirror

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"pkg.blksails.net/yamuxproxy"
)

// Config 描述一次镜像订阅所需参数。
type Config struct {
	ServerAddress string // Hub 的 TLS 地址 host:port，必填
	Token         string // 共享认证 token，必填
	AppID         string // 订阅的 app_id，必填
	TargetURL     string // 本地反代目标，如 http://127.0.0.1:8080，必填

	// 路由规则（注册到 Hub，决定哪些请求会被镜像下来）
	RuleID     string            // 客户端自定义规则 ID，默认 "bk-mirror"
	Method     string            // HTTP 方法过滤，"*" 表示任意，默认 "*"
	PathPrefix string            // 路径前缀，默认 "/"
	Host       string            // 可选 Host 头精确匹配
	Headers    map[string]string // 可选请求头过滤（全部需匹配）

	// TLS
	Insecure   bool   // 跳过证书校验（仅开发）
	CAFile     string // 可选 PEM CA bundle
	ServerName string // TLS ServerName 覆盖，默认取 ServerAddress 的 host

	Logger yamuxproxy.Logger // 可选日志器
}

// Run 建立镜像订阅并阻塞，直到 ctx 取消或发生不可恢复错误。
func Run(ctx context.Context, cfg Config) error {
	if cfg.ServerAddress == "" || cfg.Token == "" || cfg.AppID == "" || cfg.TargetURL == "" {
		return fmt.Errorf("mirror: server/token/app/target 均为必填")
	}
	targetURL, err := url.Parse(cfg.TargetURL)
	if err != nil {
		return fmt.Errorf("mirror: 无效的 target %q: %w", cfg.TargetURL, err)
	}
	if targetURL.Scheme == "" || targetURL.Host == "" {
		return fmt.Errorf("mirror: target 必须含 scheme 与 host，得到 %q", cfg.TargetURL)
	}

	tlsCfg, err := buildTLSConfig(cfg.ServerAddress, cfg.ServerName, cfg.CAFile, cfg.Insecure)
	if err != nil {
		return fmt.Errorf("mirror: 构建 TLS 配置失败: %w", err)
	}

	if cfg.RuleID == "" {
		cfg.RuleID = "bk-mirror"
	}
	if cfg.Method == "" {
		cfg.Method = "*"
	}
	if cfg.PathPrefix == "" {
		cfg.PathPrefix = "/"
	}

	consumer, err := yamuxproxy.NewConsumer(yamuxproxy.ConsumerOptions{
		ServerAddress: cfg.ServerAddress,
		TLSConfig:     tlsCfg,
		SharedToken:   cfg.Token,
		AppID:         cfg.AppID,
		Handler:       newReverseProxy(targetURL),
		Rules: []yamuxproxy.RuleSpec{{
			RuleID:     cfg.RuleID,
			Method:     cfg.Method,
			PathPrefix: cfg.PathPrefix,
			Host:       cfg.Host,
			Headers:    cfg.Headers,
		}},
		Logger: cfg.Logger,
	})
	if err != nil {
		return fmt.Errorf("mirror: 创建 consumer 失败: %w", err)
	}

	if err := consumer.Run(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("mirror: consumer 退出: %w", err)
	}
	return nil
}

// buildTLSConfig 构建拨入 Hub 的 TLS 配置。
func buildTLSConfig(server, serverName, caFile string, insecure bool) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if serverName != "" {
		cfg.ServerName = serverName
	} else if h, _, splitErr := net.SplitHostPort(server); splitErr == nil {
		cfg.ServerName = h
	}

	if insecure {
		cfg.InsecureSkipVerify = true
		return cfg, nil
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("读取 CA 失败: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA 文件 %q 不含有效 PEM 证书", caFile)
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

// newReverseProxy 构建到本地 target 的单主机反向代理。
func newReverseProxy(target *url.URL) http.Handler {
	rp := httputil.NewSingleHostReverseProxy(target)
	origDirector := rp.Director
	rp.Director = func(r *http.Request) {
		origDirector(r)
		r.Host = target.Host
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}
	rp.Transport = &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return rp
}
