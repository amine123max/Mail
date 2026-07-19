package mailservice

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	stdmail "net/mail"
	"net/textproto"
	"strings"
	"time"

	"golang.org/x/net/html/charset"
)

type parsedAttachment struct {
	ID          string
	Filename    string
	ContentType string
	ContentID   string
	Inline      bool
	Content     []byte
}

type parsedMessage struct {
	Subject     string
	From        string
	To          string
	CC          string
	Date        time.Time
	Text        string
	HTML        string
	Attachments []parsedAttachment
}

func parseMIMEMessage(source []byte) (parsedMessage, error) {
	message, err := stdmail.ReadMessage(bytes.NewReader(source))
	if err != nil {
		return parsedMessage{}, err
	}
	result := parsedMessage{
		Subject: decodeHeader(message.Header.Get("Subject")),
		From:    decodeHeader(message.Header.Get("From")),
		To:      decodeHeader(message.Header.Get("To")),
		CC:      decodeHeader(message.Header.Get("Cc")),
		Date:    time.Now().UTC(),
	}
	if date, err := stdmail.ParseDate(message.Header.Get("Date")); err == nil {
		result.Date = date
	}
	header := textproto.MIMEHeader(message.Header)
	if err := walkMIMEPart(header, message.Body, &result, nil); err != nil {
		return result, err
	}
	result.Text = strings.TrimSpace(result.Text)
	return result, nil
}

func walkMIMEPart(header textproto.MIMEHeader, body io.Reader, result *parsedMessage, path []int) error {
	mediaType, parameters, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || mediaType == "" {
		mediaType = "text/plain"
	}
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := parameters["boundary"]
		if boundary == "" {
			return nil
		}
		reader := multipart.NewReader(body, boundary)
		for index := 1; ; index++ {
			part, err := reader.NextPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			partPath := append(append([]int(nil), path...), index)
			if err := walkMIMEPart(part.Header, part, result, partPath); err != nil {
				_ = part.Close()
				return err
			}
			_ = part.Close()
		}
	}
	decoded, err := decodeTransfer(body, header.Get("Content-Transfer-Encoding"))
	if err != nil {
		return err
	}
	disposition, dispositionParams, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	filename := dispositionParams["filename"]
	if filename == "" {
		filename = parameters["name"]
	}
	filename = decodeHeader(filename)
	contentID := normalizeContentID(header.Get("Content-ID"))
	attachment := strings.EqualFold(disposition, "attachment") || filename != "" || contentID != ""
	if attachment {
		if len(path) == 0 {
			path = []int{1}
		}
		result.Attachments = append(result.Attachments, parsedAttachment{
			ID: stableMIMEAttachmentID(path), Filename: filename, ContentType: mediaType, ContentID: contentID,
			Inline: strings.EqualFold(disposition, "inline") || contentID != "", Content: decoded,
		})
		return nil
	}
	if mediaType == "text/plain" || mediaType == "text/html" {
		decoded = decodeCharset(decoded, parameters["charset"])
		if mediaType == "text/html" && result.HTML == "" {
			result.HTML = string(decoded)
		}
		if mediaType == "text/plain" && result.Text == "" {
			result.Text = string(decoded)
		}
	}
	return nil
}

func decodeTransfer(body io.Reader, encoding string) ([]byte, error) {
	var reader io.Reader = body
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		reader = quotedprintable.NewReader(body)
	}
	return io.ReadAll(io.LimitReader(reader, 32<<20))
}

func decodeCharset(value []byte, label string) []byte {
	label = strings.TrimSpace(label)
	if label == "" || strings.EqualFold(label, "utf-8") || strings.EqualFold(label, "us-ascii") {
		return value
	}
	reader, err := charset.NewReaderLabel(label, bytes.NewReader(value))
	if err != nil {
		return value
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return value
	}
	return decoded
}

