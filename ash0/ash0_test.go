package ash0

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"testing"
)

func testInputs() map[string][]byte {
	random := rand.New(rand.NewPCG(1, 2))

	noise := make([]byte, 70000)
	for i := range noise {
		noise[i] = byte(random.IntN(256))
	}

	// * Shaped like course data: mostly empty, with short records in it
	sparse := make([]byte, 86016)
	for i := 0; i < 400; i++ {
		at := random.IntN(len(sparse) - 32)
		for j := range 8 + random.IntN(24) {
			sparse[at+j] = byte(random.IntN(16))
		}
	}

	return map[string][]byte{
		"one byte":     {0x42},
		"two bytes":    {0x00, 0xFF},
		"one value":    bytes.Repeat([]byte{7}, 5000),
		"longest copy": bytes.Repeat([]byte("ab"), 600),
		"text":         bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 200),
		"every byte value": func() []byte {
			b := make([]byte, 256)
			for i := range b {
				b[i] = byte(i)
			}
			return b
		}(),
		"noise":      noise,
		"sparse":     sparse,
		"far repeat": append(append(append([]byte{}, noise[:1500]...), make([]byte, 100)...), noise[:1500]...),
	}
}

func TestCompressThenDecompressGivesTheSameData(t *testing.T) {
	for name, input := range testInputs() {
		t.Run(name, func(t *testing.T) {
			compressed, err := Compress(input)
			if err != nil {
				t.Fatal(err)
			}

			if !IsCompressed(compressed) {
				t.Fatal("the output does not have an ASH0 header")
			}

			if size, err := DecompressedSize(compressed); err != nil || size != len(input) {
				t.Fatalf("header says %d bytes (%v), want %d", size, err, len(input))
			}

			if len(compressed)%4 != 0 {
				t.Errorf("the output is %d bytes, which is not a whole number of words", len(compressed))
			}

			output, err := Decompress(compressed)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(output, input) {
				t.Fatalf("got %d bytes back that differ from the %d put in", len(output), len(input))
			}
		})
	}
}

func TestCompressMakesRepetitiveDataSmaller(t *testing.T) {
	inputs := testInputs()

	for name, most := range map[string]int{"one value": 100, "sparse": 12000, "text": 300, "far repeat": 1900} {
		compressed, err := Compress(inputs[name])
		if err != nil {
			t.Fatal(err)
		}

		if len(compressed) > most {
			t.Errorf("%s: %d bytes became %d, want at most %d", name, len(inputs[name]), len(compressed), most)
		}
	}
}

func TestCompressRefusesWhatTheFormatCanNotHold(t *testing.T) {
	if _, err := Compress(nil); !errors.Is(err, ErrEmpty) {
		t.Errorf("no data: got %v, want ErrEmpty", err)
	}

	if _, err := Compress(make([]byte, MaxSize+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too much data: got %v, want ErrTooLarge", err)
	}
}

// * A stream written by hand from the format's rules, not by Compress:
// * symbol tree (node, leaf 'A', leaf 0x100), symbols A then a copy of 3,
// * distance tree (node, leaf 0, leaf 1), distance code 0 meaning 1 back
func TestDecompressReadsAHandWrittenStream(t *testing.T) {
	stream := []byte{
		'A', 'S', 'H', '0',
		0, 0, 0, 4,
		0, 0, 0, 0x14,
		// * 1 | 0 001000001 | 0 100000000 | 0 | 1, then padding
		0b1000_1000, 0b0010_1000, 0b0000_0010, 0,
		0, 0, 0, 0,
		// * 1 | 0 00000000000 | 0 00000000001 | 0, then padding
		0b1000_0000, 0b0000_0000, 0b0000_0000, 0b1000_0000,
		0, 0, 0, 0,
	}

	output, err := Decompress(stream)
	if err != nil {
		t.Fatal(err)
	}

	if string(output) != "AAAA" {
		t.Errorf("got %q, want AAAA", output)
	}
}

func TestDecompressRefusesBadData(t *testing.T) {
	good, err := Compress(bytes.Repeat([]byte("course data "), 500))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Decompress([]byte("not ash0 data at all")); !errors.Is(err, ErrNotASH0) {
		t.Errorf("wrong header: got %v, want ErrNotASH0", err)
	}

	if _, err := Decompress(good[:len(good)/2]); err == nil {
		t.Error("data cut in half was accepted")
	}

	badOffset := bytes.Clone(good)
	badOffset[8] = 0x7F
	if _, err := Decompress(badOffset); !errors.Is(err, ErrCorrupt) {
		t.Errorf("distance stream outside the data: got %v, want ErrCorrupt", err)
	}

	// * Changing bytes must never crash or hang, whatever it returns
	random := rand.New(rand.NewPCG(3, 4))
	for range 2000 {
		damaged := bytes.Clone(good)
		for range 1 + random.IntN(4) {
			damaged[4+random.IntN(len(damaged)-4)] = byte(random.IntN(256))
		}

		// * Keep the claimed size small so a damaged size can not ask for 16 MB each time
		damaged[5] = 0
		_, _ = Decompress(damaged)
	}
}
