package contentrepo

import (
	"bytes"
	"crypto"
	"crypto/dsa" //nolint:staticcheck // SAP systems still sign secKeys with DSA by default.
	"crypto/ecdsa"
	"crypto/md5"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"strings"

	"golang.org/x/crypto/ripemd160" //nolint:staticcheck // listed by the SAP Content Server spec
)

// Minimal PKCS#7 / CMS SignedData verification for SAP secKey signatures.
// SAP's SSF library produces detached signatures; the signed content is
// the concatenation of URL parameter values. Go's standard library has no
// PKCS#7 support and no longer verifies DSA, which SAP uses by default.

var (
	oidSignedData    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}

	oidMD5       = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 5}
	oidRIPEMD160 = asn1.ObjectIdentifier{1, 3, 36, 3, 2, 1}
	oidSHA1      = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidSHA224    = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 4}
	oidSHA256    = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA384    = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidSHA512    = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}
)

type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,optional,tag:0"`
}

type signedData struct {
	Version          int
	DigestAlgorithms asn1.RawValue
	ContentInfo      contentInfo
	Certificates     asn1.RawValue `asn1:"optional,tag:0"`
	CRLs             asn1.RawValue `asn1:"optional,tag:1"`
	SignerInfos      []signerInfo  `asn1:"set"`
}

type signerInfo struct {
	Version                   int
	SignerIdentifier          asn1.RawValue
	DigestAlgorithm           pkix.AlgorithmIdentifier
	AuthenticatedAttributes   asn1.RawValue `asn1:"optional,tag:0"`
	DigestEncryptionAlgorithm pkix.AlgorithmIdentifier
	EncryptedDigest           []byte
	UnauthenticatedAttributes asn1.RawValue `asn1:"optional,tag:1"`
}

type attribute struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue `asn1:"set"`
}

type dsaSignature struct{ R, S *big.Int }

// signature is a parsed PKCS#7 SignedData with its embedded certificates.
// content is set when the signed data is embedded (non-detached).
type signature struct {
	signers []signerInfo
	certs   []*x509.Certificate
	content []byte
}

// decodeSecKey decodes the base64 secKey (already URL-decoded). Spaces are
// restored to '+' because some clients do not escape '+' in query strings.
func decodeSecKey(secKey string) ([]byte, error) {
	s := strings.Map(func(r rune) rune {
		switch r {
		case ' ':
			return '+'
		case '\r', '\n', '\t':
			return -1
		}
		return r
	}, secKey)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("secKey is not valid base64")
}

func parseSignature(der []byte) (*signature, error) {
	var ci contentInfo
	if _, err := asn1.Unmarshal(der, &ci); err != nil {
		return nil, fmt.Errorf("pkcs7: %w", err)
	}
	if !ci.ContentType.Equal(oidSignedData) {
		return nil, fmt.Errorf("pkcs7: content type %v is not signedData", ci.ContentType)
	}
	var sd signedData
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		return nil, fmt.Errorf("pkcs7 signedData: %w", err)
	}
	if len(sd.SignerInfos) == 0 {
		return nil, errors.New("pkcs7: no signer")
	}
	sig := &signature{signers: sd.SignerInfos}
	if len(sd.ContentInfo.Content.Bytes) > 0 {
		var octets []byte
		if _, err := asn1.Unmarshal(sd.ContentInfo.Content.Bytes, &octets); err == nil {
			sig.content = octets
		} else {
			sig.content = sd.ContentInfo.Content.Bytes
		}
	}
	if len(sd.Certificates.Bytes) > 0 {
		if certs, err := x509.ParseCertificates(sd.Certificates.Bytes); err == nil {
			sig.certs = certs
		}
	}
	return sig, nil
}

func hashFor(oid asn1.ObjectIdentifier) (crypto.Hash, func() hash.Hash, error) {
	switch {
	case oid.Equal(oidMD5):
		return crypto.MD5, md5.New, nil
	case oid.Equal(oidRIPEMD160):
		return crypto.RIPEMD160, ripemd160.New, nil
	case oid.Equal(oidSHA1):
		return crypto.SHA1, sha1.New, nil
	case oid.Equal(oidSHA224):
		return crypto.SHA224, sha256.New224, nil
	case oid.Equal(oidSHA256):
		return crypto.SHA256, sha256.New, nil
	case oid.Equal(oidSHA384):
		return crypto.SHA384, sha512.New384, nil
	case oid.Equal(oidSHA512):
		return crypto.SHA512, sha512.New, nil
	}
	return 0, nil, fmt.Errorf("pkcs7: unsupported digest algorithm %v", oid)
}