func decodeHeader(value string) string {
	if value == "" {
		return ""
	}
	decoded, err := (&mime.WordDecoder{CharsetReader: func(charsetName string, input io.Reader) (io.Reader, error) {
		return charset.NewReaderLabel(charsetName, input)
	}}).DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

func (s *Service) parsedDetail(ctx context.Context, uid any, parsed parsedMessage) MessageDetail {
	inline := make(map[string]string)
	attachments := make([]Attachment, 0)
	for _, attachment := range parsed.Attachments {
		if attachment.Inline && attachment.ContentID != "" && safeImageContentType(attachment.ContentType) && len(attachment.Content) <= 2_000_000 {
			inline[attachment.ContentID] = "data:" + attachment.ContentType + ";base64," + base64.StdEncoding.EncodeToString(attachment.Content)
			continue
		}
		attachments = append(attachments, Attachment{ID: attachment.ID, Index: len(attachments), Filename: fallbackFilename(attachment.Filename, len(attachments)), ContentType: fallbackContentType(attachment.ContentType), Size: len(attachment.Content)})
	}
	htmlBody := ""
	if parsed.HTML != "" {
		htmlBody = s.renderMessageHTML(ctx, parsed.HTML, inline)
	}
	textBody := parsed.Text
	if textBody == "" && parsed.HTML != "" {
		textBody = stripHTML(parsed.HTML)
	}
	return MessageDetail{UID: uid, Subject: fallbackSubject(parsed.Subject), From: parsed.From, To: parsed.To, CC: parsed.CC, Date: parsed.Date.UTC().Format(time.RFC3339Nano), HTML: htmlBody, Text: textBody, Attachments: attachments}
}

func sourcePreview(source []byte) string {
	parsed, err := parseMIMEMessage(source)
	if err != nil {
		return ""
	}
	text := parsed.Text
	if text == "" {
		text = stripHTML(parsed.HTML)
	}
	return previewText(text)
}

func buildMIMEMessage(from string, request SendRequest) ([]byte, []string, string, error) {
	to, err := recipientAddresses(request.To)
	if err != nil {
		return nil, nil, "", err
	}
	cc, err := recipientAddresses(request.CC)
	if err != nil {
		return nil, nil, "", err
	}
	bcc, err := recipientAddresses(request.BCC)
	if err != nil {
		return nil, nil, "", err
	}
	accepted := append(append(append([]string{}, to...), cc...), bcc...)
	if len(to) == 0 {
		return nil, nil, "", serviceError("至少需要一个有效收件人", "INVALID_RECIPIENT_ADDRESS", 400)
	}
	boundaryMixed, boundaryAlternative := randomMIMEBoundary(), randomMIMEBoundary()
	messageID := "<" + randomMIMEBoundary() + "@mail.local>"
	var output bytes.Buffer
	writeMIMEHeader(&output, "From", from)
	writeMIMEHeader(&output, "To", strings.Join(to, ", "))
	if len(cc) > 0 {
		writeMIMEHeader(&output, "Cc", strings.Join(cc, ", "))
	}
	writeMIMEHeader(&output, "Subject", mime.QEncoding.Encode("UTF-8", request.Subject))
	writeMIMEHeader(&output, "Date", time.Now().Format(time.RFC1123Z))
	writeMIMEHeader(&output, "Message-ID", messageID)
	writeMIMEHeader(&output, "MIME-Version", "1.0")
	writeMIMEHeader(&output, "Content-Type", `multipart/mixed; boundary="`+boundaryMixed+`"`)
	output.WriteString("\r\n--" + boundaryMixed + "\r\n")
	output.WriteString(`Content-Type: multipart/alternative; boundary="` + boundaryAlternative + `"` + "\r\n\r\n")
	writeTextMIMEPart(&output, boundaryAlternative, "text/plain", request.Text)
	if request.HTML != "" {
		writeTextMIMEPart(&output, boundaryAlternative, "text/html", request.HTML)
	}
	output.WriteString("--" + boundaryAlternative + "--\r\n")
	for _, attachment := range request.Attachments {
		content, err := decodeAttachment(attachment)
		if err != nil {
			return nil, nil, "", err
		}
		filename := safeFilename(attachment.Filename)
		output.WriteString("--" + boundaryMixed + "\r\n")
		writeMIMEHeader(&output, "Content-Type", attachment.ContentType+`; name="`+mime.QEncoding.Encode("UTF-8", filename)+`"`)
		writeMIMEHeader(&output, "Content-Disposition", `attachment; filename="`+mime.QEncoding.Encode("UTF-8", filename)+`"`)
		writeMIMEHeader(&output, "Content-Transfer-Encoding", "base64")
		output.WriteString("\r\n")
		encoded := base64.StdEncoding.EncodeToString(content)
		for len(encoded) > 76 {
			output.WriteString(encoded[:76] + "\r\n")
			encoded = encoded[76:]
		}
		output.WriteString(encoded + "\r\n")
	}
	output.WriteString("--" + boundaryMixed + "--\r\n")
	return output.Bytes(), accepted, messageID, nil
}

func writeTextMIMEPart(output *bytes.Buffer, boundary, contentType, value string) {
	output.WriteString("--" + boundary + "\r\nContent-Type: " + contentType + "; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	w := quotedprintable.NewWriter(output)
	_, _ = w.Write([]byte(value))
	_ = w.Close()
	output.WriteString("\r\n")
}

func writeMIMEHeader(output *bytes.Buffer, name, value string) {
	output.WriteString(textproto.CanonicalMIMEHeaderKey(name) + ": " + value + "\r\n")
}

func randomMIMEBoundary() string {
	value := make([]byte, 18)
	_, _ = io.ReadFull(rand.Reader, value)
	return "mail-" + base64.RawURLEncoding.EncodeToString(value)
}
