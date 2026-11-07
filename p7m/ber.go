package p7m

import (
	"encoding/asn1"
	"errors"
)

// Parser BER minimale.
//
// encoding/asn1 della libreria standard accetta solo DER, mentre molti file
// p7m (in particolare quelli prodotti da software di firma "in streaming")
// usano la codifica BER con lunghezza indefinita e OCTET STRING costruite
// (contenuto spezzato in più blocchi). Questo parser gestisce entrambi i casi.

const (
	classUniversal = 0
	classContext   = 2

	tagInteger     = 2
	tagOctetString = 4
	tagOID         = 6
	tagSequence    = 16
	tagSet         = 17

	// Profondità massima di annidamento: protegge da file malevoli
	// costruiti per esaurire lo stack.
	maxDepth = 64
)

var (
	errTruncated  = errors.New("dati troncati")
	errBadLength  = errors.New("lunghezza non valida")
	errTooDeep    = errors.New("annidamento ASN.1 eccessivo")
	errTagTooLong = errors.New("tag ASN.1 troppo lungo")
)

type berNode struct {
	class       int
	constructed bool
	tag         int
	content     []byte     // contenuto (solo per i tipi primitivi)
	children    []*berNode // figli (solo per i tipi costruiti)
	raw         []byte     // codifica completa del nodo (tag + lunghezza + contenuto)
}

func (n *berNode) is(class, tag int, constructed bool) bool {
	return n.class == class && n.tag == tag && n.constructed == constructed
}

// octets restituisce il contenuto di una OCTET STRING, concatenando i
// blocchi nel caso di codifica costruita.
func (n *berNode) octets() []byte {
	if !n.constructed {
		return n.content
	}
	var buf []byte
	n.appendOctets(&buf)
	return buf
}

func (n *berNode) appendOctets(buf *[]byte) {
	if !n.constructed {
		*buf = append(*buf, n.content...)
		return
	}
	for _, c := range n.children {
		c.appendOctets(buf)
	}
}

// oid decodifica un OBJECT IDENTIFIER.
func (n *berNode) oid() (asn1.ObjectIdentifier, bool) {
	if !n.is(classUniversal, tagOID, false) || len(n.content) == 0 {
		return nil, false
	}
	var ids asn1.ObjectIdentifier
	v, first := 0, true
	for i, c := range n.content {
		if v > 1<<24 {
			return nil, false
		}
		v = v<<7 | int(c&0x7f)
		if c&0x80 != 0 {
			if i == len(n.content)-1 {
				return nil, false
			}
			continue
		}
		if first {
			if v < 80 {
				ids = append(ids, v/40, v%40)
			} else {
				ids = append(ids, 2, v-80)
			}
			first = false
		} else {
			ids = append(ids, v)
		}
		v = 0
	}
	return ids, true
}

// parseBER decodifica il primo elemento BER presente in data.
// Eventuali byte in coda vengono ignorati.
func parseBER(data []byte) (*berNode, error) {
	n, _, err := parseNode(data, 0, 0)
	return n, err
}

func parseNode(data []byte, pos, depth int) (*berNode, int, error) {
	if depth > maxDepth {
		return nil, pos, errTooDeep
	}
	start := pos
	if pos >= len(data) {
		return nil, pos, errTruncated
	}
	b := data[pos]
	pos++
	n := &berNode{
		class:       int(b >> 6),
		constructed: b&0x20 != 0,
		tag:         int(b & 0x1f),
	}

	// Tag in forma estesa (numero di tag >= 31).
	if n.tag == 0x1f {
		n.tag = 0
		for {
			if pos >= len(data) {
				return nil, pos, errTruncated
			}
			c := data[pos]
			pos++
			if n.tag > 1<<23 {
				return nil, pos, errTagTooLong
			}
			n.tag = n.tag<<7 | int(c&0x7f)
			if c&0x80 == 0 {
				break
			}
		}
	}

	if pos >= len(data) {
		return nil, pos, errTruncated
	}
	l := data[pos]
	pos++

	// Lunghezza indefinita: i figli proseguono fino al marcatore 00 00.
	if l == 0x80 {
		if !n.constructed {
			return nil, pos, errBadLength
		}
		for {
			if pos+1 < len(data) && data[pos] == 0 && data[pos+1] == 0 {
				pos += 2
				break
			}
			child, next, err := parseNode(data, pos, depth+1)
			if err != nil {
				return nil, next, err
			}
			n.children = append(n.children, child)
			pos = next
		}
		n.raw = data[start:pos]
		return n, pos, nil
	}

	// Lunghezza definita, forma corta o lunga.
	var length uint64
	if l&0x80 == 0 {
		length = uint64(l)
	} else {
		k := int(l & 0x7f)
		if k > 8 {
			return nil, pos, errBadLength
		}
		if pos+k > len(data) {
			return nil, pos, errTruncated
		}
		for i := 0; i < k; i++ {
			length = length<<8 | uint64(data[pos+i])
		}
		pos += k
	}
	if length > uint64(len(data)-pos) {
		return nil, pos, errTruncated
	}
	end := pos + int(length)

	if n.constructed {
		bounded := data[:end]
		for pos < end {
			child, next, err := parseNode(bounded, pos, depth+1)
			if err != nil {
				return nil, next, err
			}
			n.children = append(n.children, child)
			pos = next
		}
	} else {
		n.content = data[pos:end]
	}
	n.raw = data[start:end]
	return n, end, nil
}
