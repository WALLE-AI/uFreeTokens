package playbooks

import "testing"

func TestPlaybooks_Parse(t *testing.T) {
	all := All()
	if len(all) < 9 {
		t.Fatalf("playbooks = %d, want >= 9", len(all))
	}
	for _, p := range all {
		if p.Body == "" || p.Starter == "" {
			t.Errorf("%s: empty body or starter", p.Name)
		}
		if p.MaxTurns <= 0 || p.MaxToolCalls <= 0 {
			t.Errorf("%s: budget missing", p.Name)
		}
	}
	if _, ok := Get("price_triage"); !ok {
		t.Error("price_triage not found")
	}
}

func TestParse_Errors(t *testing.T) {
	cases := map[string]string{
		"no front matter": "hello",
		"unknown key":     "---\nname: x\nfoo: bar\n---\nbody",
		"missing tools":   "---\nname: x\ntitle: X\n---\nbody",
		"name mismatch":   "---\nname: y\ntitle: X\nallowed_tools: a\n---\nbody",
	}
	for name, src := range cases {
		if _, err := Parse("x.md", []byte(src)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
