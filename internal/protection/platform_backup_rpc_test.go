// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"bytes"
	"testing"

	"google.golang.org/grpc"

	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
)

type fakeExportStream struct {
	grpc.ServerStream
	sent [][]byte
}

func (f *fakeExportStream) Send(m *controlv1.ExportPlatformResponse) error {
	f.sent = append(f.sent, bytes.Clone(m.Data))
	return nil
}

func TestChunkSenderBatchesMiB(t *testing.T) {
	fs := &fakeExportStream{}
	cs := &chunkSender{stream: fs, buf: make([]byte, 0, platformChunkSize)}
	data := bytes.Repeat([]byte{7}, 2*platformChunkSize+123)
	for i := 0; i < len(data); i += 64 << 10 {
		if _, err := cs.Write(data[i:min(i+64<<10, len(data))]); err != nil {
			t.Fatal(err)
		}
	}
	if err := cs.flush(); err != nil {
		t.Fatal(err)
	}
	if len(fs.sent) != 3 || len(fs.sent[0]) != platformChunkSize || len(fs.sent[2]) != 123 {
		t.Fatalf("chunks: %d", len(fs.sent))
	}
	if !bytes.Equal(bytes.Join(fs.sent, nil), data) {
		t.Fatal("data mismatch")
	}
}
