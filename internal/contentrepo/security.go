package contentrepo

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
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
		return errStatus(http.StatusUnauthorized, "secKey required")
	}
	accessMode, authID, expiration := p.get("accessMode"), p.get("authId"), p.get("expiration")
	if accessMode == "" || authID == "" || expiration == "" {
		return errStatus(http.StatusUnauthorized, "accessMode, authId and expiration are required with secKey")
	}
	if strings.IndexByte(accessMode, need) < 0 {
		return errStatus(http.StatusUnauthorized, "accessMode %q does not permit '%c'", accessMode, need)
	}
	exp, err := time.Parse("20060102150405", expiration)
	if err != nil {
		return errStatus(http.StatusUnauthorized, "invalid expiration")
	}
	if s.now().UTC().After(exp.Add(s.cfg.ClockSkew)) {
		return errStatus(http.StatusUnauthorized, "URL expired")
	}

	rec := s.certs.lookup(cr.id, authID)
	if rec == nil || !rec.Active {
		// Another instance may have received or activated it; reload at most every 10 s.
		_ = s.certs.reload(context.WithoutCancel(r.Context()), 10*time.Second)
		rec = s.certs.lookup(cr.id, authID)
	}
	switch {
	case rec == nil:
		return errStatus(http.StatusUnauthorized, "no certificate for authId %q; send it from OAC0 (putCert) and activate it", authID)
	case !rec.Active:
		return errStatus(http.StatusUnauthorized, "certificate for authId %q is not activated", authID)
	}

	der, err := decodeSecKey(secKey)
	if err != nil {
		return errStatus(http.StatusUnauthorized, "invalid secKey")
	}
	sig, err := parseSignature(der)
	if err != nil {
		return errStatus(http.StatusUnauthorized, "invalid secKey: %v", err)
	}
	var lastErr error
	for _, msg := range signedMessages(p.cmd, r.URL.RawQuery) {
		if lastErr = sig.verify(msg, rec.cert.PublicKey); lastErr == nil {
			return nil
		}
	}
	s.log.Warn("secKey verification failed", "contRep", cr.id, "authId", authID, "command", p.cmd, "error", lastErr)
	return errStatus(http.StatusUnauthorized, "secKey verification failed")
}
