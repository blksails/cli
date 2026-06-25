package proxytarget

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 一份代表性的 hub config.yaml：wxwork 是 mirror（不含 forward_*），infra 是 forwarder。
const sampleConfig = `listen_address: ":6234"

tls:
  cert_file: /etc/yproxy/server.crt
  key_file: /etc/yproxy/server.key

auth:
  shared_token_env: YPROXY_SHARED_TOKEN

apps:
  - id: wxwork
    max_producers: 4
    max_consumers: 8
  - id: infra
    max_forwarders: 8
    forward_targets:
      - "old.target:1"

limits:
  auth_timeout: 5s

log:
  level: info
  format: json
`

// TestRenderForwardTargets_UpdatesManagedAppOnly 验证：只改被管理 app 的 forward_targets，
// 其它 app（mirror）与全局字段原样保留；目标去重升序；max_forwarders 缺省补 8。
func TestRenderForwardTargets_UpdatesManagedAppOnly(t *testing.T) {
	byApp := map[string][]string{
		"infra": {"dokku.temporal.main:7233", "dokku.temporal.main.ui:8080", "dokku.temporal.main.ui:8080"},
	}
	out, warnings, changed, err := RenderForwardTargets([]byte(sampleConfig), byApp)
	if err != nil {
		t.Fatalf("RenderForwardTargets 失败: %v", err)
	}
	if !changed {
		t.Fatalf("infra 目标从 old.target:1 变为 temporal，changed 应为 true")
	}
	if len(warnings) != 0 {
		t.Fatalf("不应有告警，实际: %v", warnings)
	}

	var root map[string]interface{}
	if err := yaml.Unmarshal(out, &root); err != nil {
		t.Fatalf("输出不是合法 YAML: %v", err)
	}
	apps := root["apps"].([]interface{})
	var wx, infra map[string]interface{}
	for _, a := range apps {
		m := a.(map[string]interface{})
		switch m["id"] {
		case "wxwork":
			wx = m
		case "infra":
			infra = m
		}
	}

	// mirror app 原样保留（仍有 max_producers，且没被塞 forward_targets）。
	if toInt(wx["max_producers"]) != 4 {
		t.Errorf("wxwork.max_producers 应保留为 4，实际: %v", wx["max_producers"])
	}
	if _, leaked := wx["forward_targets"]; leaked {
		t.Errorf("未被管理的 wxwork 不应出现 forward_targets")
	}

	// infra 的 forward_targets 被替换为去重升序后的两条。
	ft, _ := infra["forward_targets"].([]interface{})
	got := make([]string, len(ft))
	for i, v := range ft {
		got[i] = v.(string)
	}
	want := []string{"dokku.temporal.main.ui:8080", "dokku.temporal.main:7233"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("infra.forward_targets 应为去重升序 %v，实际 %v", want, got)
	}
	if toInt(infra["max_forwarders"]) <= 0 {
		t.Errorf("infra.max_forwarders 应 >0")
	}

	// 全局字段保留。
	if root["listen_address"] != ":6234" {
		t.Errorf("listen_address 应保留 :6234，实际 %v", root["listen_address"])
	}
}

// TestRenderForwardTargets_DefaultsMaxForwarders 验证：被管理但原本无 max_forwarders 的 app，
// 渲染后补默认 8（否则 forwarder 被禁用）。
func TestRenderForwardTargets_DefaultsMaxForwarders(t *testing.T) {
	cfg := "apps:\n  - id: newapp\n"
	out, warnings, changed, err := RenderForwardTargets([]byte(cfg), map[string][]string{"newapp": {"h:1"}})
	if err != nil {
		t.Fatalf("失败: %v", err)
	}
	if !changed {
		t.Fatalf("newapp 新增目标，changed 应为 true")
	}
	if len(warnings) != 0 {
		t.Fatalf("newapp 在 config 中存在，不应告警: %v", warnings)
	}
	var root map[string]interface{}
	_ = yaml.Unmarshal(out, &root)
	app := root["apps"].([]interface{})[0].(map[string]interface{})
	if toInt(app["max_forwarders"]) != 8 {
		t.Errorf("缺省 max_forwarders 应补 8，实际 %v", app["max_forwarders"])
	}
}

// TestRenderForwardTargets_WarnUnknownApp 验证：表里有但 config.apps 里不存在的 app_id 进 warnings，
// 且不改动 config。
func TestRenderForwardTargets_WarnUnknownApp(t *testing.T) {
	_, warnings, changed, err := RenderForwardTargets([]byte(sampleConfig), map[string][]string{"ghost": {"h:1"}})
	if err != nil {
		t.Fatalf("失败: %v", err)
	}
	if changed {
		t.Errorf("ghost 不在 config.apps 中，未改动任何受管 app，changed 应为 false")
	}
	if len(warnings) != 1 || warnings[0] != "ghost" {
		t.Errorf("应告警未知 app ghost，实际 %v", warnings)
	}
}

// TestRenderForwardTargets_NoChangeWhenSame 验证：表与 config 语义一致时 changed=false
// （即便 YAML 重序列化后字节不同），sync 据此跳过写入与重启。
func TestRenderForwardTargets_NoChangeWhenSame(t *testing.T) {
	byApp := map[string][]string{"infra": {"old.target:1"}}
	_, _, changed, err := RenderForwardTargets([]byte(sampleConfig), byApp)
	if err != nil {
		t.Fatalf("失败: %v", err)
	}
	if changed {
		t.Errorf("infra 目标与 config 相同（old.target:1），changed 应为 false")
	}
}

// TestRenderForwardTargets_EmptyTargetsClears 验证：某 app 目标清空 → forward_targets 变空列表
// （等于该 app 拒绝所有转发），而非保留旧值。
func TestRenderForwardTargets_EmptyTargetsClears(t *testing.T) {
	out, _, changed, err := RenderForwardTargets([]byte(sampleConfig), map[string][]string{"infra": {}})
	if err != nil {
		t.Fatalf("失败: %v", err)
	}
	if !changed {
		t.Fatalf("infra 目标从 old.target:1 清空，changed 应为 true")
	}
	var root map[string]interface{}
	_ = yaml.Unmarshal(out, &root)
	for _, a := range root["apps"].([]interface{}) {
		m := a.(map[string]interface{})
		if m["id"] == "infra" {
			ft, _ := m["forward_targets"].([]interface{})
			if len(ft) != 0 {
				t.Errorf("清空后 infra.forward_targets 应为空，实际 %v", ft)
			}
		}
	}
}
