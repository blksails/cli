package proxyhub

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// cache.go：proxy hub 目录的本地缓存（与 internal/hosts/cache.go 同构）。登录成功后把在线
// cli.proxy_hub 拉取结果按 profile 写入 ~/.local/bk/proxyhub.json，后续 `bk proxy forward`
// 离线读取，实现「登录即用」。
//
// ⚠️ 缓存含 token（团队共享访问凭据），故文件以 0600 写入，与 auth.json 同级保护。

type cacheFile struct {
	Profiles map[string][]Hub `json:"profiles"`
}

// Save 把某 profile 的 hub 目录写入缓存 path（覆盖该 profile 条目，保留其它 profile）。
func Save(path, profile string, list []Hub) error {
	cf, err := load(path)
	if err != nil {
		return err
	}
	if cf.Profiles == nil {
		cf.Profiles = map[string][]Hub{}
	}
	if list == nil {
		list = []Hub{}
	}
	cf.Profiles[profile] = list

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建缓存目录失败: %w", err)
	}
	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 hub 缓存失败: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("写入 hub 缓存失败: %w", err)
	}
	return nil
}

// Load 返回某 profile 缓存的 hub 目录。文件或条目缺失时返回空切片（非错误），
// 使调用方平滑回退到本地 .bs.yaml。
func Load(path, profile string) ([]Hub, error) {
	cf, err := load(path)
	if err != nil {
		return nil, err
	}
	list := cf.Profiles[profile]
	sort.SliceStable(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

func load(path string) (cacheFile, error) {
	var cf cacheFile
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cacheFile{Profiles: map[string][]Hub{}}, nil
		}
		return cf, fmt.Errorf("读取 hub 缓存失败: %w", err)
	}
	if len(data) == 0 {
		return cacheFile{Profiles: map[string][]Hub{}}, nil
	}
	if err := json.Unmarshal(data, &cf); err != nil {
		return cf, fmt.Errorf("解析 hub 缓存失败: %w", err)
	}
	if cf.Profiles == nil {
		cf.Profiles = map[string][]Hub{}
	}
	return cf, nil
}
