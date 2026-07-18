package mailservice

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	imageSourcePattern = regexp.MustCompile(`(?is)<img\b([^>]*?)\bsrc\s*=\s*(?:"([^"]+)"|'([^']+)'|([^\s"'=<>` + "`" + `]+))([^>]*)>`)
	cssURLPattern      = regexp.MustCompile(`(?is)url\s*\([^)]*\)`)
	cssImportPattern   = regexp.MustCompile(`(?is)@import[^;]+;?`)
	safeDataImage      = regexp.MustCompile(`(?i)^data:image/(?:avif|gif|jpeg|jpg|png|webp);base64,[a-z0-9+/=\r\n]+$`)
)

func (s *Service) renderMessageHTML(ctx context.Context, source string, inlineImages map[string]string) string {
	if source == "" {
		return ""
	}
	source = cssURLPattern.ReplaceAllString(cssImportPattern.ReplaceAllString(source, ""), "none")
	sources := remoteImageSources(source)
	remote := make(map[string]string, len(sources))
	var remoteMu sync.Mutex
	var remoteWait sync.WaitGroup
	remoteSlots := make(chan struct{}, 4)
	for _, imageURL := range sources {
		imageURL := imageURL
		remoteWait.Add(1)
		go func() {
			defer remoteWait.Done()
			select {
			case remoteSlots <- struct{}{}:
				defer func() { <-remoteSlots }()
			case <-ctx.Done():
				return
			}
			if data, err := s.fetchRemoteImage(ctx, imageURL); err == nil && data != "" {
				remoteMu.Lock()
				remote[imageURL] = data
				remoteMu.Unlock()
			}
		}()
	}
	remoteWait.Wait()
	source = imageSourcePattern.ReplaceAllStringFunc(source, func(tag string) string {
		match := imageSourcePattern.FindStringSubmatch(tag)
		if len(match) < 6 {
			return `<span class="mail-image-unavailable">[图片无法安全加载]</span>`
		}
		value := strings.TrimSpace(firstNonEmpty(match[2], match[3], match[4]))
		value = strings.ReplaceAll(strings.ReplaceAll(value, "&amp;", "&"), "&#38;", "&")
		var embedded string
		if strings.HasPrefix(strings.ToLower(value), "cid:") {
			embedded = inlineImages[normalizeContentID(value)]
		} else if normalized := normalizeRemoteSource(value); normalized != "" {
			embedded = remote[normalized]
		} else if safeDataImage.MatchString(value) {
			embedded = value
		}
		if embedded == "" {
			return `<span class="mail-image-unavailable">[图片无法安全加载]</span>`
		}
		return "<img" + match[1] + ` src="` + embedded + `"` + match[5] + ">"
	})
	return sanitizeMessageHTML(source, inlineImages)
}

func ValidateRemoteImageURL(ctx context.Context, value string) (*url.URL, error) {
	parsed, _, err := validateRemoteImageURL(ctx, value)
	return parsed, err
}

func validateRemoteImageURL(ctx context.Context, value string) (*url.URL, []net.IP, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, nil, err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return nil, nil, errors.New("Unsupported remote image URL")
	}
	if port := parsed.Port(); port != "" && port != "80" && port != "443" {
		return nil, nil, errors.New("Unsupported remote image port")
	}
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if hostname == "" {
		return nil, nil, errors.New("Missing remote image host")
	}
	if hostname == "localhost" || strings.HasSuffix(hostname, ".local") || strings.HasSuffix(hostname, ".internal") {
		return nil, nil, errors.New("Private remote image host")
	}
	addresses, err := net.DefaultResolver.LookupIP(ctx, "ip", hostname)
	if err != nil || len(addresses) == 0 {
		return nil, nil, errors.New("Private remote image address")
	}
	for _, address := range addresses {
		if privateAddress(address) {
			return nil, nil, errors.New("Private remote image address")
		}
	}
	return parsed, addresses, nil
}

func (s *Service) fetchRemoteImage(ctx context.Context, source string) (string, error) {
	current := source
	for redirects := 0; redirects <= 3; redirects++ {
		parsed, addresses, err := validateRemoteImageURL(ctx, current)
		if err != nil {
			return "", err
		}
		transport := &http.Transport{
			Proxy:             nil,
			DisableKeepAlives: true,
			TLSClientConfig:   &tls.Config{ServerName: parsed.Hostname(), MinVersion: tls.VersionTLS12},
			DialContext: func(dialContext context.Context, network, address string) (net.Conn, error) {
				_, port, splitErr := net.SplitHostPort(address)
				if splitErr != nil || (port != "80" && port != "443") {
					return nil, errors.New("Unsupported remote image endpoint")
				}
				dialer := &net.Dialer{Timeout: 5 * time.Second}
				var lastError error
				for _, resolved := range addresses {
					connection, dialErr := dialer.DialContext(dialContext, network, net.JoinHostPort(resolved.String(), port))
					if dialErr == nil {
						return connection, nil
					}
					lastError = dialErr
				}
				return nil, lastError
			},
		}
		client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return "", err
		}
		request.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/gif,image/svg+xml")
		request.Header.Set("User-Agent", "MailImageFetcher/1.0")
		response, err := client.Do(request)
		if err != nil {
			transport.CloseIdleConnections()
			return "", err
		}
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			location := response.Header.Get("Location")
			_ = response.Body.Close()
			transport.CloseIdleConnections()
			if location == "" || redirects == 3 {
				return "", errors.New("remote image redirect rejected")
			}
			next, err := parsed.Parse(location)
			if err != nil {
				return "", err
			}
			current = next.String()
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			_ = response.Body.Close()
			transport.CloseIdleConnections()
			return "", fmt.Errorf("remote image HTTP %d", response.StatusCode)
		}
		contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
		if !safeImageContentType(contentType) || response.ContentLength > 2_000_000 {
			_ = response.Body.Close()
			transport.CloseIdleConnections()
			return "", errors.New("remote image type or size rejected")
		}
		content, err := io.ReadAll(io.LimitReader(response.Body, 2_000_001))
		_ = response.Body.Close()
		transport.CloseIdleConnections()
		if err != nil || len(content) == 0 || len(content) > 2_000_000 {
			return "", errors.New("remote image size rejected")
		}
		return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(content), nil
	}
	return "", errors.New("remote image unavailable")
}

func safeImageContentType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0])) {
	case "image/avif", "image/gif", "image/jpeg", "image/jpg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

func remoteImageSources(source string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, 12)
	for _, match := range imageSourcePattern.FindAllStringSubmatch(source, -1) {
		value := normalizeRemoteSource(firstNonEmpty(match[2], match[3], match[4]))
		if value == "" || len(value) > 2048 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == 12 {
			break
		}
	}
	return result
}

func normalizeRemoteSource(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "&amp;", "&"))
	if strings.HasPrefix(value, "//") {
		return "https:" + value
	}
	if strings.HasPrefix(strings.ToLower(value), "http://") || strings.HasPrefix(strings.ToLower(value), "https://") {
		return value
	}
	return ""
}

func privateAddress(address net.IP) bool {
	if address == nil || address.IsUnspecified() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsPrivate() {
		return true
	}
	if ipv4 := address.To4(); ipv4 != nil {
		return ipv4[0] == 0 || ipv4[0] >= 224 || (ipv4[0] == 100 && ipv4[1] >= 64 && ipv4[1] <= 127) || (ipv4[0] == 198 && (ipv4[1] == 18 || ipv4[1] == 19))
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
