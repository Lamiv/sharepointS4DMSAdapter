package contentrepo

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *safeBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// Synthetic sensitive values: none may appear in a log line, even at debug level.
const (
	synthSecKey = "SYNTH-SECKEY-0a1b2c3d4e5f"
	synthBearer = "SYNTH-BEARER-6a7b8c9d0e1f"
	synthAPIKey = "SYNTH-APIKEY-2f3e4d5c6b7a"
)

func TestSAPRequestSecretsAreNeverLogged(t *testing.T) {
	logs := &safeBuf{}
	e := setupCSWithLogger(t, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))

	var answers []string
	run := func(method, url, body string, hdr map[string]string) {
		resp, b := e.do(method, url, strings.NewReader(body), hdr)
		answers = append(answers, resp.Header.Get("X-ErrorDescription"), string(b))
	}

	// A signed repository, a made-up secKey, and credentials in headers.
	run("GET", e.u("get", false, "", "contRep", "Z1", "docId", "D1", "compId", "data")+"&accessMode=r&authId=CN%3DS4H&expiration=20991231235959&secKey="+synthSecKey,
		"", map[string]string{"Authorization": "Bearer " + synthBearer, "X-API-Key": synthAPIKey})
	// Same on an optional-signature repository.
	run("GET", e.u("info", false, "", "contRep", "Z3", "docId", "D1")+"&secKey="+synthSecKey, "", map[string]string{"Authorization": synthBearer})
	// A multipart create whose parts carry credential headers and no X-compId (the logged-headers path).
	mp := "--b\r\nContent-Disposition: form-data; name=\"data\"\r\nAuthorization: Bearer " + synthBearer + "\r\nX-Api-Key: " + synthAPIKey + "\r\nContent-Type: text/plain\r\n\r\nhello\r\n--b--\r\n"
	run("POST", e.u("create", false, "", "contRep", "Z2", "docId", "DMP")+"&secKey="+synthSecKey, mp,
		map[string]string{"Content-Type": "multipart/form-data; boundary=b", "Authorization": "Bearer " + synthBearer})
	// The admin endpoint with a wrong token.
	resp, _ := e.do("GET", e.admin+"/admin/contentserver/certificates", nil, map[string]string{"Authorization": "Bearer " + synthBearer})
	_ = resp

	all := logs.String() + "\n" + strings.Join(answers, "\n")
	for _, s := range []string{synthSecKey, synthBearer, synthAPIKey} {
		if strings.Contains(all, s) {
			t.Errorf("synthetic secret %q appears in a log line or response:\n%s", s, all)
		}
	}
	out := logs.String()
	for _, want := range []string{"content server request refused", "request_id=", "code=", "status=", "command=", "contRep="} {
		if !strings.Contains(out, want) {
			t.Errorf("diagnostics must stay useful: log lacks %q:\n%s", want, out)
		}
	}
}

func TestPVersionIsOnlyEchoedWhenItIsFourDigits(t *testing.T) {
	for v, want := range map[string]bool{"0047": true, "0045": true, "": false, "47": false, "00470": false, "00a7": false, "<script>": false, "0047\r\nX: y": false} {
		if got := validPVersion(v); got != want {
			t.Errorf("validPVersion(%q) = %v, want %v", v, got, want)
		}
	}
}
