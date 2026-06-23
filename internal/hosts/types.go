// Package hosts 实现「Dokku 主机目录」的领域层：在线读取 cli.hosts（Store）、
// 本地缓存读写（Cache）以及领域类型。
//
// 服务端只下发可公开的连接坐标（host/user/port），绝不含私钥、密码或 identity 路径；
// 私钥与本机安全选项始终由客户端本地 .bs.yaml 提供。
package hosts

import "errors"

// 可识别错误：与 sshkeys 包风格一致，便于 cmd 层用 errors.Is 区分处理。
var (
	// ErrPermission：被 RLS/权限拒绝（如 schema 未暴露、无读权限）。
	ErrPermission = errors.New("hosts: 权限不足或 schema 未暴露")
	// ErrNotFound：未找到匹配的主机记录。
	ErrNotFound = errors.New("hosts: 未找到主机记录")
)

// Host 是 cli.hosts 一行记录的领域表示。json tag 与 DB 列名一一对应，既用于
// PostgREST 读取，也用于本地缓存文件（hosts.json）的序列化。
//
// 安全不变量：不含任何私钥/密码/identity 字段——服务端只存可公开的连接坐标。
type Host struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	SSHUser     string `json:"ssh_user,omitempty"`
	SSHPort     int    `json:"ssh_port,omitempty"`
	IsDefault   bool   `json:"is_default,omitempty"`
	Description string `json:"description,omitempty"`
}

// Pick 从一组主机记录中按名称选择目标主机：
//   - name 非空 → 精确匹配 Name；无匹配返回 ErrNotFound。
//   - name 为空 → 优先取 is_default=true 的那条；若无默认且只有一条则取该条；
//     若无默认且有多条 → 返回 ErrNotFound（需调用方提示用户用 host_name 指定）。
func Pick(list []Host, name string) (Host, error) {
	if name != "" {
		for _, h := range list {
			if h.Name == name {
				return h, nil
			}
		}
		return Host{}, ErrNotFound
	}
	for _, h := range list {
		if h.IsDefault {
			return h, nil
		}
	}
	if len(list) == 1 {
		return list[0], nil
	}
	return Host{}, ErrNotFound
}
