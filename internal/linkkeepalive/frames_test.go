package linkkeepalive

import (
	"encoding/binary"
	"testing"
)

func TestFrameObserverBoundaries(t *testing.T) {
	// A text fragment, two empty continuations (one masked), then a ping.
	frames := [][]byte{
		{0x01, 0x03, 'a', 'b', 'c'},
		{0x00, 0x00},
		{0x00, 0x80, 1, 2, 3, 4},
		{0x89, 0x02, 0x00, 0x00},
	}
	// Exercise both extended length encodings, with payload bytes that also
	// look like frame headers. The observer must not treat them as new frames.
	medium := append([]byte{0x82, 126, 0, 126}, make([]byte, 126)...)
	maskedMedium := append([]byte{0x82, 0xfe, 0, 126, 1, 2, 3, 4}, make([]byte, 126)...)
	large := make([]byte, 10+65536)
	large[0], large[1] = 0x82, 127
	binary.BigEndian.PutUint64(large[2:10], 65536)
	frames = append(frames, medium, maskedMedium, large)

	for _, split := range []bool{false, true} {
		count := 0
		observer := frameObserver{received: func() { count++ }}
		for i, frame := range frames {
			if split {
				for j, b := range frame {
					observer.observe([]byte{b})
					want := i
					if j == len(frame)-1 {
						want++
					}
					if count != want {
						t.Fatalf("split frame %d byte %d: got %d completions, want %d", i, j, count, want)
					}
				}
			} else {
				observer.observe(frame)
				if count != i+1 {
					t.Fatalf("frame %d: got %d completions", i, count)
				}
			}
		}
	}

	count := 0
	observer := frameObserver{received: func() { count++ }}
	observer.observe(append(append([]byte{}, frames[0]...), frames[1]...))
	if count != 2 {
		t.Fatalf("coalesced frames: got %d completions", count)
	}
}
