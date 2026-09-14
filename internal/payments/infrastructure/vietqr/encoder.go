package vietqr

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"

	qrcode "github.com/skip2/go-qrcode"
)

var ErrInvalid = errors.New("invalid VietQR instruction")

type Encoder struct{}

func (Encoder) Encode(bankBIN, accountNumber, reference string, amountMinor int64) (string, string, error) {
	if len(bankBIN) != 6 || len(accountNumber) < 1 || len(accountNumber) > 19 || len(reference) < 1 || len(reference) > 25 || amountMinor < 0 {
		return "", "", ErrInvalid
	}
	beneficiary := field("00", bankBIN) + field("01", accountNumber)
	merchant := field("00", "A000000727") + field("01", beneficiary) + field("02", "QRIBFTTA")
	payload := field("00", "01") + field("01", "12") + field("38", merchant) + field("53", "704") + field("54", strconv.FormatInt(amountMinor, 10)) + field("58", "VN") + field("62", field("08", reference)) + "6304"
	payload += fmt.Sprintf("%04X", crc16([]byte(payload)))
	png, err := qrcode.Encode(payload, qrcode.Medium, 320)
	if err != nil {
		return "", "", fmt.Errorf("render VietQR image: %w", err)
	}
	return payload, base64.StdEncoding.EncodeToString(png), nil
}

func field(id, value string) string {
	return id + fmt.Sprintf("%02d", len(value)) + value
}

func crc16(value []byte) uint16 {
	crc := uint16(0xffff)
	for _, part := range value {
		crc ^= uint16(part) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
