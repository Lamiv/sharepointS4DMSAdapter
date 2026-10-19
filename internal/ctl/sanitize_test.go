package ctl

import (
	"strings"
	"testing"
)

func TestSanitizerMasksByValueAndByPattern(t *testing.T) {
	s := NewSanitizer([]string{secClient, secAPI, "short"})
	in := []string{
		"client secret is " + secClient + " ok",
		"url-encoded " + strings.ReplaceAll(secClient, "~", "%7E"),
		"key " + secAPI,
		`{"authorization":"Bearer abc.def.ghi","msg":"x"}`,
		`{"token":"opaque-token-value","other":"keep"}`,
		"Authorization: Bearer SYNTH-BEARER-VALUE-1",
		"https://x.sharepoint.com/dl?tempauth=SYNTH-TEMPAUTH&e=2",
		"GET /ContentServer/ContentServer.dll?get&secKey=SYNTH-SECKEY&docId=D1",
		"jwt eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTYifQ.c2lnbmF0dXJl end",
		"client_secret=SYNTH-CS password=hunter22",
	}
	for _, line := range in {
		out := s.Line(line)
		for _, bad := range []string{secClient, "SYNTH~client", secAPI, "abc.def.ghi", "opaque-token-value", "SYNTH-BEARER-VALUE-1", "SYNTH-TEMPAUTH", "SYNTH-SECKEY", "eyJhbGci", "SYNTH-CS", "hunter22"} {
			if strings.Contains(out, bad) {
				t.Errorf("%q survives in %q (from %q)", bad, out, line)
			}
		}
	}
	// What is not a secret stays readable; short values are not masked by value.
	if out := s.Line(`{"token":"x","other":"keep","docId":"D1"} short word`); !strings.Contains(out, `"other":"keep"`) || !strings.Contains(out, "short word") {
		t.Errorf("harmless content must survive: %q", out)
	}
	if out := s.Line("request_id=r-1 status=200 code=CS_CERT_INACTIVE"); out != "request_id=r-1 status=200 code=CS_CERT_INACTIVE" {
		t.Errorf("correlation IDs and codes must survive: %q", out)
	}
}

func TestSanitizerForDeploymentMasksEnvSecretsOnly(t *testing.T) {
	e := newEnv(t)
	d := newDeployment(e.dir)
	vals := d.secretValues()
	joined := strings.Join(vals, "|")
	for _, want := range []string{secClient, secAPI, secAdmin} {
		if !strings.Contains(joined, want) {
			t.Errorf("secret values must include %q", want)
		}
	}
	if strings.Contains(joined, "contoso") || strings.Contains(joined, guidA) {
		t.Errorf("non-secret settings must not be treated as secrets: %v", vals)
	}
}