// verify checks that some signer in sig signed content with pub. If the
// signature embeds its content, that content must equal the expected one.
func (sig *signature) verify(content []byte, pub crypto.PublicKey) error {
	if sig.content != nil && !bytes.Equal(sig.content, content) {
		return errors.New("pkcs7: signed content does not match URL parameters")
	}
	var lastErr error = errors.New("pkcs7: no signer verified")
	for _, si := range sig.signers {
		if err := verifySigner(si, content, pub); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

func verifySigner(si signerInfo, content []byte, pub crypto.PublicKey) error {
	ch, newHash, err := hashFor(si.DigestAlgorithm.Algorithm)
	if err != nil {
		return err
	}
	h := newHash()
	h.Write(content)
	contentDigest := h.Sum(nil)

	signed := contentDigest
	if len(si.AuthenticatedAttributes.FullBytes) > 0 {
		// Signed attributes must contain the content digest; the signature
		// covers their DER encoding re-tagged as a SET.
		var attrs []attribute
		if _, err := asn1.UnmarshalWithParams(si.AuthenticatedAttributes.FullBytes, &attrs, "set,tag:0"); err != nil {
			return fmt.Errorf("pkcs7 attributes: %w", err)
		}
		var md []byte
		for _, a := range attrs {
			if a.Type.Equal(oidMessageDigest) {
				if _, err := asn1.Unmarshal(a.Values.Bytes, &md); err != nil {
					return fmt.Errorf("pkcs7 messageDigest: %w", err)
				}
			}
		}
		if md == nil || !bytes.Equal(md, contentDigest) {
			return errors.New("pkcs7: message digest mismatch")
		}
		raw := append([]byte{}, si.AuthenticatedAttributes.FullBytes...)
		raw[0] = 0x31 // [0] IMPLICIT -> SET OF
		h = newHash()
		h.Write(raw)
		signed = h.Sum(nil)
	}

	switch k := pub.(type) {
	case *rsa.PublicKey:
		if ch == crypto.RIPEMD160 {
			return verifyRSADigestInfo(k, signed, si.EncryptedDigest)
		}
		return rsa.VerifyPKCS1v15(k, ch, signed, si.EncryptedDigest)
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(k, signed, si.EncryptedDigest) {
			return errors.New("pkcs7: ECDSA signature invalid")
		}
		return nil
	case *dsa.PublicKey:
		var ds dsaSignature
		if _, err := asn1.Unmarshal(si.EncryptedDigest, &ds); err != nil {
			return fmt.Errorf("pkcs7 DSA signature: %w", err)
		}
		// FIPS 186-3: use the leftmost bits of the hash, up to the size of Q.
		if n := (k.Q.BitLen() + 7) / 8; len(signed) > n {
			signed = signed[:n]
		}
		if !dsa.Verify(k, signed, ds.R, ds.S) {
			return errors.New("pkcs7: DSA signature invalid")
		}
		return nil
	}
	return fmt.Errorf("pkcs7: unsupported public key type %T", pub)
}

// parseCertificateLenient accepts the formats SAP or administrators may
// send: DER, PEM, base64 DER, or a PKCS#7 certs-only bundle.
func parseCertificateLenient(body []byte) (*x509.Certificate, error) {
	body = bytes.TrimSpace(body)
	if blk, _ := pem.Decode(body); blk != nil {
		body = blk.Bytes
	} else if dec, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(body)), "")); err == nil && len(dec) > 0 {
		body = dec
	}
	if c, err := x509.ParseCertificate(body); err == nil {
		return c, nil
	}
	if sig, err := parseSignature(body); err == nil && len(sig.certs) > 0 {
		return sig.certs[0], nil
	}
	return nil, errors.New("body is not an X.509 certificate (DER, PEM or PKCS#7)")
}

// verifyRSADigestInfo checks an RSA PKCS#1 v1.5 signature by decoding the
// DigestInfo explicitly. Used for RIPEMD-160, where Go's crypto/rsa expects
// the ISO OID (1.0.10118.3.0.49) but OpenSSL/SSF emit TeleTrusT 1.3.36.3.2.1.
func verifyRSADigestInfo(pub *rsa.PublicKey, digest, sig []byte) error {
	k := (pub.N.BitLen() + 7) / 8
	if len(sig) != k {
		return errors.New("pkcs7: RSA signature length mismatch")
	}
	m := new(big.Int).Exp(new(big.Int).SetBytes(sig), big.NewInt(int64(pub.E)), pub.N)
	em := m.FillBytes(make([]byte, k))
	// EM = 0x00 || 0x01 || PS (0xFF...) || 0x00 || DigestInfo
	if em[0] != 0 || em[1] != 1 {
		return errors.New("pkcs7: RSA signature invalid")
	}
	i := 2
	for i < len(em) && em[i] == 0xff {
		i++
	}
	if i < 10 || i >= len(em) || em[i] != 0 {
		return errors.New("pkcs7: RSA signature invalid")
	}
	var di struct {
		Algorithm pkix.AlgorithmIdentifier
		Digest    []byte
	}
	if rest, err := asn1.Unmarshal(em[i+1:], &di); err != nil || len(rest) != 0 {
		return errors.New("pkcs7: RSA DigestInfo invalid")
	}
	iso := asn1.ObjectIdentifier{1, 0, 10118, 3, 0, 49}
	if !di.Algorithm.Algorithm.Equal(oidRIPEMD160) && !di.Algorithm.Algorithm.Equal(iso) {
		return errors.New("pkcs7: RSA DigestInfo algorithm mismatch")
	}
	if subtle.ConstantTimeCompare(di.Digest, digest) != 1 {
		return errors.New("pkcs7: RSA signature invalid")
	}
	return nil
}
