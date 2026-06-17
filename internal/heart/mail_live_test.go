package heart

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestMailLive exercises the real IMAP client against a live mailbox. It is
// skipped unless GMAIL_USERNAME / GMAIL_APP_PASSWORD are set in the environment,
// so the normal test suite never touches the network. Run with:
//
//	GMAIL_USERNAME=... GMAIL_APP_PASSWORD=... go test -tags fts5 -run TestMailLive -v ./internal/heart/
func TestMailLive(t *testing.T) {
	user := os.Getenv("GMAIL_USERNAME")
	pass := os.Getenv("GMAIL_APP_PASSWORD")
	if user == "" || pass == "" {
		t.Skip("set GMAIL_USERNAME and GMAIL_APP_PASSWORD to run the live IMAP check")
	}
	host := os.Getenv("GMAIL_IMAP_HOST")
	if host == "" {
		host = "imap.gmail.com"
	}
	port := 993
	if p := os.Getenv("GMAIL_IMAP_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dial := IMAPDialer(host, port, user, pass, "INBOX")
	f, err := dial(ctx)
	if err != nil {
		t.Fatalf("dial/login/select failed: %v", err)
	}
	defer f.Close()

	msgs, err := f.FetchUnseen(ctx)
	if err != nil {
		t.Fatalf("fetch unseen failed: %v", err)
	}

	t.Logf("connected OK; %d unseen message(s) in INBOX", len(msgs))
	for i, m := range msgs {
		if i >= 5 {
			t.Logf("... (%d more)", len(msgs)-5)
			break
		}
		t.Logf("  [%d] from=%q subject=%q id=%q", i, m.From, m.Subject, m.MessageID)
	}
}
