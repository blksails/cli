// Package vault accesses Supabase's built-in Vault through session-scoped RPCs.
// Encryption and key management belong to the database extension, not this CLI.
package vault

import (
	"encoding/json"
	"errors"
)

type Secret struct {
	App   string `json:"app"`
	Key   string `json:"key"`
	Value string `json:"value"`
}
type RPCClient interface {
	Rpc(name, count string, body interface{}) string
}
type Store struct{ client RPCClient }

func NewStore(client RPCClient) *Store { return &Store{client: client} }

var ErrPermission = errors.New("权限不足：请确认登录身份")
var ErrNotFound = errors.New("未找到对应的 secret 记录")

// Never include backend messages in errors: PostgREST may echo a secret payload.
func (s *Store) call(name string, args map[string]interface{}, out interface{}) error {
	raw := s.client.Rpc(name, "", args)
	var failure struct {
		Code string `json:"code"`
	}
	if json.Unmarshal([]byte(raw), &failure) == nil && failure.Code != "" {
		switch failure.Code {
		case "P0002":
			return ErrNotFound
		case "42501", "PGRST301", "PGRST302", "28000":
			return ErrPermission
		case "PGRST202", "42883":
			return errors.New("Vault RPC 未安装，请先应用 Supabase 内置 Vault 迁移")
		default:
			return errors.New("Vault 请求失败")
		}
	}
	if !json.Valid([]byte(raw)) {
		return errors.New("Vault 服务请求失败")
	}
	if out == nil {
		var accepted bool
		if json.Unmarshal([]byte(raw), &accepted) != nil || !accepted {
			return errors.New("Vault 写入响应无效")
		}
		return nil
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return errors.New("Vault 响应格式无效")
	}
	return nil
}
func (s *Store) Set(app, key, value string) error {
	return s.call("bk_vault_set", map[string]interface{}{"p_app": app, "p_key": key, "p_value": value}, nil)
}
func (s *Store) Get(app, key string) (string, error) {
	var value *string
	err := s.call("bk_vault_get", map[string]interface{}{"p_app": app, "p_key": key}, &value)
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", ErrNotFound
	}
	return *value, nil
}
func (s *Store) ListKeys(app string) ([]string, error) {
	var keys []string
	err := s.call("bk_vault_list_keys", map[string]interface{}{"p_app": app}, &keys)
	if err == nil && keys == nil {
		err = errors.New("Vault 列表响应无效")
	}
	return keys, err
}
func (s *Store) List(app string) ([]Secret, error) {
	var rows []Secret
	err := s.call("bk_vault_list", map[string]interface{}{"p_app": app}, &rows)
	if err == nil && rows == nil {
		err = errors.New("Vault 列表响应无效")
	}
	return rows, err
}
func (s *Store) Remove(app, key string) error {
	return s.call("bk_vault_remove", map[string]interface{}{"p_app": app, "p_key": key}, nil)
}
