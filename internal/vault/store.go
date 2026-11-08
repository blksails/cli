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
	client          *http.Client
}

var ErrPermission = errors.New("权限不足：请确认登录身份")
var ErrNotFound = errors.New("未找到对应的 secret 记录")

func NewStore(endpoint, token string) (*Store, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return nil, errors.New("Vault 地址必须使用 HTTPS")
	}
	return &Store{endpoint: endpoint, token: token, client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (s *Store) call(operation, app, key string, value *string, out interface{}) error {
	payload := map[string]interface{}{"operation": operation, "app": app}
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
