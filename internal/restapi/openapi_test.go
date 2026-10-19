package restapi_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// openAPIOperations returns "METHOD /path" for every operation in docs/api/openapi.yaml.
func openAPIOperations(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../docs/api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	methods := map[string]bool{"get": true, "put": true, "post": true, "delete": true, "patch": true, "head": true}
	var ops []string
	for p, item := range spec.Paths {
		for m := range item {
			if methods[m] {
				ops = append(ops, strings.ToUpper(m)+" "+p)
			}
		}
	}
	sort.Strings(ops)
	return ops
}

// Every operation documented in openapi.yaml must be served, and every route
// the API registers must be documented, so the contract cannot drift.
func TestOpenAPIMatchesRoutes(t *testing.T) {
	documented := openAPIOperations(t)
	if len(documented) == 0 {
		t.Fatal("no operations found in docs/api/openapi.yaml")
	}

	src, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?:r|mux\.Handle)\("(GET|PUT|POST|DELETE|PATCH|HEAD) "\+prefix\+"([^"]+)"`)
	registered := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		registered[m[1]+" "+m[2]] = true
	}
	if len(registered) == 0 {
		t.Fatal("found no routes in api.go; update the pattern in this test")
	}
	doc := map[string]bool{}
	for _, op := range documented {
		doc[op] = true
		if !registered[op] {
			t.Errorf("documented in openapi.yaml but not registered: %s", op)
		}
	}
	for op := range registered {
		if !doc[op] {
			t.Errorf("registered in api.go but missing from openapi.yaml: %s", op)
		}
	}

	// The documented operations really answer: an unauthenticated call reaches
	// the route (401), instead of falling through to the mux's 404 or 405.
	e := setup(t, "proxy")
	paramRe := regexp.MustCompile(`\{[^}]+\}`)
	for _, op := range documented {
		method, path, _ := strings.Cut(op, " ")
		path = paramRe.ReplaceAllString(path, "x")
		resp := e.do(t, method, path, "", nil, nil)
		if resp.StatusCode != 401 {
			t.Errorf("%s: unauthenticated call answered %d, want 401 (route missing?)", op, resp.StatusCode)
		}
	}
}
