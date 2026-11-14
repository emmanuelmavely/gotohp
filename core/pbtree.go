package core

import "encoding/binary"

// pbKind selects how a pbNode is written to the wire.
type pbKind int

const (
	pbKindMessage pbKind = iota
	pbKindString
	pbKindBytes
	pbKindVarint
	// pbKindVarints is a repeated integer field emitted unpacked, matching
	// the encoding the Google Photos mobile app produces.
	pbKindVarints
)

// pbNode is one field of a hand-rolled protobuf message. The library sync
// request bodies are huge opaque server projections whose full schemas are
// unknown, so they are built as node trees instead of generated structs while
// still being byte-identical to what the mobile app sends.
type pbNode struct {
	num  int
	kind pbKind

	str  string
	val  int64
	ints []uint64
	subs []pbNode
}

// nMsg builds a nested message field.
func nMsg(num int, subs ...pbNode) pbNode {
	return pbNode{num: num, kind: pbKindMessage, subs: subs}
}

// nStr builds a string field.
func nStr(num int, s string) pbNode {
	return pbNode{num: num, kind: pbKindString, str: s}
}

// nBytes builds a bytes field; the payload is given as a Go string literal so
// opaque binary blobs stay readable in the generated builders.
func nBytes(num int, b string) pbNode {
	return pbNode{num: num, kind: pbKindBytes, str: b}
}

// nVarint builds a varint field.
func nVarint(num int, v int64) pbNode {
	return pbNode{num: num, kind: pbKindVarint, val: v}
}

// nRepeat builds a repeated integer field, emitted unpacked.
func nRepeat(num int, ints ...int64) pbNode {
	vals := make([]uint64, len(ints))
	for i, v := range ints {
		vals[i] = uint64(v)
	}
	return pbNode{num: num, kind: pbKindVarints, ints: vals}
}

// encodePBTree serializes node trees to protobuf bytes. Fields are written in
// the order they appear in the tree, which mirrors the field order the mobile
// app uses for the library sync projections.
func encodePBTree(nodes []pbNode) []byte {
	var buf []byte
	for i := range nodes {
		n := &nodes[i]
		switch n.kind {
		case pbKindVarint:
			buf = append(buf, pbTag(n.num, 0)...)
			buf = binary.AppendUvarint(buf, uint64(n.val))
		case pbKindString, pbKindBytes:
			buf = append(buf, pbTag(n.num, 2)...)
			buf = binary.AppendUvarint(buf, uint64(len(n.str)))
			buf = append(buf, n.str...)
		case pbKindMessage:
			sub := encodePBTree(n.subs)
			buf = append(buf, pbTag(n.num, 2)...)
			buf = binary.AppendUvarint(buf, uint64(len(sub)))
			buf = append(buf, sub...)
		case pbKindVarints:
			for _, v := range n.ints {
				buf = append(buf, pbTag(n.num, 0)...)
				buf = binary.AppendUvarint(buf, v)
			}
		}
	}
	return buf
}

func pbTag(num, wire int) []byte {
	var tag [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tag[:], uint64(num)<<3|uint64(wire))
	return tag[:n]
}
