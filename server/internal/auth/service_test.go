package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amine123max/Mail/server/internal/config"
	"github.com/amine123max/Mail/server/internal/secure"
	"github.com/amine123max/Mail/server/internal/store"
)

func openAuthTestService(t *testing.T, mutate func(*config.Config)) (*Service, *store.Store) {
	t.Helper()
	dataDir := t.TempDir()
	cfg := config.Config{
		DataDir:       dataDir,
		SessionSecret: strings.Repeat("session-secret-", 4),
		CookiePath:    "/",
		WebRoot:       dataDir,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	box, err := secure.New(dataDir, strings.Repeat("03", 32), false)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := store.Open(dataDir, box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	return New(cfg, storage), storage
}

func TestNodeCompatiblePasswordHash(t *testing.T) {
	const password = "NodeCompatible!123"
	const nodeHash = "scrypt:AAECAwQFBgcICQoLDA0ODw:sW1COcKH7Z2BDFE6mMHuCBgIPw6OwcK45RqKR1FrA7g"
	valid, err := verifyPassword(password, nodeHash)
	if err != nil || !valid {
		t.Fatalf("Node password hash was not accepted: %v", err)
	}
	valid, err = verifyPassword("wrong-password", nodeHash)
	if err != nil || valid {
		t.Fatalf("wrong password result: valid=%v err=%v", valid, err)
	}
	generated, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	valid, err = verifyPassword(password, generated)
	if err != nil || !valid {
		t.Fatalf("generated password hash was not accepted: %v", err)
	}
}

func TestVerificationMessageLanguageAndLayout(t *testing.T) {
	for _, test := range []struct {
		language string
		want     string
	}{
		{language: "zh", want: "此验证码将在 5 分钟后失效"},
		{language: "en", want: "This code expires in 5 minutes"},
	} {
		message := BuildVerificationMessage("123456", test.language)
		if !strings.Contains(message.HTML, "123456") || !strings.Contains(message.HTML, `align="center"`) || !strings.Contains(message.HTML, test.want) {
			t.Fatalf("verification template missing expected content for %s", test.language)
		}
		for _, removed := range []string{"如果并非你本人", "安全管理 Outlook", "If this wasn't you"} {
			if strings.Contains(message.HTML, removed) {
				t.Fatalf("verification template contains removed copy %q", removed)
			}
		}
	}
	if ResolveLanguage("fr-FR, en-US;q=0.9, zh-CN;q=0.8") != "en" || ResolveLanguage("zh-CN, en;q=0.5") != "zh" {
		t.Fatal("Accept-Language resolution mismatch")
	}
}

func TestVerificationConfigurationErrorContract(t *testing.T) {
	service, _ := openAuthTestService(t, nil)
	if _, err := service.BootstrapAdministrator(context.Background(), "admin", "admin@example.com", "AdminPassword!123"); err != nil {
		t.Fatal(err)
	}
	_, err := service.RequestRegistrationCode(context.Background(), "user@example.com", "register", "en")
	var authErr *Error
	if !errors.As(err, &authErr) || authErr.Code != "VERIFICATION_EMAIL_NOT_CONFIGURED" {
		t.Fatalf("unexpected verification error: %#v", err)
	}
}

func TestVerificationRequestDoesNotRevealAccountExistence(t *testing.T) {
	service, _ := openAuthTestService(t, nil)
	if _, err := service.BootstrapAdministrator(context.Background(), "admin", "admin@example.com", "AdminPassword!123"); err != nil {
		t.Fatal(err)
	}
	unknownReset, err := service.RequestRegistrationCode(context.Background(), "missing@example.com", "reset", "en")
	if err != nil || !unknownReset.Suppressed || unknownReset.RetryAfter != int(VerificationCooldown.Seconds()) {
		t.Fatalf("unknown reset request leaked account state: result=%#v err=%v", unknownReset, err)
	}
	existingRegister, err := service.RequestRegistrationCode(context.Background(), "admin@example.com", "register", "en")
	if err != nil || !existingRegister.Suppressed || existingRegister.ExpiresIn != int(VerificationLifetime.Seconds()) {
		t.Fatalf("existing registration request leaked account state: result=%#v err=%v", existingRegister, err)
	}
}

func TestResetPasswordWithEmailVerificationCode(t *testing.T) {
	service, storage := openAuthTestService(t, nil)
	const email = "admin@example.com"
	const oldPassword = "AdminPassword!123"
	const newPassword = "ChangedPassword!456"
	user, err := service.BootstrapAdministrator(context.Background(), "admin", email, oldPassword)
	if err != nil {
		t.Fatal(err)
	}
	desktopSession, err := service.CreateDesktopSession(context.Background(), user, "password-reset-device", "Password reset test", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	claims, valid := service.readDesktopAccessToken(desktopSession.AccessToken)
	if !valid {
		t.Fatal("desktop access token was invalid before password reset")
	}
	const code = "123456"
	if err := storage.SaveEmailVerification(context.Background(), email, service.verificationHash(email, code), time.Now().Add(VerificationLifetime)); err != nil {
		t.Fatal(err)
	}
	if err := service.ResetPassword(context.Background(), email, newPassword, code); err != nil {
		t.Fatal(err)
	}
	if user, err := service.Authenticate(context.Background(), email, oldPassword); err != nil || user != nil {
		t.Fatalf("old password remained valid: user=%v err=%v", user, err)
	}
	if user, err := service.Authenticate(context.Background(), email, newPassword); err != nil || user == nil {
		t.Fatalf("new password was rejected: user=%v err=%v", user, err)
	}
	active, err := storage.DesktopSessionActive(context.Background(), claims.SessionID, claims.UserID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("password reset did not revoke the desktop session")
	}
}
