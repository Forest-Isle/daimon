package tool

import (
	"context"
	"os"
	"strconv"
	"testing"
)

// TestSendEmailLive sends one real email to the configured account itself. It is
// skipped unless GMAIL_USERNAME / GMAIL_APP_PASSWORD are set, so the normal
// suite never sends mail. Run with:
//
//	GMAIL_USERNAME=... GMAIL_APP_PASSWORD=... go test -tags fts5 -run TestSendEmailLive -v ./internal/tool/
func TestSendEmailLive(t *testing.T) {
	user := os.Getenv("GMAIL_USERNAME")
	pass := os.Getenv("GMAIL_APP_PASSWORD")
	if user == "" || pass == "" {
		t.Skip("set GMAIL_USERNAME and GMAIL_APP_PASSWORD to run the live SMTP check")
	}
	host := os.Getenv("GMAIL_SMTP_HOST")
	if host == "" {
		host = "smtp.gmail.com"
	}
	port := 587
	if p := os.Getenv("GMAIL_SMTP_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}

	tool := NewSendEmailTool(host, port, user, pass, user, false)
	res, err := tool.Execute(context.Background(), []byte(`{"to":"`+user+`","subject":"Daimon SMTP live test","body":"This is an automated send_email verification from Daimon."}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if res.Error != "" {
		t.Fatalf("send failed: %s", res.Error)
	}
	t.Logf("sent OK: %s", res.Output)
}
