package sshkeys

import (
	"errors"
	"fmt"
	"testing"
)

// TestStatusIsValid 校验状态枚举的取值合法性（Requirement 8.4）：
// pending/installed/revoked 合法，其它（含空串与拼写错误）非法。
func TestStatusIsValid(t *testing.T) {
	valid := []Status{StatusPending, StatusInstalled, StatusRevoked}
	for _, s := range valid {
		if !s.IsValid() {
			t.Errorf("Status(%q).IsValid() = false, want true", s)
		}
	}

	invalid := []Status{
		Status(""),
		Status("Pending"),     // 大小写敏感
		Status("installed "),  // 尾随空格
		Status("deleted"),     // 不存在的状态
		Status("unknown"),
	}
	for _, s := range invalid {
		if s.IsValid() {
			t.Errorf("Status(%q).IsValid() = true, want false", s)
		}
	}
}

// TestStatusConstants 锁定枚举常量的底层字符串值，与 SQL CHECK 约束及 DB 列值对齐。
func TestStatusConstants(t *testing.T) {
	cases := map[Status]string{
		StatusPending:   "pending",
		StatusInstalled: "installed",
		StatusRevoked:   "revoked",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("status constant = %q, want %q", string(got), want)
		}
	}
}

// TestSentinelErrorsAreDistinct 校验三个 sentinel 错误互不相等，
// 且各自可被 errors.Is 单独识别（Requirement 7.3, 8.4）。
func TestSentinelErrorsAreDistinct(t *testing.T) {
	sentinels := []error{ErrPermission, ErrNotFound, ErrKeyExists}
	for _, e := range sentinels {
		if e == nil {
			t.Fatal("sentinel error is nil")
		}
	}

	// 互不相等：任意两个不同的 sentinel 不能被 errors.Is 互相识别。
	for i, a := range sentinels {
		for j, b := range sentinels {
			match := errors.Is(a, b)
			if i == j && !match {
				t.Errorf("errors.Is(sentinel[%d], itself) = false, want true", i)
			}
			if i != j && match {
				t.Errorf("errors.Is(sentinel[%d], sentinel[%d]) = true, want false (distinct sentinels)", i, j)
			}
		}
	}
}

// TestSentinelErrorsWrappable 校验 sentinel 错误被 %w 包裹后仍可被 errors.Is 识别，
// 这是 store/cmd 层据此区分 RLS 拒绝 / 未找到 / 文件已存在的前提。
func TestSentinelErrorsWrappable(t *testing.T) {
	cases := []struct {
		name     string
		sentinel error
		others   []error
	}{
		{"permission", ErrPermission, []error{ErrNotFound, ErrKeyExists}},
		{"notfound", ErrNotFound, []error{ErrPermission, ErrKeyExists}},
		{"keyexists", ErrKeyExists, []error{ErrPermission, ErrNotFound}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("context: %w", tc.sentinel)
			if !errors.Is(wrapped, tc.sentinel) {
				t.Errorf("errors.Is(wrapped, %v) = false, want true", tc.sentinel)
			}
			for _, other := range tc.others {
				if errors.Is(wrapped, other) {
					t.Errorf("errors.Is(wrapped-%s, %v) = true, want false", tc.name, other)
				}
			}
		})
	}
}
