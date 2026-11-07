/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"testing"
)

// vault_test.go 覆盖 cmd/vault.go 的公共辅助与父命令装配（design：File Structure
// Plan cmd/vault.go；Requirement 1.4/1.5/1.6/6.1/6.2/6.5/7.1/7.2）。
// 仅测纯逻辑与本机文件行为，不触达网络/Supabase。

// TestParseVaultKeyValue 校验 KEY=VALUE 解析：仅以首个 '=' 分隔（R1.5），
// 缺 '=' 或空 key 报错（R1.4），允许空值（KEY=）。
func TestParseVaultKeyValue(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantKey   string
		wantValue string
		wantErr   bool
	}{
		{name: "simple", in: "K=V", wantKey: "K", wantValue: "V"},
		{name: "value with equals", in: "K=a=b=c", wantKey: "K", wantValue: "a=b=c"},
		{name: "url value", in: "URL=http://x?a=b", wantKey: "URL", wantValue: "http://x?a=b"},
		{name: "empty value ok", in: "K=", wantKey: "K", wantValue: ""},
		{name: "no equals", in: "novalue", wantErr: true},
		{name: "empty key", in: "=V", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, val, err := parseVaultKeyValue(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseVaultKeyValue(%q) expected error, got key=%q val=%q", tc.in, key, val)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVaultKeyValue(%q) unexpected error: %v", tc.in, err)
			}
			if key != tc.wantKey || val != tc.wantValue {
				t.Fatalf("parseVaultKeyValue(%q) = (%q, %q), want (%q, %q)", tc.in, key, val, tc.wantKey, tc.wantValue)
			}
		})
	}
}
