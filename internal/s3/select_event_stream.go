package s3

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
)

// writeESMessage encodes and writes a single AWS event stream message.
// All header values use string type (type=7) per the S3 Select protocol.
//
// Message layout:
//
//	[total_length:4B BE][headers_length:4B BE][prelude_crc:4B][headers][payload][msg_crc:4B]
func writeESMessage(w io.Writer, headers [][2]string, payload []byte) error {
	hbuf := encodeESHeaders(headers)

	msgSize := 16 + len(hbuf) + len(payload)
	totalLen := uint32(msgSize)     //nolint:gosec // payload <4GB for dev emulator
	headersLen := uint32(len(hbuf)) //nolint:gosec // headers are short strings

	var prelude [8]byte
	binary.BigEndian.PutUint32(prelude[0:4], totalLen)
	binary.BigEndian.PutUint32(prelude[4:8], headersLen)
	preludeCRC := crc32.ChecksumIEEE(prelude[:])

	msg := make([]byte, 0, totalLen)
	msg = append(msg, prelude[:]...)
	msg = binary.BigEndian.AppendUint32(msg, preludeCRC)
	msg = append(msg, hbuf...)
	msg = append(msg, payload...)
	msgCRC := crc32.ChecksumIEEE(msg)
	msg = binary.BigEndian.AppendUint32(msg, msgCRC)

	_, err := w.Write(msg)
	return err
}

// encodeESHeaders serialises event stream headers.
// Format per header: [name_len:1B][name][type:1B=7][value_len:2B BE][value]
func encodeESHeaders(headers [][2]string) []byte {
	var buf []byte
	for _, h := range headers {
		name := []byte(h[0])
		value := []byte(h[1])
		nameLen := len(name)
		buf = append(buf, byte(nameLen)) //nolint:gosec // name is a short protocol string
		buf = append(buf, name...)
		buf = append(buf, 7)       // string type
		vLen := uint16(len(value)) //nolint:gosec // header value is a short protocol string
		buf = binary.BigEndian.AppendUint16(buf, vLen)
		buf = append(buf, value...)
	}
	return buf
}

func writeRecordsEvent(w io.Writer, payload []byte) error {
	return writeESMessage(w, [][2]string{
		{":message-type", "event"},
		{":event-type", "Records"},
		{":content-type", "application/octet-stream"},
	}, payload)
}

func writeStatsEvent(w io.Writer, bytesScanned, bytesProcessed, bytesReturned int64) error {
	payload := fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?>`+
			`<Stats>`+
			`<BytesScanned>%d</BytesScanned>`+
			`<BytesProcessed>%d</BytesProcessed>`+
			`<BytesReturned>%d</BytesReturned>`+
			`</Stats>`,
		bytesScanned, bytesProcessed, bytesReturned,
	)
	return writeESMessage(w, [][2]string{
		{":message-type", "event"},
		{":event-type", "Stats"},
		{":content-type", "text/xml"},
	}, []byte(payload))
}

func writeEndEvent(w io.Writer) error {
	return writeESMessage(w, [][2]string{
		{":message-type", "event"},
		{":event-type", "End"},
	}, nil)
}

func writeESErrorEvent(w io.Writer, code, message string) error {
	return writeESMessage(w, [][2]string{
		{":message-type", "error"},
		{":error-code", code},
		{":error-message", message},
	}, nil)
}
