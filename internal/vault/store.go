// Package vault accesses the shared BlackSail application vault over authenticated HTTPS.
package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Secret struct {
	App   string `json:"app"`
	Key   string `json:"key"`
	Value string `json:"value"`
}
type Store struct {
	endpoint, token string
	ownership       string
	client          *http.Client
}

var ErrPermission = errors.New("权限不足：请确认登录身份")
var ErrConflict = errors.New("共享冲突：个人密钥已变化或企业已有同名密钥，未转移任何条目")
var ErrNotFound = errors.New("未找到对应的 secret 记录")

func NewStore(endpoint, token string) (*Store, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return nil, errors.New("Vault 地址必须使用 HTTPS")
	}
	return &Store{endpoint: endpoint, token: token, client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// NewScopedStore selects an ownership kind; the server resolves the actual owner
// from the verified session. No caller-supplied user or company ID is sent.
func NewScopedStore(endpoint, token, ownership string) (*Store, error) {
	if ownership != "personal" && ownership != "company" {
		return nil, errors.New("无效的密钥归属")
	}
	store, err := NewStore(endpoint, token)
	if err != nil {
		return nil, err
	}
	store.ownership = ownership
	return store, nil
}

func (s *Store) call(operation, app, key string, value *string, out interface{}) error {
	return s.callWithKeys(operation, app, key, value, nil, out)
}

func (s *Store) callWithKeys(operation, app, key string, value *string, keys []string, out interface{}) error {
	payload := map[string]interface{}{"operation": operation, "app": app}
	if s.ownership == "company" {
		payload["ownership"] = "company"
	}
	if keys != nil {
		payload["keys"] = keys
	}
	if key != "" {
		payload["key"] = key
	}
	if value != nil {
		payload["value"] = *value
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("Vault 请求格式错误")
	}
	req, err := http.NewRequest(http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("Vault 请求失败")
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return errors.New("Vault 服务连接失败")
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case 401, 403:
		return ErrPermission
	case 404:
		return ErrNotFound
	case 409:
		return ErrConflict
	}
	if response.StatusCode != 200 {
		return errors.New("Vault 请求失败")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16*1024*1024+1))
	if err != nil || len(raw) > 16*1024*1024 {
		return errors.New("Vault 响应无效")
	}
	var result struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Data) == 0 || string(result.Data) == "null" {
		return errors.New("Vault 响应无效")
	}
	if out == nil {
		var ok bool
		if json.Unmarshal(result.Data, &ok) != nil || !ok {
			return errors.New("Vault 写入响应无效")
		}
		return nil
	}
	if json.Unmarshal(result.Data, out) != nil {
		return errors.New("Vault 响应无效")
	}
	return nil
}
func (s *Store) Set(app, key, value string) error { return s.call("set", app, key, &value, nil) }
func (s *Store) Get(app, key string) (string, error) {
	var value string
	err := s.call("get", app, key, nil, &value)
	return value, err
}
func (s *Store) ListKeys(app string) ([]string, error) {
	var keys []string
	err := s.call("keys", app, "", nil, &keys)
	return keys, err
}
func (s *Store) List(app string) ([]Secret, error) {
	var rows []Secret
	err := s.call("list", app, "", nil, &rows)
	return rows, err
}
func (s *Store) Remove(app, key string) error { return s.call("remove", app, key, nil, nil) }

// Share atomically transfers selected personal secrets to the session's company.
func (s *Store) Share(app string, keys []string) (int, error) {
	if s.ownership == "company" {
		return 0, errors.New("只能将个人密钥转为企业共享")
	}
	if len(keys) == 0 || len(keys) > 100 {
		return 0, errors.New("每次需指定 1 至 100 个 key")
	}
	var count int
	err := s.callWithKeys("share", app, "", nil, keys, &count)
	return count, err
}
