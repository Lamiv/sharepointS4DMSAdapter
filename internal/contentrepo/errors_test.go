package contentrepo

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/url"
	"strings"
	"testing"
)

// The X-ErrorDescription header is what SAP shows. It must carry the code,
// the concrete values and the fix, on one ASCII line.
func TestSAPErrorDescriptionsExplainAndFix(t *testing.T) {
	e := setupCS(t)
	check := func(name, method, query string, body io.Reader, status int, code string, want ...string) {
		t.Helper()
		resp, out := e.do(method, e.url+"?"+query, body, nil)
		if resp.StatusCode != status {
			t.Fatalf("%s: status %d want %d (%s)", name, resp.StatusCode, status, resp.Header.Get("X-ErrorDescription"))
		}
		d := resp.Header.Get("X-ErrorDescription")
		if !strings.HasPrefix(d, code+": ") || resp.Header.Get("X-ErrorCode") != code || !strings.Contains(d, "Fix: ") {
			t.Errorf("%s: description %q (code header %q)", name, d, resp.Header.Get("X-ErrorCode"))
		}
		for _, w := range want {
			if !strings.Contains(d, w) {
				t.Errorf("%s: description lacks %q:\n  %s", name, w, d)
			}
		}
		for _, r := range d {
			if r < 0x20 || r > 0x7e {
				t.Errorf("%s: header is not one line of ASCII: %q", name, d)
				break
			}
		}
		if !strings.Contains(string(out), code) {
			t.Errorf("%s: body lacks the code", name)
		}
	}
	check("unknown contRep", "GET", "get&pVersion=0047&contRep=QQ&docId=D", nil, 404, "CS_CONTREP_UNKNOWN", `"QQ"`, "Z1, Z2, Z3")
	check("no command", "GET", "pVersion=0047&contRep=Z2", nil, 400, "CS_COMMAND_MISSING")
	check("unknown command", "GET", "frobnicate&contRep=Z2", nil, 400, "CS_COMMAND_UNKNOWN", `"frobnicate"`)
	check("unsupported command", "GET", "attrSearch&contRep=Z2&docId=D&pattern=x", nil, 501, "CS_COMMAND_UNSUPPORTED", "attrSearch")
	check("CSADMIN", "GET", "adminContRep&configGet&pVersion=0046&contRep=Z2", nil, 501, "CS_ADMIN_UNSUPPORTED", "configget", "config.yaml")
	check("param missing", "GET", "get&pVersion=0047&contRep=Z2", nil, 400, "CS_PARAM_MISSING", "docId")
	check("document missing", "GET", "get&pVersion=0047&contRep=Z2&docId=NOPE", nil, 404, "CS_DOC_NOT_FOUND", `"NOPE"`, "repository Z2")
	check("method", "GET", "append&pVersion=0047&contRep=Z2&docId=D&compId=data", nil, 405, "CS_METHOD_NOT_ALLOWED", "append", "GET", "PUT")
	check("not multipart", "POST", "create&pVersion=0047&contRep=Z2&docId=MP", strings.NewReader("x"), 400, "CS_MULTIPART_REQUIRED", "multipart/form-data")
	check("not a certificate", "PUT", "putCert&pVersion=0047&contRep=Z1&authId=CN%3DX", strings.NewReader("garbage"), 406, "CS_CERT_NOT_RECOGNIZED", "7 bytes")
	check("signature missing", "GET", "get&pVersion=0047&contRep=Z1&docId=D", nil, 401, "CS_SECKEY_MISSING", "content repository Z1", "OAC0")

	// A document to test component and offset errors on.
	resp, out := e.do("PUT", e.u("create", false, "", "contRep", "Z2", "docId", "ERR1", "compId", "data"), strings.NewReader("0123456789"), nil)
	e.expect(resp, out, 201)
	check("component missing", "GET", "get&pVersion=0047&contRep=Z2&docId=ERR1&compId=zzz", nil, 404, "CS_COMP_NOT_FOUND", `"zzz"`, "components: data")
	check("offset outside", "GET", "get&pVersion=0047&contRep=Z2&docId=ERR1&compId=data&fromOffset=50", nil, 400, "CS_OFFSET_INVALID", "size 10")
	check("offset not a number", "GET", "get&pVersion=0047&contRep=Z2&docId=ERR1&compId=data&fromOffset=abc", nil, 400, "CS_OFFSET_INVALID", `"abc"`)
	check("exists", "PUT", "create&pVersion=0047&contRep=Z2&docId=ERR1&compId=data", strings.NewReader("x"), 403, "CS_COMP_EXISTS", `"data"`, `"ERR1"`)

	// Signature problems name the cause and the exact command to run.
	resp, out = e.do("GET", e.u("get", true, "r", "contRep", "Z1", "docId", "D"), nil, nil)
	e.expect(resp, out, 401)
	if d := resp.Header.Get("X-ErrorDescription"); !strings.HasPrefix(d, "CS_CERT_UNKNOWN: ") || !strings.Contains(d, `"CN=S4H"`) || !strings.Contains(d, "none; send the certificate from OAC0") {
		t.Errorf("unknown certificate: %s", d)
	}
	resp, out = e.do("PUT", e.u("putCert", false, "", "contRep", "Z1", "authId", "CN=S4H"), bytes.NewReader(e.signer.cert.Raw), nil)
	e.expect(resp, out, 200)
	resp, out = e.do("GET", e.u("get", true, "r", "contRep", "Z1", "docId", "D"), nil, nil)
	e.expect(resp, out, 401)
	if d := resp.Header.Get("X-ErrorDescription"); !strings.HasPrefix(d, "CS_CERT_INACTIVE: ") || !strings.Contains(d, "adapter certs activate Z1 'CN=S4H'") || !strings.Contains(d, "STRUST") {
		t.Errorf("inactive certificate: %s", d)
	}
	e.activate()
	expired := e.url + "?get&pVersion=0047&contRep=Z1&docId=D&accessMode=r&authId=CN%3DS4H&expiration=20200101000000"
	expired += "&secKey=" + url.QueryEscape(base64.StdEncoding.EncodeToString(e.signer.sign(signedMessages("get", strings.TrimPrefix(expired, e.url+"?"))[0])))
	resp, out = e.do("GET", expired, nil, nil)
	e.expect(resp, out, 401)
	if d := resp.Header.Get("X-ErrorDescription"); !strings.HasPrefix(d, "CS_URL_EXPIRED: ") || !strings.Contains(d, "2020-01-01 00:00:00 UTC") || !strings.Contains(d, "server time") {
		t.Errorf("expired URL: %s", d)
	}
	tampered := strings.Replace(e.u("get", true, "r", "contRep", "Z1", "docId", "D"), "docId=D", "docId=E", 1)
	resp, out = e.do("GET", tampered, nil, nil)
	e.expect(resp, out, 401)
	if d := resp.Header.Get("X-ErrorDescription"); !strings.HasPrefix(d, "CS_SECKEY_INVALID: ") || !strings.Contains(d, "activated certificate") {
		t.Errorf("tampered URL: %s", d)
	}
	resp, out = e.do("GET", e.u("get", true, "d", "contRep", "Z1", "docId", "D"), nil, nil)
	e.expect(resp, out, 401)
	if d := resp.Header.Get("X-ErrorDescription"); !strings.HasPrefix(d, "CS_ACCESSMODE_DENIED: ") || !strings.Contains(d, `"d"`) {
		t.Errorf("access mode: %s", d)
	}
}
