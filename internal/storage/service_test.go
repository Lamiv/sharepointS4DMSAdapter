package storage

import "testing"

func TestCleanPath(t *testing.T) {
	ok := map[string]string{"": "", "/a/b/": "a/b", `a\b`: "a/b", "BUS2081/4711": "BUS2081/4711"}
	for in, want := range ok {
		if got, err := CleanPath(in); err != nil || got != want {
			t.Errorf("CleanPath(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"..", "a/../b", "a//b", "a/./b", "a:b", "x?", "trailing.", " lead"} {
		if _, err := CleanPath(bad); err == nil {
			t.Errorf("CleanPath(%q) accepted", bad)
		}
	}
}

func TestRepositoryRel(t *testing.T) {
	r := &Repository{RootPath: "SAP/DMS"}
	cases := map[string]struct {
		rel string
		ok  bool
	}{
		"/SAP/DMS/a.pdf":      {"a.pdf", true},
		"/SAP/DMS":            {"", true},
		"/SAP/DMSX/a.pdf":     {"", false},
		"/HR/a.pdf":           {"", false},
		"/SAP/DMS/x/y/z.docx": {"x/y/z.docx", true},
	}
	for in, want := range cases {
		rel, ok := r.Rel(in)
		if rel != want.rel || ok != want.ok {
			t.Errorf("Rel(%q) = %q,%v want %q,%v", in, rel, ok, want.rel, want.ok)
		}
	}
}
