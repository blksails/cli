// Package tunnel 把 yamuxproxy 的 Forwarder 适配成 internal/proxy 的 Dialer，
// 作为 bk proxy forward 模式的 yamux 隧道传输：每个本地连接经 Hub 拨到远端目标。
//
// Hub 侧需为对应 app 开启 forwarding（MaxForwarders>0 且配置 ForwardTargets 白名单）。
package tunnel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"os"

	"pkg.blksails.net/bk/internal/proxy"
	"pkg.blksails.net/yamuxproxy"
)

// Config 描述隧道接入 Hub 所需参数（与 mirror 模式共享同一套连接配置约定）。
type Config struct {
	ServerAddress string // Hub TLS 地址 host:port，必填
	Token         string // 共享 token，必填
	AppID         string // app_id，必填（Hub 上该 app 须开启 forwarding）

	Insecure   bool   // 跳过证书校验（仅开发）
	CAFile     string // 可选 PEM CA bundle
	ServerName string // TLS ServerName 覆盖，默认取 ServerAddress 的 host

	Logger yamuxproxy.Logger
}

// New 建立 yamux Forwarder 并返回其 proxy.Dialer 适配器与可关闭句柄。
func New(cfg Config) (proxy.Dialer, io.Closer, error) {
	if cfg.ServerAddress == "" || cfg.Token == "" || cfg.AppID == "" {
		return nil, nil, fmt.Errorf("tunnel: server/token/app 均为必填")
	}
	tlsCfg, err := buildTLSConfig(cfg.ServerAddress, cfg.ServerName, cfg.CAFile, cfg.Insecure)
	if err != nil {
		return nil, nil, err
	}
	fwd, err := yamuxproxy.NewForwarder(yamuxproxy.ForwarderOptions{
		ServerAddress: cfg.ServerAddress,
		TLSConfig:     tlsCfg,
		SharedToken:   cfg.Token,
		AppID:         cfg.AppID,
		Logger:        cfg.Logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("tunnel: 创建 forwarder 失败: %w", err)
	}
	return dialer{f: fwd}, fwd, nil
}

// dialer 适配 proxy.Dialer：忽略 network，按 addr 经隧道拨远端目标。
type dialer struct {
	f *yamuxproxy.Forwarder
}

func (d dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.f.Dial(ctx, addr)
}

// buildTLSConfig 构建拨入 Hub 的 TLS 配置。
func buildTLSConfig(server, serverName, caFile string, insecure bool) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if serverName != "" {
		cfg.ServerName = serverName
	} else if h, _, err := net.SplitHostPort(server); err == nil {
		cfg.ServerName = h
	}
	if insecure {
		cfg.InsecureSkipVerify = true
		return cfg, nil
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("tunnel: 读取 CA 失败: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("tunnel: CA 文件 %q 不含有效 PEM 证书", caFile)
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}
