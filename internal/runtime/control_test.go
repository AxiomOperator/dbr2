// SPDX-License-Identifier: Apache-2.0

package runtime

import "testing"

func TestTailBuffer(t *testing.T) {
	b := NewTailBuffer(4)
	_, _ = b.Write([]byte("ab"))
	if string(b.Bytes()) != "ab" || b.Truncated() {
		t.Fatalf("got %q %v", b.Bytes(), b.Truncated())
	}
	_, _ = b.Write([]byte("cdef"))
	if string(b.Bytes()) != "cdef" || !b.Truncated() {
		t.Fatalf("got %q %v", b.Bytes(), b.Truncated())
	}
}
