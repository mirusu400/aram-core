package ktf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

var ErrProtectedContent = errors.New("KTF package contains protected OMA DRM content")

// ProtectedContentError reports a structurally valid OMA DRM object that
// cannot be opened because no Rights Object key is on hand for it. The loader
// deliberately does not guess or derive the key; supply it through
// RightsKeyEnv or SetRightsKeys.
type ProtectedContentError struct {
	Path      string
	ContentID string
	Algorithm uint16
	// WrongKey marks the case where a key was available but did not decrypt
	// the object, as opposed to no key being configured at all.
	WrongKey bool
}

func (e *ProtectedContentError) Error() string {
	content := ""
	if e.ContentID != "" {
		content = fmt.Sprintf(" content %q", e.ContentID)
	}
	if e.WrongKey {
		return fmt.Sprintf(
			"KTF package %q: OMA DRM%s did not decrypt with the configured rights key",
			e.Path,
			content,
		)
	}
	return fmt.Sprintf(
		"KTF package %q: OMA DRM%s uses %s and requires a rights key"+
			" (set %s to a file naming the content key)",
		e.Path,
		content,
		omaDRMAlgorithmName(e.Algorithm),
		RightsKeyEnv,
	)
}

func (e *ProtectedContentError) Unwrap() error {
	return ErrProtectedContent
}

type omaDCFBox struct {
	kind       string
	start      int
	payload    int
	end        int
	headerSize int
}

type omaDCFHeaders struct {
	algorithm      uint16
	padding        uint16
	plaintextBytes uint64
	contentID      string
}

func isOMADCF(data []byte) bool {
	return len(data) >= 4 && bytes.Equal(data[:4], []byte("odcf"))
}

// unwrapOMADCF returns the embedded object only when the DCF explicitly uses
// the NULL encryption method. AES-protected objects require a carrier/device
// Rights Object and are returned as ErrProtectedContent instead of being
// misreported as corrupt ZIP files.
func unwrapOMADCF(data []byte, label string) ([]byte, error) {
	if len(data) < 8 || !isOMADCF(data) {
		return nil, &FormatError{Path: label, Reason: "invalid OMA DRM header"}
	}
	if version := binary.BigEndian.Uint16(data[4:6]); version != 2 {
		return nil, &FormatError{
			Path:   label,
			Reason: fmt.Sprintf("unsupported OMA DRM DCF version %d", version),
		}
	}

	container, err := parseOMADCFBox(data, 8, len(data), label)
	if err != nil {
		return nil, err
	}
	if container.kind != "odrm" {
		return nil, omaDCFFormatError(label, container.start, "missing odrm container")
	}
	children, err := omaDCFFullBoxChildren(data, container, label)
	if err != nil {
		return nil, err
	}
	var headerBox, dataBox omaDCFBox
	for offset := children; offset < container.end; {
		box, parseErr := parseOMADCFBox(data, offset, container.end, label)
		if parseErr != nil {
			return nil, parseErr
		}
		switch box.kind {
		case "odhe":
			if headerBox.end != 0 {
				return nil, omaDCFFormatError(label, box.start, "multiple odhe boxes")
			}
			headerBox = box
		case "odda":
			if dataBox.end != 0 {
				return nil, omaDCFFormatError(label, box.start, "multiple odda boxes")
			}
			dataBox = box
		}
		offset = box.end
	}
	if headerBox.end == 0 || dataBox.end == 0 {
		return nil, omaDCFFormatError(label, container.start, "odhe or odda box is missing")
	}

	headers, err := parseOMADCFHeaders(data, headerBox, label)
	if err != nil {
		return nil, err
	}
	object, err := parseOMADCFObject(data, dataBox, label)
	if err != nil {
		return nil, err
	}
	if headers.plaintextBytes > MaxMemberSize {
		return nil, &FormatError{Path: label, Reason: "OMA DRM plaintext exceeds size limit"}
	}
	if headers.algorithm != 0 {
		return openProtectedOMADCF(object, headers, label)
	}
	if headers.padding != 0 {
		return nil, omaDCFFormatError(
			label,
			headerBox.start,
			"NULL-encrypted OMA DRM object has nonzero padding",
		)
	}
	if uint64(len(object)) != headers.plaintextBytes {
		return nil, omaDCFFormatError(
			label,
			dataBox.start,
			fmt.Sprintf(
				"OMA DRM plaintext length is %d, want %d",
				len(object),
				headers.plaintextBytes,
			),
		)
	}
	return object, nil
}

