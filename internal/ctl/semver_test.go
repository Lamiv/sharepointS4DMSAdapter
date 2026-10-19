package ctl

import "testing"

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]string{"0.1.0": "0.1.0", "v1.2.3": "1.2.3", "0.1.0-beta.1": "0.1.0-beta.1", "v2.0.0-rc.2+build.5": "2.0.0-rc.2", "0.1.0-3-gabc1234": "0.1.0-3-gabc1234"} {
		v, ok := ParseVersion(in)
		if !ok || v.String() != want {
			t.Errorf("ParseVersion(%q) = %v %v, want %s", in, v, ok, want)
		}
	}
	for _, in := range []string{"", "dev", "1.2", "01.2.3", "1.2.3.4", "a.b.c", "../1.2.3"} {
		if _, ok := ParseVersion(in); ok {
			t.Errorf("%q must not parse", in)
		}
	}
}

func TestVersionPrecedence(t *testing.T) {
	order := []string{"0.1.0-alpha", "0.1.0-alpha.1", "0.1.0-alpha.beta", "0.1.0-beta", "0.1.0-beta.2", "0.1.0-beta.11", "0.1.0-rc.1", "0.1.0", "0.1.1", "0.2.0", "1.0.0"}
	for i := range order {
		for j := range order {
			a, _ := ParseVersion(order[i])
			b, _ := ParseVersion(order[j])
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := a.Compare(b); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
}

func TestCompatibilityLines(t *testing.T) {
	cases := []struct {
		a, b string
		same bool
	}{
		{"0.1.0", "0.1.7", true},
		{"0.1.0-beta.1", "0.1.0", true},
		{"0.1.0", "0.2.0", false}, // before 1.0 a minor release may break
		{"1.2.0", "1.9.3", true},
		{"1.2.0", "2.0.0", false},
		{"0.9.0", "1.0.0", false},
	}
	for _, c := range cases {
		a, _ := ParseVersion(c.a)
		b, _ := ParseVersion(c.b)
		if SameLine(a, b) != c.same {
			t.Errorf("SameLine(%s, %s) = %v, want %v", c.a, c.b, !c.same, c.same)
		}
	}
	if lvl, _ := CompatNote("0.1.0", "0.1.4"); lvl != "ok" {
		t.Errorf("level %s", lvl)
	}
	if lvl, note := CompatNote("0.1.0", "0.2.0"); lvl != "warn" || note == "" {
		t.Errorf("level %s note %q", lvl, note)
	}
	if lvl, _ := CompatNote("dev", "0.1.0"); lvl != "unknown" {
		t.Errorf("a development build cannot be compared: %s", lvl)
	}
}
