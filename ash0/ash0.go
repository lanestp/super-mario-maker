// Package ash0 reads and writes the ASH0 compression format that Super
// Mario Maker uses for the parts of a course file.
//
// The layout was worked out from the decompressor that Pretendo publishes
// (github.com/PretendoNetwork/ASH0), which this package's output is
// checked against:
//
//	0x0  "ASH0"
//	0x4  size of the data once decompressed. Only the low 3 bytes are used
//	0x8  offset of the distance stream
//	0xC  the symbol stream
//
// Both streams are bits packed most significant first into big endian 32
// bit words. Each starts with a Huffman tree written depth first: a 1 bit
// is a node with a left and then a right child, a 0 bit is a leaf followed
// by its value (9 bits in the symbol stream, 11 bits in the distance
// stream). Walking the tree, a 0 bit goes left and a 1 bit goes right.
//
// A symbol below 0x100 is a byte to output. Any other symbol means "copy
// symbol-0xFD bytes from earlier in the output", and is followed by a code
// from the distance stream, whose value plus 1 is how far back to copy from
package ash0

import (
	"container/heap"
	"encoding/binary"
	"errors"
)

const (
	magic = "ASH0"

	headerSize = 0xC

	// * The size is stored in 3 bytes
	MaxSize = 0xFFFFFF

	symbolBits   = 9
	distanceBits = 11

	symbolCount   = 1 << symbolBits
	distanceCount = 1 << distanceBits

	firstCopySymbol = 0x100
	minCopyLength   = 3
	maxCopyLength   = symbolCount - 1 - firstCopySymbol + minCopyLength
	maxDistance     = distanceCount
)

var (
	ErrNotASH0   = errors.New("ash0: data does not start with an ASH0 header")
	ErrCorrupt   = errors.New("ash0: compressed data is corrupt")
	ErrEmpty     = errors.New("ash0: there is no data to compress")
	ErrTooLarge  = errors.New("ash0: data is larger than the format can hold")
	errTruncated = errors.New("ash0: compressed data ends early")
)

// IsCompressed reports whether data starts with an ASH0 header
func IsCompressed(data []byte) bool {
	return len(data) > headerSize && string(data[:len(magic)]) == magic
}

// DecompressedSize returns the size that data says it decompresses to
func DecompressedSize(data []byte) (int, error) {
	if !IsCompressed(data) {
		return 0, ErrNotASH0
	}

	return int(binary.BigEndian.Uint32(data[4:8]) & MaxSize), nil
}

// Decompress returns the data held in an ASH0 stream
func Decompress(data []byte) ([]byte, error) {
	size, err := DecompressedSize(data)
	if err != nil {
		return nil, err
	}

	distanceOffset := int(binary.BigEndian.Uint32(data[8:12]))
	if distanceOffset < headerSize || distanceOffset > len(data) {
		return nil, ErrCorrupt
	}

	symbols := &bitReader{data: data, position: headerSize}
	distances := &bitReader{data: data, position: distanceOffset}

	symbolTree, err := readTree(symbols, symbolBits)
	if err != nil {
		return nil, err
	}

	distanceTree, err := readTree(distances, distanceBits)
	if err != nil {
		return nil, err
	}

	output := make([]byte, 0, size)

	for len(output) < size {
		symbol, err := symbolTree.decode(symbols)
		if err != nil {
			return nil, err
		}

		if symbol < firstCopySymbol {
			output = append(output, byte(symbol))
			continue
		}

		value, err := distanceTree.decode(distances)
		if err != nil {
			return nil, err
		}

		length := symbol - firstCopySymbol + minCopyLength
		distance := value + 1

		if distance > len(output) || len(output)+length > size {
			return nil, ErrCorrupt
		}

		// * Copied a byte at a time, as the copy may overlap what it writes
		for range length {
			output = append(output, output[len(output)-distance])
		}
	}

	return output, nil
}