// openProtectedOMADCF decrypts an AES-protected OMA DRM object when the
// rights-key store holds the content-encryption key a KTF Rights Object
// carried for it.
//
// KTF issues AES-128-CTR objects whose OMADRMData length equals the plaintext
// length, so the object carries no counter-block prefix and decryption starts
// from an all-zero counter. The spec's prefixed-counter layout is accepted
// too, for objects that do carry one.
func openProtectedOMADCF(
	object []byte,
	headers omaDCFHeaders,
	label string,
) ([]byte, error) {
	key := lookupRightsKey(headers.contentID)
	if key == nil || headers.algorithm != omaDRMAlgorithmAESCTR {
		return nil, &ProtectedContentError{
			Path:      label,
			ContentID: headers.contentID,
			Algorithm: headers.algorithm,
		}
	}
	if headers.padding != 0 {
		return nil, omaDCFFormatError(
			label,
			0,
			"AES-CTR OMA DRM object has nonzero padding",
		)
	}

	var counter [aes.BlockSize]byte
	body := object
	switch {
	case uint64(len(body)) == headers.plaintextBytes:
	case uint64(len(body)) == headers.plaintextBytes+aes.BlockSize:
		copy(counter[:], body[:aes.BlockSize])
		body = body[aes.BlockSize:]
	default:
		return nil, omaDCFFormatError(
			label,
			0,
			fmt.Sprintf(
				"OMA DRM ciphertext is %d bytes, want %d",
				len(body),
				headers.plaintextBytes,
			),
		)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, &ProtectedContentError{
			Path:      label,
			ContentID: headers.contentID,
			Algorithm: headers.algorithm,
			WrongKey:  true,
		}
	}
	plaintext := make([]byte, len(body))
	cipher.NewCTR(block, counter[:]).XORKeyStream(plaintext, body)
	if !bytes.HasPrefix(plaintext, []byte("PK\x03\x04")) {
		return nil, &ProtectedContentError{
			Path:      label,
			ContentID: headers.contentID,
			Algorithm: headers.algorithm,
			WrongKey:  true,
		}
	}
	return plaintext, nil
}

func parseOMADCFHeaders(
	data []byte,
	box omaDCFBox,
	label string,
) (omaDCFHeaders, error) {
	offset, err := omaDCFFullBoxChildren(data, box, label)
	if err != nil {
		return omaDCFHeaders{}, err
	}
	if offset >= box.end {
		return omaDCFHeaders{}, omaDCFFormatError(
			label,
			box.start,
			"odhe content type length is missing",
		)
	}
	contentTypeBytes := int(data[offset])
	offset++
	if contentTypeBytes > box.end-offset {
		return omaDCFHeaders{}, omaDCFFormatError(
			label,
			box.start,
			"odhe content type is truncated",
		)
	}
	offset += contentTypeBytes
	common, err := parseOMADCFBox(data, offset, box.end, label)
	if err != nil {
		return omaDCFHeaders{}, err
	}
	if common.kind != "ohdr" {
		return omaDCFHeaders{}, omaDCFFormatError(
			label,
			common.start,
			"odhe does not begin with an ohdr box",
		)
	}
	offset, err = omaDCFFullBoxChildren(data, common, label)
	if err != nil {
		return omaDCFHeaders{}, err
	}
	const fixedBytes = 2 + 2 + 8 + 2 + 2 + 2
	if common.end-offset < fixedBytes {
		return omaDCFHeaders{}, omaDCFFormatError(
			label,
			common.start,
			"ohdr fields are truncated",
		)
	}
	headers := omaDCFHeaders{
		algorithm:      binary.BigEndian.Uint16(data[offset:]),
		padding:        binary.BigEndian.Uint16(data[offset+2:]),
		plaintextBytes: binary.BigEndian.Uint64(data[offset+4:]),
	}
	contentIDBytes := int(binary.BigEndian.Uint16(data[offset+12:]))
	issuerBytes := int(binary.BigEndian.Uint16(data[offset+14:]))
	textBytes := int(binary.BigEndian.Uint16(data[offset+16:]))
	offset += fixedBytes
	totalStrings := contentIDBytes + issuerBytes + textBytes
	if totalStrings < contentIDBytes || totalStrings > common.end-offset {
		return omaDCFHeaders{}, omaDCFFormatError(
			label,
			common.start,
			"ohdr strings are truncated",
		)
	}
	headers.contentID = strings.ToValidUTF8(
		string(bytes.TrimRight(data[offset:offset+contentIDBytes], "\x00")),
		"\ufffd",
	)
	if len(headers.contentID) > 255 {
		headers.contentID = headers.contentID[:255]
	}
	return headers, nil
}

