// Minimal server→client WebSocket (RFC 6455, text frames only, stdlib).
// Why not SSE: an SSE stream is a plain HTTP/1.1 request, and browsers allow
// only 6 per host across ALL tabs — a few open archive tabs starve every
// /api call. WebSockets sit outside that per-host pool.
package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

func wsFrame(op byte, payload []byte) []byte {
	n := len(payload)
	hdr := []byte{0x80 | op}
	switch {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n < 1<<16:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	return append(hdr, payload...)
}

func (h *hub) handleWS(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || key == "" {
		http.Error(w, "websocket only", 400)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "no hijack", 500)
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
		base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
	if rw.Flush() != nil {
		return
	}

	ch := make(chan string, 64)
	h.mu.Lock()
	h.subs[ch] = true
	st := h.status
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.subs, ch); h.mu.Unlock() }()

	// reader: we ignore client frames; any read error or close frame ends the session
	done := make(chan struct{})
	go func() {
		defer close(done)
		readUntilClose(rw.Reader)
	}()

	send := func(p []byte, op byte) bool {
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, err := conn.Write(wsFrame(op, p))
		return err == nil
	}
	first, _ := json.Marshal(map[string]any{"event": "status", "data": map[string]any{"status": st}})
	if !send(first, 0x1) {
		return
	}
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-done:
			return
		case f := <-ch:
			if !send([]byte(f), 0x1) {
				return
			}
		case <-ping.C:
			if !send(nil, 0x9) {
				return
			}
		}
	}
}

// readUntilClose consumes masked client frames until a close frame or error.
func readUntilClose(r *bufio.Reader) {
	var hdr [2]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return
		}
		if hdr[0]&0x0f == 0x8 {
			return
		}
		n := uint64(hdr[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(r, b[:]); err != nil {
				return
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(r, b[:]); err != nil {
				return
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if hdr[1]&0x80 != 0 {
			n += 4 // mask key
		}
		if _, err := io.CopyN(io.Discard, r, int64(n)); err != nil {
			return
		}
	}
}
