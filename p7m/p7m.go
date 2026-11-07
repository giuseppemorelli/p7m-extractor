// Package p7m estrae il documento originale da buste firmate PKCS#7/CMS
// (.p7m, CAdES). Usa solo la libreria standard, quindi può essere compilato
// per qualsiasi sistema operativo e anche per WebAssembly.
//
// Il package NON verifica la validità della firma né la catena di
// certificazione: estrae il contenuto e le informazioni dichiarate.
package p7m

import (
	"bytes"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"time"
	"unicode"
)

var (
	oidSignedData  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidSigningTime = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 5}
)

var (
	ErrNotP7M    = errors.New("il file non è una busta PKCS#7/CMS firmata (SignedData)")
	ErrMalformed = errors.New("busta PKCS#7/CMS malformata")
	ErrDetached  = errors.New("firma detached: la busta non contiene il documento (serve il file originale)")
)

// Numero massimo di buste annidate che vengono aperte.
const maxNesting = 16

// Envelope rappresenta una busta CMS SignedData già aperta.
type Envelope struct {
	ContentType asn1.ObjectIdentifier
	Content     []byte
	Certs       []*x509.Certificate
	Signers     []Signer
}

// Signer descrive un firmatario della busta.
type Signer struct {
	Cert        *x509.Certificate // nil se il certificato non è incluso nella busta
	SigningTime time.Time         // zero se l'attributo signingTime è assente
}

// Extract apre la busta p7m e restituisce il documento contenuto.
// Se nested è true apre anche le eventuali buste annidate (file .p7m.p7m).
// Le buste aperte vengono restituite dalla più esterna alla più interna.
func Extract(data []byte, nested bool) ([]byte, []*Envelope, error) {
	env, err := openEnvelope(data)
	if err != nil {
		return nil, nil, err
	}
	layers := []*Envelope{env}
	content := env.Content
	for nested && len(layers) < maxNesting {
		inner, err := openEnvelope(content)
		if err != nil {
			break
		}
		layers = append(layers, inner)
		content = inner.Content
	}
	return content, layers, nil
}

// openEnvelope accetta sia il formato binario (DER/BER) sia quello testuale
// (Base64, con o senza intestazioni PEM).
func openEnvelope(data []byte) (*Envelope, error) {
	env, err := parseSignedData(data)
	if err == nil {
		return env, nil
	}
	if decoded, ok := decodeText(data); ok {
		if env, err2 := parseSignedData(decoded); err2 == nil {
			return env, nil
		}
	}
	return nil, err
}

func decodeText(data []byte) ([]byte, bool) {
	if bytes.Contains(data, []byte("-----BEGIN")) {
		if block, _ := pem.Decode(data); block != nil {
			return block.Bytes, true
		}
	}
	clean := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, string(data))
	if clean == "" {
		return nil, false
	}
	if b, err := base64.StdEncoding.DecodeString(clean); err == nil {
		return b, true
	}
	if b, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(clean, "=")); err == nil {
		return b, true
	}
	return nil, false
}

// parseSignedData interpreta la struttura (RFC 5652):
//
//	ContentInfo ::= SEQUENCE {
//	    contentType  OBJECT IDENTIFIER,            -- signedData
//	    content      [0] EXPLICIT SignedData }
//
//	SignedData ::= SEQUENCE {
//	    version           INTEGER,
//	    digestAlgorithms  SET,
//	    encapContentInfo  SEQUENCE {
//	        eContentType  OBJECT IDENTIFIER,
//	        eContent      [0] EXPLICIT OCTET STRING OPTIONAL },
//	    certificates      [0] IMPLICIT SET OPTIONAL,
//	    crls              [1] IMPLICIT SET OPTIONAL,
//	    signerInfos       SET }
func parseSignedData(data []byte) (*Envelope, error) {
	if len(data) == 0 || data[0] != 0x30 {
		return nil, ErrNotP7M
	}
	root, err := parseBER(data)
	if err != nil {
		return nil, ErrNotP7M
	}
	if !root.is(classUniversal, tagSequence, true) || len(root.children) < 2 {
		return nil, ErrNotP7M
	}
	if oid, ok := root.children[0].oid(); !ok || !oid.Equal(oidSignedData) {
		return nil, ErrNotP7M
	}

	wrap := root.children[1]
	if !wrap.is(classContext, 0, true) || len(wrap.children) != 1 {
		return nil, ErrMalformed
	}
	sd := wrap.children[0]
	if !sd.is(classUniversal, tagSequence, true) || len(sd.children) < 4 {
		return nil, ErrMalformed
	}

	encap := sd.children[2]
	if !encap.is(classUniversal, tagSequence, true) || len(encap.children) == 0 {
		return nil, ErrMalformed
	}
	env := &Envelope{}
	env.ContentType, _ = encap.children[0].oid()
	if len(encap.children) < 2 {
		return nil, ErrDetached
	}
	ec := encap.children[1]
	if !ec.is(classContext, 0, true) || len(ec.children) != 1 {
		return nil, ErrMalformed
	}
	if body := ec.children[0]; body.class == classUniversal && body.tag == tagOctetString {
		env.Content = body.octets()
	} else {
		// PKCS#7 v1.5 consente contenuti non incapsulati in una OCTET STRING.
		env.Content = body.raw
	}

	for _, c := range sd.children[3:] {
		switch {
		case c.is(classContext, 0, true): // certificates
			for _, cc := range c.children {
				if !cc.is(classUniversal, tagSequence, true) {
					continue
				}
				if cert, err := x509.ParseCertificate(cc.raw); err == nil {
					env.Certs = append(env.Certs, cert)
				}
			}
		case c.is(classUniversal, tagSet, true): // signerInfos
			for _, si := range c.children {
				env.Signers = append(env.Signers, parseSigner(si, env.Certs))
			}
		}
	}
	return env, nil
}

// parseSigner estrae da un SignerInfo il certificato del firmatario e la
// data di firma dichiarata (attributo firmato signingTime).
func parseSigner(n *berNode, certs []*x509.Certificate) Signer {
	var s Signer
	if !n.is(classUniversal, tagSequence, true) || len(n.children) < 3 {
		return s
	}

	sid := n.children[1]
	switch {
	case sid.is(classUniversal, tagSequence, true) && len(sid.children) == 2:
		// IssuerAndSerialNumber
		issuer := sid.children[0].raw
		serial := new(big.Int).SetBytes(sid.children[1].content)
		for _, c := range certs {
			if c.SerialNumber.Cmp(serial) == 0 {
				if bytes.Equal(c.RawIssuer, issuer) {
					s.Cert = c
					break
				}
				if s.Cert == nil {
					s.Cert = c
				}
			}
		}
	case sid.is(classContext, 0, false):
		// SubjectKeyIdentifier
		for _, c := range certs {
			if bytes.Equal(c.SubjectKeyId, sid.content) {
				s.Cert = c
				break
			}
		}
	}

	for _, c := range n.children[2:] {
		if !c.is(classContext, 0, true) { // signedAttrs
			continue
		}
		for _, attr := range c.children {
			if len(attr.children) < 2 || len(attr.children[1].children) == 0 {
				continue
			}
			if oid, ok := attr.children[0].oid(); ok && oid.Equal(oidSigningTime) {
				var t time.Time
				if _, err := asn1.Unmarshal(attr.children[1].children[0].raw, &t); err == nil {
					s.SigningTime = t
				}
			}
		}
		break
	}
	return s
}