// Compress returns data as an ASH0 stream
func Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, ErrEmpty
	}

	if len(data) > MaxSize {
		return nil, ErrTooLarge
	}

	tokens := findCopies(data)

	symbolUses := make([]int, symbolCount)
	distanceUses := make([]int, distanceCount)

	for _, t := range tokens {
		symbolUses[t.symbol]++

		if t.symbol >= firstCopySymbol {
			distanceUses[t.distance-1]++
		}
	}

	symbolTree := buildTree(symbolUses)
	distanceTree := buildTree(distanceUses)

	symbolCodes := make([]code, symbolCount)
	distanceCodes := make([]code, distanceCount)
	symbolTree.codes(symbolCodes, code{})
	distanceTree.codes(distanceCodes, code{})

	symbols := &bitWriter{}
	distances := &bitWriter{}

	symbolTree.write(symbols, symbolBits)
	distanceTree.write(distances, distanceBits)

	for _, t := range tokens {
		symbols.writeCode(symbolCodes[t.symbol])

		if t.symbol >= firstCopySymbol {
			distances.writeCode(distanceCodes[t.distance-1])
		}
	}

	symbolStream := symbols.finish()
	distanceStream := distances.finish()

	output := make([]byte, headerSize, headerSize+len(symbolStream)+len(distanceStream))
	copy(output, magic)
	binary.BigEndian.PutUint32(output[4:8], uint32(len(data)))
	binary.BigEndian.PutUint32(output[8:12], uint32(headerSize+len(symbolStream)))

	output = append(output, symbolStream...)
	output = append(output, distanceStream...)

	return output, nil
}

// * One step of the compressed data: a byte, or a copy of earlier output
type token struct {
	symbol   int
	distance int
}

const (
	hashBits = 15

	// * How many earlier positions are tried for each copy
	maxCandidates = 64
)

func hashAt(data []byte, position int) uint32 {
	value := uint32(data[position]) | uint32(data[position+1])<<8 | uint32(data[position+2])<<16

	return (value * 0x9E3779B1) >> (32 - hashBits)
}

// * Turns data into bytes and copies, taking the longest copy found at
// * each position
func findCopies(data []byte) []token {
	tokens := make([]token, 0, len(data)/2)

	// * head holds the last position seen for each hash, and previous
	// * the position before that with the same hash. Both hold position+1,
	// * so that 0 means none
	head := make([]int32, 1<<hashBits)
	previous := make([]int32, len(data))

	remember := func(position int) {
		if position+minCopyLength > len(data) {
			return
		}

		hash := hashAt(data, position)
		previous[position] = head[hash]
		head[hash] = int32(position + 1)
	}

	position := 0

	for position < len(data) {
		bestLength, bestDistance := 0, 0

		if position+minCopyLength <= len(data) {
			limit := min(maxCopyLength, len(data)-position)
			candidate := int(head[hashAt(data, position)]) - 1

			for tries := 0; candidate >= 0 && position-candidate <= maxDistance && tries < maxCandidates; tries++ {
				length := 0
				for length < limit && data[candidate+length] == data[position+length] {
					length++
				}

				if length > bestLength {
					bestLength, bestDistance = length, position-candidate

					if length == limit {
						break
					}
				}

				candidate = int(previous[candidate]) - 1
			}
		}

		if bestLength < minCopyLength {
			tokens = append(tokens, token{symbol: int(data[position])})
			remember(position)
			position++

			continue
		}

		tokens = append(tokens, token{symbol: firstCopySymbol + bestLength - minCopyLength, distance: bestDistance})

		for range bestLength {
			remember(position)
			position++
		}
	}

	return tokens
}

// * A Huffman tree. A node with no children is a leaf holding value
type node struct {
	value       int
	uses        int
	order       int
	left, right *node
}

type code struct {
	bits   uint64
	length int
}

type nodeHeap []*node

func (h nodeHeap) Len() int { return len(h) }

// * Ties are broken by order, so that the same data always gives the same tree
func (h nodeHeap) Less(i, j int) bool {
	if h[i].uses != h[j].uses {
		return h[i].uses < h[j].uses
	}

	return h[i].order < h[j].order
}

func (h nodeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x any)   { *h = append(*h, x.(*node)) }

func (h *nodeHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]

	return last
}

