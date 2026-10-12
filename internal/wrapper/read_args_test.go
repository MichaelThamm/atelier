package wrapper

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadModuleArgs_blocksArgsAndRefs(t *testing.T) {
	dir := t.TempDir()
	main := `module "producer" {
  source = "./modules/producer"
  name   = "hello"
}

module "consumer" {
  source     = "git::https://github.com/o/r.git//mod?ref=v1"
  input      = module.producer.greeting
  list       = [module.producer.greeting, module.producer.greeting]
  text       = "url: ${module.producer.endpoint}"
  depends_on = [module.producer]
  count_expr = length(module.producer.items)
}
`
	if err := os.WriteFile(filepath.Join(dir, MainTF), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	blocks, err := ReadModuleArgs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}

	p := blocks[0]
	if p.Name != "producer" || p.Source != "./modules/producer" {
		t.Errorf("producer = %+v", p)
	}
	if name := argByName(t, p, "name"); len(name.Refs) != 0 {
		t.Errorf("literal name refs = %v, want none", name.Refs)
	}

	c := blocks[1]
	if c.Source != "git::https://github.com/o/r.git//mod?ref=v1" {
		t.Errorf("consumer source = %q", c.Source)
	}
	cases := []struct {
		arg  string
		want []string
	}{
		{"input", []string{"module.producer.greeting"}},
		{"list", []string{"module.producer.greeting"}}, // de-duplicated
		{"text", []string{"module.producer.endpoint"}},
		{"depends_on", []string{"module.producer"}},
		{"count_expr", []string{"module.producer.items"}},
	}
	for _, tc := range cases {
		got := argByName(t, c, tc.arg).Refs
		if len(got) != len(tc.want) {
			t.Errorf("%s refs = %v, want %v", tc.arg, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s refs = %v, want %v", tc.arg, got, tc.want)
			}
		}
	}
}

func TestReadModuleArgs_missingMainTF(t *testing.T) {
	blocks, err := ReadModuleArgs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if blocks != nil {
		t.Errorf("blocks = %v, want nil", blocks)
	}
}

func argByName(t *testing.T, b ModuleBlockArgs, name string) ModuleArg {
	t.Helper()
	for _, a := range b.Args {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("block %s has no argument %q", b.Name, name)
	return ModuleArg{}
}
