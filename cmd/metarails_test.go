package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMetarailsCommandsRegistered(t *testing.T) {
	for _, path := range [][]string{{"materials", "download"}, {"materials", "upload"}, {"metarails", "download"}} {
		cmd, _, err := rootCmd.Find(path)
		if err != nil || cmd == rootCmd {
			t.Fatalf("command %v not registered: cmd=%v err=%v", path, cmd, err)
		}
	}
}

func TestParseDownloadEntries(t *testing.T) {
	entries, err := parseDownloadEntries([]string{"42=campaign/hero.png", "7"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].ID != 42 || entries[0].Path != "campaign/hero.png" || entries[1].Path != "7" {
		t.Fatalf("entries=%#v", entries)
	}
	for _, bad := range []string{"0", "abc=x", "1=../secret", "1=/absolute"} {
		if _, err := parseDownloadEntries([]string{bad}); err == nil {
			t.Fatalf("expected %q to fail", bad)
		}
	}
}

func TestValidateUploadsInfersTypeAndRejectsMixed(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "a.png")
	video := filepath.Join(dir, "b.mp4")
	if err := os.WriteFile(png, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(video, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	paths, typ, err := validateUploads([]string{png}, "auto")
	if err != nil || typ != "IMAGE" || len(paths) != 1 {
		t.Fatalf("paths=%v type=%q err=%v", paths, typ, err)
	}
	if _, _, err := validateUploads([]string{png, video}, "auto"); err == nil {
		t.Fatal("expected mixed types to fail")
	}
	if _, typ, err := validateUploads([]string{png, video}, "VIDEO"); err != nil || typ != "VIDEO" {
		t.Fatalf("type=%q err=%v", typ, err)
	}
}
