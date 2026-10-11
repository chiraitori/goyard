package hub

import (
	"bytes"
	"compress/zlib"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func decompressZlib(t *testing.T, data []byte) []byte {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("failed to decompress: %v", err)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("readall error: %v", err)
	}
	return out
}

func TestHubPublishSingleDelivery(t *testing.T) {
	h := NewHub()

	var closed atomic.Bool
	client := NewClient(h.NextID(), false, func(code int, reason string) {
		closed.Store(true)
	})
	h.AddClient(client)
	defer h.RemoveClient(client)

	// Subscribe client to user AND global
	h.SubscribeUser(client, "user_1")
	h.SubscribeAll(client)

	h.Publish("user_1", []byte(`{"status":"online"}`))

	// Should receive exactly 1 message
	select {
	case f := <-client.Send:
		if string(f.Data) != `{"op":0,"t":"PRESENCE_UPDATE","d":{"status":"online"}}` {
			t.Errorf("unexpected data: %s", string(f.Data))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected message not received")
	}

	// Verify no duplicate message
	select {
	case f := <-client.Send:
		t.Fatalf("unexpected duplicate message: %s", string(f.Data))
	default:
	}
}

func TestHubZlibCompression(t *testing.T) {
	h := NewHub()

	client := NewClient(h.NextID(), true, nil)
	h.AddClient(client)
	defer h.RemoveClient(client)

	h.SubscribeUser(client, "user_zlib")

	rawJSON := `{"status":"dnd"}`
	h.Publish("user_zlib", []byte(rawJSON))

	select {
	case f := <-client.Send:
		if !f.IsZlib {
			t.Error("expected frame to be zlib")
		}
		decompressed := decompressZlib(t, f.Data)
		expected := `{"op":0,"t":"PRESENCE_UPDATE","d":{"status":"dnd"}}`
		if string(decompressed) != expected {
			t.Errorf("expected %s, got %s", expected, string(decompressed))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected frame not received")
	}
}

func TestHubSlowConsumerDropped(t *testing.T) {
	h := NewHub()

	var closeCode atomic.Int32
	var closeReason atomic.Value

	slowClient := NewClient(h.NextID(), false, func(code int, reason string) {
		closeCode.Store(int32(code))
		closeReason.Store(reason)
	})
	h.AddClient(slowClient)
	defer h.RemoveClient(slowClient)

	normalClient := NewClient(h.NextID(), false, nil)
	h.AddClient(normalClient)
	defer h.RemoveClient(normalClient)

	h.SubscribeUser(slowClient, "user_slow")
	h.SubscribeUser(normalClient, "user_slow")

	// Fill slow client's buffer (capacity 64)
	for i := 0; i < 64; i++ {
		slowClient.Send <- Frame{Data: []byte("fill")}
	}

	// Publisher sends another message
	h.Publish("user_slow", []byte(`{"test":1}`))

	// Normal client must receive it promptly without being blocked
	select {
	case f := <-normalClient.Send:
		if !bytes.Contains(f.Data, []byte(`"test":1`)) {
			t.Errorf("normal client got unexpected data: %s", string(f.Data))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("normal client timed out waiting for message")
	}

	// Slow client should have triggered closeFn(1013, "slow_consumer")
	if closeCode.Load() != 1013 {
		t.Errorf("expected closeCode 1013, got %d", closeCode.Load())
	}
	if reason, _ := closeReason.Load().(string); reason != "slow_consumer" {
		t.Errorf("expected reason 'slow_consumer', got %s", reason)
	}
}

func TestHubUnsubscribeVariants(t *testing.T) {
	h := NewHub()

	client := NewClient(h.NextID(), false, nil)
	h.AddClient(client)
	defer h.RemoveClient(client)

	h.SubscribeUser(client, "u1")
	h.SubscribeUser(client, "u2")
	h.SubscribeAll(client)

	h.UnsubscribeUser(client, "u1")
	// Still subscribed via global
	h.Publish("u1", []byte(`1`))
	<-client.Send

	h.UnsubscribeAll(client)
	// Now neither u1 nor u2 should reach client
	h.Publish("u1", []byte(`2`))
	h.Publish("u2", []byte(`3`))

	select {
	case f := <-client.Send:
		t.Fatalf("unexpected message after unsubscribeAll: %s", string(f.Data))
	default:
	}
}

func TestHubConcurrency(t *testing.T) {
	h := NewHub()
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		c := NewClient(h.NextID(), i%2 == 0, nil)
		h.AddClient(c)
		h.SubscribeAll(c)
		h.SubscribeUser(c, "user_concurrent")

		wg.Add(1)
		go func(client *Client) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				select {
				case <-client.Send:
				case <-time.After(100 * time.Millisecond):
				}
			}
		}(c)
	}

	for i := 0; i < 50; i++ {
		h.Publish("user_concurrent", []byte(`{"count":1}`))
	}

	wg.Wait()
}