// * Builds a tree with a leaf for each value that is used, where the
// * more a value is used, the shorter its code is
func buildTree(uses []int) *node {
	nodes := nodeHeap{}

	for value, count := range uses {
		if count > 0 {
			nodes = append(nodes, &node{value: value, uses: count, order: value})
		}
	}

	// * The reader needs a tree to start with a node, so there are
	// * always at least two leaves, even when they are never used
	for value := 0; len(nodes) < 2; value++ {
		if uses[value] == 0 {
			nodes = append(nodes, &node{value: value, order: value})
		}
	}

	heap.Init(&nodes)

	for order := len(uses); nodes.Len() > 1; order++ {
		left := heap.Pop(&nodes).(*node)
		right := heap.Pop(&nodes).(*node)

		heap.Push(&nodes, &node{uses: left.uses + right.uses, order: order, left: left, right: right})
	}

	return nodes[0]
}

func (n *node) isLeaf() bool { return n.left == nil }

func (n *node) codes(codes []code, prefix code) {
	if n.isLeaf() {
		codes[n.value] = prefix
		return
	}

	n.left.codes(codes, code{bits: prefix.bits << 1, length: prefix.length + 1})
	n.right.codes(codes, code{bits: prefix.bits<<1 | 1, length: prefix.length + 1})
}

func (n *node) write(w *bitWriter, valueBits int) {
	if n.isLeaf() {
		w.writeBits(0, 1)
		w.writeBits(uint64(n.value), valueBits)

		return
	}

	w.writeBits(1, 1)
	n.left.write(w, valueBits)
	n.right.write(w, valueBits)
}

func (n *node) decode(r *bitReader) (int, error) {
	for !n.isLeaf() {
		bit, err := r.readBits(1)
		if err != nil {
			return 0, err
		}

		if bit == 0 {
			n = n.left
		} else {
			n = n.right
		}
	}

	return n.value, nil
}

func readTree(r *bitReader, valueBits int) (*node, error) {
	// * A tree can not have more leaves than there are values, which
	// * stops corrupt data from asking for an endless tree
	budget := 2 * (1 << valueBits)

	root, err := readNode(r, valueBits, &budget)
	if err != nil {
		return nil, err
	}

	// * The format has no way to write a tree that is a single leaf
	if root.isLeaf() {
		return nil, ErrCorrupt
	}

	return root, nil
}

func readNode(r *bitReader, valueBits int, budget *int) (*node, error) {
	*budget--
	if *budget < 0 {
		return nil, ErrCorrupt
	}

	isNode, err := r.readBits(1)
	if err != nil {
		return nil, err
	}

	if isNode == 0 {
		value, err := r.readBits(valueBits)
		if err != nil {
			return nil, err
		}

		return &node{value: int(value)}, nil
	}

	left, err := readNode(r, valueBits, budget)
	if err != nil {
		return nil, err
	}

	right, err := readNode(r, valueBits, budget)
	if err != nil {
		return nil, err
	}

	return &node{left: left, right: right}, nil
}

type bitReader struct {
	data     []byte
	position int
	current  uint32
	left     int
}

func (r *bitReader) readBits(count int) (uint32, error) {
	var value uint32

	for range count {
		if r.left == 0 {
			if r.position+4 > len(r.data) {
				return 0, errTruncated
			}

			r.current = binary.BigEndian.Uint32(r.data[r.position:])
			r.position += 4
			r.left = 32
		}

		value = value<<1 | r.current>>31
		r.current <<= 1
		r.left--
	}

	return value, nil
}

type bitWriter struct {
	data    []byte
	current uint32
	used    int
}

func (w *bitWriter) writeBits(bits uint64, count int) {
	for shift := count - 1; shift >= 0; shift-- {
		w.current = w.current<<1 | uint32(bits>>shift&1)
		w.used++

		if w.used == 32 {
			w.data = binary.BigEndian.AppendUint32(w.data, w.current)
			w.current, w.used = 0, 0
		}
	}
}

func (w *bitWriter) writeCode(c code) {
	// * A code longer than 64 bits would need more uses than the
	// * 16 MB size limit allows, so the code always fits
	w.writeBits(c.bits, c.length)
}

// * Ends the stream on a whole word, with nothing after it. Files made
// * by the game end each stream on the last word that holds any bits, and
// * the game finds the next part of a course file right after the end of
// * the one before, so a spare word here makes it lose the parts that
// * follow. A course packed with one loaded in the game with no blocks
func (w *bitWriter) finish() []byte {
	if w.used > 0 {
		w.data = binary.BigEndian.AppendUint32(w.data, w.current<<(32-w.used))
		w.current, w.used = 0, 0
	}

	return w.data
}