func parseOMADCFObject(data []byte, box omaDCFBox, label string) ([]byte, error) {
	offset, err := omaDCFFullBoxChildren(data, box, label)
	if err != nil {
		return nil, err
	}
	if box.end-offset < 8 {
		return nil, omaDCFFormatError(label, box.start, "odda data length is truncated")
	}
	size := binary.BigEndian.Uint64(data[offset:])
	offset += 8
	if size > uint64(box.end-offset) {
		return nil, omaDCFFormatError(label, box.start, "odda data is truncated")
	}
	return data[offset : offset+int(size)], nil
}

func omaDCFFullBoxChildren(
	data []byte,
	box omaDCFBox,
	label string,
) (int, error) {
	if box.end-box.payload < 4 {
		return 0, omaDCFFormatError(label, box.start, box.kind+" FullBox header is truncated")
	}
	if version := data[box.payload]; version != 0 {
		return 0, omaDCFFormatError(
			label,
			box.start,
			fmt.Sprintf("%s FullBox version %d is unsupported", box.kind, version),
		)
	}
	return box.payload + 4, nil
}

func parseOMADCFBox(
	data []byte,
	offset, limit int,
	label string,
) (omaDCFBox, error) {
	if offset < 0 || limit < offset || limit > len(data) || limit-offset < 8 {
		return omaDCFBox{}, omaDCFFormatError(label, offset, "truncated OMA DRM box header")
	}
	size := uint64(binary.BigEndian.Uint32(data[offset:]))
	headerSize := 8
	if size == 1 {
		if limit-offset < 16 {
			return omaDCFBox{}, omaDCFFormatError(
				label,
				offset,
				"truncated OMA DRM large-size box header",
			)
		}
		size = binary.BigEndian.Uint64(data[offset+8:])
		headerSize = 16
	} else if size == 0 {
		size = uint64(limit - offset)
	}
	if size < uint64(headerSize) || size > uint64(limit-offset) {
		return omaDCFBox{}, omaDCFFormatError(label, offset, "invalid OMA DRM box size")
	}
	end := offset + int(size)
	return omaDCFBox{
		kind:       string(data[offset+4 : offset+8]),
		start:      offset,
		payload:    offset + headerSize,
		end:        end,
		headerSize: headerSize,
	}, nil
}

func omaDCFFormatError(label string, offset int, reason string) error {
	return &FormatError{
		Path:   label,
		Reason: fmt.Sprintf("invalid OMA DRM DCF at offset 0x%x: %s", offset, reason),
	}
}

const (
	omaDRMAlgorithmNull   uint16 = 0
	omaDRMAlgorithmAESCBC uint16 = 1
	omaDRMAlgorithmAESCTR uint16 = 2
)

func omaDRMAlgorithmName(algorithm uint16) string {
	switch algorithm {
	case omaDRMAlgorithmNull:
		return "NULL encryption"
	case omaDRMAlgorithmAESCBC:
		return "AES-128-CBC"
	case omaDRMAlgorithmAESCTR:
		return "AES-128-CTR"
	default:
		return fmt.Sprintf("encryption algorithm 0x%04x", algorithm)
	}
}
