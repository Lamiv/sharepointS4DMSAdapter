package contentrepo

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"sharepointadapter/internal/apperr"
)

// alwaysSigned parameters per the spec ("secKey" page); signedExtra lists
// the per-function additions from the "Sign" column of each function's
// parameter table. A parameter is part of the message only if it is in
// the URL, and values are concatenated in URL order without separators.
var alwaysSigned = []string{"contrep", "accessmode", "authid", "expiration"}

var signedExtra = map[string][]string{
	"info":       {"docid"},
	"get":        {"docid"},
	"docGet":     {"docid"},
	"search":     {"docid"},
	"attrSearch": {"docid"},
	"create":     {"docid", "compid", "docprot"}, // compId is a URL parameter only for PUT
	"mCreate":    {"docid", "docprot"},           // only the first docId (the one in the URL)
	"append":     {"docid", "compid"},
	"update":     {"docid", "compid"}, // compId is a URL parameter only for PUT
	"delete":     {"docid", "compid"},
}

// signedMessages returns candidate messages: values URL-decoded (expected)
// and raw as they appear in the URL (the spec does not say which is signed).
func signedMessages(cmd, rawQuery string) [][]byte {
	set := map[string]bool{}
	for _, n := range alwaysSigned {
		set[n] = true
	}
	for _, n := range signedExtra[cmd] {
		set[n] = true
	}
	var dec, raw strings.Builder
	for i, tok := range strings.Split(rawQuery, "&") {
		k, v, hasEq := strings.Cut(tok, "=")
		if i == 0 && !hasEq {
			continue // command name is never signed
		}
		name, _ := url.QueryUnescape(k)
		if !set[strings.ToLower(name)] {
			continue
		}
		raw.WriteString(v)
		if d, err := url.QueryUnescape(v); err == nil {
			dec.WriteString(d)
		} else {
			dec.WriteString(v)
		}
	}
	if dec.String() == raw.String() {
		return [][]byte{[]byte(dec.String())}
	}
	return [][]byte{[]byte(dec.String()), []byte(raw.String())}
}

// authBefore enforces signatures for repositories configured as "required"
// before any document is touched.
func (s *Server) authBefore(r *http.Request, p params, cr *contentRep, need byte) error {
	if cr.cfg.Signature == "required" {
		return s.verify(r, p, cr, need)
	}
	return nil
}

// authAfter applies the per-document security level (docProt) for
// repositories configured as "optional": a signature is required when the
// document protects this access mode, and always checked when present.
func (s *Server) authAfter(r *http.Request, p params, cr *contentRep, need byte, docProt string) error {
	if cr.cfg.Signature != "optional" {
		return nil
	}
	if p.get("secKey") != "" || strings.IndexByte(docProt, need) >= 0 {
		return s.verify(r, p, cr, need)
	}
	return nil
}

func (s *Server) verify(r *http.Request, p params, cr *contentRep, need byte) error {
	secKey := p.get("secKey")
	if secKey == "" {
		return apperr.New(apperr.CSSecKeyMissing, "the %s command on content repository %s needs a signed URL (secKey) and none was sent", p.cmd, cr.id)
	}
	accessMode, authID, expiration := p.get("accessMode"), p.get("authId"), p.get("expiration")
	if accessMode == "" || authID == "" || expiration == "" {
		return apperr.New(apperr.CSSecKeyParamsMissing, "a secKey was sent without %s", missingParams(accessMode, authID, expiration))
	}
	if strings.IndexByte(accessMode, need) < 0 {
		return apperr.New(apperr.CSAccessModeDenied, "the signature allows accessMode %q but this command needs '%c' (r=read, c=create, u=update, d=delete)", accessMode, need)
	}
	exp, err := time.Parse("20060102150405", expiration)
	if err != nil {
		return apperr.New(apperr.CSExpirationInvalid, "expiration %q is not YYYYMMDDHHMMSS (UTC)", expiration)
	}
	if s.now().UTC().After(exp.Add(s.cfg.ClockSkew)) {
		return apperr.New(apperr.CSURLExpired, "the URL expired at %s UTC; the adapter server time is %s UTC (allowed clock difference %s)", exp.Format("2006-01-02 15:04:05"), s.now().UTC().Format("2006-01-02 15:04:05"), s.cfg.ClockSkew)
	}

	rec := s.certs.lookup(cr.id, authID)
	if rec == nil || !rec.Active {
		// Another instance may have received or activated it; reload at most every 10 s.
		_ = s.certs.reload(context.WithoutCancel(r.Context()), 10*time.Second)
		rec = s.certs.lookup(cr.id, authID)
	}
	switch {
	case rec == nil:
		return apperr.New(apperr.CSCertUnknown, "no certificate was received for authId %q in content repository %s (certificates known for it: %s)", authID, cr.id, s.certs.knownFor(cr.id))
	case !rec.Active:
		return apperr.New(apperr.CSCertInactive, "the certificate for authId %q in content repository %s was received but is not activated", authID, cr.id).
			WithFix("Compare its fingerprint %s with STRUST, then run: adapter certs activate %s '%s'", short(rec.Fingerprint), cr.id, authID)
	}

	der, err := decodeSecKey(secKey)
	if err != nil {
		return apperr.New(apperr.CSSecKeyMalformed, "the secKey is not valid Base64")
	}
	sig, err := parseSignature(der)
	if err != nil {
		return apperr.New(apperr.CSSecKeyMalformed, "the secKey is not a PKCS#7 signature: %v", err)
	}
	var lastErr error
	for _, msg := range signedMessages(p.cmd, r.URL.RawQuery) {
		if lastErr = sig.verify(msg, rec.cert.PublicKey); lastErr == nil {
			return nil
		}
	}
	s.log.Warn("secKey verification failed", "contRep", cr.id, "authId", authID, "command", p.cmd, "error", lastErr)
	return apperr.New(apperr.CSSecKeyInvalid, "the signature does not match the URL and the activated certificate %s for authId %q (%v)", short(rec.Fingerprint), authID, lastErr)
}

func missingParams(accessMode, authID, expiration string) string {
	var m []string
	for n, v := range map[string]string{"accessMode": accessMode, "authId": authID, "expiration": expiration} {
		if v == "" {
			m = append(m, n)
		}
	}
	sort.Strings(m)
	return strings.Join(m, ", ")
}

func short(fp string) string {
	if len(fp) > 16 {
		return fp[:16] + "..."
	}
	return fp
}
