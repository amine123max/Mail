package mailservice

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPXOAUTH2Protocol(t *testing.T) {
	certificate, roots := smtpTestCertificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverError := make(chan error, 1)
	received := make(chan string, 1)
	go func() {
		serverError <- serveMockSMTP(listener, certificate, received)
	}()

	endpoint := smtpEndpoint{
		Address: listener.Addr().String(), ServerName: "localhost",
		TLSConfig: &tls.Config{ServerName: "localhost", RootCAs: roots, MinVersion: tls.VersionTLS12},
	}
	raw := []byte("From: sender@example.com\r\nTo: receiver@example.com\r\nSubject: Test\r\n\r\nHello\r\n")
	result, err := sendSMTPXOAUTH2(context.Background(), endpoint, "sender@example.com", "test-access-token", []string{"receiver@example.com"}, raw, "<message-id@mail.local>")
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID != "<message-id@mail.local>" || len(result.Accepted) != 1 {
		t.Fatalf("SMTP result mismatch: %#v", result)
	}
	select {
	case message := <-received:
		if !strings.Contains(message, "Subject: Test") || !strings.Contains(message, "Hello") {
			t.Fatalf("mock SMTP received invalid data: %q", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mock SMTP did not receive DATA")
	}
	if err := <-serverError; err != nil {
		t.Fatal(err)
	}
}

func serveMockSMTP(listener net.Listener, certificate tls.Certificate, received chan<- string) error {
	connection, err := listener.Accept()
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	reader, writer := bufio.NewReader(connection), bufio.NewWriter(connection)
	write := func(value string) error {
		if _, err := writer.WriteString(value); err != nil {
			return err
		}
		return writer.Flush()
	}
	read := func(prefix string) (string, error) {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(strings.ToUpper(line), strings.ToUpper(prefix)) {
			return "", fmt.Errorf("expected %s, received %q", prefix, line)
		}
		return strings.TrimSpace(line), nil
	}
	if err := write("220 mock.local ESMTP ready\r\n"); err != nil {
		return err
	}
	if _, err := read("EHLO"); err != nil {
		return err
	}
	if err := write("250-mock.local\r\n250-STARTTLS\r\n250 AUTH XOAUTH2\r\n"); err != nil {
		return err
	}
	if _, err := read("STARTTLS"); err != nil {
		return err
	}
	if err := write("220 Ready to start TLS\r\n"); err != nil {
		return err
	}
	tlsConnection := tls.Server(connection, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	if err := tlsConnection.Handshake(); err != nil {
		return err
	}
	reader, writer = bufio.NewReader(tlsConnection), bufio.NewWriter(tlsConnection)
	if _, err := read("EHLO"); err != nil {
		return err
	}
	if err := write("250-mock.local\r\n250 AUTH XOAUTH2\r\n"); err != nil {
		return err
	}
	authLine, err := read("AUTH XOAUTH2 ")
	if err != nil {
		return err
	}
	encoded := strings.TrimSpace(strings.TrimPrefix(authLine, "AUTH XOAUTH2 "))
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(payload) != "user=sender@example.com\x01auth=Bearer test-access-token\x01\x01" {
		return fmt.Errorf("invalid SMTP XOAUTH2 payload")
	}
	if err := write("235 2.7.0 Authentication successful\r\n"); err != nil {
		return err
	}
	if _, err := read("MAIL FROM:"); err != nil {
		return err
	}
	if err := write("250 2.1.0 Sender accepted\r\n"); err != nil {
		return err
	}
	if _, err := read("RCPT TO:"); err != nil {
		return err
	}
	if err := write("250 2.1.5 Recipient accepted\r\n"); err != nil {
		return err
	}
	if _, err := read("DATA"); err != nil {
		return err
	}
	if err := write("354 End data with <CR><LF>.<CR><LF>\r\n"); err != nil {
		return err
	}
	var data strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if line == ".\r\n" || line == ".\n" {
			break
		}
		data.WriteString(line)
	}
	received <- data.String()
	if err := write("250 2.0.0 Queued\r\n"); err != nil {
		return err
	}
	if _, err := read("QUIT"); err != nil {
		return err
	}
	return write("221 2.0.0 Bye\r\n")
}

func smtpTestCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certificate, err := tls.X509KeyPair(certificatePEM, privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificatePEM) {
		t.Fatal("failed to add SMTP test certificate")
	}
	return certificate, roots
}
