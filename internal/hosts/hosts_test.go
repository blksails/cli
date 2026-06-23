package hosts

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestPick_ByName：指定名称时精确匹配 Name。
func TestPick_ByName(t *testing.T) {
	list := []Host{{Name: "prod", Host: "p"}, {Name: "stg", Host: "s"}}
	h, err := Pick(list, "stg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.Host != "s" {
		t.Fatalf("host = %q, want s", h.Host)
	}
}

// TestPick_UnknownName：指定了不存在的名称 → ErrNotFound。
func TestPick_UnknownName(t *testing.T) {
	_, err := Pick([]Host{{Name: "prod"}}, "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestPick_DefaultWhenNoName：未指定名称时取 is_default=true 的那条。
func TestPick_DefaultWhenNoName(t *testing.T) {
	list := []Host{{Name: "prod", Host: "p"}, {Name: "stg", Host: "s", IsDefault: true}}
	h, err := Pick(list, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.Name != "stg" {
		t.Fatalf("name = %q, want stg (default)", h.Name)
	}
}

// TestPick_SingleWhenNoDefault：无默认但只有一条时取该条。
func TestPick_SingleWhenNoDefault(t *testing.T) {
	h, err := Pick([]Host{{Name: "only", Host: "o"}}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.Name != "only" {
		t.Fatalf("name = %q, want only", h.Name)
	}
}

// TestPick_MultiNoDefault：多条且无默认、未指定名称 → ErrNotFound（需用户指定）。
func TestPick_MultiNoDefault(t *testing.T) {
	_, err := Pick([]Host{{Name: "a"}, {Name: "b"}}, "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestCache_SaveLoadRoundtrip：Save 后 Load 取回同一 profile 的记录。
func TestCache_SaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	in := []Host{{Name: "prod", Host: "1.2.3.4", SSHUser: "dokku", SSHPort: 22, IsDefault: true}}
	if err := Save(path, "default", in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(path, "default")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(out) != 1 || out[0].Name != "prod" || out[0].Host != "1.2.3.4" {
		t.Fatalf("roundtrip mismatch: %+v", out)
	}
}

// TestCache_PerProfileIsolation：不同 profile 的缓存互不覆盖。
func TestCache_PerProfileIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	if err := Save(path, "p1", []Host{{Name: "h1", Host: "1"}}); err != nil {
		t.Fatalf("Save p1: %v", err)
	}
	if err := Save(path, "p2", []Host{{Name: "h2", Host: "2"}}); err != nil {
		t.Fatalf("Save p2: %v", err)
	}
	p1, _ := Load(path, "p1")
	p2, _ := Load(path, "p2")
	if len(p1) != 1 || p1[0].Name != "h1" {
		t.Fatalf("p1 polluted: %+v", p1)
	}
	if len(p2) != 1 || p2[0].Name != "h2" {
		t.Fatalf("p2 polluted: %+v", p2)
	}
}

// TestCache_LoadMissingFile：缺文件返回空切片而非错误。
func TestCache_LoadMissingFile(t *testing.T) {
	out, err := Load(filepath.Join(t.TempDir(), "nope.json"), "default")
	if err != nil {
		t.Fatalf("Load missing should not error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("want empty, got %+v", out)
	}
}
