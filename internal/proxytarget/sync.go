package proxytarget

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// RenderForwardTargets 在**保留 hub config.yaml 其余字段**的前提下，把各 app 的
// forward_targets 替换为 byApp[app_id]（去重、升序），并对被管理的 app 确保 max_forwarders>0
// （为 0/缺省时补 8）。未在 byApp 中出现的 app（如 wxwork mirror）完全不动。
//
// 返回：新 YAML 字节、warnings（byApp 里有但 config.apps 里不存在的 app_id）、changed
// （是否有被管理 app 的 forward_targets/max_forwarders 发生**语义**变化——用于让 sync 在无变化时
// 跳过写入与重启；不能用字节比较，因为 YAML 重序列化会改变键序/格式但语义不变）。
//
// 实现要点：解析为通用 map（map[string]interface{}），只改 forward_targets/max_forwarders，
// 其它键原样回写，避免丢失未建模字段（mirror app 的 max_producers 等）。本函数纯函数、可单测。
func RenderForwardTargets(current []byte, byApp map[string][]string) (out []byte, warnings []string, changed bool, err error) {
	var root map[string]interface{}
	if err := yaml.Unmarshal(current, &root); err != nil {
		return nil, nil, false, fmt.Errorf("解析 hub config.yaml 失败: %w", err)
	}
	appsRaw, ok := root["apps"].([]interface{})
	if !ok {
		return nil, nil, false, fmt.Errorf("hub config.yaml 缺少 apps 列表（或格式异常）")
	}

	seen := map[string]bool{}
	for _, a := range appsRaw {
		m, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		targets, want := byApp[id]
		if !want {
			continue // 未被管理的 app（如 mirror）原样保留
		}
		seen[id] = true

		newTargets := dedupeSorted(targets)
		oldTargets := dedupeSorted(toStringSlice(m["forward_targets"]))
		if !equalStrings(oldTargets, newTargets) {
			changed = true
		}
		arr := make([]interface{}, len(newTargets))
		for i, t := range newTargets {
			arr[i] = t
		}
		m["forward_targets"] = arr

		if toInt(m["max_forwarders"]) <= 0 {
			if toInt(m["max_forwarders"]) != 8 {
				changed = true
			}
			m["max_forwarders"] = 8
		}
	}

	for id := range byApp {
		if !seen[id] {
			warnings = append(warnings, id)
		}
	}
	sort.Strings(warnings)

	out, err = yaml.Marshal(root)
	if err != nil {
		return nil, nil, false, fmt.Errorf("序列化 hub config.yaml 失败: %w", err)
	}
	return out, warnings, changed, nil
}

// toStringSlice 把 yaml 解码出的 []interface{} 转 []string（非字符串元素忽略）。
func toStringSlice(v interface{}) []string {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// equalStrings 比较两个已排序字符串切片是否相等。
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// dedupeSorted 去重并升序排序。
func dedupeSorted(in []string) []string {
	set := map[string]struct{}{}
	for _, s := range in {
		if s != "" {
			set[s] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// toInt 把 yaml 解码出的数值（int / float64）转 int；非数值返回 0。
func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}
