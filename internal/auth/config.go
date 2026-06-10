package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/supabase-community/gotrue-go/types"
	"github.com/supabase-community/supabase-go"
)

type AuthConfig struct {
	Profile string  `json:"profile"`
	Session Session `json:"session"`
}

type Session struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	ExpiresAt    int64  `json:"expires_at"`
	User         User   `json:"user"`
}

type User struct {
	ID               string       `json:"id"`
	Aud              string       `json:"aud"`
	Role             string       `json:"role"`
	Email            string       `json:"email"`
	EmailConfirmedAt time.Time    `json:"email_confirmed_at"`
	Phone            string       `json:"phone"`
	LastSignInAt     time.Time    `json:"last_sign_in_at"`
	AppMetadata      AppMetadata  `json:"app_metadata"`
	UserMetadata     UserMetadata `json:"user_metadata"`
	Identities       []Identity   `json:"identities"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	ConfirmedAt      time.Time    `json:"confirmed_at"`
}

type AppMetadata struct {
	Provider  string   `json:"provider"`
	Providers []string `json:"providers"`
}

type Identity struct {
	ID           string       `json:"id"`
	UserID       string       `json:"user_id"`
	IdentityData IdentityData `json:"identity_data"`
	Provider     string       `json:"provider"`
	LastSignInAt time.Time    `json:"last_sign_in_at"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

type IdentityData struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	PhoneVerified bool   `json:"phone_verified"`
	Sub           string `json:"sub"`
}

type UserMetadata struct {
	EmailVerified    bool `json:"email_verified"`
	ProfileCompleted bool `json:"profile_completed"`
}

// LoadAuthConfig 加载 auth.json 文件
func LoadAuthConfig(path string) ([]*AuthConfig, error) {
	jsonFile, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer jsonFile.Close()
	var configs []*AuthConfig
	err = json.NewDecoder(jsonFile).Decode(&configs)
	if err != nil {
		return nil, err
	}

	return configs, nil
}

// AddAuthConfig 添加 auth.json 文件
func AddAuthConfig(path string, config *AuthConfig) error {
	err := ensureConfigFile(path)
	if err != nil {
		return err
	}

	var configs []*AuthConfig
	jsonFile, err := os.Open(path)
	if err == nil {
		err = json.NewDecoder(jsonFile).Decode(&configs)
		if err != nil {
			return err
		}
	}
	defer jsonFile.Close()

	found := false
	for i, c := range configs {
		if c.Profile == config.Profile {
			configs[i] = config
			found = true
			break
		}
	}

	if !found {
		configs = append(configs, config)
	}

	jsonFile, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer jsonFile.Close()
	err = json.NewEncoder(jsonFile).Encode(configs)
	if err != nil {
		return err
	}
	return nil
}

// RemoveAuthConfig 移除指定 profile 的会话条目并将结果整体回写 auth.json，其余
// profile 不受影响。它复用纯内存的 RemoveProfile 计算结果，再以 O_TRUNC 整体覆盖
// 写入（与 AddAuthConfig 的写法一致）。该操作幂等：当 path 不存在、为空或目标
// profile 无会话条目时不报错也不破坏既有内容（设计：Service Interface RemoveAuthConfig；
// Requirement 4.1/4.2/4.3）。
func RemoveAuthConfig(path, profile string) error {
	configs, err := LoadAuthConfig(path)
	if err != nil {
		// 文件不存在视为「无会话」：登出幂等，直接返回 nil（Requirement 4.3）。
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	configs = RemoveProfile(configs, profile)

	if err := ensureConfigFile(path); err != nil {
		return err
	}

	jsonFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer jsonFile.Close()
	if err := json.NewEncoder(jsonFile).Encode(configs); err != nil {
		return err
	}
	return nil
}

func ensureConfigFile(filename string) error {
	path := filepath.Dir(filename)
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return os.MkdirAll(path, 0755)
	}
	return nil
}

func FromAuthConfig(config *AuthConfig, client *supabase.Client) *supabase.Client {
	session := types.Session{
		AccessToken:  config.Session.AccessToken,
		RefreshToken: config.Session.RefreshToken,
		ExpiresIn:    int(config.Session.ExpiresIn),
		ExpiresAt:    config.Session.ExpiresAt,
		TokenType:    config.Session.TokenType,
		User: types.User{
			ID:    uuid.MustParse(config.Session.User.ID),
			Email: config.Session.User.Email,
		},
	}

	client.UpdateAuthSession(session)
	return client
}
