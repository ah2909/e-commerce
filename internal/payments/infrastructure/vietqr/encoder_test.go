package vietqr

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

func TestEncoderProducesDynamicVietQRWithValidCRC(t *testing.T) {
	payload, image, err := (Encoder{}).Encode("970436", "0017040702738", "VQABCDEFGHIJKLMNOPQR", 125000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(payload, "000201010212") || !strings.Contains(payload, "A000000727") || !strings.Contains(payload, "970436") || !strings.Contains(payload, "0017040702738") || !strings.Contains(payload, "125000") || !strings.Contains(payload, "VQABCDEFGHIJKLMNOPQR") {
		t.Fatalf("payload = %q", payload)
	}
	wantCRC := fmt.Sprintf("%04X", crc16([]byte(payload[:len(payload)-4])))
	if payload[len(payload)-4:] != wantCRC {
		t.Fatalf("CRC = %q, want %q", payload[len(payload)-4:], wantCRC)
	}
	pngBytes, err := base64.StdEncoding.DecodeString(image)
	if err != nil || len(pngBytes) < 8 || string(pngBytes[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("PNG is invalid: bytes=%d error=%v", len(pngBytes), err)
	}
	decodedImage, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(decodedImage)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := qrcode.NewQRCodeReader().Decode(bitmap, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.GetText() != payload {
		t.Fatalf("decoded QR payload mismatch: got=%q want=%q", decoded.GetText(), payload)
	}
}

func TestEncoderRejectsInvalidFields(t *testing.T) {
	for _, test := range []struct {
		bank, account, reference string
		amount                   int64
	}{{"97043", "1234", "VQREF", 1}, {"970436", "", "VQREF", 1}, {"970436", "1234", "", 1}, {"970436", "1234", "VQREF", -1}} {
		if _, _, err := (Encoder{}).Encode(test.bank, test.account, test.reference, test.amount); err == nil {
			t.Fatalf("Encode(%#v) succeeded", test)
		}
	}
}
