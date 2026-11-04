// Package uuid generates RFC 9562 version 7 UUIDs: a 48-bit Unix millisecond
// timestamp followed by random bits. They sort by creation time, which keeps
// B-tree indexes on payload_logs.id compact and makes IDs roughly chronological.
package uuid

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// NewV7 returns a new UUIDv7 in canonical 8-4-4-4-12 form.
func NewV7() string { return newV7At(time.Now()) }

func newV7At(t time.Time) string {
	var u [16]byte
	if _, err := rand.Read(u[6:]); err != nil {
		panic("uuid: crypto/rand failed: " + err.Error()) // never happens on supported platforms
	}
	ms := uint64(t.UnixMilli())
	u[0] = byte(ms >> 40)
	u[1] = byte(ms >> 32)
	u[2] = byte(ms >> 24)
	u[3] = byte(ms >> 16)
	u[4] = byte(ms >> 8)
	u[5] = byte(ms)
	u[6] = (u[6] & 0x0f) | 0x70 // version 7
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 9562 variant

	var out [36]byte
	hex.Encode(out[0:8], u[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], u[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], u[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], u[8:10])
	out[23] = '-'
	hex.Encode(out[24:], u[10:])
	return string(out[:])
}
