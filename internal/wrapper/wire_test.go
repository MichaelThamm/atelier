package wrapper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWireModuleInput_setsTheInputAndPreservesTheRest(t *testing.T) {
	dir := t.TempDir()
	main := `# hand-written comment
module "cos_lite" {
  source = "./cos-lite"
}

module "charmed_spark" {
  source = "./spark"
  # a comment the user wrote
  replicas = 3
}
`
	if err := os.WriteFile(filepath.Join(dir, MainTF), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WireModuleInput(dir, "charmed_spark", "cos_offers", "module.cos_lite.cos_offers"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, MainTF))
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if !strings.Contains(out, "cos_offers = module.cos_lite.cos_offers") {
		t.Errorf("wire not written, got:\n%s", out)
	}
	// hclwrite realigns `=` within the block, so assert the value, not the
	// exact spacing.
	for _, want := range []string{"# hand-written comment", "# a comment the user wrote", "replicas", "= 3", `module "cos_lite"`} {
		if !strings.Contains(out, want) {
			t.Errorf("lost %q, got:\n%s", want, out)
		}
	}
}

func TestWireModuleInput_unknownBlock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, MainTF), []byte(`module "a" { source = "./a" }`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WireModuleInput(dir, "missing", "x", "module.a.x"); err == nil {
		t.Error("expected an error for an unknown block")
	}
}
