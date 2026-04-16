package shared

type Bitfield []byte

func (b Bitfield) HasPiece(index int) bool {
	byteIndex := index / 8
	offset := index % 8
	return b[byteIndex]>>(7-offset)&1 == 1
}

func (b Bitfield) SetPiece(index int) {
	byteIndex := index / 8
	offset := index % 8
	b[byteIndex] |= 1 << (7 - offset)
}
