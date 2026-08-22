package terraform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeStateMetadata(t *testing.T) {
	got, err := decodeStateMetadata([]byte(`{"lineage":"abc","serial":7}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Lineage != "abc" || got.Serial != 7 {
		t.Fatalf("unexpected metadata: %#v", got)
	}
	if _, err := decodeStateMetadata([]byte(`{"serial":1}`)); err == nil {
		t.Fatal("expected missing lineage to fail")
	}
}

func TestTerraformEnvReplacesSensitiveInheritedValues(t *testing.T) {
	t.Setenv("TF_VAR_token", "old")
	t.Setenv("TF_DATA_DIR", "old-data")
	environment := terraformEnv("new-data", map[string]string{"token": "new"})
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "TF_VAR_token=old") || strings.Contains(joined, "TF_DATA_DIR=old-data") {
		t.Fatalf("inherited Terraform value was not removed: %s", joined)
	}
	if !strings.Contains(joined, "TF_VAR_token=new") || !strings.Contains(joined, "TF_DATA_DIR=new-data") {
		t.Fatalf("replacement Terraform value missing: %s", joined)
	}
}

func TestCopyTreeSkipsTerraformCache(t *testing.T) {
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "copy")
	if err := os.WriteFile(filepath.Join(source, "main.tf"), []byte("terraform {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, ".terraform"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".terraform", "cached"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(source, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "main.tf")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".terraform")); !os.IsNotExist(err) {
		t.Fatalf("expected .terraform cache to be skipped, got %v", err)
	}
}
