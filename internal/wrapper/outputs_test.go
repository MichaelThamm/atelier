package wrapper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteOutputsFile_reExportsInNameOrderWithSensitivity(t *testing.T) {
	dir := t.TempDir()
	exports := []OutputExport{
		{Name: "cos_lite_secret", Module: "cos_lite", Output: "secret", Sensitive: true},
		{Name: "cos_lite_model_uuid", Module: "cos_lite", Output: "model_uuid"},
	}
	if err := WriteOutputsFile(dir, exports); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, OutputsFile))
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	for _, want := range []string{
		`output "cos_lite_model_uuid"`,
		"value = module.cos_lite.model_uuid",
		`output "cos_lite_secret"`,
		"module.cos_lite.secret",
		"sensitive = true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("outputs.tf missing %q, got:\n%s", want, out)
		}
	}
	if strings.Index(out, "cos_lite_model_uuid") > strings.Index(out, "cos_lite_secret") {
		t.Errorf("outputs should be sorted by name, got:\n%s", out)
	}
}

func TestWriteOutputsFile_noExportsRemovesTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteOutputsFile(dir, []OutputExport{{Name: "x", Module: "m", Output: "o"}}); err != nil {
		t.Fatal(err)
	}
	if err := WriteOutputsFile(dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, OutputsFile)); !os.IsNotExist(err) {
		t.Errorf("outputs.tf should be removed when there are no exports, err=%v", err)
	}
}
