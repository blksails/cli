package hosts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// cache.go 实现主机目录的本地缓存：登录成功后把在线 cli.hosts 拉取结果按 profile
// 写入 ~/.local/bk/hosts.json，后续命令离线读取，避免每次连主机都查 Supabase。
//
// 缓存只含可公开的连接坐标（见 Host），不含任何敏感信息，文件权限 0600 仍按最小化处理。

// cacheFile 是缓存文件的全部内容：按 profile 名索引一组主机记录。
type cacheFile struct {
	// Profiles 映射 profile → 该 profile 登录时拉取到的主机目录快照。
	Profiles map[string][]Host `json:"profiles"`
}

// Save 把某 profile 的主机目录写入缓存文件 path（覆盖该 profile 条目，保留其它 profile）。
// 自动创建父目录；文件以 0600 写入。list 为空也会写入空切片（表示「已拉取但目录为空」）。
func Save(path, profile string, list []Host) error {
	cf, err := load(path)
	if err != nil {
		return err
	}
	if cf.Profiles == nil {
		cf.Profiles = map[string][]Host{}
	}
	if list == nil {
		list = []Host{}
	}
	cf.Profiles[profile] = list

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建缓存目录失败: %w", err)
	}
	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化主机缓存失败: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("写入主机缓存失败: %w", err)
	}
	return nil
}

// Load 返回某 profile 缓存的主机目录。文件或条目缺失时返回空切片（非错误），
// 使调用方可平滑回退到本地 .bs.yaml 或报「未配置主机」。
func Load(path, profile string) ([]Host, error) {
	cf, err := load(path)
	if err != nil {
		return nil, err
	}
	list := cf.Profiles[profile]
	// 稳定排序（按 name），使展示/选择确定性。
	sort.SliceStable(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

// load 读取并解析缓存文件；文件不存在返回空 cacheFile（非错误）。
func load(path string) (cacheFile, error) {
	var cf cacheFile
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cacheFile{Profiles: map[string][]Host{}}, nil
		}
		return cf, fmt.Errorf("读取主机缓存失败: %w", err)
	}
	if len(data) == 0 {
		return cacheFile{Profiles: map[string][]Host{}}, nil
	}
	if err := json.Unmarshal(data, &cf); err != nil {
		return cf, fmt.Errorf("解析主机缓存失败: %w", err)
	}
	if cf.Profiles == nil {
		cf.Profiles = map[string][]Host{}
	}
	return cf, nil
}
