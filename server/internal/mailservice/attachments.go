package mailservice

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
)

const MaxAttachmentDownloadBytes int64 = 100 << 20

func stableMIMEAttachmentID(path []int) string {
	parts := make([]string, len(path))
	for index, value := range path {
		parts[index] = strconv.Itoa(value)
	}
	digest := sha256.Sum256([]byte("mime-part\x00" + strings.Join(parts, ".")))
	return "mime:" + hex.EncodeToString(digest[:])
}

func graphAttachmentID(value string) string {
	return "graph:" + base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeGraphAttachmentID(value string) (string, bool) {
	if !strings.HasPrefix(value, "graph:") {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "graph:"))
	if err != nil || len(decoded) == 0 || len(decoded) > 1000 {
		return "", false
	}
	return string(decoded), true
}
