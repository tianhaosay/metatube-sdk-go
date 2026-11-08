package seed

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureMappingInfoInstallsOnEmptyDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "mapping_info.xml")

	written, err := EnsureMappingInfo(path)
	if err != nil {
		t.Fatalf("EnsureMappingInfo: %v", err)
	}
	if !written {
		t.Fatal("expected the seed to be written")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	if len(data) == 0 || string(data) != string(MappingInfoXML()) {
		t.Fatal("written table does not match the embedded table")
	}
}

func TestEnsureMappingInfoKeepsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mapping_info.xml")
	custom := []byte("<?xml version=\"1.0\"?><info><a zh_cn=\"测试\" keyword=\",test,\"/></info>")
	if err := os.WriteFile(path, custom, 0o644); err != nil {
		t.Fatal(err)
	}

	written, err := EnsureMappingInfo(path)
	if err != nil {
		t.Fatalf("EnsureMappingInfo: %v", err)
	}
	if written {
		t.Fatal("existing file must not be overwritten")
	}
	data, _ := os.ReadFile(path)
	if string(data) != string(custom) {
		t.Fatal("existing file was modified")
	}
}

func TestEnsureMappingInfoWithoutPath(t *testing.T) {
	written, err := EnsureMappingInfo("")
	if err != nil || written {
		t.Fatalf("empty path should be a no-op, got written=%v err=%v", written, err)
	}
}
