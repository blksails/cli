package cmd

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeVaultSharer struct {
	app  string
	keys []string
	err  error
}

func (f *fakeVaultSharer) Share(app string, keys []string) (int, error) {
	f.app = app
	f.keys = keys
	return len(keys), f.err
}
func TestVaultShareSelectedKeys(t *testing.T) {
	var output bytes.Buffer
	store := &fakeVaultSharer{}
	if err := runVaultShare(&output, "global", []string{"A", "B"}, store); err != nil {
		t.Fatal(err)
	}
	if store.app != "global" || !reflect.DeepEqual(store.keys, []string{"A", "B"}) {
		t.Fatal("wrong selection")
	}
	if !strings.Contains(output.String(), "个人条目已移除") {
		t.Fatal("missing transfer confirmation")
	}
	output.Reset()
	store.err = errors.New("conflict")
	if err := runVaultShare(&output, "global", []string{"A"}, store); err == nil || output.Len() != 0 {
		t.Fatal("failure reported success")
	}
}
func TestVaultListHasLsAlias(t *testing.T) {
	found, _, err := vaultCmd.Find([]string{"ls"})
	if err != nil || found != vaultListCmd {
		t.Fatalf("ls alias: %v", err)
	}
}
