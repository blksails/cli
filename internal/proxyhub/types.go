// Package proxyhub 实现「proxy hub 目录」的领域层：在线读取 cli.proxy_hub（Store）、
// 本地缓存读写（Cache）与领域类型，使客户端「登录即用」proxy 隧道（与 internal/hosts 同构）。
//
// 与 hosts 不同：本目录会下发 token（团队共享访问凭据，与内置 anon key 同信任级）与
// ca_cert（证书，公开物）。本地缓存文件因此含 token，按 0600 最小化写入。
package proxyhub

import "errors"

var (
	// ErrPermission：被 RLS/权限拒绝（schema 未暴露、无读权限等）。
	ErrPermission = errors.New("proxyhub: 权限不足或 schema 未暴露")
	// ErrNotFound：未找到匹配的 hub 记录。
	ErrNotFound = errors.New("proxyhub: 未找到 hub 记录")
)

// Hub 是 cli.proxy_hub 一行记录的领域表示。json tag 与 DB 列名一一对应，既用于
// PostgREST 读取，也用于本地缓存文件（proxyhub.json）的序列化。
type Hub struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Server      string `json:"server"` // host:port
	App         string `json:"app"`
	Token       string `json:"token"`
	CACert      string `json:"ca_cert,omitempty"` // PEM
	Insecure    bool   `json:"insecure,omitempty"`
	IsDefault   bool   `json:"is_default,omitempty"`
	Description string `json:"description,omitempty"`
}

// Pick 从一组 hub 记录中按名称选择目标：
//   - name 非空 → 精确匹配 Name；无匹配返回 ErrNotFound。
//   - name 为空 → 优先取 is_default=true 的那条；若无默认且只有一条则取该条；
//     若无默认且有多条 → 返回 ErrNotFound（需调用方提示用 proxy.hub_name 指定）。
func Pick(list []Hub, name string) (Hub, error) {
	if name != "" {
		for _, h := range list {
			if h.Name == name {
				return h, nil
			}
		}
		return Hub{}, ErrNotFound
	}
	for _, h := range list {
		if h.IsDefault {
			return h, nil
		}
	}
	if len(list) == 1 {
		return list[0], nil
	}
	return Hub{}, ErrNotFound
}
